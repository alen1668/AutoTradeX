package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type PositionHistoryRow struct {
	ID                  int64
	StrategyID          string
	Symbol              string
	Side                string
	Qty                 decimal.Decimal
	EntrySignalPrice    decimal.Decimal
	EntryFillPrice      decimal.Decimal
	ExitSignalPrice     decimal.Decimal
	ExitFillPrice       decimal.Decimal
	PnLUSDC             decimal.Decimal
	PnLPct              decimal.Decimal
	FeesUSDC            decimal.Decimal
	OpenSignalToFillMs  int
	CloseSignalToFillMs int
	OpenSlippageBP      decimal.Decimal
	CloseSlippageBP     decimal.Decimal
	CloseReason         string
	DurationSeconds     int
	OpenedAt            time.Time
	ClosedAt            time.Time
}

type PositionHistoryRepo struct {
	pool *pgxpool.Pool
}

func NewPositionHistoryRepo(pool *pgxpool.Pool) *PositionHistoryRepo {
	return &PositionHistoryRepo{pool: pool}
}

func (r *PositionHistoryRepo) Insert(ctx context.Context, q Querier, in PositionHistoryRow) error {
	_, err := q.Exec(ctx, `
INSERT INTO position_history
  (strategy_id, symbol, side, qty, entry_signal_price, entry_fill_price,
   exit_signal_price, exit_fill_price, pnl_usdc, pnl_pct, fees_usdc,
   open_signal_to_fill_ms, close_signal_to_fill_ms, open_slippage_bp,
   close_slippage_bp, close_reason, duration_seconds, opened_at, closed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		in.StrategyID, in.Symbol, in.Side, in.Qty, in.EntrySignalPrice, in.EntryFillPrice,
		in.ExitSignalPrice, in.ExitFillPrice, in.PnLUSDC, in.PnLPct, in.FeesUSDC,
		in.OpenSignalToFillMs, in.CloseSignalToFillMs, in.OpenSlippageBP,
		in.CloseSlippageBP, in.CloseReason, in.DurationSeconds, in.OpenedAt, in.ClosedAt)
	return err
}

func (r *PositionHistoryRepo) ListByStrategy(ctx context.Context, q Querier, strategyID string, limit int) ([]*PositionHistoryRow, error) {
	rows, err := q.Query(ctx, `
SELECT id, strategy_id, symbol, side, qty, entry_signal_price, entry_fill_price,
       exit_signal_price, exit_fill_price, pnl_usdc, pnl_pct, fees_usdc,
       open_signal_to_fill_ms, close_signal_to_fill_ms, open_slippage_bp,
       close_slippage_bp, close_reason, duration_seconds, opened_at, closed_at
  FROM position_history
 WHERE strategy_id=$1
 ORDER BY closed_at DESC
 LIMIT $2`, strategyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PositionHistoryRow{}
	for rows.Next() {
		var h PositionHistoryRow
		var openMs, closeMs *int
		var openSlip, closeSlip *decimal.Decimal
		if err := rows.Scan(&h.ID, &h.StrategyID, &h.Symbol, &h.Side, &h.Qty,
			&h.EntrySignalPrice, &h.EntryFillPrice, &h.ExitSignalPrice, &h.ExitFillPrice,
			&h.PnLUSDC, &h.PnLPct, &h.FeesUSDC,
			&openMs, &closeMs, &openSlip, &closeSlip,
			&h.CloseReason, &h.DurationSeconds, &h.OpenedAt, &h.ClosedAt); err != nil {
			return nil, err
		}
		if openMs != nil {
			h.OpenSignalToFillMs = *openMs
		}
		if closeMs != nil {
			h.CloseSignalToFillMs = *closeMs
		}
		if openSlip != nil {
			h.OpenSlippageBP = *openSlip
		}
		if closeSlip != nil {
			h.CloseSlippageBP = *closeSlip
		}
		out = append(out, &h)
	}
	return out, rows.Err()
}
