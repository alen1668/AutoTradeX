package market

import (
	"testing"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"
)

func TestKlineToCandle_OHLC(t *testing.T) {
	bar := &futures.Kline{
		OpenTime: 1715731200000, // 2024-05-15 00:00 UTC in ms
		Open:     "100.5",
		High:     "111.0",
		Low:      "95.25",
		Close:    "108.75",
	}
	c, err := klineToCandle(bar)
	if err != nil {
		t.Fatalf("klineToCandle: %v", err)
	}
	if !c.High.Equal(decimal.RequireFromString("111")) {
		t.Errorf("High: %s", c.High.String())
	}
	if !c.Low.Equal(decimal.RequireFromString("95.25")) {
		t.Errorf("Low: %s", c.Low.String())
	}
	if c.OpenTime.Unix() != 1715731200 {
		t.Errorf("OpenTime unix: %d", c.OpenTime.Unix())
	}
}
