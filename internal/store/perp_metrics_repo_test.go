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

func TestPerpMetricsRepo_InsertAndLatest(t *testing.T) {
	pool := testPool(t)
	repo := NewPerpMetricsRepo(pool)
	ctx := context.Background()

	// Empty table -> Latest returns (nil, nil).
	got, err := repo.Latest(ctx, pool, "BTCUSDT")
	require.NoError(t, err)
	assert.Nil(t, got, "Latest on empty table should return nil")

	now := time.Now().UTC().Truncate(time.Second)
	rec := PerpMetricsRecord{
		Symbol:             "BTCUSDT",
		ObservedAt:         now,
		FundingRate:        decimal.NewFromFloat(0.00025),
		NextFundingTime:    now.Add(4 * time.Hour),
		MarkPrice:          decimal.NewFromFloat(50123.45),
		OpenInterest:       decimal.NewFromFloat(123456.78),
		OpenInterest24hPct: decimal.NewFromFloat(3.2),
		Price24hPct:        decimal.NewFromFloat(2.1),
		TopLSRatio:         decimal.NewFromFloat(1.85),
		FundingLabel:       "mild_long",
		OISignal:           "new_longs",
		LSLabel:            "bullish",
	}
	id, err := repo.Insert(ctx, pool, rec)
	require.NoError(t, err)
	assert.NotZero(t, id)

	got, err = repo.Latest(ctx, pool, "BTCUSDT")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "BTCUSDT", got.Symbol)
	assert.Equal(t, "mild_long", got.FundingLabel)
	assert.Equal(t, "new_longs", got.OISignal)
	assert.Equal(t, "bullish", got.LSLabel)
	assert.True(t, got.FundingRate.Equal(decimal.NewFromFloat(0.00025)),
		"FundingRate: got %v want 0.00025", got.FundingRate)
	assert.True(t, got.OpenInterest24hPct.Equal(decimal.NewFromFloat(3.2)))
}

func TestPerpMetricsRepo_Latest_PerSymbol(t *testing.T) {
	pool := testPool(t)
	repo := NewPerpMetricsRepo(pool)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	_, err := repo.Insert(ctx, pool, PerpMetricsRecord{
		Symbol: "BTCUSDT", ObservedAt: now.Add(-time.Minute),
		FundingRate: decimal.NewFromFloat(0.0001), OpenInterest: decimal.NewFromInt(100),
		FundingLabel: "neutral", OISignal: "neutral", LSLabel: "balanced",
	})
	require.NoError(t, err)
	_, err = repo.Insert(ctx, pool, PerpMetricsRecord{
		Symbol: "ETHUSDT", ObservedAt: now,
		FundingRate: decimal.NewFromFloat(0.0002), OpenInterest: decimal.NewFromInt(200),
		FundingLabel: "mild_long", OISignal: "new_longs", LSLabel: "bullish",
	})
	require.NoError(t, err)

	btc, err := repo.Latest(ctx, pool, "BTCUSDT")
	require.NoError(t, err)
	require.NotNil(t, btc)
	assert.Equal(t, "BTCUSDT", btc.Symbol)
	assert.Equal(t, "neutral", btc.FundingLabel)

	eth, err := repo.Latest(ctx, pool, "ETHUSDT")
	require.NoError(t, err)
	require.NotNil(t, eth)
	assert.Equal(t, "ETHUSDT", eth.Symbol)
	assert.Equal(t, "mild_long", eth.FundingLabel)
}

func TestPerpMetricsRepo_LatestBefore(t *testing.T) {
	pool := testPool(t)
	repo := NewPerpMetricsRepo(pool)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	_, err := repo.Insert(ctx, pool, PerpMetricsRecord{
		Symbol: "ETHUSDT", ObservedAt: now.Add(-25 * time.Hour),
		FundingRate: decimal.NewFromFloat(0.0001), OpenInterest: decimal.NewFromInt(100),
		FundingLabel: "mild_long", OISignal: "neutral", LSLabel: "balanced",
	})
	require.NoError(t, err)
	_, err = repo.Insert(ctx, pool, PerpMetricsRecord{
		Symbol: "ETHUSDT", ObservedAt: now,
		FundingRate: decimal.NewFromFloat(0.0002), OpenInterest: decimal.NewFromInt(110),
		FundingLabel: "mild_long", OISignal: "neutral", LSLabel: "balanced",
	})
	require.NoError(t, err)

	got, err := repo.LatestBefore(ctx, pool, "ETHUSDT", now.Add(-1*time.Hour))
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.True(t, got.OpenInterest.Equal(decimal.NewFromInt(100)),
		"LatestBefore got OI=%v, want 100", got.OpenInterest)
}
