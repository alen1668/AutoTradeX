package regime

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/market"
)

// build168 generates a candle slice from a closes array. High/Low are close±0.1%.
func build168(closes []float64) []market.Candle {
	out := make([]market.Candle, len(closes))
	now := time.Now().UTC().Truncate(time.Hour)
	for i, c := range closes {
		cd := decimal.NewFromFloat(c)
		out[i] = market.Candle{
			OpenTime: now.Add(-time.Duration(len(closes)-1-i) * time.Hour),
			Open:     cd,
			High:     cd.Mul(decimal.NewFromFloat(1.001)),
			Low:      cd.Mul(decimal.NewFromFloat(0.999)),
			Close:    cd,
		}
	}
	return out
}

func TestClassify_Range_FlatPriceFlatVol(t *testing.T) {
	closes := make([]float64, 168)
	for i := range closes {
		closes[i] = 50000 + float64(i%5)
	}
	r := Classify(build168(closes))
	if r.Label != "range" {
		t.Errorf("flat -> Label = %q want range", r.Label)
	}
}

func TestClassify_TrendUp(t *testing.T) {
	closes := make([]float64, 168)
	// 0.2%/h compounded ≈ 40% over 168h, ~4-5% in last 24h. EMA-fast > EMA-slow.
	closes[0] = 50000
	for i := 1; i < 168; i++ {
		closes[i] = closes[i-1] * 1.002
	}
	r := Classify(build168(closes))
	if r.Label != "trend_up" {
		t.Errorf("rising -> Label = %q want trend_up (24h%%=%s emaStrength=%s)",
			r.Label, r.Change24hPct.String(), r.TrendStrength.String())
	}
	if r.TrendStrength.IsZero() {
		t.Error("trend_up should have non-zero TrendStrength")
	}
}

func TestClassify_TrendDown(t *testing.T) {
	closes := make([]float64, 168)
	closes[0] = 50000
	for i := 1; i < 168; i++ {
		closes[i] = closes[i-1] * 0.998
	}
	r := Classify(build168(closes))
	if r.Label != "trend_down" {
		t.Errorf("falling -> Label = %q want trend_down (24h%%=%s)", r.Label, r.Change24hPct.String())
	}
}

func TestClassify_Crash(t *testing.T) {
	closes := make([]float64, 168)
	for i := 0; i < 144; i++ {
		closes[i] = 50000
	}
	// last 24h: drop ~10% with high volatility (alternating spikes around a descending trend).
	for i := 144; i < 168; i++ {
		k := float64(i - 144)
		base := 50000 - k*220
		if i%2 == 0 {
			closes[i] = base + 300
		} else {
			closes[i] = base - 300
		}
	}
	r := Classify(build168(closes))
	if r.Label != "crash" {
		t.Errorf("-10%% 24h with high vol -> Label = %q want crash (vol_pctl=%s)", r.Label, r.VolPercentile.String())
	}
}

func TestClassify_Spike(t *testing.T) {
	closes := make([]float64, 168)
	for i := 0; i < 167; i++ {
		closes[i] = 50000 + float64(i%5)
	}
	closes[167] = 52000 // last bar: +4%
	r := Classify(build168(closes))
	if r.Label != "spike" {
		t.Errorf("last-1h spike -> Label = %q want spike", r.Label)
	}
}

func TestClassify_EmptyInput(t *testing.T) {
	r := Classify(nil)
	if r.Label != "" || r.KlineCount != 0 {
		t.Errorf("empty input should yield empty Result: %+v", r)
	}
}

func TestClassify_TooShortInput(t *testing.T) {
	closes := make([]float64, 10)
	for i := range closes {
		closes[i] = 50000
	}
	r := Classify(build168(closes))
	if r.Label != "" {
		t.Errorf("too-short input should yield empty Result, got Label=%q", r.Label)
	}
}

func TestClassify_VolPercentileInRange(t *testing.T) {
	closes := make([]float64, 200)
	for i := range closes {
		closes[i] = 50000 + float64(i%5)
	}
	r := Classify(build168(closes))
	if r.VolPercentile.LessThan(decimal.Zero) || r.VolPercentile.GreaterThan(decimal.NewFromInt(1)) {
		t.Errorf("VolPercentile %s out of [0,1]", r.VolPercentile.String())
	}
}
