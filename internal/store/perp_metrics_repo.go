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
