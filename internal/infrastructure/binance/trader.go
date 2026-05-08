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
	steps   sync.Map // symbol → decimal.Decimal LOT_SIZE.stepSize
	ticks   sync.Map // symbol → decimal.Decimal PRICE_FILTER.tickSize
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

// FuturesClient exposes the underlying *futures.Client so other components
// (e.g. agent/market kline fetcher) can share the connection pool + auth.
func (t *Trader) FuturesClient() *futures.Client { return t.client }

// Place sends an order to Binance and returns the result. Conditional
// orders (STOP / STOP_MARKET / TAKE_PROFIT_MARKET) are routed to the Algo
// Order endpoint because the new demo.binance.com platform and Multi-Asset
// / Portfolio Margin accounts reject them on /fapi/v1/order with -4120.
func (t *Trader) Place(ctx context.Context, req trade.OrderRequest) (*trade.OrderResult, error) {
	if isAlgoType(req.Type) {
		return t.placeAlgo(ctx, req)
	}
	return t.placeRegular(ctx, req)
}

func (t *Trader) placeRegular(ctx context.Context, req trade.OrderRequest) (*trade.OrderResult, error) {
	side := futures.SideTypeBuy
	if req.Side == trade.OrderSideSell {
		side = futures.SideTypeSell
	}

	svc := t.client.NewCreateOrderService().
		Symbol(req.Symbol).
		Side(side).
		NewClientOrderID(req.ClientOrderID).
		Quantity(req.Qty.String())

	switch req.Type {
	case trade.OrderTypeMarket:
		svc.Type(futures.OrderTypeMarket).
			NewOrderResponseType(futures.NewOrderRespTypeRESULT)
		if isExitPurpose(req.Purpose) {
			svc.ReduceOnly(true)
		}
	default:
		return nil, fmt.Errorf("unsupported regular order type %s", req.Type)
	}

	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	res, err := svc.Do(cctx)
	if err != nil {
		return nil, fmt.Errorf("binance place order: %w", err)
	}
	return mapCreateOrderResult(res), nil
}

func (t *Trader) placeAlgo(ctx context.Context, req trade.OrderRequest) (*trade.OrderResult, error) {
	side := futures.SideTypeBuy
	if req.Side == trade.OrderSideSell {
		side = futures.SideTypeSell
	}

	// Round prices to the symbol's PRICE_FILTER.tickSize. Without this Binance
	// returns -1111 ("Precision is over the maximum") because computed stop
	// prices like 2319.6777502 don't align with ETHUSDT's 0.01 tick.
	tick := t.priceTick(ctx, req.Symbol)
	stopPrice := quantizeToTick(req.StopPrice, tick)

	svc := t.client.NewCreateAlgoOrderService().
		AlgoType(futures.OrderAlgoTypeConditional).
		Symbol(req.Symbol).
		Side(side).
		ClientAlgoId(req.ClientOrderID).
		Quantity(req.Qty.String()).
		ReduceOnly(true).
		TriggerPrice(stopPrice.String()).
		Type(futures.AlgoOrderType(req.Type))

	// STOP (limit-stop) carries a limit price after trigger.
	if req.Type == trade.OrderTypeStop {
		limitPrice := quantizeToTick(req.Price, tick)
		svc.Price(limitPrice.String())
	}

	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	res, err := svc.Do(cctx)
	if err != nil {
		return nil, fmt.Errorf("binance place order: %w", err)
	}
	return mapCreateAlgoResult(res), nil
}

// Cancel cancels an order by clientOrderID. Dispatches to the algo or
// regular endpoint based on the clientOrderID prefix. "Unknown order"
// errors are swallowed because the order may already be filled or cancelled.
func (t *Trader) Cancel(ctx context.Context, symbol, clientOrderID string) error {
	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	var err error
	if isAlgoClientOrderID(clientOrderID) {
		_, err = t.client.NewCancelAlgoOrderService().
			ClientAlgoID(clientOrderID).
			Do(cctx)
	} else {
		_, err = t.client.NewCancelOrderService().
			Symbol(symbol).
			OrigClientOrderID(clientOrderID).
			Do(cctx)
	}
	if err != nil {
		if strings.Contains(err.Error(), "Unknown order") {
			return nil
		}
		return fmt.Errorf("binance cancel order: %w", err)
	}
	return nil
}

