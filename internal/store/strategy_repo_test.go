//go:build integration

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrategyRepo_CreateGetUpdate(t *testing.T) {
	pool := testPool(t)
	repo := NewStrategyRepo(pool)
	ctx := context.Background()

	in := StrategyRow{
		ID: "macd_eth", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.NewFromInt(100), StopLossPct: decimal.NewFromFloat(1.5),
		TakeProfitPct: decimal.NewFromFloat(3.0),
		MaxOpenUSDC:   decimal.NewFromInt(500),
		Enabled:       true,
	}
	require.NoError(t, repo.Create(ctx, pool, in))

	got, err := repo.Get(ctx, pool, "macd_eth")
	require.NoError(t, err)
	assert.Equal(t, in.Symbol, got.Symbol)
	assert.Equal(t, in.Leverage, got.Leverage)
	assert.True(t, in.SizeUSDC.Equal(got.SizeUSDC))

	in.Enabled = false
	in.SizeUSDC = decimal.NewFromInt(200)
	require.NoError(t, repo.Update(ctx, pool, in))

	got2, err := repo.Get(ctx, pool, "macd_eth")
	require.NoError(t, err)
	assert.False(t, got2.Enabled)
	assert.True(t, decimal.NewFromInt(200).Equal(got2.SizeUSDC))
}

func TestStrategyRepo_NotFound(t *testing.T) {
	pool := testPool(t)
	repo := NewStrategyRepo(pool)
	_, err := repo.Get(context.Background(), pool, "missing")
	require.Error(t, err)
	assert.True(t, errors.Is(err, pgx.ErrNoRows))
}

func TestStrategyRepo_List(t *testing.T) {
	pool := testPool(t)
	repo := NewStrategyRepo(pool)
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, repo.Create(ctx, pool, StrategyRow{
			ID: id, Symbol: "ETHUSDC", Leverage: 1,
			SizeUSDC: decimal.NewFromInt(10), StopLossPct: decimal.NewFromFloat(1),
			MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true,
		}))
	}
	rows, err := repo.List(ctx, pool)
	require.NoError(t, err)
	assert.Len(t, rows, 3)
}

func TestStrategyRepo_Delete(t *testing.T) {
	pool := testPool(t)
	repo := NewStrategyRepo(pool)
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, pool, StrategyRow{
		ID: "x", Symbol: "ETHUSDC", Leverage: 1,
		SizeUSDC: decimal.NewFromInt(10), StopLossPct: decimal.NewFromFloat(1),
		MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true,
	}))
	require.NoError(t, repo.Delete(ctx, pool, "x"))
	_, err := repo.Get(ctx, pool, "x")
	assert.True(t, errors.Is(err, pgx.ErrNoRows))
}
