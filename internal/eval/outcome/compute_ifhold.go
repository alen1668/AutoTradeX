package outcome

import (
	"time"

	"github.com/shopspring/decimal"
)

// IfHoldInput is the pure-computation input for the if-hold counterfact
// of one agent_exit_decisions row. ActualPnLUSDPct may be nil — see the
// label-decision matrix in the spec §9 for the exact semantics:
//   - shadow mode (any action) → leave ActualPnLUSDPct=nil → Label nil
//   - active hold → leave nil → Label nil
//   - active non-hold → set to realised PnL pct → Label = improved/worsened/unchanged
type IfHoldInput struct {
	Symbol       string
	Side         string // "long" | "short"
	EntryPrice   decimal.Decimal
	DecisionTime time.Time
	HorizonMin   int

	// CounterfactPrice is the close price near DecisionTime + HorizonMin.
	// Nil → still pending (Pending=true).
	CounterfactPrice *decimal.Decimal

	// ActualPnLUSDPct is the realised PnL pct (decimal, e.g. 0.005 = 0.5%).
	// Nil → no comparison baseline (Label stays nil).
	ActualPnLUSDPct *decimal.Decimal
}

// IfHoldResult is what the worker writes back via SetIfHoldOutcome.
type IfHoldResult struct {
	IfHoldPnLPct decimal.Decimal
	Label        *string // improved | worsened | unchanged | nil (no baseline)
	Pending      bool    // true when CounterfactPrice is nil
}

// ifHoldUnchangedThreshPct: |actual - if_hold| ≤ this → "unchanged".
const ifHoldUnchangedThreshPct = 0.001 // 0.1%

// ComputeIfHold — pure. (counterfact - entry) / entry * sign(side) gives
// the if-hold pct. Compares to ActualPnLUSDPct: |actual - if_hold| ≤
// threshold → unchanged; if_hold better than actual → improved (i.e. we
// shouldn't have intervened — hold would have produced a better number);
// else worsened (intervention paid off).
func ComputeIfHold(in IfHoldInput) IfHoldResult {
	if in.CounterfactPrice == nil {
		return IfHoldResult{Pending: true}
	}
	dirSign := decimal.NewFromInt(1)
	if in.Side == "short" {
		dirSign = decimal.NewFromInt(-1)
	}
	pct := in.CounterfactPrice.Sub(in.EntryPrice).Div(in.EntryPrice).Mul(dirSign)

	out := IfHoldResult{IfHoldPnLPct: pct}
	if in.ActualPnLUSDPct == nil {
		return out
	}
	diff := pct.Sub(*in.ActualPnLUSDPct).Abs()
	if diff.LessThanOrEqual(decimal.NewFromFloat(ifHoldUnchangedThreshPct)) {
		l := "unchanged"
		out.Label = &l
		return out
	}
	if pct.GreaterThan(*in.ActualPnLUSDPct) {
		l := "improved"
		out.Label = &l
		return out
	}
	l := "worsened"
	out.Label = &l
	return out
}
