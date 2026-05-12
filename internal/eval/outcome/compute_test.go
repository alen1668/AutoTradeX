package outcome

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func dec(s string) decimal.Decimal { d, _ := decimal.NewFromString(s); return d }
func dp(s string) *decimal.Decimal { d := dec(s); return &d }

func TestCompute_ApproveWin(t *testing.T) {
	pnl := dec("42.50")
	in := Input{
		SignalID:     1, Direction: "buy", HorizonMin: 60,
		WinThresh: dec("0.003"), LossThresh: dec("-0.003"),
		ActualPnLUSD: &pnl,
	}
	r := Compute(in)
	if r.Label != LabelWin {
		t.Fatalf("want win, got %s", r.Label)
	}
	if r.PnLUSD == nil || !r.PnLUSD.Equal(pnl) {
		t.Fatalf("want pnl=42.5, got %v", r.PnLUSD)
	}
	if r.PnLPct != nil {
		t.Fatalf("approve path should not set PnLPct")
	}
	if r.HorizonMin != 60 {
		t.Fatalf("horizon not propagated")
	}
}

func TestCompute_ApproveLoss(t *testing.T) {
	pnl := dec("-15.00")
	in := Input{ActualPnLUSD: &pnl, HorizonMin: 60}
	r := Compute(in)
	if r.Label != LabelLoss {
		t.Fatalf("want loss, got %s", r.Label)
	}
}

func TestCompute_ApproveFlat(t *testing.T) {
	pnl := dec("0")
	in := Input{ActualPnLUSD: &pnl, HorizonMin: 60}
	r := Compute(in)
	if r.Label != LabelFlat {
		t.Fatalf("want flat, got %s", r.Label)
	}
}

// Pending: no inputs, not yet past stale cutoff.
func TestCompute_Pending(t *testing.T) {
	now := time.Now()
	in := Input{
		SignalTime:   now.Add(-time.Hour),
		Now:          now,
		StaleCutoffH: 24,
		HorizonMin:   60,
	}
	r := Compute(in)
	if r.Label != LabelPending {
		t.Fatalf("want pending (empty), got %q", r.Label)
	}
}

// Unavailable: past stale cutoff with still no inputs.
func TestCompute_Unavailable(t *testing.T) {
	now := time.Now()
	in := Input{
		SignalTime:   now.Add(-30 * time.Hour),
		Now:          now,
		StaleCutoffH: 24,
		HorizonMin:   60,
	}
	r := Compute(in)
	if r.Label != LabelUnavailable {
		t.Fatalf("want unavailable, got %s", r.Label)
	}
}

func TestCompute_AbandonBuyWin(t *testing.T) {
	in := Input{
		Direction: "buy", SignalPrice: dec("100"),
		CounterfactPrice: dp("100.5"), // +0.5%
		WinThresh: dec("0.003"), LossThresh: dec("-0.003"),
		HorizonMin: 60,
	}
	r := Compute(in)
	if r.Label != LabelWin {
		t.Fatalf("want win, got %s", r.Label)
	}
	if r.PnLPct == nil || !r.PnLPct.Round(4).Equal(dec("0.005")) {
		t.Fatalf("want pnl_pct=0.005, got %v", r.PnLPct)
	}
}

func TestCompute_AbandonSellWin(t *testing.T) {
	// sell + price dropped → counterfactual win
	in := Input{
		Direction: "sell", SignalPrice: dec("100"),
		CounterfactPrice: dp("99.5"), // -0.5% raw, *(-1) = +0.5%
		WinThresh: dec("0.003"), LossThresh: dec("-0.003"),
	}
	r := Compute(in)
	if r.Label != LabelWin {
		t.Fatalf("sell + price down should be win, got %s", r.Label)
	}
}

func TestCompute_AbandonFlat(t *testing.T) {
	in := Input{
		Direction: "buy", SignalPrice: dec("100"),
		CounterfactPrice: dp("100.1"), // +0.1% < win thresh 0.3%
		WinThresh: dec("0.003"), LossThresh: dec("-0.003"),
	}
	r := Compute(in)
	if r.Label != LabelFlat {
		t.Fatalf("want flat, got %s", r.Label)
	}
}

func TestCompute_AbandonLoss(t *testing.T) {
	in := Input{
		Direction: "buy", SignalPrice: dec("100"),
		CounterfactPrice: dp("99.4"), // -0.6%
		WinThresh: dec("0.003"), LossThresh: dec("-0.003"),
	}
	r := Compute(in)
	if r.Label != LabelLoss {
		t.Fatalf("want loss, got %s", r.Label)
	}
}

func TestCompute_AbandonZeroSignalPrice(t *testing.T) {
	// Edge: signal_price=0 → unavailable (would div-by-zero)
	in := Input{
		Direction: "buy", SignalPrice: dec("0"),
		CounterfactPrice: dp("100"),
	}
	r := Compute(in)
	if r.Label != LabelUnavailable {
		t.Fatalf("want unavailable on zero signal_price, got %s", r.Label)
	}
}
