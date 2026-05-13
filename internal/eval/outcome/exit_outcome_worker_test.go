package outcome

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

type fakePendingExitReader struct {
	rows []ExitDecisionForOutcome
	err  error
}

func (f *fakePendingExitReader) ListPending(_ context.Context, _ time.Time, _ int) ([]ExitDecisionForOutcome, error) {
	return f.rows, f.err
}

type fakeKlineCounterfact struct {
	close *decimal.Decimal
	err   error
}

func (f *fakeKlineCounterfact) CounterfactClose(_ context.Context, _ string, _ time.Time) (*decimal.Decimal, error) {
	return f.close, f.err
}

type fakeExitOutcomeWriter struct {
	updates []struct {
		ID      int64
		Pct     *decimal.Decimal
		Label   *string
		Horizon int
	}
}

func (f *fakeExitOutcomeWriter) SetIfHoldOutcome(_ context.Context, id int64, h int, pct *decimal.Decimal, l *string) error {
	f.updates = append(f.updates, struct {
		ID      int64
		Pct     *decimal.Decimal
		Label   *string
		Horizon int
	}{id, pct, l, h})
	return nil
}

func TestExitOutcomeWorker_BackfillsImproved(t *testing.T) {
	close := decimal.NewFromFloat(2350)
	pct := decimal.NewFromFloat(0.005)
	r := &fakePendingExitReader{rows: []ExitDecisionForOutcome{{
		ID: 1, Symbol: "ETHUSDC", Side: "long",
		EntryPrice:   decimal.NewFromFloat(2300),
		Action:       "exit_now",
		Mode:         "active",
		DecisionTime: time.Now().Add(-2 * time.Hour),
		ActualPnLPct: &pct,
	}}}
	wr := &fakeExitOutcomeWriter{}
	w := NewExitOutcomeWorker(r, &fakeKlineCounterfact{close: &close}, wr, 60, 24, zerolog.Nop())
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(wr.updates) != 1 {
		t.Fatalf("update count: %d", len(wr.updates))
	}
	if wr.updates[0].Label == nil || *wr.updates[0].Label != "improved" {
		t.Errorf("label: %v", wr.updates[0].Label)
	}
	if wr.updates[0].Horizon != 60 {
		t.Errorf("horizon: %d", wr.updates[0].Horizon)
	}
}

func TestExitOutcomeWorker_HoldDecisionLeavesLabelNil(t *testing.T) {
	close := decimal.NewFromFloat(2350)
	pct := decimal.NewFromFloat(0.005) // present, but should be ignored for hold
	r := &fakePendingExitReader{rows: []ExitDecisionForOutcome{{
		ID: 2, Symbol: "ETHUSDC", Side: "long",
		EntryPrice:   decimal.NewFromFloat(2300),
		Action:       "hold",
		Mode:         "active",
		DecisionTime: time.Now().Add(-2 * time.Hour),
		ActualPnLPct: &pct,
	}}}
	wr := &fakeExitOutcomeWriter{}
	w := NewExitOutcomeWorker(r, &fakeKlineCounterfact{close: &close}, wr, 60, 24, zerolog.Nop())
	_ = w.RunOnce(context.Background())
	if len(wr.updates) != 1 {
		t.Fatalf("update count: %d", len(wr.updates))
	}
	if wr.updates[0].Label != nil {
		t.Errorf("hold should leave label nil, got %v", *wr.updates[0].Label)
	}
	if wr.updates[0].Pct == nil {
		t.Errorf("pct should still be filled")
	}
}

func TestExitOutcomeWorker_ShadowDecisionLeavesLabelNil(t *testing.T) {
	close := decimal.NewFromFloat(2350)
	pct := decimal.NewFromFloat(0.005)
	r := &fakePendingExitReader{rows: []ExitDecisionForOutcome{{
		ID: 3, Symbol: "ETHUSDC", Side: "long",
		EntryPrice:   decimal.NewFromFloat(2300),
		Action:       "exit_now",
		Mode:         "shadow",
		DecisionTime: time.Now().Add(-2 * time.Hour),
		ActualPnLPct: &pct,
	}}}
	wr := &fakeExitOutcomeWriter{}
	w := NewExitOutcomeWorker(r, &fakeKlineCounterfact{close: &close}, wr, 60, 24, zerolog.Nop())
	_ = w.RunOnce(context.Background())
	if wr.updates[0].Label != nil {
		t.Errorf("shadow should leave label nil, got %v", *wr.updates[0].Label)
	}
}

func TestExitOutcomeWorker_PendingNoCounterfactSkips(t *testing.T) {
	r := &fakePendingExitReader{rows: []ExitDecisionForOutcome{{
		ID: 4, Symbol: "ETHUSDC", Side: "long",
		EntryPrice: decimal.NewFromFloat(2300),
		Action:     "hold",
		// Recent (< stale cutoff) and counterfact missing → skip.
		DecisionTime: time.Now().Add(-30 * time.Minute),
	}}}
	wr := &fakeExitOutcomeWriter{}
	w := NewExitOutcomeWorker(r, &fakeKlineCounterfact{close: nil}, wr, 60, 24, zerolog.Nop())
	_ = w.RunOnce(context.Background())
	if len(wr.updates) != 0 {
		t.Errorf("pending should not write, got %d", len(wr.updates))
	}
}

func TestExitOutcomeWorker_StaleNoCounterfactMarksUnavailable(t *testing.T) {
	r := &fakePendingExitReader{rows: []ExitDecisionForOutcome{{
		ID: 5, Symbol: "ETHUSDC", Side: "long",
		EntryPrice: decimal.NewFromFloat(2300),
		Action:     "hold",
		// 2d old + no counterfact → mark unavailable.
		DecisionTime: time.Now().Add(-48 * time.Hour),
	}}}
	wr := &fakeExitOutcomeWriter{}
	w := NewExitOutcomeWorker(r, &fakeKlineCounterfact{close: nil}, wr, 60, 24, zerolog.Nop())
	_ = w.RunOnce(context.Background())
	if len(wr.updates) != 1 {
		t.Fatalf("stale should write 1 update, got %d", len(wr.updates))
	}
	if wr.updates[0].Label == nil || *wr.updates[0].Label != "unavailable" {
		t.Errorf("expected unavailable, got %v", wr.updates[0].Label)
	}
}
