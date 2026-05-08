package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type StrategyRow struct {
	ID            string
	Symbol        string
	Leverage      int
	SizeUSDC      decimal.Decimal
	StopLossPct   decimal.Decimal
	TakeProfitPct decimal.Decimal // zero = none
	MaxOpenUSDC   decimal.Decimal
	Enabled       bool
	Archived      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type StrategyRepo struct {
	pool *pgxpool.Pool
}

func NewStrategyRepo(pool *pgxpool.Pool) *StrategyRepo { return &StrategyRepo{pool: pool} }

func (r *StrategyRepo) Create(ctx context.Context, q Querier, in StrategyRow) error {
	_, err := q.Exec(ctx, `
INSERT INTO strategies
  (id, symbol, leverage, size_usdc, stop_loss_pct, take_profit_pct, max_open_usdc, enabled)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		in.ID, in.Symbol, in.Leverage, in.SizeUSDC, in.StopLossPct,
		nullableDecimal(in.TakeProfitPct), in.MaxOpenUSDC, in.Enabled)
	return err
}

func (r *StrategyRepo) Update(ctx context.Context, q Querier, in StrategyRow) error {
	_, err := q.Exec(ctx, `
UPDATE strategies SET
  symbol=$2, leverage=$3, size_usdc=$4, stop_loss_pct=$5, take_profit_pct=$6,
  max_open_usdc=$7, enabled=$8, updated_at=now()
WHERE id=$1`,
		in.ID, in.Symbol, in.Leverage, in.SizeUSDC, in.StopLossPct,
		nullableDecimal(in.TakeProfitPct), in.MaxOpenUSDC, in.Enabled)
	return err
}

func (r *StrategyRepo) Get(ctx context.Context, q Querier, id string) (*StrategyRow, error) {
	var s StrategyRow
	var tp *decimal.Decimal
	err := q.QueryRow(ctx, `
SELECT id, symbol, leverage, size_usdc, stop_loss_pct, take_profit_pct,
       max_open_usdc, enabled, archived, created_at, updated_at
  FROM strategies WHERE id=$1`, id,
	).Scan(&s.ID, &s.Symbol, &s.Leverage, &s.SizeUSDC, &s.StopLossPct, &tp,
		&s.MaxOpenUSDC, &s.Enabled, &s.Archived, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if tp != nil {
		s.TakeProfitPct = *tp
	}
	return &s, nil
}

// List returns strategies. archived selects which side: false = active list,
// true = archived list. Bot internal callers (e.g. ingest) typically want
// archived=false to ignore archived strategies.
func (r *StrategyRepo) List(ctx context.Context, q Querier, archived bool) ([]*StrategyRow, error) {
	rows, err := q.Query(ctx, `
SELECT id, symbol, leverage, size_usdc, stop_loss_pct, take_profit_pct,
       max_open_usdc, enabled, archived, created_at, updated_at
  FROM strategies WHERE archived=$1 ORDER BY id`, archived)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*StrategyRow{}
	for rows.Next() {
		var s StrategyRow
		var tp *decimal.Decimal
		if err := rows.Scan(&s.ID, &s.Symbol, &s.Leverage, &s.SizeUSDC, &s.StopLossPct, &tp,
			&s.MaxOpenUSDC, &s.Enabled, &s.Archived, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		if tp != nil {
			s.TakeProfitPct = *tp
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// SetArchived flips the archive flag for a strategy.
func (r *StrategyRepo) SetArchived(ctx context.Context, q Querier, id string, archived bool) error {
	_, err := q.Exec(ctx,
		`UPDATE strategies SET archived=$2, updated_at=now() WHERE id=$1`, id, archived)
	return err
}

func (r *StrategyRepo) Delete(ctx context.Context, q Querier, id string) error {
	_, err := q.Exec(ctx, `DELETE FROM strategies WHERE id=$1`, id)
	return err
}

func nullableDecimal(d decimal.Decimal) any {
	if d.IsZero() {
		return nil
	}
	return d
}
