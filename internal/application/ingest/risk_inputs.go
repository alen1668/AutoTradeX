package ingest

import (
	"context"
	"errors"
	"net"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/domain/position"
	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
	"github.com/lizhaojie/tvbot/internal/risk"
	"github.com/lizhaojie/tvbot/internal/store"
)

// LoadCtx is everything we read from DB to populate a risk.Input.
type LoadCtx struct {
	Strategy         *strategy.Strategy
	StrategyArchived bool // true → ingest must reject before reaching risk pipeline
	CurrentPosition  *position.VirtualPosition
	OpenNotionalSum  decimal.Decimal
	DailyPnLUSDC     decimal.Decimal
	BreakerTripped   bool
}

func loadAll(ctx context.Context, q store.Querier, pool *pgxpool.Pool,
	strategyRepo *store.StrategyRepo, posRepo *store.VirtualPositionRepo,
	systemRepo *store.SystemStateRepo, strategyID string,
) (*LoadCtx, error) {
	stratRow, err := strategyRepo.Get(ctx, q, strategyID)
	if err != nil {
		return nil, err
	}
	strat, err := strategy.New(strategy.Config{
		ID: stratRow.ID, Symbol: stratRow.Symbol, Leverage: stratRow.Leverage,
		SizeUSDC: stratRow.SizeUSDC, StopLossPct: stratRow.StopLossPct,
		TakeProfitPct: stratRow.TakeProfitPct, MaxOpenUSDC: stratRow.MaxOpenUSDC,
		Enabled: stratRow.Enabled,
	})
	if err != nil {
		return nil, err
	}

	posRow, err := posRepo.GetActiveByStrategy(ctx, q, strategyID)
	var pos *position.VirtualPosition
	if err == nil {
		pos = &position.VirtualPosition{
			ID: posRow.ID, StrategyID: posRow.StrategyID, Symbol: posRow.Symbol,
			Side: position.Side(posRow.Side), Qty: posRow.Qty,
			EntrySignalPrice: posRow.EntrySignalPrice, EntryFillPrice: posRow.EntryFillPrice,
			EntrySignalID: posRow.EntrySignalID, EntryOrderID: posRow.EntryOrderID,
			StopOrderID: posRow.StopOrderID, BackupStopOrderID: posRow.BackupStopOrderID,
			TakeProfitOrderID: posRow.TakeProfitOrderID,
			Status:            position.Status(posRow.Status),
			OpenedAt:          posRow.OpenedAt, ClosedAt: posRow.ClosedAt,
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	state, err := systemRepo.Get(ctx, q)
	if err != nil {
		return nil, err
	}

	openNotional, err := sumOpenNotional(ctx, q)
	if err != nil {
		return nil, err
	}

	return &LoadCtx{
		Strategy: strat, StrategyArchived: stratRow.Archived,
		CurrentPosition: pos,
		OpenNotionalSum: openNotional,
		DailyPnLUSDC:    state.DailyPnLUSDC,
		BreakerTripped:  state.BreakerTripped,
	}, nil
}

func sumOpenNotional(ctx context.Context, q store.Querier) (decimal.Decimal, error) {
	var sum decimal.Decimal
	err := q.QueryRow(ctx, `
SELECT COALESCE(SUM(vp.qty * COALESCE(vp.entry_fill_price, vp.entry_signal_price)), 0)
  FROM virtual_positions vp
 WHERE vp.status IN ('opening','open','closing')`).Scan(&sum)
	return sum, err
}

func buildRiskInput(sig *sigpkg.Signal, ld *LoadCtx, equity decimal.Decimal, ip net.IP) risk.Input {
	return risk.Input{
		Signal: sig, Strategy: ld.Strategy, CurrentPosition: ld.CurrentPosition,
		OpenNotionalSum: ld.OpenNotionalSum, AccountEquity: equity,
		DailyPnLUSDC: ld.DailyPnLUSDC, BreakerTripped: ld.BreakerTripped,
		ClientIP: ip,
	}
}
