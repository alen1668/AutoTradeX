//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedStrategyForVP(t *testing.T, ctx context.Context, q Querier) {
	t.Helper()
	repo := NewStrategyRepo(nil)
	require.NoError(t, repo.Create(ctx, q, StrategyRow{
		ID: "s", Symbol: "ETHUSDC", Leverage: 1,
		SizeUSDC: decimal.NewFromInt(10), StopLossPct: decimal.NewFromFloat(1),
		MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true,
	}))
}

func seedSignalForVP(t *testing.T, ctx context.Context, q Querier, ts int64) int64 {
	t.Helper()
	repo := NewSignalRepo(nil)
	id, _, err := repo.Insert(ctx, q, SignalRow{
		StrategyID: "s", Symbol: "ETHUSDC", Kind: "long",
		SignalPrice: decimal.NewFromInt(100), TVTimestampMs: ts,
		ReceivedAt: time.Now().UTC(), RawPayload: json.RawMessage(`{}`),
		ClientIP: net.ParseIP("127.0.0.1"), Decision: "accepted", TraceID: "t",
	})
	require.NoError(t, err)
	return id
}

func TestVirtualPositionRepo_OpenAndGetActive(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	sigID := seedSignalForVP(t, ctx, pool, 1)
	repo := NewVirtualPositionRepo(pool)

	id, err := repo.Insert(ctx, pool, VirtualPositionRow{
		StrategyID:       "s",
		Symbol:           "ETHUSDC",
		Side:             "long",
		Qty:              decimal.NewFromFloat(0.1),
		EntrySignalPrice: decimal.NewFromInt(100),
		EntrySignalID:    sigID,
		Status:           "opening",
	})
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	got, err := repo.GetActiveByStrategy(ctx, pool, "s")
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, "opening", got.Status)
}

func TestVirtualPositionRepo_PartialUniqueIndexBlocksDoubleActive(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	sigID := seedSignalForVP(t, ctx, pool, 1)
	repo := NewVirtualPositionRepo(pool)

	_, err := repo.Insert(ctx, pool, VirtualPositionRow{
		StrategyID:       "s",
		Symbol:           "ETHUSDC",
		Side:             "long",
		Qty:              decimal.NewFromInt(1),
		EntrySignalPrice: decimal.NewFromInt(100),
		EntrySignalID:    sigID,
		Status:           "open",
	})
	require.NoError(t, err)

	// Try to insert a second active row → DB unique-violation
	sig2 := seedSignalForVP(t, ctx, pool, 2)
	_, err = repo.Insert(ctx, pool, VirtualPositionRow{
		StrategyID:       "s",
		Symbol:           "ETHUSDC",
		Side:             "short",
		Qty:              decimal.NewFromInt(1),
		EntrySignalPrice: decimal.NewFromInt(100),
		EntrySignalID:    sig2,
		Status:           "opening",
	})
	require.Error(t, err)
}

func TestVirtualPositionRepo_TransitionStatus(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	sigID := seedSignalForVP(t, ctx, pool, 1)
	repo := NewVirtualPositionRepo(pool)

	id, _ := repo.Insert(ctx, pool, VirtualPositionRow{
		StrategyID: "s", Symbol: "ETHUSDC", Side: "long",
		Qty: decimal.NewFromInt(1), EntrySignalPrice: decimal.NewFromInt(100),
		EntrySignalID: sigID, Status: "opening",
	})

	require.NoError(t, repo.SetEntryFill(ctx, pool, id, decimal.NewFromFloat(99.5), 42))
	require.NoError(t, repo.UpdateStatus(ctx, pool, id, "open"))
	got, err := repo.GetByID(ctx, pool, id)
	require.NoError(t, err)
	assert.Equal(t, "open", got.Status)
	assert.True(t, decimal.NewFromFloat(99.5).Equal(got.EntryFillPrice))
	assert.Equal(t, int64(42), got.EntryOrderID)

	require.NoError(t, repo.SetProtectiveOrders(ctx, pool, id, 50, 51, 0))
	got2, _ := repo.GetByID(ctx, pool, id)
	assert.Equal(t, int64(50), got2.StopOrderID)
	assert.Equal(t, int64(51), got2.BackupStopOrderID)
	assert.Zero(t, got2.TakeProfitOrderID)

	require.NoError(t, repo.MarkClosed(ctx, pool, id))
	_, err = repo.GetActiveByStrategy(ctx, pool, "s")
	assert.True(t, errors.Is(err, pgx.ErrNoRows))
}
