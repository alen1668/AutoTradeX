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

// Recovery performs boot-time reconciliation between DB virtual_positions and
// the exchange's view of open positions. It must complete before the HTTP
// server starts accepting traffic.
type Recovery struct {
	pool        *pgxpool.Pool
	posRepo     *store.VirtualPositionRepo
	orderRepo   *store.OrderRepo
	historyRepo *store.PositionHistoryRepo
	systemRepo  *store.SystemStateRepo
	trader      trade.Trader
	notifier    notify.Notifier
	log         zerolog.Logger

	publisher eval.Publisher // nil-safe Phase 3 SSE wiring
}

// WithPublisher wires the Phase 3 SSE broker. nil is accepted (no-op).
func (r *Recovery) WithPublisher(p eval.Publisher) *Recovery {
	r.publisher = p
	return r
}

// NewRecovery creates a Recovery instance.
func NewRecovery(pool *pgxpool.Pool, posRepo *store.VirtualPositionRepo,
	orderRepo *store.OrderRepo, historyRepo *store.PositionHistoryRepo,
	systemRepo *store.SystemStateRepo,
	trader trade.Trader, notifier notify.Notifier, log zerolog.Logger) *Recovery {
	return &Recovery{
		pool:        pool,
		posRepo:     posRepo,
		orderRepo:   orderRepo,
		historyRepo: historyRepo,
		systemRepo:  systemRepo,
		trader:      trader,
		notifier:    notifier,
		log:         log,
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

	if err := r.reconcileAllActive(ctx); err != nil {
		return err
	}
	r.log.Info().Msg("startup recovery: done — system disarmed")
	return nil
}

// RunPeriodic loops every `interval` calling reconcileAllActive, until ctx
// is canceled. This is the runtime "position heartbeat" — startup recovery
// only runs once at boot, so without this a position closed externally
// (stop loss triggered server-side, manual close on the exchange UI) would
// stay 'open' in DB indefinitely. Returns when ctx is done.
func (r *Recovery) RunPeriodic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 120 * time.Second
	}
	r.log.Info().Dur("interval", interval).Msg("position heartbeat: started")
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			r.log.Info().Msg("position heartbeat: stopped")
			return
		case <-t.C:
			if err := r.reconcileAllActive(ctx); err != nil {
				r.log.Warn().Err(err).Msg("position heartbeat: reconcile cycle failed")
			}
		}
	}
}

// reconcileAllActive runs reconcileOne against every currently-open VP,
// then scans for "ghost" positions (qty on exchange, no matching VP).
// Shared by startup Run and the runtime heartbeat (RunPeriodic).
func (r *Recovery) reconcileAllActive(ctx context.Context) error {
	rows, err := r.pool.Query(ctx, `
SELECT id FROM virtual_positions
 WHERE status IN ('opening','open','closing')`)
	if err != nil {
		return fmt.Errorf("list active VPs: %w", err)
	}
	var ids []int64
	activeSymbols := map[string]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()

	// Build the set of symbols that DB *thinks* are active (qty != 0).
	// We need this for the ghost check below — anything on the exchange
	// for a symbol not in this set is a ghost.
	if len(ids) > 0 {
		symRows, err := r.pool.Query(ctx,
			`SELECT DISTINCT symbol FROM virtual_positions WHERE id = ANY($1)`, ids)
		if err == nil {
			for symRows.Next() {
				var s string
				_ = symRows.Scan(&s)
				activeSymbols[s] = true
			}
			symRows.Close()
		}
	}

	r.log.Info().Int("count", len(ids)).Msg("recovery: active VPs to reconcile")

	for _, id := range ids {
		if err := r.reconcileOne(ctx, id); err != nil {
			r.log.Error().Err(err).Int64("vp_id", id).Msg("recovery: position reconcile failed")
			_ = r.notifier.Send(ctx, notify.BuildRecoveryAnomalyMessage(id, err.Error()))
		}
	}

	r.detectGhostPositions(ctx, activeSymbols)
	return nil
}

// AllPositionsLister is the optional capability the position heartbeat
// uses to find ghost positions. BinanceTrader implements it; DryRunTrader
// does not (and ghost detection is a no-op for the simulator).
type AllPositionsLister interface {
	AllPositions(ctx context.Context) ([]trade.Position, error)
}

