package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type VirtualPositionRow struct {
	ID                int64
	StrategyID        string
	Symbol            string
	Side              string
	Qty               decimal.Decimal
	EntrySignalPrice  decimal.Decimal
	EntryFillPrice    decimal.Decimal
	EntrySignalID     int64
	EntryOrderID      int64
	StopOrderID       int64
	BackupStopOrderID int64
	TakeProfitOrderID int64
	Status            string
	OpenedAt          time.Time
	ClosedAt          time.Time
}

type VirtualPositionRepo struct {
	pool *pgxpool.Pool
}

func NewVirtualPositionRepo(pool *pgxpool.Pool) *VirtualPositionRepo {
	return &VirtualPositionRepo{pool: pool}
}

func (r *VirtualPositionRepo) Insert(ctx context.Context, q Querier, in VirtualPositionRow) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
INSERT INTO virtual_positions
  (strategy_id, symbol, side, qty, entry_signal_price, entry_signal_id, status)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		in.StrategyID, in.Symbol, in.Side, in.Qty, in.EntrySignalPrice, in.EntrySignalID, in.Status,
	).Scan(&id)
	return id, err
}

func (r *VirtualPositionRepo) GetByID(ctx context.Context, q Querier, id int64) (*VirtualPositionRow, error) {
	return r.scanOne(ctx, q,
		`SELECT id, strategy_id, symbol, side, qty, entry_signal_price, entry_fill_price,
                entry_signal_id, entry_order_id, stop_order_id, backup_stop_order_id,
                take_profit_order_id, status, opened_at, closed_at
           FROM virtual_positions WHERE id=$1`, id)
}

func (r *VirtualPositionRepo) GetActiveByStrategy(ctx context.Context, q Querier, strategyID string) (*VirtualPositionRow, error) {
	return r.scanOne(ctx, q,
		`SELECT id, strategy_id, symbol, side, qty, entry_signal_price, entry_fill_price,
                entry_signal_id, entry_order_id, stop_order_id, backup_stop_order_id,
                take_profit_order_id, status, opened_at, closed_at
           FROM virtual_positions
          WHERE strategy_id=$1 AND status IN ('opening','open','closing')`, strategyID)
}

func (r *VirtualPositionRepo) UpdateStatus(ctx context.Context, q Querier, id int64, status string) error {
	_, err := q.Exec(ctx, `UPDATE virtual_positions SET status=$2 WHERE id=$1`, id, status)
	return err
}

func (r *VirtualPositionRepo) SetEntryFill(ctx context.Context, q Querier, id int64, fillPrice decimal.Decimal, entryOrderID int64) error {
	_, err := q.Exec(ctx,
		`UPDATE virtual_positions SET entry_fill_price=$2, entry_order_id=$3 WHERE id=$1`,
		id, fillPrice, entryOrderID)
	return err
}

func (r *VirtualPositionRepo) SetProtectiveOrders(ctx context.Context, q Querier, id, stopID, backupStopID, tpID int64) error {
	_, err := q.Exec(ctx, `
UPDATE virtual_positions
   SET stop_order_id        = NULLIF($2,0),
       backup_stop_order_id = NULLIF($3,0),
       take_profit_order_id = NULLIF($4,0)
 WHERE id=$1`, id, stopID, backupStopID, tpID)
	return err
}

func (r *VirtualPositionRepo) MarkClosed(ctx context.Context, q Querier, id int64) error {
	_, err := q.Exec(ctx,
		`UPDATE virtual_positions SET status='closed', closed_at=now() WHERE id=$1`, id)
	return err
}

func (r *VirtualPositionRepo) scanOne(ctx context.Context, q Querier, sql string, args ...any) (*VirtualPositionRow, error) {
	var v VirtualPositionRow
	var entryFill *decimal.Decimal
	var entryOrderID, stopID, backupID, tpID *int64
	var closedAt *time.Time
	err := q.QueryRow(ctx, sql, args...).Scan(
		&v.ID, &v.StrategyID, &v.Symbol, &v.Side, &v.Qty, &v.EntrySignalPrice, &entryFill,
		&v.EntrySignalID, &entryOrderID, &stopID, &backupID, &tpID,
		&v.Status, &v.OpenedAt, &closedAt)
	if err != nil {
		return nil, err
	}
	if entryFill != nil {
		v.EntryFillPrice = *entryFill
	}
	if entryOrderID != nil {
		v.EntryOrderID = *entryOrderID
	}
	if stopID != nil {
		v.StopOrderID = *stopID
	}
	if backupID != nil {
		v.BackupStopOrderID = *backupID
	}
	if tpID != nil {
		v.TakeProfitOrderID = *tpID
	}
	if closedAt != nil {
		v.ClosedAt = *closedAt
	}
	return &v, nil
}

// ListActive returns every currently-open virtual position across all
// strategies (status in opening/open/closing). Used by the agent
// portfolio provider to feed the LLM the system-wide exposure snapshot.
func (r *VirtualPositionRepo) ListActive(ctx context.Context, q Querier) ([]*VirtualPositionRow, error) {
	rows, err := q.Query(ctx, `
SELECT id, strategy_id, symbol, side, qty, entry_signal_price, entry_fill_price,
       entry_signal_id, entry_order_id, stop_order_id, backup_stop_order_id,
       take_profit_order_id, status, opened_at, closed_at
  FROM virtual_positions
 WHERE status IN ('opening','open','closing')
 ORDER BY opened_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*VirtualPositionRow
	for rows.Next() {
		var v VirtualPositionRow
		var entryFill *decimal.Decimal
		var entryOrderID, stopID, backupID, tpID *int64
		var closedAt *time.Time
		if err := rows.Scan(&v.ID, &v.StrategyID, &v.Symbol, &v.Side, &v.Qty,
			&v.EntrySignalPrice, &entryFill,
			&v.EntrySignalID, &entryOrderID, &stopID, &backupID, &tpID,
			&v.Status, &v.OpenedAt, &closedAt); err != nil {
			return nil, err
		}
		if entryFill != nil {
			v.EntryFillPrice = *entryFill
		}
		if entryOrderID != nil {
			v.EntryOrderID = *entryOrderID
		}
		if stopID != nil {
			v.StopOrderID = *stopID
		}
		if backupID != nil {
			v.BackupStopOrderID = *backupID
		}
		if tpID != nil {
			v.TakeProfitOrderID = *tpID
		}
		if closedAt != nil {
			v.ClosedAt = *closedAt
		}
		out = append(out, &v)
	}
	return out, rows.Err()
}
