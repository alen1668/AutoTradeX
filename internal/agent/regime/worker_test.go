package regime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/market"
	"github.com/lizhaojie/tvbot/internal/store"
)

type stubKline struct {
	mu      sync.Mutex
	calls   int
	candles []market.Candle
	err     error
}

func (s *stubKline) Get1hCloses(ctx context.Context, symbol string, limit int) ([]decimal.Decimal, error) {
	return nil, errors.New("not used by regime worker")
}
func (s *stubKline) Get1hOHLC(ctx context.Context, symbol string, limit int) ([]market.Candle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.candles, s.err
}

type stubSettings struct {
	enabled  bool
	interval int
}

func (s *stubSettings) Read(ctx context.Context) (WorkerSettings, error) {
	return WorkerSettings{Enabled: s.enabled, IntervalMin: s.interval}, nil
}

type stubRepo struct {
	mu   sync.Mutex
	recs []store.MarketRegimeRecord
}

func (r *stubRepo) Insert(ctx context.Context, rec store.MarketRegimeRecord) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec)
	return int64(len(r.recs)), nil
}

func (r *stubRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.recs)
}

func constantCloses(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v + float64(i%3)
	}
	return out
}

func TestWorker_RunOnceWritesRowWhenEnabled(t *testing.T) {
	kline := &stubKline{candles: build168(constantCloses(168, 50000))}
	settings := &stubSettings{enabled: true, interval: 30}
	repo := &stubRepo{}

	w := NewWorker(kline, repo, settings, zerolog.Nop()).WithSymbol("BTCUSDT")
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if repo.count() != 1 {
		t.Fatalf("expected 1 inserted row, got %d", repo.count())
	}
	if repo.recs[0].Label == "" {
		t.Errorf("Label must be non-empty: %+v", repo.recs[0])
	}
}

func TestWorker_RunOnceSkipsWhenDisabled(t *testing.T) {
	kline := &stubKline{candles: build168(constantCloses(168, 50000))}
	settings := &stubSettings{enabled: false, interval: 30}
	repo := &stubRepo{}

	w := NewWorker(kline, repo, settings, zerolog.Nop()).WithSymbol("BTCUSDT")
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if repo.count() != 0 {
		t.Fatalf("expected 0 rows (disabled), got %d", repo.count())
	}
	if kline.calls != 0 {
		t.Errorf("expected 0 kline calls when disabled, got %d", kline.calls)
	}
}

func TestWorker_RunOnceSkipsOnKlineErr(t *testing.T) {
	kline := &stubKline{err: errors.New("binance 503")}
	settings := &stubSettings{enabled: true, interval: 30}
	repo := &stubRepo{}

	w := NewWorker(kline, repo, settings, zerolog.Nop()).WithSymbol("BTCUSDT")
	if err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce should propagate kline error")
	}
	if repo.count() != 0 {
		t.Errorf("nothing should be inserted when kline fails")
	}
}

func TestWorker_StartCancellable(t *testing.T) {
	kline := &stubKline{candles: build168(constantCloses(168, 50000))}
	settings := &stubSettings{enabled: true, interval: 30}
	repo := &stubRepo{}

	ctx, cancel := context.WithCancel(context.Background())
	w := NewWorker(kline, repo, settings, zerolog.Nop()).WithSymbol("BTCUSDT")

	done := make(chan struct{})
	go func() {
		w.Start(ctx)
		close(done)
	}()
	for i := 0; i < 100 && repo.count() == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not return after ctx cancel")
	}
}
