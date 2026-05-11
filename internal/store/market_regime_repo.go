package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// MarketRegimeRecord mirrors one row of market_regime.
type MarketRegimeRecord struct {
	ID            int64
	MeasuredAt    time.Time
	Label         string
	TrendStrength decimal.Decimal
	Volatility24h decimal.Decimal
	VolPercentile decimal.Decimal
	Change24hPct  decimal.Decimal
	PriceRangePos decimal.Decimal
	KlineCount    int
}

// MarketRegimeRepo persists market_regime rows. Writes are append-only;
// the worker INSERTs a new row each tick, callers read via Latest.
type MarketRegimeRepo struct {
	pool *pgxpool.Pool
}

func NewMarketRegimeRepo(pool *pgxpool.Pool) *MarketRegimeRepo {
	return &MarketRegimeRepo{pool: pool}
}

func (r *MarketRegimeRepo) Insert(ctx context.Context, q Querier, rec MarketRegimeRecord) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
INSERT INTO market_regime
    (measured_at, label, trend_strength, volatility_24h, vol_percentile,
     change_24h_pct, price_range_pos, kline_count)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
RETURNING id`,
		rec.MeasuredAt, rec.Label, rec.TrendStrength, rec.Volatility24h, rec.VolPercentile,
		rec.Change24hPct, rec.PriceRangePos, rec.KlineCount,
	).Scan(&id)
	return id, err
}

// Latest returns the most recently measured row. Returns pgx.ErrNoRows when
// the table is empty (callers should treat that as "regime data unavailable").
func (r *MarketRegimeRepo) Latest(ctx context.Context, q Querier) (*MarketRegimeRecord, error) {
	var rec MarketRegimeRecord
	err := q.QueryRow(ctx, `
SELECT id, measured_at, label, trend_strength, volatility_24h, vol_percentile,
       change_24h_pct, price_range_pos, kline_count
  FROM market_regime
 ORDER BY measured_at DESC
 LIMIT 1`,
	).Scan(&rec.ID, &rec.MeasuredAt, &rec.Label, &rec.TrendStrength, &rec.Volatility24h,
		&rec.VolPercentile, &rec.Change24hPct, &rec.PriceRangePos, &rec.KlineCount)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}
