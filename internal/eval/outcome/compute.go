package outcome

import (
	"time"

	"github.com/shopspring/decimal"
)

// Compute applies the outcome decision tree to one evaluation. Pure,
// deterministic, no I/O. Caller (worker.go) supplies the optional
// ActualPnLUSD or CounterfactPrice after consulting the Reader.
func Compute(in Input) Result {
	r := Result{HorizonMin: in.HorizonMin, ComputedAt: time.Now().UTC()}

	if in.ActualPnLUSD != nil {
		r.PnLUSD = in.ActualPnLUSD
		switch {
		case in.ActualPnLUSD.IsPositive():
			r.Label = LabelWin
		case in.ActualPnLUSD.IsNegative():
			r.Label = LabelLoss
		default:
			r.Label = LabelFlat
		}
		return r
	}

	if in.CounterfactPrice != nil {
		// pnl_pct = (close - signal_price) / signal_price * dir
		if in.SignalPrice.IsZero() {
			r.Label = LabelUnavailable
			return r
		}
		dir := int64(1)
		if in.Direction == "sell" {
			dir = -1
		}
		raw := in.CounterfactPrice.Sub(in.SignalPrice).
			Div(in.SignalPrice).
			Mul(decimal.NewFromInt(dir))
		r.PnLPct = &raw
		switch {
		case raw.GreaterThanOrEqual(in.WinThresh):
			r.Label = LabelWin
		case raw.LessThanOrEqual(in.LossThresh):
			r.Label = LabelLoss
		default:
			r.Label = LabelFlat
		}
		return r
	}

	// No inputs yet. Pending unless past stale cutoff.
	if !in.SignalTime.IsZero() && !in.Now.IsZero() &&
		in.Now.Sub(in.SignalTime) > time.Duration(in.StaleCutoffH)*time.Hour {
		r.Label = LabelUnavailable
		return r
	}
	r.Label = LabelPending
	return r
}
