package trade

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/domain/order"
	"github.com/lizhaojie/tvbot/internal/domain/position"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
	"github.com/lizhaojie/tvbot/internal/eval"
	"github.com/lizhaojie/tvbot/internal/store"
	tradepkg "github.com/lizhaojie/tvbot/internal/trade"
)

// shortClientID builds a Binance client_order_id within the 36-char hard
// limit. It uses single-character purpose prefixes (e=entry, s=stop,
// b=backup_stop, t=take_profit, x=exit) and strips the "tv-" prefix from
// the trace id, preserving uniqueness while saving ~7 characters.
//
// Format: <prefix>-<traceID-without-tv>-<suffix>
// Example: e-20260508TRX-1778310000000-44 (29 chars, fits with margin
// even for 11-char strategy IDs).
func shortClientID(prefix, traceID string, suffix int64) string {
	t := strings.TrimPrefix(traceID, "tv-")
	return fmt.Sprintf("%s-%s-%d", prefix, t, suffix)
}

// StepSizer returns the lot-size step for a given symbol. BinanceTrader
// satisfies this interface; DryRunTrader also provides a no-op implementation.
type StepSizer interface {
	StepSize(ctx context.Context, symbol string) (decimal.Decimal, error)
}

// Service orchestrates open/close/stop coordination for virtual positions.
type Service struct {
	pool        *pgxpool.Pool
	orderRepo   *store.OrderRepo
	posRepo     *store.VirtualPositionRepo
	historyRepo *store.PositionHistoryRepo
	systemRepo  *store.SystemStateRepo // optional: when non-nil, ClosePosition rolls PnL into daily_pnl_usdc (powers UI + DailyLossBreaker)
	trader      tradepkg.Trader
	stepSizer   StepSizer
	publisher   eval.Publisher // nil-safe Phase 3 SSE wiring
}

func NewService(pool *pgxpool.Pool, orderRepo *store.OrderRepo, posRepo *store.VirtualPositionRepo,
	historyRepo *store.PositionHistoryRepo, trader tradepkg.Trader) *Service {
	svc := &Service{
		pool: pool, orderRepo: orderRepo, posRepo: posRepo, historyRepo: historyRepo,
		trader: trader,
	}
	// If the trader also implements StepSizer, use it by default.
	if ss, ok := trader.(StepSizer); ok {
		svc.stepSizer = ss
	}
	return svc
}

// WithSystemRepo enables daily PnL accumulation. Without it, daily_pnl_usdc
// stays at 0 — that breaks the daily-loss breaker rule. cmd/tvbot/main.go
// always wires this.
func (s *Service) WithSystemRepo(r *store.SystemStateRepo) *Service {
	s.systemRepo = r
	return s
}

// WithStepSizer overrides the StepSizer used to look up lot-size step for a symbol.
func (s *Service) WithStepSizer(ss StepSizer) {
	s.stepSizer = ss
}

// WithPublisher wires the Phase 3 SSE broker into the trade service.
// nil is accepted and disables trade_closed publishing.
func (s *Service) WithPublisher(p eval.Publisher) *Service {
	s.publisher = p
	return s
}

// OpenInput captures everything needed to open a virtual position.
type OpenInput struct {
	Strategy    *strategy.Strategy
	Side        position.Side
	SignalPrice decimal.Decimal
	SignalID    int64
	TraceID     string
}

// OpenResult is what the caller gets back.
type OpenResult struct {
	VirtualPositionID int64
	EntryFillPrice    decimal.Decimal
	Qty               decimal.Decimal
}

