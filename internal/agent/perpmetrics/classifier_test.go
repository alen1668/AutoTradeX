package perpmetrics_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/perpmetrics"
)

func dec(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }

func TestFundingLabel(t *testing.T) {
	cases := []struct {
		rate float64
		want string
	}{
		{0.001, "extreme_long"},     // 0.1%
		{0.0005001, "extreme_long"}, // 边界右
		{0.0005, "mild_long"},       // 边界 — 0.05% 算 mild
		{0.0002, "mild_long"},
		{0.0001, "neutral"}, // 0.01% 边界 — 算 neutral
		{0, "neutral"},
		{-0.0001, "neutral"},
		{-0.0002, "mild_short"},
		{-0.0005, "mild_short"}, // 边界 — -0.05% 算 mild
		{-0.0005001, "extreme_short"},
		{-0.001, "extreme_short"},
	}
	for _, tc := range cases {
		got := perpmetrics.FundingLabel(dec(tc.rate))
		if got != tc.want {
			t.Errorf("FundingLabel(%v) = %q, want %q", tc.rate, got, tc.want)
		}
	}
}

func TestOISignal(t *testing.T) {
	cases := []struct {
		oi24, price24 float64
		want          string
	}{
		{0, 5, "neutral"},      // OI 无变化
		{2.9, 5, "neutral"},    // |OI| < 3%
		{3, 0.3, "neutral"},    // |price| ≤ 0.5%, 视为横盘
		{3, 0.5, "neutral"},    // |price| = 0.5% (边界, 不视为有方向)
		{5, 2, "new_longs"},    // 都上
		{5, -2, "new_shorts"},  // OI 上、价格下
		{-5, 2, "short_squeeze"},
		{-5, -2, "capitulation"},
	}
	for _, tc := range cases {
		got := perpmetrics.OISignal(dec(tc.oi24), dec(tc.price24))
		if got != tc.want {
			t.Errorf("OISignal(oi=%v price=%v) = %q, want %q",
				tc.oi24, tc.price24, got, tc.want)
		}
	}
}

func TestLSLabel(t *testing.T) {
	cases := []struct {
		ratio float64
		want  string
	}{
		{3.0, "strongly_bullish"},
		{2.01, "strongly_bullish"},
		{2.0, "bullish"}, // 边界 — = 2.0 算 bullish
		{1.5, "bullish"},
		{1.3, "balanced"}, // 边界 — = 1.3 算 balanced
		{1.0, "balanced"},
		{0.7, "balanced"}, // 边界 — = 0.7 算 balanced
		{0.69, "bearish"},
		{0.5, "bearish"}, // 边界 — = 0.5 算 bearish
		{0.49, "strongly_bearish"},
		{0.3, "strongly_bearish"},
	}
	for _, tc := range cases {
		got := perpmetrics.LSLabel(dec(tc.ratio))
		if got != tc.want {
			t.Errorf("LSLabel(%v) = %q, want %q", tc.ratio, got, tc.want)
		}
	}
}
