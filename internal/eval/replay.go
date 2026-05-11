package eval

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

// historySnapshot mirrors the JSON shape persisted by LLMScorer.persistEval.
// Field tags (snake_case) match the scorer's serialization.
type historySnapshot struct {
	SymbolHistory   []scorer.HistoricalTrade  `json:"symbol_history"`
	StrategyHistory []scorer.HistoricalTrade  `json:"strategy_history"`
	Portfolio       *scorer.PortfolioSnapshot `json:"portfolio"`
	Market          *scorer.MarketContext     `json:"market"`
	HighVolWindows  []string                  `json:"high_vol_windows"`
}

// ReplayOne reconstructs ScoreInput from c, renders the new template,
// calls llm, parses, and returns one ReplayRow. Never panics; any failure
// fills Row.Error and leaves NewScore=0/NewDecision="".
func ReplayOne(
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

// LoadReplayCases pulls every signal that's been agent-evaluated since
// `cutoff`, with its old score/decision/reasoning, history snapshot, and
// (if the trade closed) realized PnL. `max<=0` means unbounded.
func LoadReplayCases(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time, max int) ([]ReplayCase, error) {
	limit := max
	if limit <= 0 {
		limit = 1_000_000
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

// RunReplay drives the replay pipeline over pre-loaded cases:
// dispatch ReplayOne concurrently, aggregate the rows, return a
// ReplayReport. Concurrency is clamped to [1, 10]. The caller is
// responsible for loading the cases (LoadReplayCases) so it can record
// samples_total before dispatching (e.g. for progress polling).
func RunReplay(
	ctx context.Context,
	cases []ReplayCase,
	tmpl *template.Template,
	llm scorer.LLMClient,
	model string,
	timeoutMs, concurrency int,
) ReplayReport {
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 10 {
		concurrency = 10
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
			rows[i] = ReplayOne(ctx, c, tmpl, llm, model, timeoutMs)
		}(i, c)
	}
	wg.Wait()

	withPnL := 0
	for _, r := range rows {
		if r.Error == "" && r.HasPnL {
			withPnL++
		}
	}

	v1Scores, v1Pnls := ExtractScoresAndPnLs(rows, func(r ReplayRow) int { return r.OldScore })
	v2Scores, v2Pnls := ExtractScoresAndPnLs(rows, func(r ReplayRow) int { return r.NewScore })

	report := ReplayReport{
		SampleSize: len(rows),
		WithPnL:    withPnL,
		V1Spearman: Spearman(v1Scores, v1Pnls),
		V2Spearman: Spearman(v2Scores, v2Pnls),
		V1Buckets:  Bucketize(rows, func(r ReplayRow) int { return r.OldScore }),
		V2Buckets:  Bucketize(rows, func(r ReplayRow) int { return r.NewScore }),
		Flips:      FlipMatrixOf(rows),
		Rows:       append([]ReplayRow{}, rows...),
	}
	SortByDeltaScoreDesc(report.Rows)
	return report
}

// LoadLLMConfig pulls (api_key, model, base_url, timeout_ms) from
// system_state. Falls back to library defaults if columns are NULL/empty.
// On query failure, prints a warning to stderr and returns defaults.
func LoadLLMConfig(ctx context.Context, pool *pgxpool.Pool) (apiKey, model, baseURL string, timeoutMs int) {
	row := pool.QueryRow(ctx, `
SELECT llm_api_key, agent_scorer_model, llm_api_base_url, agent_scorer_timeout_ms
  FROM system_state LIMIT 1`)
	if err := row.Scan(&apiKey, &model, &baseURL, &timeoutMs); err != nil {
		fmt.Fprintf(os.Stderr, "warn: load llm config: %v (using defaults)\n", err)
		model = scorer.DefaultModel
		timeoutMs = 5000
	}
	if model == "" {
		model = scorer.DefaultModel
	}
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	return
}

// MakeLLMClient constructs the production LLM client. Centralized here so
// cmd/agent-eval and the Phase 2 web worker share one place that knows
// which scorer constructor to call.
func MakeLLMClient(apiKey, baseURL string) scorer.LLMClient {
	return scorer.NewAnthropicClient(apiKey, baseURL)
}
