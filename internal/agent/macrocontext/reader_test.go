package macrocontext

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/calendar"
	"github.com/lizhaojie/tvbot/internal/store"
)

type fakeRegimeRepo struct {
	rec *store.MarketRegimeRecord
	err error
}

func (f *fakeRegimeRepo) Latest(ctx context.Context) (*store.MarketRegimeRecord, error) {
	return f.rec, f.err
}

type fakeCalendarSrc struct {
	events []calendar.Event
	err    error
}

func (f *fakeCalendarSrc) ActiveBetween(ctx context.Context, from, to time.Time) ([]calendar.Event, error) {
	return f.events, f.err
}

type fakeNewsRepo struct {
	rec *store.NewsSnapshotRecord
	err error
}

func (f *fakeNewsRepo) Latest(ctx context.Context) (*store.NewsSnapshotRecord, error) {
	return f.rec, f.err
}

type fakePerpRepo struct {
	bySymbol map[string]*store.PerpMetricsRecord
	err      error
}

func (f *fakePerpRepo) Latest(ctx context.Context, symbol string) (*store.PerpMetricsRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.bySymbol[symbol], nil
}

type fakePerpSettings struct{ lookback int }

func (f *fakePerpSettings) Get(ctx context.Context) (int, error) { return f.lookback, nil }

func TestReader_Load_HappyPath(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC)

	regimeRec := &store.MarketRegimeRecord{
		MeasuredAt:    now.Add(-12 * time.Minute),
		Label:         "range",
		TrendStrength: decimal.NewFromFloat(0.1),
		Volatility24h: decimal.NewFromFloat(0.01),
		VolPercentile: decimal.NewFromFloat(0.4),
		Change24hPct:  decimal.NewFromFloat(-1.5),
		PriceRangePos: decimal.NewFromFloat(0.55),
	}
	events := []calendar.Event{
		{Name: "CPI m/m", Currency: "USD", Impact: "High", StartsAt: now.Add(20 * time.Minute)},
	}
	perHeadline, _ := json.Marshal([]HeadlineJudgment{{Title: "A", Impact: "high"}})
	newsRec := &store.NewsSnapshotRecord{
		MeasuredAt:  now.Add(-5 * time.Minute),
		Impact:      "high",
		Summary:     "整体偏空",
		Reasoning:   "标题 0 属于 SEC 起诉",
		PerHeadline: perHeadline,
	}

	r := NewReader(&fakeRegimeRepo{rec: regimeRec},
		&fakeCalendarSrc{events: events},
		&fakeNewsRepo{rec: newsRec}).
		WithClock(func() time.Time { return now })

	got := r.Load(context.Background(), "BTCUSDT")
	if got.Regime == nil || got.Regime.Label != "range" {
		t.Fatalf("Regime: %+v", got.Regime)
	}
	if got.Regime.StaleMinutes != 12 {
		t.Errorf("StaleMinutes: %d", got.Regime.StaleMinutes)
	}
	if len(got.Events) != 1 || got.Events[0].MinutesTo != 20 {
		t.Errorf("Events: %+v", got.Events)
	}
	if got.Events[0].RelativeText != "还有 20 分钟" {
		t.Errorf("RelativeText: %q", got.Events[0].RelativeText)
	}
	if got.News == nil || got.News.Impact != "high" {
		t.Fatalf("News: %+v", got.News)
	}
	if len(got.News.PerHeadline) != 1 || got.News.PerHeadline[0].Title != "A" {
		t.Errorf("News.PerHeadline: %+v", got.News.PerHeadline)
	}
}

func TestReader_Load_RegimeMissing(t *testing.T) {
	r := NewReader(
		&fakeRegimeRepo{err: errors.New("ErrNoRows")},
		&fakeCalendarSrc{},
		&fakeNewsRepo{},
	)
	got := r.Load(context.Background(), "BTCUSDT")
	if got.Regime != nil {
		t.Errorf("Regime should be nil: %+v", got.Regime)
	}
	if len(got.Events) != 0 {
		t.Errorf("Events should be empty: %+v", got.Events)
	}
	if got.News != nil {
		t.Errorf("News should be nil: %+v", got.News)
	}
}

func TestReader_Load_PartialFailures(t *testing.T) {
	r := NewReader(
		&fakeRegimeRepo{rec: &store.MarketRegimeRecord{Label: "range", MeasuredAt: time.Now()}},
		&fakeCalendarSrc{err: errors.New("db")},
		&fakeNewsRepo{err: errors.New("no rows")},
	)
	got := r.Load(context.Background(), "BTCUSDT")
	if got.Regime == nil {
		t.Error("Regime should still be present despite other failures")
	}
	if len(got.Events) != 0 {
		t.Errorf("Events should be empty on error: %+v", got.Events)
	}
	if got.News != nil {
		t.Error("News should be nil on error")
	}
}

func TestReader_Load_NewsPerHeadlineInvalidJSON(t *testing.T) {
	rec := &store.NewsSnapshotRecord{
		MeasuredAt:  time.Now(),
		Impact:      "high",
		Summary:     "x",
		PerHeadline: []byte("not json"),
	}
	r := NewReader(&fakeRegimeRepo{err: errors.New("none")},
		&fakeCalendarSrc{},
		&fakeNewsRepo{rec: rec})
	got := r.Load(context.Background(), "BTCUSDT")
	if got.News == nil || got.News.Impact != "high" {
		t.Errorf("News should still be present: %+v", got.News)
	}
	if len(got.News.PerHeadline) != 0 {
		t.Errorf("PerHeadline should be empty on parse error: %+v", got.News.PerHeadline)
	}
}

