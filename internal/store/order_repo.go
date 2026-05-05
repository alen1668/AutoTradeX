package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type OrderRow struct {
	ID                int64
	VirtualPositionID int64
	StrategyID        string
	Symbol            string
	Side              string
	Type              string
	Purpose           string
	Qty               decimal.Decimal
	Price             decimal.Decimal
	StopPrice         decimal.Decimal
	ClientOrderID     string
	ExchangeOrderID   string
	Status            string
	FilledQty         decimal.Decimal
	AvgFillPrice      decimal.Decimal
	FeesUSDC          decimal.Decimal
	SubmittedAt       time.Time
	FilledAt          time.Time
	RawResponse       json.RawMessage
}

type OrderRepo struct {
	pool *pgxpool.Pool
}

func NewOrderRepo(pool *pgxpool.Pool) *OrderRepo { return &OrderRepo{pool: pool} }

func (r *OrderRepo) Insert(ctx context.Context, q Querier, in OrderRow) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
INSERT INTO orders
  (virtual_position_id, strategy_id, symbol, side, type, purpose, qty, price,
   stop_price, client_order_id, status)
VALUES (NULLIF($1,0)::bigint, $2,$3,$4,$5,$6,$7, NULLIF($8,0)::numeric, NULLIF($9,0)::numeric, $10, $11::order_status)
RETURNING id`,
		in.VirtualPositionID, in.StrategyID, in.Symbol, in.Side, in.Type, in.Purpose,
		in.Qty, in.Price, in.StopPrice, in.ClientOrderID, in.Status,
	).Scan(&id)
	return id, err
}

func (r *OrderRepo) GetByClientID(ctx context.Context, q Querier, clientID string) (*OrderRow, error) {
	return r.scanOne(ctx, q,
		`SELECT id, COALESCE(virtual_position_id,0), strategy_id, symbol, side, type, purpose,
                qty, COALESCE(price,0), COALESCE(stop_price,0),
                client_order_id, COALESCE(exchange_order_id,''), status::text,
                filled_qty, COALESCE(avg_fill_price,0), fees_usdc,
                COALESCE(submitted_at, '0001-01-01'::timestamptz),
                COALESCE(filled_at, '0001-01-01'::timestamptz),
                COALESCE(raw_response::text,'{}')
           FROM orders WHERE client_order_id=$1`, clientID)
}

func (r *OrderRepo) UpdateOnFill(ctx context.Context, q Querier, id int64, exchangeOrderID string,
	filledQty, avgFillPrice, fees decimal.Decimal) error {
	_, err := q.Exec(ctx, `
UPDATE orders SET
  status='filled'::order_status,
  exchange_order_id=$2,
  filled_qty=$3,
  avg_fill_price=$4,
  fees_usdc=$5,
  filled_at=now(),
  updated_at=now()
WHERE id=$1`, id, exchangeOrderID, filledQty, avgFillPrice, fees)
	return err
}

func (r *OrderRepo) UpdateStatus(ctx context.Context, q Querier, id int64, status string) error {
	_, err := q.Exec(ctx,
		`UPDATE orders SET status=$2::order_status, updated_at=now() WHERE id=$1`,
		id, status)
	return err
}

func (r *OrderRepo) ListPending(ctx context.Context, q Querier) ([]*OrderRow, error) {
	rows, err := q.Query(ctx, `
SELECT id, COALESCE(virtual_position_id,0), strategy_id, symbol, side, type, purpose,
       qty, COALESCE(price,0), COALESCE(stop_price,0),
       client_order_id, COALESCE(exchange_order_id,''), status::text,
       filled_qty, COALESCE(avg_fill_price,0), fees_usdc,
       COALESCE(submitted_at,'0001-01-01'::timestamptz),
       COALESCE(filled_at,'0001-01-01'::timestamptz),
       COALESCE(raw_response::text,'{}')
  FROM orders WHERE status IN ('pending','submitted','partial')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*OrderRow{}
	for rows.Next() {
		o, err := r.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *OrderRepo) scanOne(ctx context.Context, q Querier, sql string, args ...any) (*OrderRow, error) {
	row := q.QueryRow(ctx, sql, args...)
	var o OrderRow
	var rawResp string
	if err := row.Scan(&o.ID, &o.VirtualPositionID, &o.StrategyID, &o.Symbol, &o.Side, &o.Type, &o.Purpose,
		&o.Qty, &o.Price, &o.StopPrice, &o.ClientOrderID, &o.ExchangeOrderID, &o.Status,
		&o.FilledQty, &o.AvgFillPrice, &o.FeesUSDC, &o.SubmittedAt, &o.FilledAt, &rawResp); err != nil {
		return nil, err
	}
	o.RawResponse = json.RawMessage(rawResp)
	return &o, nil
}

func (r *OrderRepo) scanRow(rows pgxRow) (*OrderRow, error) {
	var o OrderRow
	var rawResp string
	if err := rows.Scan(&o.ID, &o.VirtualPositionID, &o.StrategyID, &o.Symbol, &o.Side, &o.Type, &o.Purpose,
		&o.Qty, &o.Price, &o.StopPrice, &o.ClientOrderID, &o.ExchangeOrderID, &o.Status,
		&o.FilledQty, &o.AvgFillPrice, &o.FeesUSDC, &o.SubmittedAt, &o.FilledAt, &rawResp); err != nil {
		return nil, err
	}
	o.RawResponse = json.RawMessage(rawResp)
	return &o, nil
}

// pgxRow narrows pgx.Rows.Scan to what scanRow needs.
type pgxRow interface {
	Scan(dest ...any) error
}
