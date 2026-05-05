//go:build integration

package store

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignalRepo_InsertAndDuplicate(t *testing.T) {
	pool := testPool(t)
	repo := NewSignalRepo(pool)
	ctx := context.Background()

	in := SignalRow{
		StrategyID:    "s1",
		Symbol:        "ETHUSDC",
		Kind:          "long",
		SignalPrice:   decimal.NewFromFloat(2300.5),
		TVTimestampMs: 1714723504000,
		ReceivedAt:    time.Now().UTC(),
		RawPayload:    json.RawMessage(`{"k":"v"}`),
		ClientIP:      net.ParseIP("10.0.0.1"),
		Decision:      "pending",
		TraceID:       "trace-1",
	}

	id1, dup1, err := repo.Insert(ctx, pool, in)
	require.NoError(t, err)
	assert.False(t, dup1)
	assert.Greater(t, id1, int64(0))

	// Same (strategy_id, tv_timestamp_ms) → duplicate
	id2, dup2, err := repo.Insert(ctx, pool, in)
	require.NoError(t, err)
	assert.True(t, dup2)
	assert.Equal(t, id1, id2)
}

func TestSignalRepo_UpdateDecision(t *testing.T) {
	pool := testPool(t)
	repo := NewSignalRepo(pool)
	ctx := context.Background()

	id, _, err := repo.Insert(ctx, pool, SignalRow{
		StrategyID: "s1", Symbol: "ETHUSDC", Kind: "long",
		SignalPrice: decimal.NewFromInt(1), TVTimestampMs: 1, ReceivedAt: time.Now().UTC(),
		RawPayload: json.RawMessage(`{}`), Decision: "pending", TraceID: "t",
	})
	require.NoError(t, err)

	require.NoError(t, repo.UpdateDecision(ctx, pool, id, "accepted", "ok"))

	got, err := repo.GetByID(ctx, pool, id)
	require.NoError(t, err)
	assert.Equal(t, "accepted", got.Decision)
	assert.Equal(t, "ok", got.DecisionReason)
}

func TestSignalRepo_ListRecent(t *testing.T) {
	pool := testPool(t)
	repo := NewSignalRepo(pool)
	ctx := context.Background()

	for i := int64(0); i < 5; i++ {
		_, _, err := repo.Insert(ctx, pool, SignalRow{
			StrategyID: "s1", Symbol: "ETHUSDC", Kind: "long",
			SignalPrice: decimal.NewFromInt(1), TVTimestampMs: 100 + i,
			ReceivedAt: time.Now().UTC(),
			RawPayload: json.RawMessage(`{}`), Decision: "accepted", TraceID: "t",
		})
		require.NoError(t, err)
	}

	rows, err := repo.ListRecent(ctx, pool, 3)
	require.NoError(t, err)
	assert.Len(t, rows, 3)
	// Ordered DESC by received_at, so latest first
	assert.Equal(t, int64(104), rows[0].TVTimestampMs)
}
