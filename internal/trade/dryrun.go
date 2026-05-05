package trade

import "context"

// DryRunTrader simulates instant fills at the request's ReferencePrice for
// MARKET orders, and never triggers stop orders. Used in dry_run mode and
// in unit/integration tests of the application layer.
type DryRunTrader struct{}

func NewDryRunTrader() *DryRunTrader { return &DryRunTrader{} }

func (DryRunTrader) Place(_ context.Context, req OrderRequest) (*OrderResult, error) {
	res := &OrderResult{
		ClientOrderID:   req.ClientOrderID,
		ExchangeOrderID: "DRYRUN-" + req.ClientOrderID,
	}
	if req.Type == OrderTypeMarket {
		res.Status = OrderStatusFilled
		res.FilledQty = req.Qty
		res.AvgFillPrice = req.ReferencePrice
		return res, nil
	}
	// stop / take-profit: parked, never triggered in dry_run
	res.Status = OrderStatusSubmitted
	return res, nil
}

func (DryRunTrader) Cancel(_ context.Context, _ string, _ string) error { return nil }
