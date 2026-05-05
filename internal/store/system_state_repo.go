package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type SystemStateRow struct {
	Armed          bool
	ArmedAt        time.Time
	ArmedBy        string
	DailyPnLUSDC   decimal.Decimal
	DailyPnLDate   time.Time
	BreakerTripped bool
	BreakerReason  string
	UpdatedAt      time.Time
}

type SystemStateRepo struct {
	pool *pgxpool.Pool
}

func NewSystemStateRepo(pool *pgxpool.Pool) *SystemStateRepo {
	return &SystemStateRepo{pool: pool}
}

func (r *SystemStateRepo) Get(ctx context.Context, q Querier) (*SystemStateRow, error) {
	var s SystemStateRow
	var armedAt *time.Time
	var armedBy, breakerReason *string
	err := q.QueryRow(ctx, `
SELECT armed, armed_at, armed_by, daily_pnl_usdc, daily_pnl_date,
       breaker_tripped, breaker_reason, updated_at
  FROM system_state WHERE id=1`,
	).Scan(&s.Armed, &armedAt, &armedBy, &s.DailyPnLUSDC, &s.DailyPnLDate,
		&s.BreakerTripped, &breakerReason, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if armedAt != nil {
		s.ArmedAt = *armedAt
	}
	if armedBy != nil {
		s.ArmedBy = *armedBy
	}
	if breakerReason != nil {
		s.BreakerReason = *breakerReason
	}
	return &s, nil
}

func (r *SystemStateRepo) Arm(ctx context.Context, q Querier, by string) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET armed=true, armed_at=now(), armed_by=$1, updated_at=now() WHERE id=1`,
		by)
	return err
}

func (r *SystemStateRepo) Disarm(ctx context.Context, q Querier) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET armed=false, updated_at=now() WHERE id=1`)
	return err
}

// AddDailyPnL atomically adds delta to daily_pnl_usdc, rolling over to 0 first
// if `now` falls on a different UTC date than daily_pnl_date.
func (r *SystemStateRepo) AddDailyPnL(ctx context.Context, q Querier, delta decimal.Decimal, now time.Time) error {
	today := now.UTC().Format("2006-01-02")
	_, err := q.Exec(ctx, `
UPDATE system_state
   SET daily_pnl_usdc = CASE WHEN daily_pnl_date = $2::date THEN daily_pnl_usdc + $1 ELSE $1 END,
       daily_pnl_date = $2::date,
       updated_at = now()
 WHERE id=1`, delta, today)
	return err
}

func (r *SystemStateRepo) TripBreaker(ctx context.Context, q Querier, reason string) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET breaker_tripped=true, breaker_reason=$1, updated_at=now() WHERE id=1`,
		reason)
	return err
}

func (r *SystemStateRepo) ResetBreaker(ctx context.Context, q Querier) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET breaker_tripped=false, breaker_reason=NULL, daily_pnl_usdc=0, updated_at=now() WHERE id=1`)
	return err
}
