package regime

import (
	"math"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/market"
)

// Tunable thresholds. Tune via code + redeploy.
const (
	spike1hAbsThreshold       = 0.03 // |1h return| >= 3%
	spikeVolPctlMin           = 0.90
	crash24hReturnThreshold   = -0.08 // -8%
	crashVolPctlMin           = 0.80
	trendUpReturnMin          = 0.03  // +3%
	trendUpEmaSlopeMin        = 0.02
	trendDownReturnMax        = -0.03 // -3%
	trendDownEmaSlopeMax      = -0.02
	trendStrengthFullDeviance = 0.05 // 5% EMA gap = full strength
	emaFastPeriod             = 12
	emaSlowPeriod             = 48
)

// Classify produces a Result from a chronologically ordered candle slice.
// candles[len-1] is the most recent bar. Empty or short input -> Result{Label: ""}.
func Classify(candles []market.Candle) Result {
	n := len(candles)
	if n < 25 {
		return Result{}
	}
	closes := make([]float64, n)
	for i, c := range candles {
		f, _ := c.Close.Float64()
		closes[i] = f
	}
	return classifyFloats(candles, closes)
}

func classifyFloats(candles []market.Candle, closes []float64) Result {
	n := len(closes)
	last := closes[n-1]
	prev1h := closes[n-2]
	prev24h := closes[n-25]
	return1h := (last - prev1h) / prev1h
	return24h := (last - prev24h) / prev24h

	high24h, low24h := candles[n-24].High, candles[n-24].Low
	for i := n - 23; i < n; i++ {
		if candles[i].High.GreaterThan(high24h) {
			high24h = candles[i].High
		}
		if candles[i].Low.LessThan(low24h) {
			low24h = candles[i].Low
		}
	}
	closeDec := candles[n-1].Close
	var rangePos decimal.Decimal
	if !high24h.Equal(low24h) {
		rangePos = closeDec.Sub(low24h).Div(high24h.Sub(low24h))
	} else {
		rangePos = decimal.NewFromFloat(0.5)
	}
	if rangePos.LessThan(decimal.Zero) {
		rangePos = decimal.Zero
	} else if rangePos.GreaterThan(decimal.NewFromInt(1)) {
		rangePos = decimal.NewFromInt(1)
	}

	vol24h := stddevReturns(closes[n-25:])

	// vol_30d window: as many as we have (cap at 720).
	volWindow := n
	if volWindow > 720 {
		volWindow = 720
	}
	volPctl := percentileRank(vol24h, rollingVolatilities(closes, 24, volWindow))

	emaFast := ema(closes, emaFastPeriod)
	emaSlow := ema(closes, emaSlowPeriod)
	var emaGap float64
	if emaSlow != 0 {
		emaGap = (emaFast - emaSlow) / emaSlow
	}

	res := Result{
		Volatility24h: decimal.NewFromFloat(vol24h),
		VolPercentile: decimal.NewFromFloat(volPctl),
		Change24hPct:  decimal.NewFromFloat(return24h * 100),
		PriceRangePos: rangePos,
		KlineCount:    n,
	}
	strength := math.Abs(emaGap) / trendStrengthFullDeviance
	if strength > 1 {
		strength = 1
	}
	res.TrendStrength = decimal.NewFromFloat(strength)

	switch {
	case math.Abs(return1h) >= spike1hAbsThreshold && volPctl >= spikeVolPctlMin:
		res.Label = "spike"
	case return24h <= crash24hReturnThreshold && volPctl >= crashVolPctlMin:
		res.Label = "crash"
	case emaGap >= trendUpEmaSlopeMin && return24h >= trendUpReturnMin:
		res.Label = "trend_up"
	case emaGap <= trendDownEmaSlopeMax && return24h <= trendDownReturnMax:
		res.Label = "trend_down"
	default:
		res.Label = "range"
	}
	return res
}

func stddevReturns(closes []float64) float64 {
	if len(closes) < 2 {
		return 0
	}
	rets := make([]float64, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		if closes[i-1] == 0 {
			rets[i-1] = 0
			continue
		}
		rets[i-1] = (closes[i] - closes[i-1]) / closes[i-1]
	}
	var mean float64
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	var sumsq float64
	for _, r := range rets {
		d := r - mean
		sumsq += d * d
	}
	return math.Sqrt(sumsq / float64(len(rets)))
}

// rollingVolatilities computes window-size 24 rolling vol on the last `lookback` bars.
func rollingVolatilities(closes []float64, window, lookback int) []float64 {
	if lookback < window+1 {
		lookback = window + 1
	}
	if lookback > len(closes) {
		lookback = len(closes)
	}
	out := make([]float64, 0, lookback-window)
	for end := window + 1; end <= lookback; end++ {
		seg := closes[len(closes)-end : len(closes)-end+window+1]
		out = append(out, stddevReturns(seg))
	}
	return out
}

// percentileRank returns the fraction of elements in vols <= v. Range [0,1].
func percentileRank(v float64, vols []float64) float64 {
	if len(vols) == 0 {
		return 0.5
	}
	sorted := append([]float64(nil), vols...)
	sort.Float64s(sorted)
	le := 0
	for _, x := range sorted {
		if x <= v {
			le++
		}
	}
	return float64(le) / float64(len(sorted))
}

func ema(values []float64, period int) float64 {
	if len(values) == 0 {
		return 0
	}
	k := 2.0 / float64(period+1)
	e := values[0]
	for i := 1; i < len(values); i++ {
		e = values[i]*k + e*(1-k)
	}
	return e
}