// detectGhostPositions queries the exchange for ALL non-zero positions
// and flags any whose symbol has no active VP in DB. Pure observation:
// just logs + alerts. Auto-creating a VP for a ghost is unsafe (we don't
// know which strategy or what the original signal was), so we leave that
// to the operator.
func (r *Recovery) detectGhostPositions(ctx context.Context, activeSymbols map[string]bool) {
	lister, ok := r.trader.(AllPositionsLister)
	if !ok {
		return // simulator / fake trader: skip
	}
	positions, err := lister.AllPositions(ctx)
	if err != nil {
		r.log.Warn().Err(err).Msg("recovery: AllPositions query failed; skipping ghost check")
		return
	}
	ghosts := 0
	for _, p := range positions {
		if activeSymbols[p.Symbol] {
			continue
		}
		ghosts++
		r.log.Error().Str("symbol", p.Symbol).
			Str("qty", p.Qty.String()).Str("entry_price", p.EntryPrice.String()).
			Msg("recovery: GHOST position on exchange — no active VP in DB")
		_ = r.notifier.Send(ctx, notify.BuildGhostPositionMessage(p.Symbol, p.Qty.String(), p.EntryPrice.String()))
	}
	r.log.Info().Int("exchange_positions", len(positions)).Int("ghosts", ghosts).
		Msg("recovery: ghost check done")
}

// closeData is what we need to record an offline close into position_history.
type closeData struct {
	ExitPrice   decimal.Decimal
	ExitQty     decimal.Decimal
	Fees        decimal.Decimal
	ClosedAt    time.Time // when the close actually happened on the exchange
	CloseReason string    // notify.CloseReasonStopLoss / TakeProfit / RecoveryOffline
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
		r.log.Warn().Int64("vp_id", vpID).Msg("recovery: real position empty; marking closed")
		// Try to derive close data from the protective orders first; fall
		// back to Income API for manual closes.
		cd := r.deriveCloseData(ctx, vp)
		if err := r.posRepo.MarkClosed(ctx, r.pool, vpID); err != nil {
			return err
		}
		if cd != nil {
			if err := r.recordHistory(ctx, vp, *cd); err != nil {
				r.log.Warn().Err(err).Int64("vp_id", vpID).Msg("recovery: write history failed")
			}
			pnl := notify.ComputePnL(vp.Side, vp.EntryFillPrice, cd.ExitPrice, cd.ExitQty)
			_ = r.notifier.Send(ctx, notify.BuildCloseMessage(
				vp.StrategyID, vp.Symbol, vp.Side, cd.CloseReason,
				vp.EntryFillPrice, cd.ExitPrice, cd.ExitQty, pnl))
		} else {
			_ = r.notifier.Send(ctx, notify.BuildRecoveryAutoClosedNoExitPriceMessage(
				vp.StrategyID, vp.Symbol, vp.Side, vp.ID, vp.EntryFillPrice, vp.Qty))
		}
	case matched:
		r.log.Info().Int64("vp_id", vpID).Msg("recovery: position matches reality")
	default:
		_ = r.notifier.Send(ctx, notify.BuildRecoveryMismatchMessage(
			vp.StrategyID, vp.Symbol, vp.Side, vp.ID, dbQty, pos.Qty.Abs()))
		r.log.Warn().Int64("vp_id", vpID).
			Str("db_qty", dbQty.String()).Str("real_qty", pos.Qty.String()).
			Msg("recovery: qty mismatch — manual review required")
	}
	return nil
}

// deriveCloseData figures out exit price/qty/fees for a VP that's been closed
// offline. Tries protective-order GetOrder first (fast, exact), then falls
// back to Income API (catches manual closes). Returns nil if neither works.
func (r *Recovery) deriveCloseData(ctx context.Context, vp *store.VirtualPositionRow) *closeData {
	// 1) Protective fill lookup
	if exitPrice, exitQty, fees, purpose, ok := r.findOfflineExitFill(ctx, vp); ok {
		reason := notify.CloseReasonStopLoss
		if purpose == "take_profit" {
			reason = notify.CloseReasonTakeProfit
		}
		return &closeData{
			ExitPrice: exitPrice, ExitQty: exitQty, Fees: fees,
			ClosedAt:    time.Now().UTC(), // protective fill timestamp not preserved by GetOrder; use now as approximation
			CloseReason: reason,
		}
	}
	// 2) Income API fallback (works for manual closes too).
	fetcher, ok := r.trader.(trade.IncomeFetcher)
	if !ok {
		return nil
	}
	since := vp.OpenedAt.Add(2 * time.Second) // skip entry commission tick
	until := time.Now().UTC().Add(60 * time.Second)
	records, err := fetcher.Income(ctx, since, until)
	if err != nil {
		r.log.Warn().Err(err).Int64("vp_id", vp.ID).Msg("recovery: income api failed")
		return nil
	}
	var pnlSum, commSum decimal.Decimal
	var actualClose time.Time
	for _, rec := range records {
		if rec.Symbol != vp.Symbol {
			continue
		}
		switch rec.Type {
		case "REALIZED_PNL":
			pnlSum = pnlSum.Add(rec.Income)
			if rec.Time.After(actualClose) {
				actualClose = rec.Time
			}
		case "COMMISSION":
			commSum = commSum.Add(rec.Income.Abs())
			if rec.Time.After(actualClose) {
				actualClose = rec.Time
			}
		}
	}
	if pnlSum.IsZero() || actualClose.IsZero() {
		return nil
	}
	// Back-compute exit price from realized PnL.
	var exitPrice decimal.Decimal
	if vp.Side == "long" {
		exitPrice = vp.EntryFillPrice.Add(pnlSum.Div(vp.Qty))
	} else {
		exitPrice = vp.EntryFillPrice.Sub(pnlSum.Div(vp.Qty))
	}
	return &closeData{
		ExitPrice: exitPrice, ExitQty: vp.Qty, Fees: commSum,
		ClosedAt:    actualClose,
		CloseReason: notify.CloseReasonRecoveryOffline,
	}
}

