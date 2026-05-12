package perpmetrics_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/perpmetrics"
)

type fakeFetcher struct {
	failOn map[string]bool
	calls  map[string]int
}

func (f *fakeFetcher) PremiumIndex(ctx context.Context, symbol string) (perpmetrics.PremiumIndexResult, error) {
	f.calls[symbol]++
	if f.failOn[symbol] {
		return perpmetrics.PremiumIndexResult{}, errors.New("boom")
	}
	return perpmetrics.PremiumIndexResult{
		FundingRate:     decimal.NewFromFloat(0.00025),
		NextFundingTime: time.Now().Add(time.Hour),
		MarkPrice:       decimal.NewFromInt(50000),
	}, nil
}
func (f *fakeFetcher) OpenInterest(ctx context.Context, symbol string) (perpmetrics.OpenInterestResult, error) {
	if f.failOn[symbol] {
		return perpmetrics.OpenInterestResult{}, errors.New("boom")
	}
	return perpmetrics.OpenInterestResult{
		Current: decimal.NewFromFloat(105), Prev24h: decimal.NewFromFloat(100),
	}, nil
}
func (f *fakeFetcher) TopLongShortRatio(ctx context.Context, symbol string) (decimal.Decimal, error) {
	if f.failOn[symbol] {
		return decimal.Decimal{}, errors.New("boom")
	}
	return decimal.NewFromFloat(1.85), nil
}

type fakeKlines struct{}

func (f fakeKlines) Price24hPct(ctx context.Context, symbol string) (decimal.Decimal, error) {
	return decimal.NewFromFloat(2.0), nil
}

type fakeStore struct {
	inserted []perpmetrics.Snapshot
}

func (f *fakeStore) Insert(ctx context.Context, s perpmetrics.Snapshot) error {
	f.inserted = append(f.inserted, s)
	return nil
}

type fakeSymbols struct{ list []string }

func (f *fakeSymbols) ActiveSymbols(ctx context.Context) ([]string, error) {
	return f.list, nil
}

type fakeWSettings struct {
	enabled  bool
	lookback int
}

func (f *fakeWSettings) Read(ctx context.Context) (perpmetrics.WorkerSettings, error) {
	return perpmetrics.WorkerSettings{Enabled: f.enabled, LookbackMinutes: f.lookback}, nil
}

func newTestWorker(fetcher *fakeFetcher, store *fakeStore, syms *fakeSymbols, set *fakeWSettings) *perpmetrics.Worker {
	return perpmetrics.NewWorker(fetcher, fakeKlines{}, store, syms, set, zerolog.Nop())
}

func TestWorker_RunOnce_Disabled_Skips(t *testing.T) {
	store := &fakeStore{}
	w := newTestWorker(
		&fakeFetcher{calls: map[string]int{}, failOn: map[string]bool{}},
		store,
		&fakeSymbols{list: []string{"ETHUSDT"}},
		&fakeWSettings{enabled: false},
	)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(store.inserted) != 0 {
		t.Errorf("disabled worker should not insert; got %d", len(store.inserted))
	}
}

func TestWorker_RunOnce_PerSymbolFailureIsolated(t *testing.T) {
	fetcher := &fakeFetcher{
		failOn: map[string]bool{"ETHUSDT": true},
		calls:  map[string]int{},
	}
	store := &fakeStore{}
	w := newTestWorker(
		fetcher,
		store,
		&fakeSymbols{list: []string{"ETHUSDT", "BTCUSDT"}},
		&fakeWSettings{enabled: true, lookback: 30},
	)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("want 1 successful insert (BTCUSDT), got %d", len(store.inserted))
	}
	if store.inserted[0].Symbol != "BTCUSDT" {
		t.Errorf("want BTCUSDT, got %s", store.inserted[0].Symbol)
	}
}

func TestWorker_RunOnce_AlwaysIncludesBTC(t *testing.T) {
	fetcher := &fakeFetcher{calls: map[string]int{}, failOn: map[string]bool{}}
	store := &fakeStore{}
	w := newTestWorker(
		fetcher,
		store,
		&fakeSymbols{list: []string{"ETHUSDT"}}, // no BTC in list
		&fakeWSettings{enabled: true, lookback: 30},
	)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	seen := map[string]bool{}
	for _, s := range store.inserted {
		seen[s.Symbol] = true
	}
	if !seen["BTCUSDT"] || !seen["ETHUSDT"] {
		t.Errorf("symbols inserted = %v, want both BTCUSDT and ETHUSDT", seen)
	}
}

func TestWorker_RunOnce_DedupesBTC(t *testing.T) {
	fetcher := &fakeFetcher{calls: map[string]int{}, failOn: map[string]bool{}}
	store := &fakeStore{}
	w := newTestWorker(
		fetcher,
		store,
		&fakeSymbols{list: []string{"BTCUSDT", "BTCUSDT", "ETHUSDT"}}, // duplicate BTC
		&fakeWSettings{enabled: true, lookback: 30},
	)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	count := map[string]int{}
	for _, s := range store.inserted {
		count[s.Symbol]++
	}
	if count["BTCUSDT"] != 1 {
		t.Errorf("BTCUSDT inserted %d times, want 1", count["BTCUSDT"])
	}
}

func TestWorker_RunOnce_ComputesLabelsFromFetcherOutput(t *testing.T) {
	fetcher := &fakeFetcher{calls: map[string]int{}, failOn: map[string]bool{}}
	store := &fakeStore{}
	w := newTestWorker(
		fetcher,
		store,
		&fakeSymbols{list: []string{}},
		&fakeWSettings{enabled: true, lookback: 30},
	)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(store.inserted) != 1 {
		t.Fatalf("want 1 row (BTCUSDT only), got %d", len(store.inserted))
	}
	s := store.inserted[0]
	// funding 0.00025 = 0.025% → mild_long
	if s.FundingLabel != "mild_long" {
		t.Errorf("FundingLabel = %q, want mild_long", s.FundingLabel)
	}
	// OI 24h = (105-100)/100*100 = 5%; price = 2% → new_longs
	if s.OISignal != "new_longs" {
		t.Errorf("OISignal = %q, want new_longs", s.OISignal)
	}
	// LS = 1.85 → bullish
	if s.LSLabel != "bullish" {
		t.Errorf("LSLabel = %q, want bullish", s.LSLabel)
	}
}