// OpenPosition inserts a virtual_position row, places the entry market order,
// records the fill, places stop + backup_stop + optional take_profit, attaches
// protective order IDs, and marks the VP open. All DB writes are in one
// transaction (acceptable for DryRun; Plan 2B will restructure for real orders).
func (s *Service) OpenPosition(ctx context.Context, in OpenInput) (*OpenResult, error) {
	// 1) Resolve qty step (from exchange info or fallback default)
	qtyStep := decimal.NewFromFloat(0.001)
	if s.stepSizer != nil {
		if step, err := s.stepSizer.StepSize(ctx, in.Strategy.Symbol); err == nil && step.IsPositive() {
			qtyStep = step
		}
	}

	// Compute qty
	notional := in.Strategy.NotionalUSDC()
	rawQty := notional.Div(in.SignalPrice)
	qty := floorTo(rawQty, qtyStep)
	if !qty.IsPositive() {
		return nil, fmt.Errorf("qty rounds to 0 (notional=%s price=%s step=%s)",
			notional, in.SignalPrice, qtyStep)
	}

	res := &OpenResult{Qty: qty}

	err := store.WithTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// 2) Insert virtual position (status='opening')
		vpID, err := s.posRepo.Insert(ctx, tx, store.VirtualPositionRow{
			StrategyID:       in.Strategy.ID,
			Symbol:           in.Strategy.Symbol,
			Side:             string(in.Side),
			Qty:              qty,
			EntrySignalPrice: in.SignalPrice,
			EntrySignalID:    in.SignalID,
			Status:           string(position.StatusOpening),
		})
		if err != nil {
			return err
		}
		res.VirtualPositionID = vpID

		// 3) Place entry market order
		entrySide := tradepkg.OrderSideBuy
		if in.Side == position.SideShort {
			entrySide = tradepkg.OrderSideSell
		}
		entryClientID := shortClientID("e", in.TraceID, vpID)
		entryRes, err := s.trader.Place(ctx, tradepkg.OrderRequest{
			ClientOrderID:  entryClientID,
			Symbol:         in.Strategy.Symbol,
			Side:           entrySide,
			Type:           tradepkg.OrderTypeMarket,
			Qty:            qty,
			ReferencePrice: in.SignalPrice,
			Purpose:        "entry",
		})
		if err != nil {
			return fmt.Errorf("place entry: %w", err)
		}
		if entryRes.Status != tradepkg.OrderStatusFilled {
			return fmt.Errorf("entry not filled (status=%s)", entryRes.Status)
		}

		// 4) Insert order row + update VP entry fill
		entryOrderID, err := s.orderRepo.Insert(ctx, tx, store.OrderRow{
			VirtualPositionID: vpID,
			StrategyID:        in.Strategy.ID,
			Symbol:            in.Strategy.Symbol,
			Side:              string(entrySide),
			Type:              string(order.TypeMarket),
			Purpose:           string(order.PurposeEntry),
			Qty:               qty,
			ClientOrderID:     entryClientID,
			Status:            string(order.StatusFilled),
		})
		if err != nil {
			return err
		}
		if err := s.orderRepo.UpdateOnFill(ctx, tx, entryOrderID, entryRes.ExchangeOrderID,
			entryRes.FilledQty, entryRes.AvgFillPrice, entryRes.FeesUSDC); err != nil {
			return err
		}
		if err := s.posRepo.SetEntryFill(ctx, tx, vpID, entryRes.AvgFillPrice, entryOrderID); err != nil {
			return err
		}
		res.EntryFillPrice = entryRes.AvgFillPrice

		// 5) Compute + place protective orders (stop, backup_stop, optional take_profit)
		stopID, backupID, tpID, err := s.placeProtectiveOrders(ctx, tx, vpID, in.Strategy, in.Side,
			entryRes.AvgFillPrice, qty, in.TraceID)
		if err != nil {
			return err
		}
		if err := s.posRepo.SetProtectiveOrders(ctx, tx, vpID, stopID, backupID, tpID); err != nil {
			return err
		}

		// 6) Mark VP as 'open'
		return s.posRepo.UpdateStatus(ctx, tx, vpID, string(position.StatusOpen))
	})
	return res, err
}

