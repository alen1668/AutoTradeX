package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/store"
	"github.com/lizhaojie/tvbot/internal/trade"
)

// Reconciler is a background loop that polls open orders and syncs their
// status from the exchange, alerting when protective orders fire.
type Reconciler struct {
	pool      *pgxpool.Pool
	orderRepo *store.OrderRepo
	posRepo   *store.VirtualPositionRepo
	trader    trade.Trader
	notifier  notify.Notifier
	log       zerolog.Logger
	interval  time.Duration
}

// New creates a Reconciler. interval controls how often the exchange is polled.
func New(pool *pgxpool.Pool, orderRepo *store.OrderRepo, posRepo *store.VirtualPositionRepo,
	trader trade.Trader, notifier notify.Notifier, log zerolog.Logger, interval time.Duration) *Reconciler {
	return &Reconciler{
		pool:      pool,
		orderRepo: orderRepo,
		posRepo:   posRepo,
		trader:    trader,
		notifier:  notifier,
		log:       log,
		interval:  interval,
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
		// If this is a protective stop/take_profit firing, alert and let
		// startup-recovery / next-trade-flow detect closed position via
		// GetPositionRisk later. We don't auto-close VP here to avoid race
		// with manual close in flight.
		if isProtective(o.Purpose) {
			_ = r.notifier.Send(ctx, notify.Message{
				Title:    "Protective order filled",
				Body:     fmt.Sprintf("%s %s @ %s — virtual position %d", o.Purpose, o.Symbol, res.AvgFillPrice, o.VirtualPositionID),
				Severity: notify.SeverityCritical,
			})
		}
	case "canceled", "rejected", "expired":
		if err := r.orderRepo.UpdateStatus(ctx, r.pool, o.ID, string(res.Status)); err != nil {
			return err
		}
		// Protective order canceled by exchange while VP is still active → alert
		if isProtective(o.Purpose) && o.VirtualPositionID > 0 {
			vp, err := r.posRepo.GetByID(ctx, r.pool, o.VirtualPositionID)
			if err == nil && vp.Status != "closed" {
				_ = r.notifier.Send(ctx, notify.Message{
					Title:    "Protective order canceled",
					Body:     fmt.Sprintf("%s on %s canceled while position %d is %s — manual review", o.Purpose, o.Symbol, vp.ID, vp.Status),
					Severity: notify.SeverityCritical,
				})
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

func isProtective(purpose string) bool {
	switch purpose {
	case "stop", "backup_stop", "take_profit":
		return true
	}
	return false
}
