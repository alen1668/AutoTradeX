// Package perpmetrics fetches binance perpetual contract metrics
// (funding rate / open interest / top-trader long-short ratio), classifies
// each into a discrete label, and persists snapshots. Read path lives in
// internal/agent/macrocontext.
package perpmetrics

import (
	"time"

	"github.com/shopspring/decimal"
)

// Snapshot is one observation of a symbol's perp metrics. The worker writes
// one row of this per (symbol, observed_at) into perp_metrics.
type Snapshot struct {
	Symbol     string
	ObservedAt time.Time

	FundingRate        decimal.Decimal // 0.00025 == 0.025%
	NextFundingTime    time.Time
	MarkPrice          decimal.Decimal
	OpenInterest       decimal.Decimal
	OpenInterest24hPct decimal.Decimal // % change vs ~24h ago snapshot
	Price24hPct        decimal.Decimal // % 24h price change
	TopLSRatio         decimal.Decimal

	FundingLabel string
	OISignal     string
	LSLabel      string
}
