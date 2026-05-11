package market

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubKlineClient struct {
	closes []decimal.Decimal
	ohlc   []Candle
	err    error
	calls  int
}

func (s *stubKlineClient) Get1hCloses(ctx context.Context, symbol string, limit int) ([]decimal.Decimal, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	if limit > len(s.closes) {
		limit = len(s.closes)
	}
	return s.closes[len(s.closes)-limit:], nil
}

func (s *stubKlineClient) Get1hOHLC(ctx context.Context, symbol string, limit int) ([]Candle, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	if limit > len(s.ohlc) {
		limit = len(s.ohlc)
	}
	return s.ohlc[len(s.ohlc)-limit:], nil
}

// 24 linearly-rising closes 2280..2303
func generateCloses() []decimal.Decimal {
	out := make([]decimal.Decimal, 24)
	for i := 0; i < 24; i++ {
		out[i] = decimal.NewFromInt(int64(2280 + i))
	}
	return out
}

type fakeMarketClock struct{ now time.Time }

func (c *fakeMarketClock) Now() time.Time { return c.now }

func TestProvider_GetContext_Basics(t *testing.T) {
	c := &stubKlineClient{closes: generateCloses()}
	p := NewProvider(c, 30*time.Second)
	ctx, err := p.GetContext(context.Background(), "ETHUSDC")
	require.NoError(t, err)
	require.NotNil(t, ctx)
	assert.Equal(t, "ETHUSDC", ctx.Symbol)
	assert.True(t, ctx.Last24hHigh.GreaterThan(ctx.Last24hLow))
	assert.Len(t, ctx.KlineLookback1h, 24)
}

func TestProvider_GetContext_24hChangePct(t *testing.T) {
	c := &stubKlineClient{closes: generateCloses()}
	p := NewProvider(c, 30*time.Second)
	ctx, _ := p.GetContext(context.Background(), "ETHUSDC")
	// first=2280, last=2303 → change ≈ (23/2280)*100 ≈ 1.0088%
	expected := decimal.NewFromFloat(1.0088)
	diff := ctx.Last24hChangePct.Sub(expected).Abs()
	assert.True(t, diff.LessThan(decimal.NewFromFloat(0.01)),
		"24h change should be ~1.01%%, got %s", ctx.Last24hChangePct)
}

func TestProvider_GetContext_PriceVs24hRange(t *testing.T) {
	c := &stubKlineClient{closes: generateCloses()}
	p := NewProvider(c, 30*time.Second)
	ctx, _ := p.GetContext(context.Background(), "ETHUSDC")
	// last=2303, range [2280, 2303], position = 1.0
	assert.True(t, ctx.PriceVs24hRange.Sub(decimal.NewFromInt(1)).Abs().LessThan(decimal.NewFromFloat(0.01)))
}

func TestProvider_GetContext_FailureReturnsNilNoErr(t *testing.T) {
	c := &stubKlineClient{err: errors.New("connection refused")}
	p := NewProvider(c, 30*time.Second)
	ctx, err := p.GetContext(context.Background(), "ETHUSDC")
	assert.NoError(t, err, "fetch failure must NOT bubble up")
	assert.Nil(t, ctx, "fetch failure must produce nil context")
}

func TestProvider_Cache_HitWithinTTL(t *testing.T) {
	c := &stubKlineClient{closes: generateCloses()}
	p := NewProvider(c, 30*time.Second)
	_, _ = p.GetContext(context.Background(), "ETHUSDC")
	_, _ = p.GetContext(context.Background(), "ETHUSDC")
	assert.Equal(t, 1, c.calls, "second call within TTL should hit cache")
}

func TestProvider_Cache_MissAfterTTL(t *testing.T) {
	c := &stubKlineClient{closes: generateCloses()}
	clk := &fakeMarketClock{now: time.Now()}
	p := NewProviderWithClock(c, 30*time.Second, clk)
	_, _ = p.GetContext(context.Background(), "ETHUSDC")
	clk.now = clk.now.Add(31 * time.Second)
	_, _ = p.GetContext(context.Background(), "ETHUSDC")
	assert.Equal(t, 2, c.calls)
}

func TestProvider_Cache_DistinctSymbols(t *testing.T) {
	c := &stubKlineClient{closes: generateCloses()}
	p := NewProvider(c, 30*time.Second)
	_, _ = p.GetContext(context.Background(), "ETHUSDC")
	_, _ = p.GetContext(context.Background(), "BTCUSDC")
	assert.Equal(t, 2, c.calls, "different symbols must not share cache")
}

func TestProvider_GetContext_FlatPricesPositionMidpoint(t *testing.T) {
	flat := make([]decimal.Decimal, 24)
	for i := range flat {
		flat[i] = decimal.NewFromInt(2300)
	}
	c := &stubKlineClient{closes: flat}
	p := NewProvider(c, 30*time.Second)
	ctx, _ := p.GetContext(context.Background(), "ETHUSDC")
	// range is zero → position defaults to 0.5
	assert.True(t, ctx.PriceVs24hRange.Sub(decimal.NewFromFloat(0.5)).Abs().LessThan(decimal.NewFromFloat(0.001)))
	assert.True(t, ctx.Volatility24h.IsZero())
}
