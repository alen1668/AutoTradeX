// Package scorer implements the agent signal pre-execution scorer.
//
// The Scorer interface is the integration point for ingest.Service: after
// the hard risk pipeline approves a signal but before trade.Execute, the
// scorer assigns a 0-100 confidence score and an approve/abandon decision.
// Below-threshold signals are abandoned (or merely logged in dry_run mode).
//
// The package itself is pure: it has no DB pool or HTTP client of its own.
// LLMScorer (scorer.go) takes an LLMClient + EvalRepo via DI; testing uses
// the StubScorer (stub.go) and a fake LLMClient.
package scorer

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/macrocontext"
	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
)

// Scorer is the integration point for ingest.Service. A nil Scorer means
// "scorer layer not wired" — behavior is identical to the bot before the
// agent feature existed.
type Scorer interface {
	Score(ctx context.Context, in ScoreInput) (ScoreResult, error)
}

// ScoreInput collects everything the agent needs to evaluate a signal.
// Portfolio and Market may be nil when their providers fail; the prompt
// renders "暂不可用" for those sections and the LLM is expected to score
// based on the remaining inputs.
type ScoreInput struct {
	Signal          *sigpkg.Signal
	Strategy        *strategy.Strategy
	SymbolHistory   []HistoricalTrade
	StrategyHistory []HistoricalTrade
	Portfolio       *PortfolioSnapshot
	Market          *MarketContext
	HighVolWindows  []string                  // never fails (pure local function)
	Macro           macrocontext.MacroContext // sub-fields nil → prompt renders "暂不可用"
}

// HistoricalTrade summarizes one closed trade for the LLM. Fields chosen
// to be self-explanatory in prose: direction + entry/exit price + PnL +
// duration + close reason.
type HistoricalTrade struct {
	OpenedAt    time.Time
	Symbol      string
	Direction   string // long | short
	EntryPrice  decimal.Decimal
	ExitPrice   decimal.Decimal
	PnLUSD      decimal.Decimal
	DurationMin int
	ExitReason  string // tp | sl | manual | reverse
}

// PortfolioSnapshot is the aggregated view of currently-open positions
// plus today's realized PnL.
type PortfolioSnapshot struct {
	TotalNotionalUSD decimal.Decimal
	OpenPositions    []OpenPosition
	DailyPnLUSD      decimal.Decimal
}

// OpenPosition is one row of PortfolioSnapshot. StrategyID matches the
// strategies.id column (used as the human-facing "name" since the table
// has no separate name column).
type OpenPosition struct {
	StrategyID    string
	Symbol        string
	Direction     string
	EntryPrice    decimal.Decimal
	NotionalUSD   decimal.Decimal
	UnrealizedPnL decimal.Decimal
}

// MarketContext is the live K-line statistics for the signal's symbol.
type MarketContext struct {
	Symbol           string
	Last24hHigh      decimal.Decimal
	Last24hLow       decimal.Decimal
	Last24hChangePct decimal.Decimal
	Last1hChangePct  decimal.Decimal
	PriceVs24hRange  decimal.Decimal // 0~1 position of current price within 24h range
	Volatility24h    decimal.Decimal // relative stddev of 1h closes / mean
	KlineLookback1h  []decimal.Decimal
}

// ScoreResult is the LLM verdict plus per-call metadata for auditing.
type ScoreResult struct {
	Score       int    // 0~100; -1 when Decision == "failed"
	Decision    string // approve | abandon | failed
	Reasoning   string
	Model       string
	LatencyMs   int
	TokenIn     int
	TokenOut    int
	PromptHash  string // sha256(prompt)[:8] hex — tracks prompt version
	PromptText  string
	ResponseRaw string
}

// IsFailed reports whether the score result represents an LLM failure
// (network error, timeout, or non-conforming JSON). Callers use this
// rather than string comparison to decide fail-open vs fail-closed.
func (r ScoreResult) IsFailed() bool {
	return r.Decision == "failed"
}
