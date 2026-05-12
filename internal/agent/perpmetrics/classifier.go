package perpmetrics

import "github.com/shopspring/decimal"

// Thresholds tuned for binance USDT-M perpetuals (v1 rules). Adjust per-PR
// — boundaries match the test cases in classifier_test.go.
var (
	fundingExtremeLong  = decimal.NewFromFloat(0.0005)  // > 0.05% → extreme_long
	fundingMildLong     = decimal.NewFromFloat(0.0001)  // > 0.01% → mild_long
	fundingMildShort    = decimal.NewFromFloat(-0.0001) // < -0.01% → mild_short
	fundingExtremeShort = decimal.NewFromFloat(-0.0005) // < -0.05% → extreme_short

	// OI signal cross-references OI 24h % and price 24h %. Both must move
	// "significantly" (|OI| ≥ 3%, |price| > 0.5%) to escape "neutral".
	oiSignificantPct = decimal.NewFromFloat(3.0)
	priceMovedPct    = decimal.NewFromFloat(0.5)

	lsStronglyBullish = decimal.NewFromFloat(2.0)
	lsBullish         = decimal.NewFromFloat(1.3)
	lsBearish         = decimal.NewFromFloat(0.7)
	lsStronglyBearish = decimal.NewFromFloat(0.5)
)

// FundingLabel buckets the funding rate. Boundary 0.05% / 0.01% / -0.01% /
// -0.05% all fall into the LOWER-intensity bucket (mild/neutral), matching
// the v1 spec.
func FundingLabel(rate decimal.Decimal) string {
	switch {
	case rate.GreaterThan(fundingExtremeLong):
		return "extreme_long"
	case rate.GreaterThan(fundingMildLong):
		return "mild_long"
	case rate.LessThan(fundingExtremeShort):
		return "extreme_short"
	case rate.LessThan(fundingMildShort):
		return "mild_short"
	default:
		return "neutral"
	}
}

// OISignal cross-references OI 24h % and price 24h %. Both must move
// significantly (|OI| ≥ 3%, |price| > 0.5%) to escape "neutral".
func OISignal(oi24hPct, price24hPct decimal.Decimal) string {
	if oi24hPct.Abs().LessThan(oiSignificantPct) {
		return "neutral"
	}
	if price24hPct.Abs().LessThanOrEqual(priceMovedPct) {
		return "neutral"
	}
	priceUp := price24hPct.IsPositive()
	oiUp := oi24hPct.IsPositive()
	switch {
	case priceUp && oiUp:
		return "new_longs"
	case priceUp && !oiUp:
		return "short_squeeze"
	case !priceUp && oiUp:
		return "new_shorts"
	default:
		return "capitulation"
	}
}

// LSLabel buckets top-trader long/short ratio. Boundary values (2.0, 1.3,
// 0.7, 0.5) fall into the LOWER bucket.
func LSLabel(ratio decimal.Decimal) string {
	switch {
	case ratio.GreaterThan(lsStronglyBullish):
		return "strongly_bullish"
	case ratio.GreaterThan(lsBullish):
		return "bullish"
	case ratio.GreaterThanOrEqual(lsBearish):
		return "balanced"
	case ratio.GreaterThanOrEqual(lsStronglyBearish):
		return "bearish"
	default:
		return "strongly_bearish"
	}
}
