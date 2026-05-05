// Package binance provides a Binance USDT-M perpetual futures adapter that
// satisfies the trade.Trader port.
package binance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	bn "github.com/adshao/go-binance/v2"
	"github.com/adshao/go-binance/v2/futures"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/config"
	"github.com/lizhaojie/tvbot/internal/trade"
)

// Trader implements trade.Trader against Binance USDT-M perpetual futures.
// It also implements StepSizer so it can be injected into the application
// service via tradeSvc.WithStepSizer(bt).
type Trader struct {
	client  *futures.Client
	timeout time.Duration
	log     zerolog.Logger
	steps   sync.Map // symbol → decimal.Decimal step size
}

// New creates a BinanceTrader wired to the correct endpoint for the given mode.
// Setting UseTestnet must happen before NewClient is called because the futures
// package reads the global at construction time to decide BaseURL.
func New(cfg config.BinanceConfig, apiKey, apiSecret string, mode config.BotMode, log zerolog.Logger) *Trader {
	// Set futures-package testnet flag before constructing client.
	switch mode {
	case config.ModeTestnet:
		futures.UseTestnet = true
	default:
		futures.UseTestnet = false
	}
	// NewFuturesClient reads futures.UseTestnet via getApiEndpoint() internally.
	c := bn.NewFuturesClient(apiKey, apiSecret)
	timeout := time.Duration(cfg.OrderTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Trader{
		client:  c,
		timeout: timeout,
		log:     log,
	}
}

// Place sends an order to Binance and returns the result.
func (t *Trader) Place(ctx context.Context, req trade.OrderRequest) (*trade.OrderResult, error) {
	side := futures.SideTypeBuy
	if req.Side == trade.OrderSideSell {
		side = futures.SideTypeSell
	}

	svc := t.client.NewCreateOrderService().
		Symbol(req.Symbol).
		Side(side).
		NewClientOrderID(req.ClientOrderID).
		Quantity(req.Qty.String())

	// futures.OrderType is `type OrderType string` so we can cast directly.
	switch req.Type {
	case trade.OrderTypeMarket:
		svc.Type(futures.OrderTypeMarket).
			NewOrderResponseType(futures.NewOrderRespTypeRESULT)
		// Market exit orders must be reduceOnly
		if isExitPurpose(req.Purpose) {
			svc.ReduceOnly(true)
		}
	case trade.OrderTypeStop:
		// "STOP" is a valid Binance futures order type; not defined as a named
		// constant in this library version, so cast from the string value.
		svc.Type(futures.OrderType(req.Type)).
			Price(req.Price.String()).
			StopPrice(req.StopPrice.String()).
			ReduceOnly(true)
	case trade.OrderTypeStopMarket:
		svc.Type(futures.OrderType(req.Type)).
			StopPrice(req.StopPrice.String()).
			ReduceOnly(true)
	case trade.OrderTypeTakeProfitMarket:
		svc.Type(futures.OrderType(req.Type)).
			StopPrice(req.StopPrice.String()).
			ReduceOnly(true)
	default:
		return nil, fmt.Errorf("unsupported order type %s", req.Type)
	}

	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	res, err := svc.Do(cctx)
	if err != nil {
		return nil, fmt.Errorf("binance place order: %w", err)
	}
	return mapCreateOrderResult(res), nil
}

// Cancel cancels an order by clientOrderID. "Unknown order" errors are swallowed
// because the order may already be filled or cancelled.
func (t *Trader) Cancel(ctx context.Context, symbol, clientOrderID string) error {
	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	_, err := t.client.NewCancelOrderService().
		Symbol(symbol).
		OrigClientOrderID(clientOrderID).
		Do(cctx)
	if err != nil {
		if strings.Contains(err.Error(), "Unknown order") {
			return nil
		}
		return fmt.Errorf("binance cancel order: %w", err)
	}
	return nil
}

// GetOrder queries a single order by clientOrderID.
func (t *Trader) GetOrder(ctx context.Context, symbol, clientOrderID string) (*trade.OrderResult, error) {
	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	o, err := t.client.NewGetOrderService().
		Symbol(symbol).
		OrigClientOrderID(clientOrderID).
		Do(cctx)
	if err != nil {
		return nil, fmt.Errorf("binance get order: %w", err)
	}
	return &trade.OrderResult{
		ClientOrderID:   o.ClientOrderID,
		ExchangeOrderID: fmt.Sprintf("%d", o.OrderID),
		Status:          mapStatus(o.Status),
		FilledQty:       parseDec(o.ExecutedQuantity),
		AvgFillPrice:    parseDec(o.AvgPrice),
	}, nil
}

// GetPositionRisk returns the current position for a symbol.
func (t *Trader) GetPositionRisk(ctx context.Context, symbol string) (*trade.Position, error) {
	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	rows, err := t.client.NewGetPositionRiskService().Symbol(symbol).Do(cctx)
	if err != nil {
		return nil, fmt.Errorf("binance get position risk: %w", err)
	}
	if len(rows) == 0 {
		return &trade.Position{Symbol: symbol}, nil
	}
	r := rows[0]
	return &trade.Position{
		Symbol:     r.Symbol,
		Qty:        parseDec(r.PositionAmt),
		EntryPrice: parseDec(r.EntryPrice),
	}, nil
}

// StepSize returns the LOT_SIZE.stepSize for the given symbol, cached for
// process lifetime. Satisfies the application/trade.StepSizer interface.
func (t *Trader) StepSize(ctx context.Context, symbol string) (decimal.Decimal, error) {
	if v, ok := t.steps.Load(symbol); ok {
		return v.(decimal.Decimal), nil
	}

	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	info, err := t.client.NewExchangeInfoService().Do(cctx)
	if err != nil {
		return decimal.Zero, fmt.Errorf("binance exchange info: %w", err)
	}
	for i := range info.Symbols {
		s := &info.Symbols[i]
		if s.Symbol != symbol {
			continue
		}
		f := s.LotSizeFilter()
		if f == nil {
			return decimal.Zero, errors.New("LOT_SIZE filter not found for " + symbol)
		}
		step, err := decimal.NewFromString(f.StepSize)
		if err != nil {
			return decimal.Zero, fmt.Errorf("parse step size %q: %w", f.StepSize, err)
		}
		t.steps.Store(symbol, step)
		return step, nil
	}
	return decimal.Zero, errors.New("symbol not found in exchange info: " + symbol)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func isExitPurpose(p string) bool {
	switch p {
	case "exit", "stop", "backup_stop", "take_profit":
		return true
	}
	return false
}

func mapStatus(s futures.OrderStatusType) trade.OrderStatus {
	switch s {
	case futures.OrderStatusTypeNew:
		return trade.OrderStatusSubmitted
	case futures.OrderStatusTypePartiallyFilled:
		return trade.OrderStatus("partial")
	case futures.OrderStatusTypeFilled:
		return trade.OrderStatusFilled
	case futures.OrderStatusTypeCanceled:
		return trade.OrderStatusCanceled
	case futures.OrderStatusTypeRejected:
		return trade.OrderStatusRejected
	case futures.OrderStatusTypeExpired:
		return trade.OrderStatus("expired")
	}
	return trade.OrderStatusSubmitted
}

func mapCreateOrderResult(o *futures.CreateOrderResponse) *trade.OrderResult {
	return &trade.OrderResult{
		ClientOrderID:   o.ClientOrderID,
		ExchangeOrderID: fmt.Sprintf("%d", o.OrderID),
		Status:          mapStatus(o.Status),
		FilledQty:       parseDec(o.ExecutedQuantity),
		AvgFillPrice:    parseDec(o.AvgPrice),
	}
}

func parseDec(s string) decimal.Decimal {
	if s == "" {
		return decimal.Zero
	}
	d, _ := decimal.NewFromString(s)
	return d
}
