package reconcile

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/store"
	"github.com/lizhaojie/tvbot/internal/trade"
)

// Recovery performs boot-time reconciliation between DB virtual_positions and
// the exchange's view of open positions. It must complete before the HTTP
// server starts accepting traffic.
type Recovery struct {
	pool       *pgxpool.Pool
	posRepo    *store.VirtualPositionRepo
	systemRepo *store.SystemStateRepo
	trader     trade.Trader
	notifier   notify.Notifier
	log        zerolog.Logger
}

// NewRecovery creates a Recovery instance.
func NewRecovery(pool *pgxpool.Pool, posRepo *store.VirtualPositionRepo,
	systemRepo *store.SystemStateRepo, trader trade.Trader,
	notifier notify.Notifier, log zerolog.Logger) *Recovery {
	return &Recovery{
		pool:       pool,
		posRepo:    posRepo,
		systemRepo: systemRepo,
		trader:     trader,
		notifier:   notifier,
		log:        log,
	}
}

// Run executes the boot reconciliation. After this method:
//   - All active virtual_positions match reality (or anomalies are flagged)
//   - system_state.armed is forced to false (operator must re-arm explicitly)
func (r *Recovery) Run(ctx context.Context) error {
	r.log.Info().Msg("startup recovery: starting")

	// 1) Force disarm — operator must re-arm explicitly after recovery
	if err := r.systemRepo.Disarm(ctx, r.pool); err != nil {
		return fmt.Errorf("force disarm: %w", err)
	}

	// 2) Find all virtual positions that DB thinks are active
	rows, err := r.pool.Query(ctx, `
SELECT id FROM virtual_positions
 WHERE status IN ('opening','open','closing')`)
	if err != nil {
		return fmt.Errorf("list active VPs: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()

	r.log.Info().Int("count", len(ids)).Msg("startup recovery: active VPs to reconcile")

	for _, id := range ids {
		if err := r.reconcileOne(ctx, id); err != nil {
			r.log.Error().Err(err).Int64("vp_id", id).Msg("recovery: position reconcile failed")
			_ = r.notifier.Send(ctx, notify.Message{
				Title:    "Startup recovery anomaly",
				Body:     fmt.Sprintf("vp_id=%d err=%s", id, err.Error()),
				Severity: notify.SeverityCritical,
			})
		}
	}
	r.log.Info().Msg("startup recovery: done — system disarmed")
	return nil
}

func (r *Recovery) reconcileOne(ctx context.Context, vpID int64) error {
	vp, err := r.posRepo.GetByID(ctx, r.pool, vpID)
	if err != nil {
		return err
	}
	pos, err := r.trader.GetPositionRisk(ctx, vp.Symbol)
	if err != nil {
		return fmt.Errorf("get position risk: %w", err)
	}

	realQty := pos.Qty.Abs()
	dbQty := vp.Qty
	matched := realQty.Equal(dbQty) && positionSidesMatch(vp.Side, pos.Qty)

	switch {
	case realQty.IsZero():
		// No real position — DB says active. Likely stop fired offline.
		// Mark closed; PnL calc will be approximate (we don't have exit price).
		r.log.Warn().Int64("vp_id", vpID).Msg("recovery: real position empty; marking closed")
		if err := r.posRepo.MarkClosed(ctx, r.pool, vpID); err != nil {
			return err
		}
		_ = r.notifier.Send(ctx, notify.Message{
			Title: "Position auto-closed during recovery",
			Body: fmt.Sprintf(
				"vp_id=%d %s — exchange position empty, likely stop fired offline. Review history.",
				vpID, vp.Symbol),
			Severity: notify.SeverityCritical,
		})
	case matched:
		r.log.Info().Int64("vp_id", vpID).Msg("recovery: position matches reality")
		// Could check for missing protective orders here; leaving as future enhancement.
	default:
		// Mismatch — cannot reconcile automatically. Flag for manual review.
		_ = r.notifier.Send(ctx, notify.Message{
			Title: "Position mismatch — manual review required",
			Body: fmt.Sprintf(
				"vp_id=%d %s db_qty=%s db_side=%s real_qty=%s — system disarmed",
				vpID, vp.Symbol, dbQty.String(), vp.Side, pos.Qty.String()),
			Severity: notify.SeverityCritical,
		})
		return fmt.Errorf("qty mismatch: db=%s real=%s", dbQty, pos.Qty)
	}
	return nil
}

func positionSidesMatch(dbSide string, realQty decimal.Decimal) bool {
	if dbSide == "long" {
		return realQty.IsPositive()
	}
	if dbSide == "short" {
		return realQty.IsNegative()
	}
	return false
}
