package trade

import (
	"context"

	"github.com/shopspring/decimal"
)

type OrderSide string

const (
	OrderSideBuy  OrderSide = "BUY"
	OrderSideSell OrderSide = "SELL"
)

type OrderType string

const (
	OrderTypeMarket           OrderType = "MARKET"
	OrderTypeStop             OrderType = "STOP"        // limit-stop
	OrderTypeStopMarket       OrderType = "STOP_MARKET" // market-stop
	OrderTypeTakeProfitMarket OrderType = "TAKE_PROFIT_MARKET"
)

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusSubmitted OrderStatus = "submitted"
	OrderStatusFilled    OrderStatus = "filled"
	OrderStatusCanceled  OrderStatus = "canceled"
	OrderStatusRejected  OrderStatus = "rejected"
)

type OrderRequest struct {
	ClientOrderID  string
	Symbol         string
	Side           OrderSide
	Type           OrderType
	Qty            decimal.Decimal
	Price          decimal.Decimal // for STOP (limit price) — optional
	StopPrice      decimal.Decimal // trigger — for STOP / STOP_MARKET / TAKE_PROFIT_MARKET
	ReferencePrice decimal.Decimal // tells DryRun what price to fill at; live impl ignores
	Purpose        string          // "entry"|"exit"|"stop"|"backup_stop"|"take_profit"
}

type OrderResult struct {
	ClientOrderID   string
	ExchangeOrderID string
	Status          OrderStatus
	FilledQty       decimal.Decimal
	AvgFillPrice    decimal.Decimal
	FeesUSDC        decimal.Decimal
}

// Position represents the trader's view of a current symbol position.
type Position struct {
	Symbol     string
	Qty        decimal.Decimal // signed: positive = long, negative = short, zero = flat
	EntryPrice decimal.Decimal
}

// Trader is the port for sending orders. Adapters: DryRunTrader, BinanceTrader.
type Trader interface {
	Place(ctx context.Context, req OrderRequest) (*OrderResult, error)
	Cancel(ctx context.Context, symbol, clientOrderID string) error
	GetOrder(ctx context.Context, symbol, clientOrderID string) (*OrderResult, error)
	GetPositionRisk(ctx context.Context, symbol string) (*Position, error)
}
