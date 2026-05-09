package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"text/template"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
)

// ReplayCase is one signal worth of input for replay: enough columns from
// signals + agent_evaluations + position_history to (a) reconstruct the
// ScoreInput, (b) compare to the old score, (c) attribute realized PnL.
type ReplayCase struct {
	SignalID      int64
	StrategyID    string
	Symbol        string
	Kind          string
	SignalPrice   decimal.Decimal
	TVTimestampMs int64
	OldScore      int
	OldDecision   string
	OldReason     string
	HistoryJSON   []byte // raw agent_evaluations.history_json
	PnLUSDC       *float64
	HasPnL        bool
}

// historySnapshot mirrors the JSON shape persisted by LLMScorer.persistEval.
// Field tags (snake_case) match the scorer's serialization.
type historySnapshot struct {
	SymbolHistory   []scorer.HistoricalTrade  `json:"symbol_history"`
	StrategyHistory []scorer.HistoricalTrade  `json:"strategy_history"`
	Portfolio       *scorer.PortfolioSnapshot `json:"portfolio"`
	Market          *scorer.MarketContext     `json:"market"`
	HighVolWindows  []string                  `json:"high_vol_windows"`
}

// replayOne reconstructs ScoreInput from c, renders the new template,
// calls llm, parses, and returns one ReplayRow. Never panics; any failure
// fills Row.Error and leaves NewScore=0/NewDecision="".
func replayOne(
	ctx context.Context,
	c ReplayCase,
	tmpl *template.Template,
	llm scorer.LLMClient,
	model string,
	timeoutMs int,
) ReplayRow {
	row := ReplayRow{
		SignalID:    c.SignalID,
		StrategyID:  c.StrategyID,
		Symbol:      c.Symbol,
		Kind:        c.Kind,
		OldScore:    c.OldScore,
		OldDecision: c.OldDecision,
		OldReason:   c.OldReason,
		PnLUSDC:     c.PnLUSDC,
		HasPnL:      c.HasPnL,
	}

	var snap historySnapshot
	if err := json.Unmarshal(c.HistoryJSON, &snap); err != nil {
		row.Error = "history_json: " + err.Error()
		return row
	}

	in := scorer.ScoreInput{
		Signal: &sigpkg.Signal{
			StrategyID:    c.StrategyID,
			Symbol:        c.Symbol,
			Kind:          sigpkg.Kind(c.Kind),
			Price:         c.SignalPrice,
			TVTimestampMs: c.TVTimestampMs,
		},
		Strategy: &strategy.Strategy{
			Config: strategy.Config{ID: c.StrategyID, Symbol: c.Symbol},
		},
		SymbolHistory:   snap.SymbolHistory,
		StrategyHistory: snap.StrategyHistory,
		Portfolio:       snap.Portfolio,
		Market:          snap.Market,
		HighVolWindows:  snap.HighVolWindows,
	}

	prompt, _, err := scorer.RenderPromptWithTemplate(in, tmpl)
	if err != nil {
		row.Error = "render: " + err.Error()
		return row
	}

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	resp, llmErr := llm.Complete(callCtx, scorer.CompleteRequest{
		Model: model, Prompt: prompt, MaxTokens: 512,
	})
	if llmErr != nil {
		row.Error = "llm: " + llmErr.Error()
		return row
	}

	var parsed struct {
		Score     *int    `json:"score"`
		Decision  *string `json:"decision"`
		Reasoning *string `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(scorer.ExtractJSON(resp.Text)), &parsed); err != nil {
		row.Error = "parse: " + err.Error()
		return row
	}
	if parsed.Score == nil || parsed.Decision == nil || parsed.Reasoning == nil {
		row.Error = "parse: missing fields"
		return row
	}
	if *parsed.Score < 0 || *parsed.Score > 100 {
		row.Error = fmt.Sprintf("score %d out of [0,100]", *parsed.Score)
		return row
	}
	if *parsed.Decision != "approve" && *parsed.Decision != "abandon" {
		row.Error = fmt.Sprintf("decision %q not approve|abandon", *parsed.Decision)
		return row
	}

	row.NewScore = *parsed.Score
	row.NewDecision = *parsed.Decision
	row.NewReason = *parsed.Reasoning
	return row
}

// loadReplayCases pulls every signal that's been agent-evaluated since
// `cutoff`, with its old score/decision/reasoning, history snapshot, and
// (if the trade closed) realized PnL.
func loadReplayCases(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time, max int) ([]ReplayCase, error) {
	limit := max
	if limit <= 0 {
		limit = 1_000_000 // effectively unbounded
	}
	rows, err := pool.Query(ctx, `
SELECT s.id, s.strategy_id, s.symbol, s.kind::text, s.signal_price, s.tv_timestamp_ms,
       s.agent_score, s.agent_decision,
       e.history_json, e.reasoning,
       ph.pnl_usdc
  FROM signals s
  JOIN agent_evaluations e ON e.signal_id = s.id
  LEFT JOIN virtual_positions vp ON vp.entry_signal_id = s.id
  LEFT JOIN position_history ph
         ON ph.strategy_id = vp.strategy_id
        AND ph.symbol = vp.symbol
        AND ph.opened_at = vp.opened_at
 WHERE s.agent_score IS NOT NULL
   AND s.received_at >= $1
 ORDER BY s.received_at DESC
 LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("load replay cases: %w", err)
	}
	defer rows.Close()

	var out []ReplayCase
	for rows.Next() {
		var c ReplayCase
		var oldScore *int
		var oldDecision *string
		var pnl *decimal.Decimal
		if err := rows.Scan(
			&c.SignalID, &c.StrategyID, &c.Symbol, &c.Kind,
			&c.SignalPrice, &c.TVTimestampMs,
			&oldScore, &oldDecision,
			&c.HistoryJSON, &c.OldReason, &pnl,
		); err != nil {
			return nil, fmt.Errorf("scan replay case: %w", err)
		}
		if oldScore != nil {
			c.OldScore = *oldScore
		}
		if oldDecision != nil {
			c.OldDecision = *oldDecision
		}
		if pnl != nil {
			f, _ := pnl.Float64()
			c.PnLUSDC = &f
			c.HasPnL = true
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// runReplay drives the full replay pipeline: load cases, render each one
// against tmpl, aggregate, return ReplayReport. Concurrency is clamped
// to [1, 10].
func runReplay(
	ctx context.Context,
	pool *pgxpool.Pool,
	tmpl *template.Template,
	llm scorer.LLMClient,
	model string,
	timeoutMs int,
	cutoff time.Time,
	since string,
	promptFile string,
	max, concurrency int,
) (ReplayReport, error) {
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 10 {
		concurrency = 10
	}

	cases, err := loadReplayCases(ctx, pool, cutoff, max)
	if err != nil {
		return ReplayReport{}, err
	}

	rows := make([]ReplayRow, len(cases))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, c := range cases {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c ReplayCase) {
			defer wg.Done()
			defer func() { <-sem }()
			rows[i] = replayOne(ctx, c, tmpl, llm, model, timeoutMs)
		}(i, c)
	}
	wg.Wait()

	// Sample / has-PnL counts.
	withPnL := 0
	for _, r := range rows {
		if r.Error == "" && r.HasPnL {
			withPnL++
		}
	}

	// Spearman: separate (score, pnl) pairs for v1 and v2.
	v1Scores, v1Pnls := extractScoresAndPnLs(rows, func(r ReplayRow) int { return r.OldScore })
	v2Scores, v2Pnls := extractScoresAndPnLs(rows, func(r ReplayRow) int { return r.NewScore })

	report := ReplayReport{
		Since:      since,
		PromptFile: promptFile,
		SampleSize: len(rows),
		WithPnL:    withPnL,
		V1Spearman: spearman(v1Scores, v1Pnls),
		V2Spearman: spearman(v2Scores, v2Pnls),
		V1Buckets:  bucketize(rows, func(r ReplayRow) int { return r.OldScore }),
		V2Buckets:  bucketize(rows, func(r ReplayRow) int { return r.NewScore }),
		Flips:      flipMatrix(rows),
		Rows:       append([]ReplayRow{}, rows...),
	}
	sortByDeltaScoreDesc(report.Rows)
	return report, nil
}

// runReplayMode is invoked from main when --replay is given.
func runReplayMode(
	ctx context.Context,
	pool *pgxpool.Pool,
	since string,
	cutoff time.Time,
	promptFile string,
	maxN, concurrency int,
	reportPath, jsonPath, modelOverride string,
) {
	if promptFile == "" {
		fail("--replay requires --prompt-file")
	}

	tmplBytes, err := os.ReadFile(promptFile)
	if err != nil {
		fail("read prompt file: %v", err)
	}
	tmpl, err := template.New("user").Parse(string(tmplBytes))
	if err != nil {
		fail("parse prompt template: %v", err)
	}

	apiKey, model, baseURL, timeoutMs := loadLLMConfig(ctx, pool)
	if modelOverride != "" {
		model = modelOverride
	}
	if env := os.Getenv("LLM_API_KEY"); env != "" {
		apiKey = env
	}
	if apiKey == "" {
		fail("no LLM API key (set LLM_API_KEY env or system_state.llm_api_key)")
	}
	llm := scorer.NewAnthropicClient(apiKey, baseURL)

	report, err := runReplay(ctx, pool, tmpl, llm, model, timeoutMs,
		cutoff, since, promptFile, maxN, concurrency)
	if err != nil {
		fail("run replay: %v", err)
	}

	if err := renderReplayText(os.Stdout, report); err != nil {
		fail("render text: %v", err)
	}
	if reportPath != "" {
		f, err := os.Create(reportPath)
		if err != nil {
			fail("create report: %v", err)
		}
		defer f.Close()
		if err := renderReplayHTML(f, report); err != nil {
			fail("render html: %v", err)
		}
		fmt.Fprintf(os.Stderr, "html report written to %s\n", reportPath)
	}
	if jsonPath != "" {
		f, err := os.Create(jsonPath)
		if err != nil {
			fail("create json: %v", err)
		}
		defer f.Close()
		if err := renderReplayJSON(f, report); err != nil {
			fail("render json: %v", err)
		}
		fmt.Fprintf(os.Stderr, "json report written to %s\n", jsonPath)
	}
}

// loadLLMConfig pulls (api_key, model, base_url, timeout_ms) from
// system_state. Falls back to library defaults if columns are NULL/empty.
func loadLLMConfig(ctx context.Context, pool *pgxpool.Pool) (apiKey, model, baseURL string, timeoutMs int) {
	row := pool.QueryRow(ctx, `
SELECT llm_api_key, agent_scorer_model, llm_api_base_url, agent_scorer_timeout_ms
  FROM system_state LIMIT 1`)
	if err := row.Scan(&apiKey, &model, &baseURL, &timeoutMs); err != nil {
		fmt.Fprintf(os.Stderr, "warn: load llm config: %v (using defaults)\n", err)
		model = "claude-haiku-4-5-20251001"
		timeoutMs = 5000
	}
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	return
}
