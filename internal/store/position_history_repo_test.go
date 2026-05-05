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
