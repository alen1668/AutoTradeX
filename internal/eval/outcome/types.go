// Package outcome computes the post-hoc correctness label for an
// agent_evaluations row. approve path uses trades.pnl_usd; abandon path
// uses a fixed-horizon counterfactual price. Pure computation — no I/O —
// is exposed via Compute; the worker package wires it to DB readers.
package outcome

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// Label is the post-hoc verdict for one evaluation.
type Label string

const (
	LabelWin         Label = "win"
	LabelLoss        Label = "loss"
	LabelFlat        Label = "flat"
	LabelUnavailable Label = "unavailable"
)

// Pending sentinel — never written, callers leave label NULL.
const LabelPending Label = ""

// Input is everything Compute needs. Producers (worker.go) fetch these
// fields from DB; tests construct them directly.
type Input struct {
	SignalID     int64
	Symbol       string
	Direction    string          // "buy" | "sell"
	SignalPrice  decimal.Decimal
	SignalTime   time.Time
	HorizonMin   int             // e.g. 60
	WinThresh    decimal.Decimal // e.g. 0.003 = 0.3%
	LossThresh   decimal.Decimal // e.g. -0.003 = -0.3%
	Now          time.Time       // for unavailable-cutoff check
	StaleCutoffH int             // e.g. 24

	// Optional inputs supplied by caller — Compute reads these in order:
	// 1) ActualPnLUSD (approve path): when non-nil, used directly
	// 2) CounterfactPrice (abandon path): when non-nil, derives pnl_pct
	// 3) Otherwise: pending (or unavailable past stale cutoff)
	ActualPnLUSD     *decimal.Decimal
	CounterfactPrice *decimal.Decimal
}

// Result is what gets written back to agent_evaluations.
// Label == "" means still pending; caller does NOT write to DB.
type Result struct {
	Label      Label
	PnLUSD     *decimal.Decimal
	PnLPct     *decimal.Decimal
	HorizonMin int
	ComputedAt time.Time
}

// PendingReader supplies DB-resident inputs to the outcome worker: the
// list of evaluations awaiting an outcome label, and the realized PnL
// of any associated closed position. PGRepo (pg.go) implements this.
type PendingReader interface {
	// PendingEvaluations returns up to limit signals whose
	// agent_evaluations.outcome_label is NULL AND that are older than
	// minAgeMin minutes. Only signals with kind IN ('long','short') are
	// returned (exit_* signals have no win/loss semantic).
	PendingEvaluations(ctx context.Context, limit, minAgeMin int) ([]EvalRow, error)
	// PositionPnL returns the realized pnl_usdc of the first
	// position_history row whose entry_signal_id matches signalID, or
	// (nil, nil) if no closed position exists for that signal.
	PositionPnL(ctx context.Context, signalID int64) (*decimal.Decimal, error)
}

// KlineFetcher supplies the counterfactual close price for the abandon
// path. Production wires this to internal/agent/market.KlineClient
// (live Binance HTTP); tests use a fake. Worker (worker.go) holds one.
type KlineFetcher interface {
	// CounterfactClose returns the close price at-or-near target_time
	// for symbol, or (nil, nil) if no data available. ±5min tolerance is
	// implementation's responsibility.
	CounterfactClose(ctx context.Context, symbol string, target time.Time) (*decimal.Decimal, error)
}

// Writer is the DB-facing interface for writing outcome columns back.
type Writer interface {
	WriteOutcome(ctx context.Context, signalID int64, r Result) error
}

// EvalRow is the minimal subset of agent_evaluations needed for Compute.
// Direction comes from signals table; worker joins.
type EvalRow struct {
	SignalID    int64
	Symbol      string
	Direction   string
	SignalPrice decimal.Decimal
	SignalTime  time.Time
}
