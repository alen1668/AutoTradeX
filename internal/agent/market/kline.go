package market

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// KlineClient abstracts two K-line fetching shapes:
// (a) "last N 1h closes" (cheap, used by the scorer's per-symbol MarketContext)
// (b) "last N 1h OHLC candles" (used by the regime worker that needs high/low)
// The production implementation in binance_kline.go wraps adshao/go-binance.
type KlineClient interface {
	Get1hCloses(ctx context.Context, symbol string, limit int) ([]decimal.Decimal, error)
	Get1hOHLC(ctx context.Context, symbol string, limit int) ([]Candle, error)
}

// Candle is one 1h OHLC bar. OpenTime is the bar's open boundary in UTC.
type Candle struct {
	OpenTime time.Time
	Open     decimal.Decimal
	High     decimal.Decimal
	Low      decimal.Decimal
	Close    decimal.Decimal
}

// MarketContext mirrors scorer.MarketContext field-for-field. Duplicated
// here so the market package has no dependency on scorer (avoids an
// import cycle when scorer needs market). The ingest hook copies values
// across when assembling ScoreInput.
type MarketContext struct {
	Symbol           string
	Last24hHigh      decimal.Decimal
	Last24hLow       decimal.Decimal
	Last24hChangePct decimal.Decimal
	Last1hChangePct  decimal.Decimal
	PriceVs24hRange  decimal.Decimal
	Volatility24h    decimal.Decimal
	KlineLookback1h  []decimal.Decimal
}

// Provider exposes GetContext, the callable that returns a MarketContext
// for a symbol. It caches per-symbol for `ttl` (typically 30s) to avoid
// hammering the exchange when many strategies fire on the same symbol.
type Provider struct {
	client KlineClient
	ttl    time.Duration
	clk    clock
	log    zerolog.Logger

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	at  time.Time
	ctx *MarketContext
}

func NewProvider(client KlineClient, ttl time.Duration) *Provider {
	return NewProviderWithClock(client, ttl, realClock{})
}

func NewProviderWithClock(client KlineClient, ttl time.Duration, c clock) *Provider {
	return &Provider{client: client, ttl: ttl, clk: c, cache: make(map[string]cacheEntry)}
}

// WithLogger sets a logger for failure paths (kline fetch errors are
// currently logged at warn — they're recoverable, the prompt just shows
// "市场数据暂不可用").
func (p *Provider) WithLogger(l zerolog.Logger) *Provider {
	p.log = l
	return p
}

// GetContext fetches 24 1h closes and computes 24h range / change /
// volatility. Failure returns (nil, nil) — the agent layer is degradable:
// the prompt will note "市场数据暂不可用" and the LLM scores from the
// remaining inputs.
func (p *Provider) GetContext(ctx context.Context, symbol string) (*MarketContext, error) {
	p.mu.Lock()
	if e, ok := p.cache[symbol]; ok && p.clk.Now().Sub(e.at) < p.ttl {
		p.mu.Unlock()
		return e.ctx, nil
	}
	p.mu.Unlock()

	closes, err := p.client.Get1hCloses(ctx, symbol, 24)
	if err != nil || len(closes) == 0 {
		p.log.Warn().Err(err).Str("symbol", symbol).Msg("market: kline fetch failed; returning nil context")
		return nil, nil
	}
	out := computeContext(symbol, closes)

	p.mu.Lock()
	p.cache[symbol] = cacheEntry{at: p.clk.Now(), ctx: out}
	p.mu.Unlock()
	return out, nil
}

func computeContext(symbol string, closes []decimal.Decimal) *MarketContext {
	high := closes[0]
	low := closes[0]
	for _, c := range closes {
		if c.GreaterThan(high) {
			high = c
		}
		if c.LessThan(low) {
			low = c
		}
	}
	last := closes[len(closes)-1]
	first := closes[0]
	prev1h := first
	if len(closes) >= 2 {
		prev1h = closes[len(closes)-2]
	}

	rng := high.Sub(low)
	var posInRange decimal.Decimal
	if rng.IsZero() {
		posInRange = decimal.NewFromFloat(0.5)
	} else {
		posInRange = last.Sub(low).Div(rng).Round(4)
	}

	return &MarketContext{
		Symbol:           symbol,
		Last24hHigh:      high,
		Last24hLow:       low,
		Last24hChangePct: percentChange(first, last),
		Last1hChangePct:  percentChange(prev1h, last),
		PriceVs24hRange:  posInRange,
		Volatility24h:    volatilityRel(closes),
		KlineLookback1h:  closes,
	}
}

func percentChange(from, to decimal.Decimal) decimal.Decimal {
	if from.IsZero() {
		return decimal.Zero
	}
	return to.Sub(from).Div(from).Mul(decimal.NewFromInt(100)).Round(4)
}

// volatilityRel returns std(closes) / mean(closes), rounded to 4 places.
// Goes via float64 for the sqrt — precision is plenty for an LLM hint
// and the alternative (decimal sqrt) is needlessly heavy.
func volatilityRel(closes []decimal.Decimal) decimal.Decimal {
	n := len(closes)
	if n < 2 {
		return decimal.Zero
	}
	sum := decimal.Zero
	for _, c := range closes {
		sum = sum.Add(c)
	}
	mean := sum.Div(decimal.NewFromInt(int64(n)))
	if mean.IsZero() {
		return decimal.Zero
	}
	meanF, _ := mean.Float64()
	var sqsum float64
	for _, c := range closes {
		f, _ := c.Float64()
		d := f - meanF
		sqsum += d * d
	}
	std := math.Sqrt(sqsum / float64(n))
	return decimal.NewFromFloat(std / meanF).Round(4)
}

// clock abstracts time.Now for cache expiry tests.
type clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
