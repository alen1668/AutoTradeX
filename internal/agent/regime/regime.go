// Package regime classifies the BTC market into one of five labels
// (trend_up/trend_down/range/crash/spike) from 1h K-line history.
// The classifier is a pure function over OHLC candles; the Worker
// goroutine schedules calls and persists results via the Repository
// interface.
package regime

import (
	"time"

	"github.com/shopspring/decimal"
)

// Result is the output of Classify. Returned with zero values when input
// is insufficient (Label == ""); callers must check.
type Result struct {
	Label         string          // trend_up|trend_down|range|crash|spike|""(empty)
	TrendStrength decimal.Decimal // 0~1
	Volatility24h decimal.Decimal
	VolPercentile decimal.Decimal // 0~1, relative to last 30 days (or all available)
	Change24hPct  decimal.Decimal
	PriceRangePos decimal.Decimal // 0~1, current close within 24h high-low
	KlineCount    int
	MeasuredAt    time.Time
}
