package exit

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/store"
)

// PriceProvider returns a recent mark price for the symbol. Production
// binding wraps a binance live ticker / 24h close.
type PriceProvider interface {
	Price(ctx context.Context, symbol string) (decimal.Decimal, error)
}

// DBOpenPositionsReader implements OpenPositionsReader on top of
// VirtualPositionRepo + a PriceProvider. We pull SL/TP limit prices
// directly from the orders table via raw SQL to avoid extending OrderRepo
// with one-off helpers.
type DBOpenPositionsReader struct {
	pool   *pgxpool.Pool
	pos    *store.VirtualPositionRepo
	prices PriceProvider
}

func NewDBOpenPositionsReader(pool *pgxpool.Pool, pos *store.VirtualPositionRepo, p PriceProvider) *DBOpenPositionsReader {
	return &DBOpenPositionsReader{pool: pool, pos: pos, prices: p}
}

func (r *DBOpenPositionsReader) ListOpen(ctx context.Context) ([]PositionSnapshot, error) {
	rows, err := r.pos.ListActive(ctx, r.pool)
	if err != nil {
		return nil, err
	}
	out := make([]PositionSnapshot, 0, len(rows))
	for _, p := range rows {
		// Filter to status='open' only — Exit Agent should not act on
		// opening (entry not yet filled) or closing (exit in flight).
		if p.Status != "open" {
			continue
		}
		curr, err := r.prices.Price(ctx, p.Symbol)
		if err != nil {
			// Skip this position if we can't get a price; next scan retries.
			continue
		}
		sl := r.orderLimitPrice(ctx, p.StopOrderID)
		tp := r.orderLimitPrice(ctx, p.TakeProfitOrderID)
		entry := p.EntryFillPrice
		if entry.IsZero() {
			entry = p.EntrySignalPrice
		}
		dirSign := decimal.NewFromInt(1)
		if p.Side == "short" {
			dirSign = decimal.NewFromInt(-1)
		}
		var pnlPct decimal.Decimal
		if !entry.IsZero() {
			pnlPct = curr.Sub(entry).Div(entry).Mul(dirSign)
		}
		pnlUsd := pnlPct.Mul(entry).Mul(p.Qty)
		out = append(out, PositionSnapshot{
			VirtualPositionID: p.ID,
			StrategyID:        p.StrategyID,
			Symbol:            p.Symbol,
			Side:              p.Side,
			EntryFillPrice:    entry,
			CurrentPrice:      curr,
			Qty:               p.Qty,
			UnrealizedPnLUSD:  pnlUsd,
			UnrealizedPnLPct:  pnlPct.Mul(decimal.NewFromInt(100)),
			PositionAge:       time.Since(p.OpenedAt),
			CurrentSLPrice:    sl,
			CurrentTPPrice:    tp,
		})
	}
	return out, nil
}

// orderLimitPrice returns the stop_price (for stop-type orders) or
// price (for limit-type orders) of the given order_id. Returns nil when
// orderID==0 or any error occurs (including "not found").
func (r *DBOpenPositionsReader) orderLimitPrice(ctx context.Context, orderID int64) *decimal.Decimal {
	if orderID == 0 {
		return nil
	}
	var stop, price *decimal.Decimal
	err := r.pool.QueryRow(ctx,
		`SELECT stop_price, price FROM orders WHERE id=$1`, orderID).Scan(&stop, &price)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return nil
	}
	// Prefer stop_price (stop / stop-loss orders); fall back to limit price.
	if stop != nil && !stop.IsZero() {
		return stop
	}
	return price
}
