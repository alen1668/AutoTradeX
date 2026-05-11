//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarketRegimeRepo_InsertAndLatest(t *testing.T) {
	pool := testPool(t)
	repo := NewMarketRegimeRepo(pool)
	ctx := context.Background()

	// Empty table -> Latest returns error.
	_, err := repo.Latest(ctx, pool)
	require.Error(t, err, "Latest on empty table should error")

	now := time.Now().UTC().Truncate(time.Second)
	rec := MarketRegimeRecord{
		MeasuredAt:    now,
		Label:         "range",
		TrendStrength: decimal.NewFromFloat(0.10),
		Volatility24h: decimal.NewFromFloat(0.0125),
		VolPercentile: decimal.NewFromFloat(0.40),
		Change24hPct:  decimal.NewFromFloat(-1.5),
		PriceRangePos: decimal.NewFromFloat(0.55),
		KlineCount:    168,
	}
	id, err := repo.Insert(ctx, pool, rec)
	require.NoError(t, err)
	assert.NotZero(t, id)

	got, err := repo.Latest(ctx, pool)
	require.NoError(t, err)
	assert.Equal(t, "range", got.Label)
	assert.Equal(t, 168, got.KlineCount)
	assert.True(t, got.MeasuredAt.Equal(now), "MeasuredAt mismatch: got %v want %v", got.MeasuredAt, now)
}

func TestMarketRegimeRepo_LatestReturnsMostRecent(t *testing.T) {
	pool := testPool(t)
	repo := NewMarketRegimeRepo(pool)
	ctx := context.Background()

	t0 := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Hour)
	for i, lbl := range []string{"range", "trend_up", "crash"} {
		_, err := repo.Insert(ctx, pool, MarketRegimeRecord{
			MeasuredAt: t0.Add(time.Duration(i) * time.Hour),
			Label:      lbl,
		})
		require.NoError(t, err)
	}
	got, err := repo.Latest(ctx, pool)
	require.NoError(t, err)
	assert.Equal(t, "crash", got.Label)
}
