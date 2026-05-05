package trade

import (
	"context"
	"errors"
	"sync"

	"github.com/shopspring/decimal"
)

// errNotFound is returned by GetOrder when the order is not in the in-memory map.
var errNotFound = errors.New("order not found")

// DryRunTrader simulates instant fills at the request's ReferencePrice for
// MARKET orders, and never triggers stop orders. Used in dry_run mode and
// in unit/integration tests of the application layer.
type DryRunTrader struct {
	mu     sync.RWMutex
	orders map[orderKey]*OrderResult // keyed by (symbol, clientOrderID)
}

type orderKey struct{ symbol, clientOrderID string }

func NewDryRunTrader() *DryRunTrader {
	return &DryRunTrader{orders: make(map[orderKey]*OrderResult)}
}

func (d *DryRunTrader) Place(_ context.Context, req OrderRequest) (*OrderResult, error) {
	res := &OrderResult{
		ClientOrderID:   req.ClientOrderID,
		ExchangeOrderID: "DRYRUN-" + req.ClientOrderID,
	}
	if req.Type == OrderTypeMarket {
		res.Status = OrderStatusFilled
		res.FilledQty = req.Qty
		res.AvgFillPrice = req.ReferencePrice
	} else {
		// stop / take-profit: parked, never triggered in dry_run
		res.Status = OrderStatusSubmitted
	}
	d.mu.Lock()
	d.orders[orderKey{req.Symbol, req.ClientOrderID}] = res
	d.mu.Unlock()
	return res, nil
}

func (d *DryRunTrader) Cancel(_ context.Context, _ string, _ string) error { return nil }

// GetOrder returns the last-seen result for (symbol, clientOrderID), or
// errNotFound if Place was never called for that pair.
func (d *DryRunTrader) GetOrder(_ context.Context, symbol, clientOrderID string) (*OrderResult, error) {
	d.mu.RLock()
	res, ok := d.orders[orderKey{symbol, clientOrderID}]
	d.mu.RUnlock()
	if !ok {
		return nil, errNotFound
	}
	return res, nil
}

// GetPositionRisk always returns zero qty. The application/trade layer
// maintains its own positions in the DB; dry_run does not simulate fills
// back into a position state.
func (d *DryRunTrader) GetPositionRisk(_ context.Context, symbol string) (*Position, error) {
	return &Position{Symbol: symbol}, nil
}

// StepSize returns the default step size used in dry_run mode.
func (d *DryRunTrader) StepSize(_ context.Context, _ string) (decimal.Decimal, error) {
	return decimal.NewFromFloat(0.001), nil
}
