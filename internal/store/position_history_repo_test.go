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

func TestPositionHistoryRepo_InsertAndList(t *testing.T) {
	pool := testPool(t)
	repo := NewPositionHistoryRepo(pool)
	ctx := context.Background()

	now := time.Now().UTC()
	in := PositionHistoryRow{
		StrategyID: "s", Symbol: "ETHUSDC", Side: "long",
		Qty:                 decimal.NewFromFloat(0.5),
		EntrySignalPrice:    decimal.NewFromFloat(100),
		EntryFillPrice:      decimal.NewFromFloat(99.9),
		ExitSignalPrice:     decimal.NewFromFloat(105),
		ExitFillPrice:       decimal.NewFromFloat(104.8),
		PnLUSDC:             decimal.NewFromFloat(2.45),
		PnLPct:              decimal.NewFromFloat(4.9),
		FeesUSDC:            decimal.NewFromFloat(0.05),
		OpenSignalToFillMs:  850,
		CloseSignalToFillMs: 920,
		OpenSlippageBP:      decimal.NewFromFloat(-10),
		CloseSlippageBP:     decimal.NewFromFloat(-19),
		CloseReason:         "signal",
		DurationSeconds:     300,
		OpenedAt:            now.Add(-5 * time.Minute),
		ClosedAt:            now,
	}
	require.NoError(t, repo.Insert(ctx, pool, in))

	rows, err := repo.ListByStrategy(ctx, pool, "s", 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].PnLUSDC.Equal(decimal.NewFromFloat(2.45)))
	assert.Equal(t, "signal", rows[0].CloseReason)
}

func makePosHistRow(strategyID, symbol, side string, pnl decimal.Decimal, closedAt time.Time) PositionHistoryRow {
	return PositionHistoryRow{
		StrategyID: strategyID, Symbol: symbol, Side: side,
		Qty:              decimal.NewFromFloat(0.1),
		EntrySignalPrice: decimal.NewFromInt(100), EntryFillPrice: decimal.NewFromInt(100),
		ExitSignalPrice: decimal.NewFromInt(101), ExitFillPrice: decimal.NewFromInt(101),
		PnLUSDC: pnl, PnLPct: decimal.NewFromFloat(1), FeesUSDC: decimal.Zero,
		CloseReason: "signal", DurationSeconds: 60,
		OpenedAt: closedAt.Add(-time.Minute), ClosedAt: closedAt,
	}
}

func TestPositionHistoryRepo_ListBySymbolAndStrategy(t *testing.T) {
	pool := testPool(t)
	repo := NewPositionHistoryRepo(pool)
	ctx := context.Background()
	now := time.Now().UTC()

	// 3 ETH trades, 2 BTC trades for strategy s1
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Insert(ctx, pool,
			makePosHistRow("s1", "ETHUSDC", "long", decimal.NewFromInt(int64(i)), now.Add(-time.Duration(i)*time.Hour))))
	}
	for i := 0; i < 2; i++ {
		require.NoError(t, repo.Insert(ctx, pool,
			makePosHistRow("s1", "BTCUSDC", "short", decimal.NewFromInt(10), now.Add(-time.Duration(i)*time.Hour))))
	}
	// 1 ETH trade for strategy s2 (must be excluded)
	require.NoError(t, repo.Insert(ctx, pool,
		makePosHistRow("s2", "ETHUSDC", "long", decimal.NewFromInt(99), now)))

	got, err := repo.ListBySymbolAndStrategy(ctx, pool, "s1", "ETHUSDC", 20)
	require.NoError(t, err)
	assert.Len(t, got, 3, "should return only s1+ETHUSDC trades")
	for _, r := range got {
		assert.Equal(t, "s1", r.StrategyID)
		assert.Equal(t, "ETHUSDC", r.Symbol)
	}
}

func TestPositionHistoryRepo_ListBySymbolAndStrategy_LimitRespected(t *testing.T) {
	pool := testPool(t)
	repo := NewPositionHistoryRepo(pool)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		require.NoError(t, repo.Insert(ctx, pool,
			makePosHistRow("s1", "ETHUSDC", "long", decimal.NewFromInt(1), now.Add(-time.Duration(i)*time.Hour))))
	}
	got, err := repo.ListBySymbolAndStrategy(ctx, pool, "s1", "ETHUSDC", 3)
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestPositionHistoryRepo_DailyRealizedPnL(t *testing.T) {
	pool := testPool(t)
	repo := NewPositionHistoryRepo(pool)
	ctx := context.Background()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	yesterday := today.Add(-24 * time.Hour)

	// 3 trades today (sums to 6), 2 yesterday (excluded)
	for _, p := range []int64{1, 2, 3} {
		require.NoError(t, repo.Insert(ctx, pool,
			makePosHistRow("s", "ETHUSDC", "long", decimal.NewFromInt(p), today.Add(time.Duration(p)*time.Hour))))
	}
	for _, p := range []int64{10, 20} {
		require.NoError(t, repo.Insert(ctx, pool,
			makePosHistRow("s", "ETHUSDC", "long", decimal.NewFromInt(p), yesterday.Add(time.Duration(p)*time.Minute))))
	}

	pnl, err := repo.DailyRealizedPnL(ctx, pool, today)
	require.NoError(t, err)
	assert.Equal(t, "6", pnl.String())
}

func TestPositionHistoryRepo_DailyRealizedPnL_EmptyDay(t *testing.T) {
	pool := testPool(t)
	repo := NewPositionHistoryRepo(pool)
	ctx := context.Background()
	pnl, err := repo.DailyRealizedPnL(ctx, pool, time.Now().UTC())
	require.NoError(t, err)
	assert.True(t, pnl.IsZero(), "empty day must return zero")
}
