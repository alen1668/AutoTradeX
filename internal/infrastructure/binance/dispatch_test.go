package binance

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"github.com/lizhaojie/tvbot/internal/trade"
)

func decimalParse(s string) (decimal.Decimal, error) { return decimal.NewFromString(s) }

func TestIsAlgoType(t *testing.T) {
	cases := map[trade.OrderType]bool{
		trade.OrderTypeMarket:           false,
		trade.OrderTypeStop:             true,
		trade.OrderTypeStopMarket:       true,
		trade.OrderTypeTakeProfitMarket: true,
	}
	for typ, want := range cases {
		t.Run(string(typ), func(t *testing.T) {
			assert.Equal(t, want, isAlgoType(typ))
		})
	}
}

func TestQuantizeToTick(t *testing.T) {
	cases := []struct {
		v, tick, want string
	}{
		{"2319.6777502", "0.01", "2319.67"},
		{"2321.99975", "0.01", "2321.99"},
		{"2350", "0.01", "2350"},
		{"43210.55555", "0.10", "43210.5"},
		{"100.123456789", "0.0001", "100.1234"},
		// tick=0 means no quantization (used when symbol unknown)
		{"100.123456789", "0", "100.123456789"},
	}
	for _, c := range cases {
		t.Run(c.v+"@"+c.tick, func(t *testing.T) {
			v, _ := decimalParse(c.v)
			tick, _ := decimalParse(c.tick)
			got := quantizeToTick(v, tick)
			assert.Equal(t, c.want, got.String())
		})
	}
}

func TestIsAlgoClientOrderID(t *testing.T) {
	// Prefix convention from application/trade/service.go:
	//   entry-{trace}-{vpID}        → MARKET (regular)
	//   exit-{trace}-{vpID}         → MARKET (regular)
	//   stop-{trace}-{vpID}         → STOP (algo)
	//   backup_stop-{trace}-{vpID}  → STOP_MARKET (algo)
	//   take_profit-{trace}-{vpID}  → TAKE_PROFIT_MARKET (algo)
	cases := map[string]bool{
		"entry-tv-foo-42": false,
		"exit-tv-foo-42":  false,
		"stop-tv-foo-42":  true,
		"bstop-tv-foo-42": true,
		"tp-tv-foo-42":    true,
		"":                false,
	}
	for id, want := range cases {
		t.Run(id, func(t *testing.T) {
			assert.Equal(t, want, isAlgoClientOrderID(id))
		})
	}
}
