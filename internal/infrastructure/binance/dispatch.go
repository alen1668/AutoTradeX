package binance

import (
	"strings"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/trade"
)

// quantizeToTick floors v to the nearest multiple of tick. Mirrors the
// LOT_SIZE flooring done for quantities — Binance rejects prices not aligned
// to PRICE_FILTER.tickSize with -1111. tick==0 means "no info, leave alone".
func quantizeToTick(v, tick decimal.Decimal) decimal.Decimal {
	if tick.IsZero() {
		return v
	}
	return v.Div(tick).Floor().Mul(tick)
}

// isAlgoType reports whether the order type must be placed via the Binance
// Algo Order endpoint (/fapi/v1/algo/futures/newOrder) rather than the
// regular order endpoint (/fapi/v1/order). On the new demo.binance.com
// platform — and on production accounts in Multi-Asset / Portfolio Margin
// mode — conditional orders (STOP, STOP_MARKET, TAKE_PROFIT_MARKET) are
// rejected with -4120 on the regular endpoint and must use Algo.
func isAlgoType(t trade.OrderType) bool {
	switch t {
	case trade.OrderTypeStop,
		trade.OrderTypeStopMarket,
		trade.OrderTypeTakeProfitMarket:
		return true
	}
	return false
}

// isAlgoClientOrderID infers from the clientOrderID prefix whether an order
// was placed via the Algo endpoint, so Cancel/GetOrder dispatch to the
// matching API. Prefix convention is owned by application/trade/service.go;
// "bstop"/"tp" are the short forms required to fit Binance's 35-char limit.
func isAlgoClientOrderID(clientOrderID string) bool {
	return strings.HasPrefix(clientOrderID, "stop-") ||
		strings.HasPrefix(clientOrderID, "bstop-") ||
		strings.HasPrefix(clientOrderID, "tp-")
}
