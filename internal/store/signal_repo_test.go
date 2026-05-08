//go:build integration

package store

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// insertSignalAt is a tiny test helper used by recovery/abandoned tests.
func insertSignalAt(t *testing.T, pool *pgxpool.Pool, repo *SignalRepo,
	strategyID, symbol, kind string, receivedAt time.Time, decision string) int64 {
	t.Helper()
	id, _, err := repo.Insert(context.Background(), pool, SignalRow{
		StrategyID:    strategyID,
		Symbol:        symbol,
		Kind:          kind,
		SignalPrice:   decimal.NewFromInt(1),
		TVTimestampMs: receivedAt.UnixMilli(),
		ReceivedAt:    receivedAt,
		RawPayload:    json.RawMessage(`{}`),
		Decision:      decision,
		TraceID:       "t-" + receivedAt.Format("150405.000"),
	})
	require.NoError(t, err)
	return id
}

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

func TestSignalRepo_ListPendingForRecovery(t *testing.T) {
	pool := testPool(t)
	repo := NewSignalRepo(pool)
	ctx := context.Background()

	now := time.Now().UTC()
	insertSignalAt(t, pool, repo, "s1", "ETHUSDT", "long", now.Add(-1*time.Minute), "pending")
	insertSignalAt(t, pool, repo, "s1", "ETHUSDT", "long", now.Add(-3*time.Minute), "pending")
	insertSignalAt(t, pool, repo, "s1", "ETHUSDT", "long", now.Add(-30*time.Minute), "pending")
	insertSignalAt(t, pool, repo, "s1", "ETHUSDT", "long", now.Add(-1*time.Minute).Add(time.Second), "accepted")

	pending, err := repo.ListPendingForRecovery(ctx, pool, now.Add(-10*time.Minute))
	require.NoError(t, err)
	assert.Len(t, pending, 2, "should return only pending signals newer than cutoff")
	assert.True(t, pending[0].ReceivedAt.Before(pending[1].ReceivedAt))
}

func TestSignalRepo_MarkAbandoned(t *testing.T) {
	pool := testPool(t)
	repo := NewSignalRepo(pool)
	ctx := context.Background()

	id1 := insertSignalAt(t, pool, repo, "s1", "ETHUSDT", "long", time.Now().Add(-30*time.Minute), "pending")
	id2 := insertSignalAt(t, pool, repo, "s1", "ETHUSDT", "long", time.Now().Add(-1*time.Minute), "accepted")

	err := repo.MarkAbandoned(ctx, pool, []int64{id1, id2}, "startup recovery: too old")
	require.NoError(t, err)

	row1, _ := repo.GetByID(ctx, pool, id1)
	row2, _ := repo.GetByID(ctx, pool, id2)
	assert.Equal(t, "abandoned", row1.Decision, "pending row should flip to abandoned")
	assert.Equal(t, "accepted", row2.Decision, "non-pending row must NOT change")
	assert.Contains(t, row1.DecisionReason, "too old")
}

func TestSignalRepo_UpdateDecision_OnlyAcceptsPending(t *testing.T) {
	pool := testPool(t)
	repo := NewSignalRepo(pool)
	ctx := context.Background()

	id := insertSignalAt(t, pool, repo, "s1", "ETHUSDT", "long", time.Now(), "pending")

	require.NoError(t, repo.UpdateDecision(ctx, pool, id, "accepted", "open_long"))

	// Second update on the now-accepted row must be a no-op.
	require.NoError(t, repo.UpdateDecision(ctx, pool, id, "risk_denied", "stale recovery"))

	row, _ := repo.GetByID(ctx, pool, id)
	assert.Equal(t, "accepted", row.Decision, "guard should prevent overwrite")
	assert.Equal(t, "open_long", row.DecisionReason)
}
