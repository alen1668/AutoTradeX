package admin

import (
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/trade"
)

// IncomeRecord and IncomeFetcher were moved to internal/trade so that
// non-web callers (e.g. recovery) can also consume them. Aliased here
// to keep admin's public API (and existing tests) unchanged.
type IncomeRecord = trade.IncomeRecord
type IncomeFetcher = trade.IncomeFetcher

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

// aggregateIncomeBySymbol returns netIncome[day][symbol] — the sum of
// REALIZED_PNL + COMMISSION + FUNDING_FEE per (day, symbol). Used to drive
// the stacked bar chart on /stats.
func aggregateIncomeBySymbol(records []IncomeRecord) map[time.Time]map[string]decimal.Decimal {
	out := make(map[time.Time]map[string]decimal.Decimal)
	for _, r := range records {
		if r.Symbol == "" {
			continue // skip account-level events that aren't tied to a symbol
		}
		day := time.Date(r.Time.Year(), r.Time.Month(), r.Time.Day(), 0, 0, 0, 0, time.UTC)
		if out[day] == nil {
			out[day] = make(map[string]decimal.Decimal)
		}
		out[day][r.Symbol] = out[day][r.Symbol].Add(r.Income)
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
