package admin

import (
	"context"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

// IncomeRecord is the exchange-agnostic shape of one income event from the
// account: a realized P&L line, a commission charge, or a funding payment.
// The admin pkg owns this type so it doesn't pull in the Binance SDK.
type IncomeRecord struct {
	Type   string          // "REALIZED_PNL" | "COMMISSION" | "FUNDING_FEE" | other
	Income decimal.Decimal // signed; commission is negative, realized P&L can be either
	Symbol string
	Time   time.Time // UTC
}

// IncomeFetcher fetches income events from the exchange for a time window.
// BinanceTrader implements it; in dry_run mode no fetcher is wired and the
// stats handler falls back to DB-only.
type IncomeFetcher interface {
	Income(ctx context.Context, since, until time.Time) ([]IncomeRecord, error)
}

// IncomeDaily is the per-day aggregate used by the /stats page when the
// fetcher is available. NetIncome = RealizedPnL + Commission + FundingFee.
type IncomeDaily struct {
	Date        time.Time
	RealizedPnL decimal.Decimal
	Commission  decimal.Decimal
	FundingFee  decimal.Decimal
	NetIncome   decimal.Decimal
}

// aggregateIncome buckets raw records into UTC-day totals, summing each
// income type. Records with unknown types are still included in NetIncome
// so the displayed P&L matches Binance's own Income History page total.
func aggregateIncome(records []IncomeRecord) map[time.Time]IncomeDaily {
	out := make(map[time.Time]IncomeDaily)
	for _, r := range records {
		day := time.Date(r.Time.Year(), r.Time.Month(), r.Time.Day(), 0, 0, 0, 0, time.UTC)
		d := out[day]
		d.Date = day
		switch r.Type {
		case "REALIZED_PNL":
			d.RealizedPnL = d.RealizedPnL.Add(r.Income)
		case "COMMISSION":
			d.Commission = d.Commission.Add(r.Income)
		case "FUNDING_FEE":
			d.FundingFee = d.FundingFee.Add(r.Income)
		}
		d.NetIncome = d.NetIncome.Add(r.Income)
		out[day] = d
	}
	return out
}

// incomeCache memoizes a (since, until) lookup for `ttl` to avoid hammering
// the exchange on every /stats page load.
type incomeCache struct {
	ttl   time.Duration
	mu    sync.Mutex
	at    time.Time
	since time.Time
	until time.Time
	data  []IncomeRecord
}

func newIncomeCache(ttl time.Duration) *incomeCache {
	return &incomeCache{ttl: ttl}
}

func (c *incomeCache) get(since, until time.Time) ([]IncomeRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) > c.ttl {
		return nil, false
	}
	if !c.since.Equal(since) || !c.until.Equal(until) {
		return nil, false
	}
	return c.data, true
}

func (c *incomeCache) set(since, until time.Time, data []IncomeRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = time.Now()
	c.since = since
	c.until = until
	c.data = data
}
