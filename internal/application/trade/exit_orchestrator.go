package trade

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	tradepkg "github.com/lizhaojie/tvbot/internal/trade"
)

var (
	// ErrConstraintViolated indicates the action would violate a safety
	// constraint (e.g. tighten_sl with a looser price). Worker rewrites
	// the decision to hold and persists.
	ErrConstraintViolated = errors.New("exit orchestrator: constraint violated")
	// ErrPositionNotFound — VP id missing or not in 'open' state.
	ErrPositionNotFound = errors.New("exit orchestrator: position not found")
)

// ExitVPRepo is the slice of *store.VirtualPositionRepo ExitOrchestrator
// uses; defined as an interface so tests can substitute a fake without
// importing the full store package.
type ExitVPRepo interface {
	GetByIDForExit(ctx context.Context, id int64) (ExitPositionView, error)
}

// ExitPositionView is the position state ExitOrchestrator needs.
type ExitPositionView struct {
	ID                int64
	StrategyID        string
	Symbol            string
	Side              string // "long" | "short"
	Qty               decimal.Decimal
	StopOrderID       int64
	BackupStopOrderID int64
	TakeProfitOrderID int64
	Status            string // expected to be "open"
}

// ExitOrderRepo lets ExitOrchestrator look up an order's client_order_id
// (needed by Trader.Cancel) without exposing the full OrderRepo.
type ExitOrderRepo interface {
	GetClientOrderIDByID(ctx context.Context, id int64) (string, error)
	UpdateStatus(ctx context.Context, id int64, status string) error
	StopPriceByID(ctx context.Context, id int64) (decimal.Decimal, error)
}

// ExitOrchestrator implements exit.Executor. It orchestrates Binance API
// calls (via Trader) + DB updates (via thin repo interfaces) for the
// three actions Exit Agent can take.
//
// IMPORTANT: This is not the full ClosePosition flow. ExitNow does NOT
// write to position_history — the reconciler picks it up on the next
// scan and runs the full closure path. This keeps the orchestrator
// simple, idempotent, and ensures PnL accounting goes through the
// canonical Service.ClosePosition path (driven by reconciler in this
// case, since the position will appear "closed on exchange but not in
// DB").
type ExitOrchestrator struct {
	pool   *pgxpool.Pool
	trader tradepkg.Trader
	vp     ExitVPRepo
	orders ExitOrderRepo
}

func NewExitOrchestrator(pool *pgxpool.Pool, trader tradepkg.Trader, vp ExitVPRepo, orders ExitOrderRepo) *ExitOrchestrator {
	return &ExitOrchestrator{pool: pool, trader: trader, vp: vp, orders: orders}
}

// TightenSL cancels the current stop-loss and places a new one at
// newPrice. Refuses if newPrice is not strictly tighter (long: > current,
// short: < current).
func (o *ExitOrchestrator) TightenSL(ctx context.Context, positionID int64, newPrice decimal.Decimal) error {
	p, err := o.vp.GetByIDForExit(ctx, positionID)
	if err != nil {
		return ErrPositionNotFound
	}
	if p.Status != "open" {
		return ErrPositionNotFound
	}

	if p.StopOrderID == 0 {
		// No SL on book → fail loud rather than silently place. The
		// safety contract is "Exit Agent makes things tighter, not
		// looser" — without a baseline we can't claim "tighter".
		return fmt.Errorf("no current stop order for position %d", positionID)
	}

	currentSL, err := o.orders.StopPriceByID(ctx, p.StopOrderID)
	if err != nil {
		return fmt.Errorf("read current sl: %w", err)
	}
	if !currentSL.IsZero() {
		if p.Side == "long" && newPrice.LessThanOrEqual(currentSL) {
			return ErrConstraintViolated
		}
		if p.Side == "short" && newPrice.GreaterThanOrEqual(currentSL) {
			return ErrConstraintViolated
		}
	}

	// Cancel the old stop on exchange.
	cid, err := o.orders.GetClientOrderIDByID(ctx, p.StopOrderID)
	if err == nil && cid != "" {
		_ = o.trader.Cancel(ctx, p.Symbol, cid)
		_ = o.orders.UpdateStatus(ctx, p.StopOrderID, "canceled")
	}

	// Place new STOP_MARKET (closer to canonical SL behaviour: triggers
	// market on touch). Same purpose tag = "stop" so reconciler treats
	// it as the protective leg.
	exitSide := tradepkg.OrderSideSell
	if p.Side == "short" {
		exitSide = tradepkg.OrderSideBuy
	}
	clientID := fmt.Sprintf("ea-tighten-%d-%d", p.ID, time.Now().UnixMilli())
	_, err = o.trader.Place(ctx, tradepkg.OrderRequest{
		ClientOrderID: clientID,
		Symbol:        p.Symbol,
		Side:          exitSide,
		Type:          tradepkg.OrderTypeStopMarket,
		Qty:           p.Qty,
		StopPrice:     newPrice,
		Purpose:       "stop",
	})
	if err != nil {
		return fmt.Errorf("place new stop: %w", err)
	}
	return nil
}

