package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EconomicEventRecord mirrors one row of economic_events.
type EconomicEventRecord struct {
	ID        int64
	SourceID  string
	Name      string
	Currency  string
	Impact    string
	StartsAt  time.Time
	FetchedAt time.Time
}

// EconomicEventsRepo persists Forex Factory event rows. Idempotent UPSERT
// keyed on source_id; queries lookup by starts_at window.
type EconomicEventsRepo struct {
	pool *pgxpool.Pool
}

func NewEconomicEventsRepo(pool *pgxpool.Pool) *EconomicEventsRepo {
	return &EconomicEventsRepo{pool: pool}
}

func (r *EconomicEventsRepo) Upsert(ctx context.Context, q Querier, ev EconomicEventRecord) error {
	_, err := q.Exec(ctx, `
INSERT INTO economic_events (source_id, name, currency, impact, starts_at, fetched_at)
VALUES ($1,$2,$3,$4,$5, NOW())
ON CONFLICT (source_id) DO UPDATE
   SET name=EXCLUDED.name, currency=EXCLUDED.currency, impact=EXCLUDED.impact,
       starts_at=EXCLUDED.starts_at, fetched_at=NOW()`,
		ev.SourceID, ev.Name, ev.Currency, ev.Impact, ev.StartsAt)
	return err
}

// ActiveBetween returns events with starts_at within [from, to], ASC.
func (r *EconomicEventsRepo) ActiveBetween(ctx context.Context, q Querier, from, to time.Time) ([]EconomicEventRecord, error) {
	rows, err := q.Query(ctx, `
SELECT id, source_id, name, currency, impact, starts_at, fetched_at
  FROM economic_events
 WHERE starts_at BETWEEN $1 AND $2
 ORDER BY starts_at`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EconomicEventRecord
	for rows.Next() {
		var ev EconomicEventRecord
		if err := rows.Scan(&ev.ID, &ev.SourceID, &ev.Name, &ev.Currency, &ev.Impact, &ev.StartsAt, &ev.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// Count is used by tests to verify idempotent UPSERT.
func (r *EconomicEventsRepo) Count(ctx context.Context, q Querier) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM economic_events`).Scan(&n)
	return n, err
}
