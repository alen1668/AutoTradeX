package outcome

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

type fakePending struct {
	pending []EvalRow
	pnl     map[int64]*decimal.Decimal
	drained bool
}

func (f *fakePending) PendingEvaluations(_ context.Context, _, _ int) ([]EvalRow, error) {
	if f.drained {
		return nil, nil
	}
	f.drained = true
	return f.pending, nil
}
func (f *fakePending) PositionPnL(_ context.Context, sid int64) (*decimal.Decimal, error) {
	return f.pnl[sid], nil
}

type fakeKline struct {
	prices map[string]*decimal.Decimal
}

func (f *fakeKline) CounterfactClose(_ context.Context, sym string, _ time.Time) (*decimal.Decimal, error) {
	return f.prices[sym], nil
}

type fakeWriter struct {
	mu    sync.Mutex
	calls map[int64]Result
}

func (f *fakeWriter) WriteOutcome(_ context.Context, sid int64, r Result) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[int64]Result{}
	}
	f.calls[sid] = r
	return nil
}

func TestWorker_RunOnce_MixedApproveAndAbandon(t *testing.T) {
	pnl := dec("10") // approve path → win
	closePx := dec("100.5") // abandon path: buy, signal 100 → +0.5%, win
	pr := &fakePending{
		pending: []EvalRow{
			{SignalID: 1, Symbol: "BTCUSDT", Direction: "buy", SignalPrice: dec("50000"), SignalTime: time.Now().Add(-90 * time.Minute)},
			{SignalID: 2, Symbol: "ETHUSDT", Direction: "buy", SignalPrice: dec("100"), SignalTime: time.Now().Add(-90 * time.Minute)},
		},
		pnl: map[int64]*decimal.Decimal{1: &pnl},
	}
	kf := &fakeKline{prices: map[string]*decimal.Decimal{"ETHUSDT": &closePx}}
	w := &fakeWriter{}
	cfg := Config{
		HorizonMin: 60, WinThresh: dec("0.003"), LossThresh: dec("-0.003"),
		BatchSize: 100, StaleCutoffH: 24,
	}
	worker := NewWorker(pr, kf, w, cfg, nil)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := w.calls[1].Label; got != LabelWin {
		t.Fatalf("approve path: want win got %s", got)
	}
	if got := w.calls[2].Label; got != LabelWin {
		t.Fatalf("abandon path: want win got %s", got)
	}
}

func TestWorker_RunOnce_PendingNotWritten(t *testing.T) {
	// abandon path, no kline, not yet stale → should NOT call Writer
	pr := &fakePending{
		pending: []EvalRow{
			{SignalID: 3, Symbol: "XRPUSDT", Direction: "buy",
				SignalPrice: dec("1"), SignalTime: time.Now().Add(-90 * time.Minute)},
		},
	}
	kf := &fakeKline{} // empty — returns nil
	w := &fakeWriter{}
	cfg := Config{HorizonMin: 60, BatchSize: 10, StaleCutoffH: 24,
		WinThresh: dec("0.003"), LossThresh: dec("-0.003")}
	worker := NewWorker(pr, kf, w, cfg, nil)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, called := w.calls[3]; called {
		t.Fatalf("pending should not be persisted, got %v", w.calls[3])
	}
}

func TestWorker_RunOnce_ReaderError(t *testing.T) {
	w := &fakeWriter{}
	worker := NewWorker(&errReader{}, &fakeKline{}, w, Config{BatchSize: 1, HorizonMin: 60}, nil)
	if err := worker.RunOnce(context.Background()); err == nil {
		t.Fatal("expected error from reader")
	}
}

type errReader struct{}

func (errReader) PendingEvaluations(context.Context, int, int) ([]EvalRow, error) {
	return nil, errors.New("boom")
}
func (errReader) PositionPnL(context.Context, int64) (*decimal.Decimal, error) {
	return nil, nil
}
