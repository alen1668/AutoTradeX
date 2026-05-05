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

func TestSystemStateRepo_DefaultIsDisarmed(t *testing.T) {
	pool := testPool(t)
	repo := NewSystemStateRepo(pool)
	st, err := repo.Get(context.Background(), pool)
	require.NoError(t, err)
	assert.False(t, st.Armed)
	assert.False(t, st.BreakerTripped)
	assert.True(t, st.DailyPnLUSDC.IsZero())
}

func TestSystemStateRepo_ArmDisarm(t *testing.T) {
	pool := testPool(t)
	repo := NewSystemStateRepo(pool)
	ctx := context.Background()

	require.NoError(t, repo.Arm(ctx, pool, "alice"))
	st, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, st.Armed)
	assert.Equal(t, "alice", st.ArmedBy)

	require.NoError(t, repo.Disarm(ctx, pool))
	st2, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.False(t, st2.Armed)
}

func TestSystemStateRepo_AddDailyPnLAndRollover(t *testing.T) {
	pool := testPool(t)
	repo := NewSystemStateRepo(pool)
	ctx := context.Background()

	// Same day: accumulates
	require.NoError(t, repo.AddDailyPnL(ctx, pool, decimal.NewFromFloat(10), time.Now().UTC()))
	require.NoError(t, repo.AddDailyPnL(ctx, pool, decimal.NewFromFloat(-3), time.Now().UTC()))
	st, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(7).Equal(st.DailyPnLUSDC))

	// New day: resets first
	tomorrow := time.Now().UTC().Add(24 * time.Hour)
	require.NoError(t, repo.AddDailyPnL(ctx, pool, decimal.NewFromFloat(5), tomorrow))
	st2, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(5).Equal(st2.DailyPnLUSDC))
}

func TestSystemStateRepo_TripAndResetBreaker(t *testing.T) {
	pool := testPool(t)
	repo := NewSystemStateRepo(pool)
	ctx := context.Background()

	require.NoError(t, repo.TripBreaker(ctx, pool, "daily loss exceeded"))
	st, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, st.BreakerTripped)
	assert.Equal(t, "daily loss exceeded", st.BreakerReason)

	require.NoError(t, repo.ResetBreaker(ctx, pool))
	st2, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.False(t, st2.BreakerTripped)
	assert.Empty(t, st2.BreakerReason)
}