// GetOrder queries a single order by clientOrderID. Dispatches to algo or
// regular endpoint based on the clientOrderID prefix.
func (t *Trader) GetOrder(ctx context.Context, symbol, clientOrderID string) (*trade.OrderResult, error) {
	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	if isAlgoClientOrderID(clientOrderID) {
		a, err := t.client.NewGetAlgoOrderService().
			ClientAlgoID(clientOrderID).
			Do(cctx)
		if err != nil {
			return nil, fmt.Errorf("binance get algo order: %w", err)
		}
		return &trade.OrderResult{
			ClientOrderID:   a.ClientAlgoId,
			ExchangeOrderID: fmt.Sprintf("%d", a.AlgoId),
			Status:          mapAlgoStatus(a.AlgoStatus),
		}, nil
	}

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
	if err := t.loadFilters(ctx, symbol); err != nil {
		return decimal.Zero, err
	}
	v, _ := t.steps.Load(symbol)
	return v.(decimal.Decimal), nil
}

// priceTick returns PRICE_FILTER.tickSize for the symbol, cached. Returns
// zero (not an error) if the symbol is unknown — quantizeToTick treats zero
// as "no quantization", letting callers proceed with the original price.
func (t *Trader) priceTick(ctx context.Context, symbol string) decimal.Decimal {
	if v, ok := t.ticks.Load(symbol); ok {
		return v.(decimal.Decimal)
	}
	if err := t.loadFilters(ctx, symbol); err != nil {
		return decimal.Zero
	}
	v, ok := t.ticks.Load(symbol)
	if !ok {
		return decimal.Zero
	}
	return v.(decimal.Decimal)
}

// loadFilters fetches LOT_SIZE.stepSize + PRICE_FILTER.tickSize for the
// symbol and stores them in the per-Trader caches.
func (t *Trader) loadFilters(ctx context.Context, symbol string) error {
	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	info, err := t.client.NewExchangeInfoService().Do(cctx)
	if err != nil {
		return fmt.Errorf("binance exchange info: %w", err)
	}
	for i := range info.Symbols {
		s := &info.Symbols[i]
		if s.Symbol != symbol {
			continue
		}
		lot := s.LotSizeFilter()
		if lot == nil {
			return errors.New("LOT_SIZE filter not found for " + symbol)
		}
		step, err := decimal.NewFromString(lot.StepSize)
		if err != nil {
			return fmt.Errorf("parse step size %q: %w", lot.StepSize, err)
		}
		t.steps.Store(symbol, step)

		// PRICE_FILTER may be absent on some symbols; that's fine — the
		// trader will treat tick=0 as "no rounding".
		if pf := s.PriceFilter(); pf != nil && pf.TickSize != "" {
			tick, err := decimal.NewFromString(pf.TickSize)
			if err == nil {
				t.ticks.Store(symbol, tick)
			}
		}
		return nil
	}
	return errors.New("symbol not found in exchange info: " + symbol)
}

// ── helpers ──────────────────────────────────────────────────────────────────

func isExitPurpose(p string) bool {
	switch p {
	case "exit", "stop", "backup_stop", "take_profit":
		return true
	}
	return false
}

func mapAlgoStatus(s futures.AlgoOrderStatusType) trade.OrderStatus {
	switch s {
	case futures.AlgoOrderStatusTypeNew:
		return trade.OrderStatusSubmitted
	case futures.AlgoOrderStatusTypeCanceled:
		return trade.OrderStatusCanceled
	case futures.AlgoOrderStatusTypeRejected:
		return trade.OrderStatusRejected
	case futures.AlgoOrderStatusTypeExpired:
		return trade.OrderStatus("expired")
	}
	// "TRIGGERED" / "FILLED" not exposed in this SDK version; treat as filled
	// when the algo's outcome is success-like, else fall back to submitted.
	if string(s) == "TRIGGERED" || string(s) == "FILLED" {
		return trade.OrderStatusFilled
	}
	return trade.OrderStatusSubmitted
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

// mapCreateAlgoResult turns an Algo create response into a trade.OrderResult.
// Algo orders are pending-by-design (they trigger later), so status maps to
// "submitted". The algoId is stored as ExchangeOrderID for parity.
func mapCreateAlgoResult(o *futures.CreateAlgoOrderResp) *trade.OrderResult {
	return &trade.OrderResult{
		ClientOrderID:   o.ClientAlgoId,
		ExchangeOrderID: fmt.Sprintf("%d", o.AlgoId),
		Status:          trade.OrderStatusSubmitted,
	}
}

func parseDec(s string) decimal.Decimal {
	if s == "" {
		return decimal.Zero
	}
	d, _ := decimal.NewFromString(s)
	return d
}