// TakePartial places a market-close for `pct` of the position. Range
// (0, 0.5]. Leaves SL/TP untouched (remaining position still protected).
//
// Note: This will leave the SL/TP qty out of sync with the now-smaller
// position. Reconciler will detect on next scan and re-balance protective
// orders. For V1 this is acceptable — partial exits are infrequent.
func (o *ExitOrchestrator) TakePartial(ctx context.Context, positionID int64, pct decimal.Decimal) error {
	if pct.LessThanOrEqual(decimal.Zero) || pct.GreaterThan(decimal.NewFromFloat(0.5)) {
		return ErrConstraintViolated
	}
	p, err := o.vp.GetByIDForExit(ctx, positionID)
	if err != nil {
		return ErrPositionNotFound
	}
	if p.Status != "open" {
		return ErrPositionNotFound
	}
	closeSide := tradepkg.OrderSideSell
	if p.Side == "short" {
		closeSide = tradepkg.OrderSideBuy
	}
	closeQty := p.Qty.Mul(pct)
	clientID := fmt.Sprintf("ea-partial-%d-%d", p.ID, time.Now().UnixMilli())
	_, err = o.trader.Place(ctx, tradepkg.OrderRequest{
		ClientOrderID: clientID,
		Symbol:        p.Symbol,
		Side:          closeSide,
		Type:          tradepkg.OrderTypeMarket,
		Qty:           closeQty,
		Purpose:       "exit",
	})
	if err != nil {
		return fmt.Errorf("place partial market: %w", err)
	}
	return nil
}

// ExitNow cancels both protective orders and market-closes the entire
// position. Position_history + virtual_positions.status='closed' write
// happens in the next reconciler tick (see type doc).
func (o *ExitOrchestrator) ExitNow(ctx context.Context, positionID int64) error {
	p, err := o.vp.GetByIDForExit(ctx, positionID)
	if err != nil {
		return ErrPositionNotFound
	}
	if p.Status != "open" {
		return ErrPositionNotFound
	}

	// Cancel protective orders best-effort. We must not fail the exit if
	// a cancel errors — the market close below is the load-bearing step.
	for _, oid := range []int64{p.StopOrderID, p.BackupStopOrderID, p.TakeProfitOrderID} {
		if oid == 0 {
			continue
		}
		cid, err := o.orders.GetClientOrderIDByID(ctx, oid)
		if err != nil || cid == "" {
			continue
		}
		_ = o.trader.Cancel(ctx, p.Symbol, cid)
		_ = o.orders.UpdateStatus(ctx, oid, "canceled")
	}

	closeSide := tradepkg.OrderSideSell
	if p.Side == "short" {
		closeSide = tradepkg.OrderSideBuy
	}
	clientID := fmt.Sprintf("ea-exit-%d-%d", p.ID, time.Now().UnixMilli())
	_, err = o.trader.Place(ctx, tradepkg.OrderRequest{
		ClientOrderID: clientID,
		Symbol:        p.Symbol,
		Side:          closeSide,
		Type:          tradepkg.OrderTypeMarket,
		Qty:           p.Qty,
		Purpose:       "exit",
	})
	if err != nil {
		return fmt.Errorf("place exit market: %w", err)
	}
	return nil
}
