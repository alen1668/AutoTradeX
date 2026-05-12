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

// Reader is the DB-facing interface — worker.go satisfies it with pgx.
// Defined here so unit tests can use a fake.
type Reader interface {
	// PendingEvaluations returns up to limit rows with outcome_label NULL
	// AND created_at older than (now - minAgeMin minutes).
	PendingEvaluations(ctx context.Context, limit, minAgeMin int) ([]EvalRow, error)
	// TradesPnL returns the actual realized PnL of the first closed trade
	// for a signal, or (nil, nil) if no closed trade.
	TradesPnL(ctx context.Context, signalID int64) (*decimal.Decimal, error)
	// CounterfactClose returns the close price at-or-after target_time
	// (within ±5min), or (nil, nil) if no data.
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
