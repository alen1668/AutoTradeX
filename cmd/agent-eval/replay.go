package main

import (
	"context"
	"encoding/json"
	"fmt"
	"text/template"
	"time"

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
