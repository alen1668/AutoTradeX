package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// PerpMetricsRecord mirrors one row of perp_metrics.
type PerpMetricsRecord struct {
	ID                 int64
	Symbol             string
	ObservedAt         time.Time
	FundingRate        decimal.Decimal
	NextFundingTime    time.Time
	MarkPrice          decimal.Decimal
	OpenInterest       decimal.Decimal
	OpenInterest24hPct decimal.Decimal
	Price24hPct        decimal.Decimal
	TopLSRatio         decimal.Decimal
	FundingLabel       string
	OISignal           string
	LSLabel            string
}

// PerpMetricsRepo persists perp_metrics rows. Writes are append-only;
// callers read via Latest(symbol) or LatestBefore(symbol, t).
type PerpMetricsRepo struct {
	pool *pgxpool.Pool
}

func NewPerpMetricsRepo(pool *pgxpool.Pool) *PerpMetricsRepo {
	return &PerpMetricsRepo{pool: pool}
}

func (r *PerpMetricsRepo) Insert(ctx context.Context, q Querier, rec PerpMetricsRecord) (int64, error) {
	var id int64
	var nextFunding any
	if !rec.NextFundingTime.IsZero() {
		nextFunding = rec.NextFundingTime
	}
	err := q.QueryRow(ctx, `
INSERT INTO perp_metrics
    (symbol, observed_at, funding_rate, next_funding_time, mark_price,
     open_interest, open_interest_24h_pct, price_24h_pct, top_ls_ratio,
     funding_label, oi_signal, ls_label)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
RETURNING id`,
		rec.Symbol, rec.ObservedAt, rec.FundingRate, nextFunding, nullableDecimal(rec.MarkPrice),
		rec.OpenInterest, nullableDecimal(rec.OpenInterest24hPct), nullableDecimal(rec.Price24hPct),
		nullableDecimal(rec.TopLSRatio),
		rec.FundingLabel, rec.OISignal, rec.LSLabel,
	).Scan(&id)
	return id, err
}

// Latest returns the most recently observed row for symbol. Returns (nil, nil)
// when no rows exist — callers treat as "perp metrics unavailable".
func (r *PerpMetricsRepo) Latest(ctx context.Context, q Querier, symbol string) (*PerpMetricsRecord, error) {
	return r.scanOne(ctx, q, `
SELECT id, symbol, observed_at, funding_rate, next_funding_time, mark_price,
       open_interest, open_interest_24h_pct, price_24h_pct, top_ls_ratio,
       funding_label, oi_signal, ls_label
  FROM perp_metrics
 WHERE symbol=$1
 ORDER BY observed_at DESC LIMIT 1`, symbol)
}

// LatestBefore returns the latest row observed at or before `before`. Used by
// the worker to compute open_interest_24h_pct against the ~24h-ago snapshot.
func (r *PerpMetricsRepo) LatestBefore(ctx context.Context, q Querier, symbol string, before time.Time) (*PerpMetricsRecord, error) {
	return r.scanOne(ctx, q, `
SELECT id, symbol, observed_at, funding_rate, next_funding_time, mark_price,
       open_interest, open_interest_24h_pct, price_24h_pct, top_ls_ratio,
       funding_label, oi_signal, ls_label
  FROM perp_metrics
 WHERE symbol=$1 AND observed_at <= $2
 ORDER BY observed_at DESC LIMIT 1`, symbol, before)
}

// CountAll returns the total number of rows (optionally filtered by symbol;
// empty symbol = no filter).
func (r *PerpMetricsRepo) CountAll(ctx context.Context, q Querier, symbol string) (int, error) {
	var n int
	if symbol == "" {
		err := q.QueryRow(ctx, `SELECT COUNT(*) FROM perp_metrics`).Scan(&n)
		return n, err
	}
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM perp_metrics WHERE symbol=$1`, symbol).Scan(&n)
	return n, err
}

// ListPage returns one page of rows (newest first). Optional symbol filter.
func (r *PerpMetricsRepo) ListPage(ctx context.Context, q Querier, symbol string, limit, offset int) ([]PerpMetricsRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var rows pgx.Rows
	var err error
	if symbol == "" {
		rows, err = q.Query(ctx, `
SELECT id, symbol, observed_at, funding_rate, next_funding_time, mark_price,
       open_interest, open_interest_24h_pct, price_24h_pct, top_ls_ratio,
       funding_label, oi_signal, ls_label
  FROM perp_metrics
 ORDER BY observed_at DESC, symbol ASC
 LIMIT $1 OFFSET $2`, limit, offset)
	} else {
		rows, err = q.Query(ctx, `
SELECT id, symbol, observed_at, funding_rate, next_funding_time, mark_price,
       open_interest, open_interest_24h_pct, price_24h_pct, top_ls_ratio,
       funding_label, oi_signal, ls_label
  FROM perp_metrics
 WHERE symbol=$1
 ORDER BY observed_at DESC
 LIMIT $2 OFFSET $3`, symbol, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PerpMetricsRecord
	for rows.Next() {
		var rec PerpMetricsRecord
		var nextFunding *time.Time
		var markPrice, oi24h, price24h, ls *decimal.Decimal
		if err := rows.Scan(&rec.ID, &rec.Symbol, &rec.ObservedAt, &rec.FundingRate,
			&nextFunding, &markPrice, &rec.OpenInterest, &oi24h, &price24h, &ls,
			&rec.FundingLabel, &rec.OISignal, &rec.LSLabel); err != nil {
			return nil, err
		}
		if nextFunding != nil {
			rec.NextFundingTime = *nextFunding
		}
		if markPrice != nil {
			rec.MarkPrice = *markPrice
		}
		if oi24h != nil {
			rec.OpenInterest24hPct = *oi24h
		}
		if price24h != nil {
			rec.Price24hPct = *price24h
		}
		if ls != nil {
			rec.TopLSRatio = *ls
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DistinctSymbols returns the unique symbols present in the table, alpha sorted.
func (r *PerpMetricsRepo) DistinctSymbols(ctx context.Context, q Querier) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT symbol FROM perp_metrics ORDER BY symbol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *PerpMetricsRepo) scanOne(ctx context.Context, q Querier, sql string, args ...any) (*PerpMetricsRecord, error) {
	var rec PerpMetricsRecord
	var nextFunding *time.Time
	var markPrice, oi24h, price24h, ls *decimal.Decimal
	err := q.QueryRow(ctx, sql, args...).Scan(
		&rec.ID, &rec.Symbol, &rec.ObservedAt, &rec.FundingRate, &nextFunding, &markPrice,
		&rec.OpenInterest, &oi24h, &price24h, &ls,
		&rec.FundingLabel, &rec.OISignal, &rec.LSLabel,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if nextFunding != nil {
		rec.NextFundingTime = *nextFunding
	}
	if markPrice != nil {
		rec.MarkPrice = *markPrice
	}
	if oi24h != nil {
		rec.OpenInterest24hPct = *oi24h
	}
	if price24h != nil {
		rec.Price24hPct = *price24h
	}
	if ls != nil {
		rec.TopLSRatio = *ls
	}
	return &rec, nil
}