// recordHistory inserts a position_history row matching the close data.
func (r *Recovery) recordHistory(ctx context.Context, vp *store.VirtualPositionRow, cd closeData) error {
	if r.historyRepo == nil {
		return nil
	}
	pnl := notify.ComputePnL(vp.Side, vp.EntryFillPrice, cd.ExitPrice, cd.ExitQty)
	pnlPct := decimal.Zero
	if !vp.EntryFillPrice.IsZero() && !cd.ExitQty.IsZero() {
		pnlPct = pnl.Div(vp.EntryFillPrice.Mul(cd.ExitQty)).Mul(decimal.NewFromInt(100))
	}
	closedAt := cd.ClosedAt
	if closedAt.IsZero() {
		closedAt = time.Now().UTC()
	}
	duration := int(closedAt.Sub(vp.OpenedAt).Seconds())
	row := store.PositionHistoryRow{
		StrategyID: vp.StrategyID, Symbol: vp.Symbol, Side: vp.Side, Qty: cd.ExitQty,
		EntrySignalPrice: vp.EntrySignalPrice, EntryFillPrice: vp.EntryFillPrice,
		ExitSignalPrice:  cd.ExitPrice, ExitFillPrice: cd.ExitPrice,
		PnLUSDC:          pnl, PnLPct: pnlPct, FeesUSDC: cd.Fees,
		CloseReason:      mapNotifyReasonToHistory(cd.CloseReason),
		DurationSeconds:  duration,
		OpenedAt:         vp.OpenedAt, ClosedAt: closedAt,
	}
	if err := r.historyRepo.Insert(ctx, r.pool, row); err != nil {
		return err
	}
	// Phase 3: push trade_closed after successful Insert. nil-safe.
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

// mapNotifyReasonToHistory translates notify.CloseReason* constants to the
// string convention used by position_history.close_reason.
func mapNotifyReasonToHistory(reason string) string {
	// notify constants happen to use the same strings as history values.
	return reason
}

// findOfflineExitFill iterates the protective orders attached to a VP and
// returns details of the first one the exchange reports as filled. Also
// syncs the order row in DB so the reconciler doesn't re-poll a stale row.
func (r *Recovery) findOfflineExitFill(ctx context.Context, vp *store.VirtualPositionRow) (decimal.Decimal, decimal.Decimal, decimal.Decimal, string, bool) {
	if r.orderRepo == nil {
		return decimal.Zero, decimal.Zero, decimal.Zero, "", false
	}
	type cand struct {
		ID      int64
		Purpose string
	}
	candidates := []cand{
		{vp.StopOrderID, "stop"},
		{vp.BackupStopOrderID, "backup_stop"},
		{vp.TakeProfitOrderID, "take_profit"},
	}
	for _, c := range candidates {
		if c.ID == 0 {
			continue
		}
		clientID, err := r.orderRepo.GetClientOrderIDByID(ctx, r.pool, c.ID)
		if err != nil {
			r.log.Warn().Err(err).Int64("order_id", c.ID).Msg("recovery: get client order id failed")
			continue
		}
		res, err := r.trader.GetOrder(ctx, vp.Symbol, clientID)
		if err != nil {
			r.log.Warn().Err(err).Str("client_order_id", clientID).Msg("recovery: GetOrder failed")
			continue
		}
		if string(res.Status) == "filled" {
			if err := r.orderRepo.UpdateOnFill(ctx, r.pool, c.ID, res.ExchangeOrderID,
				res.FilledQty, res.AvgFillPrice, res.FeesUSDC); err != nil {
				r.log.Warn().Err(err).Int64("order_id", c.ID).Msg("recovery: sync filled order failed")
			}
			return res.AvgFillPrice, res.FilledQty, res.FeesUSDC, c.Purpose, true
		}
	}
	return decimal.Zero, decimal.Zero, decimal.Zero, "", false
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