func (s *Service) placeProtectiveOrders(ctx context.Context, tx pgx.Tx, vpID int64,
	strat *strategy.Strategy, side position.Side, entryFill, qty decimal.Decimal, traceID string,
) (stopID, backupID, tpID int64, err error) {
	// Direction multipliers: long → stop below, take-profit above. short → opposite.
	dir := decimal.NewFromInt(1)
	if side == position.SideShort {
		dir = decimal.NewFromInt(-1)
	}
	pct := strat.StopLossPct.Div(decimal.NewFromInt(100))
	mainStopTrigger := entryFill.Mul(decimal.NewFromInt(1).Sub(pct.Mul(dir)))
	mainStopLimit := mainStopTrigger.Mul(decimal.NewFromFloat(0.999)) // slight slip toward unfavorable
	backupTrigger := mainStopTrigger.Mul(decimal.NewFromFloat(0.998))

	exitSide := tradepkg.OrderSideSell
	if side == position.SideShort {
		exitSide = tradepkg.OrderSideBuy
	}

	// 1) Main stop (limit stop)
	stopClientID := shortClientID("s", traceID, vpID)
	stopRes, err := s.trader.Place(ctx, tradepkg.OrderRequest{
		ClientOrderID: stopClientID, Symbol: strat.Symbol, Side: exitSide,
		Type: tradepkg.OrderTypeStop, Qty: qty, Price: mainStopLimit, StopPrice: mainStopTrigger,
		ReferencePrice: entryFill, Purpose: "stop",
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("place stop: %w", err)
	}
	_ = stopRes
	stopID, err = s.orderRepo.Insert(ctx, tx, store.OrderRow{
		VirtualPositionID: vpID, StrategyID: strat.ID, Symbol: strat.Symbol,
		Side: string(exitSide), Type: string(order.TypeStop), Purpose: string(order.PurposeStop),
		Qty: qty, Price: mainStopLimit, StopPrice: mainStopTrigger,
		ClientOrderID: stopClientID, Status: string(order.StatusSubmitted),
	})
	if err != nil {
		return 0, 0, 0, err
	}

	// 2) Backup market stop
	// "bstop"/"tp" prefixes (instead of "backup_stop"/"take_profit") keep the
	// Binance clientOrderId under the 35-char limit (-4015). The internal
	// `purpose` column on orders rows is unaffected.
	backupClientID := shortClientID("b", traceID, vpID)
	if _, err := s.trader.Place(ctx, tradepkg.OrderRequest{
		ClientOrderID: backupClientID, Symbol: strat.Symbol, Side: exitSide,
		Type: tradepkg.OrderTypeStopMarket, Qty: qty, StopPrice: backupTrigger,
		ReferencePrice: entryFill, Purpose: "backup_stop",
	}); err != nil {
		return 0, 0, 0, fmt.Errorf("place backup_stop: %w", err)
	}
	backupID, err = s.orderRepo.Insert(ctx, tx, store.OrderRow{
		VirtualPositionID: vpID, StrategyID: strat.ID, Symbol: strat.Symbol,
		Side: string(exitSide), Type: string(order.TypeStopMarket), Purpose: string(order.PurposeBackupStop),
		Qty: qty, StopPrice: backupTrigger,
		ClientOrderID: backupClientID, Status: string(order.StatusSubmitted),
	})
	if err != nil {
		return 0, 0, 0, err
	}

	// 3) Optional take profit
	if strat.HasTakeProfit() {
		tpPct := strat.TakeProfitPct.Div(decimal.NewFromInt(100))
		tpTrigger := entryFill.Mul(decimal.NewFromInt(1).Add(tpPct.Mul(dir)))
		tpClientID := shortClientID("t", traceID, vpID)
		if _, err := s.trader.Place(ctx, tradepkg.OrderRequest{
			ClientOrderID: tpClientID, Symbol: strat.Symbol, Side: exitSide,
			Type: tradepkg.OrderTypeTakeProfitMarket, Qty: qty, StopPrice: tpTrigger,
			ReferencePrice: entryFill, Purpose: "take_profit",
		}); err != nil {
			return 0, 0, 0, fmt.Errorf("place take_profit: %w", err)
		}
		tpID, err = s.orderRepo.Insert(ctx, tx, store.OrderRow{
			VirtualPositionID: vpID, StrategyID: strat.ID, Symbol: strat.Symbol,
			Side: string(exitSide), Type: string(order.TypeTakeProfitMarket),
			Purpose: string(order.PurposeTakeProfit), Qty: qty, StopPrice: tpTrigger,
			ClientOrderID: tpClientID, Status: string(order.StatusSubmitted),
		})
		if err != nil {
			return 0, 0, 0, err
		}
	}
	return stopID, backupID, tpID, nil
}

// CloseInput is what the caller passes to close an existing virtual position.
type CloseInput struct {
	VirtualPositionID int64
	StrategyID        string
	Symbol            string
	Side              position.Side
	Qty               decimal.Decimal
	EntryFillPrice    decimal.Decimal
	StopOrderID       int64
	BackupStopOrderID int64
	TakeProfitOrderID int64
	OpenedAt          time.Time
	EntrySignalPrice  decimal.Decimal
	ExitSignalPrice   decimal.Decimal
	CloseReason       string // "signal" | "stop_loss" | "take_profit" | "manual"
	TraceID           string
}

// CloseResult is returned after a successful close.
type CloseResult struct {
	ExitFillPrice decimal.Decimal
	PnLUSDC       decimal.Decimal
}

// ClosePosition cancels protective orders (best-effort), places the exit market
// order, computes PnL, inserts a position_history row, and marks the VP closed.
// The cancel + market Place() happen outside a transaction; DB writes are inside
// one transaction.
func (s *Service) ClosePosition(ctx context.Context, in CloseInput) (*CloseResult, error) {
	// 1) Cancel protective orders (best-effort; for Plan 2 DryRun, Cancel is a no-op)
	for _, oid := range []int64{in.StopOrderID, in.BackupStopOrderID, in.TakeProfitOrderID} {
		if oid == 0 {
			continue
		}
		cid, err := s.orderRepo.GetClientOrderIDByID(ctx, s.pool, oid)
		if err != nil {
			// Best-effort cancel; log but don't abort
			continue
		}
		_ = s.trader.Cancel(ctx, in.Symbol, cid)
		_ = s.orderRepo.UpdateStatus(ctx, s.pool, oid, "canceled")
	}

	// 2) Place exit market order (outside tx — network call)
	exitSide := tradepkg.OrderSideSell
	if in.Side == position.SideShort {
		exitSide = tradepkg.OrderSideBuy
	}
	exitClientID := shortClientID("x", in.TraceID, in.VirtualPositionID)
	exitRes, err := s.trader.Place(ctx, tradepkg.OrderRequest{
		ClientOrderID:  exitClientID,
		Symbol:         in.Symbol,
		Side:           exitSide,
		Type:           tradepkg.OrderTypeMarket,
		Qty:            in.Qty,
		ReferencePrice: in.ExitSignalPrice,
		Purpose:        "exit",
	})
	if err != nil {
		return nil, err
	}
	if exitRes.Status != tradepkg.OrderStatusFilled {
		return nil, errors.New("exit not filled")
	}

	// 3) Compute PnL
	pnl := computePnL(in.Side, in.Qty, in.EntryFillPrice, exitRes.AvgFillPrice)

	// 4) DB writes inside one transaction
	err = store.WithTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		exitOrderID, err := s.orderRepo.Insert(ctx, tx, store.OrderRow{
			VirtualPositionID: in.VirtualPositionID,
			StrategyID:        in.StrategyID,
			Symbol:            in.Symbol,
			Side:              string(exitSide),
			Type:              string(order.TypeMarket),
			Purpose:           string(order.PurposeExit),
			Qty:               in.Qty,
			ClientOrderID:     exitClientID,
			Status:            string(order.StatusFilled),
		})
		if err != nil {
			return err
		}
		if err := s.orderRepo.UpdateOnFill(ctx, tx, exitOrderID, exitRes.ExchangeOrderID,
			exitRes.FilledQty, exitRes.AvgFillPrice, exitRes.FeesUSDC); err != nil {
			return err
		}

		// 5) Mark VP closed
		if err := s.posRepo.MarkClosed(ctx, tx, in.VirtualPositionID); err != nil {
			return err
		}

		// 6) Insert position_history row
		now := time.Now().UTC()
		dur := int(now.Sub(in.OpenedAt).Seconds())
		if dur < 0 {
			dur = 0
		}
		pnlPct := decimal.Zero
		if !in.EntryFillPrice.IsZero() {
			delta := exitRes.AvgFillPrice.Sub(in.EntryFillPrice)
			if in.Side == position.SideShort {
				delta = delta.Neg()
			}
			pnlPct = delta.Div(in.EntryFillPrice).Mul(decimal.NewFromInt(100))
		}
		if err := s.historyRepo.Insert(ctx, tx, store.PositionHistoryRow{
			StrategyID:          in.StrategyID,
			Symbol:              in.Symbol,
			Side:                string(in.Side),
			Qty:                 in.Qty,
			EntrySignalPrice:    in.EntrySignalPrice,
			EntryFillPrice:      in.EntryFillPrice,
			ExitSignalPrice:     in.ExitSignalPrice,
			ExitFillPrice:       exitRes.AvgFillPrice,
			PnLUSDC:             pnl,
			PnLPct:              pnlPct,
			FeesUSDC:            exitRes.FeesUSDC,
			OpenSignalToFillMs:  0, // Plan 2B will populate
			CloseSignalToFillMs: 0,
			OpenSlippageBP:      decimal.Zero,
			CloseSlippageBP:     decimal.Zero,
			CloseReason:         in.CloseReason,
			DurationSeconds:     dur,
			OpenedAt:            in.OpenedAt,
			ClosedAt:            now,
		}); err != nil {
			return err
		}

		// 7) Accumulate into daily PnL (powers UI + DailyLossBreakerRule).
		// systemRepo can be nil in unit tests that don't care; when nil
		// we silently skip — pre-existing tests still pass.
		if s.systemRepo != nil {
			if err := s.systemRepo.AddDailyPnL(ctx, tx, pnl, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Phase 3: push trade_closed event to any SSE subscribers. Strictly
	// after the transaction commits so we never publish an event that
	// didn't actually persist. Fire-and-forget — broker is non-blocking.
	if s.publisher != nil {
		pnlFloat, _ := pnl.Float64()
		s.publisher.Publish(eval.EvalEvent{
			Kind:       "trade_closed",
			Symbol:     in.Symbol,
			PnLUSDC:    &pnlFloat,
			OccurredAt: time.Now().Unix(),
		})
	}

	return &CloseResult{ExitFillPrice: exitRes.AvgFillPrice, PnLUSDC: pnl}, nil
}

// computePnL returns realised PnL in USDC for a closed position.
// Long:  (exit - entry) * qty
// Short: (entry - exit) * qty
func computePnL(side position.Side, qty, entry, exit decimal.Decimal) decimal.Decimal {
	delta := exit.Sub(entry)
	if side == position.SideShort {
		delta = delta.Neg()
	}
	return delta.Mul(qty)
}

// floorTo floors v to the nearest multiple of step (e.g. step=0.001 for LOT_SIZE).
func floorTo(v, step decimal.Decimal) decimal.Decimal {
	if step.IsZero() {
		return v
	}
	return v.Div(step).Floor().Mul(step)
}