func TestReader_Load_AllNilDependencies(t *testing.T) {
	r := NewReader(nil, nil, nil)
	got := r.Load(context.Background(), "BTCUSDT")
	if got.Regime != nil || got.Events != nil || got.News != nil {
		t.Errorf("all nil deps should yield zero MacroContext: %+v", got)
	}
	if got.PerpSelf != nil || got.PerpBTC != nil {
		t.Errorf("perp should be nil without WithPerp wired: %+v / %+v", got.PerpSelf, got.PerpBTC)
	}
}

func TestReader_Load_PerpWired_NonBTCSignal(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	perp := &fakePerpRepo{bySymbol: map[string]*store.PerpMetricsRecord{
		"ETHUSDT": {
			Symbol: "ETHUSDT", ObservedAt: now.Add(-3 * time.Minute),
			FundingRate:        decimal.NewFromFloat(0.00025),
			FundingLabel:       "mild_long",
			OpenInterest24hPct: decimal.NewFromFloat(3.2),
			OISignal:           "new_longs",
			TopLSRatio:         decimal.NewFromFloat(1.85),
			LSLabel:            "bullish",
		},
		"BTCUSDT": {
			Symbol: "BTCUSDT", ObservedAt: now.Add(-3 * time.Minute),
			FundingRate:  decimal.NewFromFloat(0.0001),
			FundingLabel: "neutral", OISignal: "neutral", LSLabel: "balanced",
		},
	}}
	r := NewReader(nil, nil, nil).
		WithPerp(perp, &fakePerpSettings{lookback: 30}).
		WithClock(func() time.Time { return now })

	got := r.Load(context.Background(), "ETHUSDT")
	if got.PerpSelf == nil || got.PerpSelf.Symbol != "ETHUSDT" {
		t.Fatalf("PerpSelf: %+v", got.PerpSelf)
	}
	if got.PerpSelf.FundingLabel != "mild_long" {
		t.Errorf("PerpSelf.FundingLabel = %q, want mild_long", got.PerpSelf.FundingLabel)
	}
	// FundingRate 0.00025 -> FundingRatePct 0.025
	if !got.PerpSelf.FundingRatePct.Equal(decimal.NewFromFloat(0.025)) {
		t.Errorf("FundingRatePct = %v, want 0.025", got.PerpSelf.FundingRatePct)
	}
	if got.PerpBTC == nil || got.PerpBTC.Symbol != "BTCUSDT" {
		t.Fatalf("PerpBTC: %+v", got.PerpBTC)
	}
	if got.PerpBTC == got.PerpSelf {
		t.Error("PerpBTC and PerpSelf must be different pointers for non-BTC signal")
	}
}

func TestReader_Load_PerpWired_BTCSignal_AliasedPointer(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	perp := &fakePerpRepo{bySymbol: map[string]*store.PerpMetricsRecord{
		"BTCUSDT": {
			Symbol: "BTCUSDT", ObservedAt: now,
			FundingRate:  decimal.NewFromFloat(0.0001),
			FundingLabel: "neutral", OISignal: "neutral", LSLabel: "balanced",
		},
	}}
	r := NewReader(nil, nil, nil).
		WithPerp(perp, &fakePerpSettings{lookback: 30}).
		WithClock(func() time.Time { return now })

	got := r.Load(context.Background(), "BTCUSDT")
	if got.PerpSelf == nil || got.PerpBTC == nil {
		t.Fatalf("PerpSelf / PerpBTC should be non-nil: %+v %+v", got.PerpSelf, got.PerpBTC)
	}
	if got.PerpSelf != got.PerpBTC {
		t.Errorf("PerpSelf and PerpBTC should be the same pointer for BTCUSDT signal")
	}
}

func TestReader_Load_PerpStaleBeyondLookback_ReturnsNil(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	perp := &fakePerpRepo{bySymbol: map[string]*store.PerpMetricsRecord{
		"BTCUSDT": {
			Symbol: "BTCUSDT", ObservedAt: now.Add(-2 * time.Hour),
			FundingLabel: "neutral", OISignal: "neutral", LSLabel: "balanced",
		},
	}}
	r := NewReader(nil, nil, nil).
		WithPerp(perp, &fakePerpSettings{lookback: 30}). // 30min < 2h
		WithClock(func() time.Time { return now })

	got := r.Load(context.Background(), "BTCUSDT")
	if got.PerpSelf != nil {
		t.Errorf("stale perp data should be treated as unavailable, got %+v", got.PerpSelf)
	}
}

func TestReader_Load_PerpRepoEmpty_FailOpen(t *testing.T) {
	now := time.Now().UTC()
	perp := &fakePerpRepo{bySymbol: map[string]*store.PerpMetricsRecord{}} // no rows
	r := NewReader(nil, nil, nil).
		WithPerp(perp, &fakePerpSettings{lookback: 30}).
		WithClock(func() time.Time { return now })
	got := r.Load(context.Background(), "BTCUSDT")
	if got.PerpSelf != nil || got.PerpBTC != nil {
		t.Error("empty perp repo should yield nil PerpSelf/PerpBTC (fail-open)")
	}
}
