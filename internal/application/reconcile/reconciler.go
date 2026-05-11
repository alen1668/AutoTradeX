package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/eval"
	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/store"
	"github.com/lizhaojie/tvbot/internal/trade"
)

// Reconciler is a background loop that polls open orders and syncs their
// status from the exchange, alerting when protective orders fire.
type Reconciler struct {
	pool        *pgxpool.Pool
	orderRepo   *store.OrderRepo
	posRepo     *store.VirtualPositionRepo
	historyRepo *store.PositionHistoryRepo
	trader      trade.Trader
	notifier    notify.Notifier
	log         zerolog.Logger
	interval    time.Duration

	publisher eval.Publisher // nil-safe Phase 3 SSE wiring
}

// WithPublisher wires the Phase 3 SSE broker. nil is accepted (no-op).
func (r *Reconciler) WithPublisher(p eval.Publisher) *Reconciler {
	r.publisher = p
	return r
}

// New creates a Reconciler. interval controls how often the exchange is polled.
func New(pool *pgxpool.Pool, orderRepo *store.OrderRepo, posRepo *store.VirtualPositionRepo,
	historyRepo *store.PositionHistoryRepo, trader trade.Trader,
	notifier notify.Notifier, log zerolog.Logger, interval time.Duration) *Reconciler {
	return &Reconciler{
		pool:        pool,
		orderRepo:   orderRepo,
		posRepo:     posRepo,
		historyRepo: historyRepo,
		trader:      trader,
		notifier:    notifier,
		log:         log,
		interval:    interval,
	}
}

// Run starts the reconciliation loop. Returns when ctx is canceled.
func (r *Reconciler) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.tick(ctx); err != nil {
				r.log.Error().Err(err).Msg("reconciler tick failed")
				// don't break the loop — keep trying
			}
		}
	}
}

func (r *Reconciler) tick(ctx context.Context) error {
	pending, err := r.orderRepo.ListPending(ctx, r.pool)
	if err != nil {
		return fmt.Errorf("list pending: %w", err)
	}
	for _, o := range pending {
		if err := r.reconcileOne(ctx, o); err != nil {
			r.log.Warn().Err(err).Str("client_order_id", o.ClientOrderID).Msg("reconcile order failed")
		}
	}
	return nil
}

func (r *Reconciler) reconcileOne(ctx context.Context, o *store.OrderRow) error {
	res, err := r.trader.GetOrder(ctx, o.Symbol, o.ClientOrderID)
	if err != nil {
		return err
	}
	if string(res.Status) == o.Status {
		return nil // unchanged
	}

	switch string(res.Status) {
	case "filled":
		if err := r.orderRepo.UpdateOnFill(ctx, r.pool, o.ID, res.ExchangeOrderID,
			res.FilledQty, res.AvgFillPrice, res.FeesUSDC); err != nil {
			return err
		}
		// If a protective stop/take_profit fired, close the VP, write
		// position_history, and alert with full PnL info — all atomically
		// from the user's perspective. (We use SELECT ... WHERE status<>'closed'
		// to be idempotent if recovery or another tick already closed it.)
		if isProtective(o.Purpose) && o.VirtualPositionID > 0 {
			vp, err := r.posRepo.GetByID(ctx, r.pool, o.VirtualPositionID)
			if err != nil {
				r.log.Warn().Err(err).Int64("vp_id", o.VirtualPositionID).
					Msg("reconcile: load VP for protective-filled close failed")
				return nil
			}
			if vp.Status == "closed" {
				return nil // already handled
			}
			pnl := notify.ComputePnL(vp.Side, vp.EntryFillPrice, res.AvgFillPrice, res.FilledQty)
			reason := notify.CloseReasonStopLoss
			if o.Purpose == "take_profit" {
				reason = notify.CloseReasonTakeProfit
			}
			if err := r.posRepo.MarkClosed(ctx, r.pool, vp.ID); err != nil {
				r.log.Warn().Err(err).Int64("vp_id", vp.ID).Msg("reconcile: mark VP closed failed")
			}
			if r.historyRepo != nil {
				if err := r.writeHistory(ctx, vp, res.AvgFillPrice, res.FilledQty, res.FeesUSDC, reason); err != nil {
					r.log.Warn().Err(err).Int64("vp_id", vp.ID).Msg("reconcile: write history failed")
				}
			}
			_ = r.notifier.Send(ctx, notify.BuildCloseMessage(
				vp.StrategyID, vp.Symbol, vp.Side, reason,
				vp.EntryFillPrice, res.AvgFillPrice, res.FilledQty, pnl))
		}
	case "canceled", "rejected", "expired":
		if err := r.orderRepo.UpdateStatus(ctx, r.pool, o.ID, string(res.Status)); err != nil {
			return err
		}
		// Protective order canceled by exchange while VP is still active → alert
		if isProtective(o.Purpose) && o.VirtualPositionID > 0 {
			vp, err := r.posRepo.GetByID(ctx, r.pool, o.VirtualPositionID)
			if err == nil && vp.Status != "closed" {
				_ = r.notifier.Send(ctx, notify.BuildProtectiveCanceledMessage(
					vp.StrategyID, vp.Symbol, o.Purpose, vp.ID, vp.Status))
			}
		}
	default:
		// e.g., partial — bump status only
		if err := r.orderRepo.UpdateStatus(ctx, r.pool, o.ID, string(res.Status)); err != nil {
			return err
		}
	}
	return nil
}

// writeHistory inserts a position_history row when reconciler has just
// observed a protective close.
func (r *Reconciler) writeHistory(ctx context.Context, vp *store.VirtualPositionRow,
	exitPrice, exitQty, fees decimal.Decimal, reason string) error {
	pnl := notify.ComputePnL(vp.Side, vp.EntryFillPrice, exitPrice, exitQty)
	pnlPct := decimal.Zero
	if !vp.EntryFillPrice.IsZero() && !exitQty.IsZero() {
		pnlPct = pnl.Div(vp.EntryFillPrice.Mul(exitQty)).Mul(decimal.NewFromInt(100))
	}
	now := time.Now().UTC()
	row := store.PositionHistoryRow{
		StrategyID: vp.StrategyID, Symbol: vp.Symbol, Side: vp.Side, Qty: exitQty,
		EntrySignalPrice: vp.EntrySignalPrice, EntryFillPrice: vp.EntryFillPrice,
		ExitSignalPrice:  exitPrice, ExitFillPrice: exitPrice,
		PnLUSDC:          pnl, PnLPct: pnlPct, FeesUSDC: fees,
		CloseReason:      reason,
		DurationSeconds:  int(now.Sub(vp.OpenedAt).Seconds()),
		OpenedAt:         vp.OpenedAt, ClosedAt: now,
	}
	if err := r.historyRepo.Insert(ctx, r.pool, row); err != nil {
		return err
	}
	// Phase 3: push trade_closed after successful Insert. publisher is
	// nil-safe; this is fire-and-forget.
	if r.publisher != nil {
		pnlFloat, _ := row.PnLUSDC.Float64()
		r.publisher.Publish(eval.EvalEvent{
			Kind:       "trade_closed",
			Symbol:     row.Symbol,
			PnLUSDC:    &pnlFloat,
			OccurredAt: time.Now().Unix(),
		})
	}
	return nil
}

func isProtective(purpose string) bool {
	switch purpose {
	case "stop", "backup_stop", "take_profit":
		return true
	}
	return false
}
