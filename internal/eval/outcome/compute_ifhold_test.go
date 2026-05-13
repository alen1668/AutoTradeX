package outcome

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func decFloat(f float64) *decimal.Decimal { d := decimal.NewFromFloat(f); return &d }

func TestComputeIfHold_LongImproved(t *testing.T) {
	entry := decimal.NewFromFloat(2300)
	curr := decimal.NewFromFloat(2350) // future close: pnl ≈ +2.17%
	in := IfHoldInput{
		Symbol: "ETHUSDC", Side: "long",
		EntryPrice: entry, DecisionTime: time.Now().Add(-2 * time.Hour), HorizonMin: 60,
		ActualPnLUSDPct:  decFloat(0.005), // realised 0.5%
		CounterfactPrice: &curr,
	}
	out := ComputeIfHold(in)
	if out.IfHoldPnLPct.IsZero() {
		t.Errorf("pnl pct should be >0")
	}
	if out.Label == nil || *out.Label != "improved" {
		t.Errorf("label want improved, got %v", out.Label)
	}
}

func TestComputeIfHold_LongWorsened(t *testing.T) {
	entry := decimal.NewFromFloat(2300)
	curr := decimal.NewFromFloat(2270) // future close, loss
	in := IfHoldInput{
		Symbol: "ETHUSDC", Side: "long",
		EntryPrice: entry, DecisionTime: time.Now().Add(-2 * time.Hour), HorizonMin: 60,
		ActualPnLUSDPct: decFloat(0.01), CounterfactPrice: &curr,
	}
	out := ComputeIfHold(in)
	if out.Label == nil || *out.Label != "worsened" {
		t.Errorf("got %v", out.Label)
	}
}

func TestComputeIfHold_Unchanged(t *testing.T) {
	entry := decimal.NewFromFloat(2300)
	curr := decimal.NewFromFloat(2304.6) // ~0.2% future
	in := IfHoldInput{
		Symbol: "ETHUSDC", Side: "long",
		EntryPrice: entry, DecisionTime: time.Now().Add(-2 * time.Hour), HorizonMin: 60,
		ActualPnLUSDPct: decFloat(0.0019), CounterfactPrice: &curr, // diff < 0.1%
	}
	out := ComputeIfHold(in)
	if out.Label == nil || *out.Label != "unchanged" {
		t.Errorf("got %v", out.Label)
	}
}

func TestComputeIfHold_NoActualKeepsLabelNil(t *testing.T) {
	entry := decimal.NewFromFloat(2300)
	curr := decimal.NewFromFloat(2350)
	in := IfHoldInput{
		Symbol: "ETHUSDC", Side: "long", EntryPrice: entry,
		DecisionTime: time.Now().Add(-2 * time.Hour), HorizonMin: 60, CounterfactPrice: &curr,
	}
	out := ComputeIfHold(in)
	if out.Label != nil {
		t.Errorf("want nil label when no actual baseline, got %v", *out.Label)
	}
	if out.IfHoldPnLPct.IsZero() {
		t.Errorf("pct should still be filled")
	}
}

func TestComputeIfHold_PendingNoCounterfact(t *testing.T) {
	in := IfHoldInput{
		Symbol: "ETHUSDC", Side: "long",
		EntryPrice: decimal.NewFromFloat(2300), DecisionTime: time.Now(), HorizonMin: 60,
	}
	out := ComputeIfHold(in)
	if out.Pending != true {
		t.Errorf("expected Pending=true")
	}
}

func TestComputeIfHold_ShortImproved(t *testing.T) {
	// Short side: counterfact down → positive PnL for short.
	entry := decimal.NewFromFloat(2300)
	curr := decimal.NewFromFloat(2250) // dropped → short profit ≈ 2.17%
	in := IfHoldInput{
		Symbol: "ETHUSDC", Side: "short",
		EntryPrice: entry, DecisionTime: time.Now().Add(-2 * time.Hour), HorizonMin: 60,
		ActualPnLUSDPct: decFloat(0.005), CounterfactPrice: &curr,
	}
	out := ComputeIfHold(in)
	if out.IfHoldPnLPct.IsNegative() {
		t.Errorf("short profit should be positive, got %v", out.IfHoldPnLPct)
	}
	if out.Label == nil || *out.Label != "improved" {
		t.Errorf("label want improved, got %v", out.Label)
	}
}
