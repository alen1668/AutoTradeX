//go:build integration

package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeSignal(t *testing.T, pool *pgxpool.Pool, repo *SignalRepo, ts int64) int64 {
	t.Helper()
	return insertSignalAt(t, pool, repo, "s1", "ETHUSDC", "long",
		time.UnixMilli(ts).UTC(), "pending")
}

func TestAgentEvalRepo_Insert(t *testing.T) {
	pool := testPool(t)
	sigRepo := NewSignalRepo(pool)
	repo := NewAgentEvalRepo(pool)
	ctx := context.Background()

	sigID := makeSignal(t, pool, sigRepo, time.Now().UnixMilli())

	score := 75
	tokenIn, tokenOut := 1234, 56
	cost := decimal.RequireFromString("0.0123")
	resp := `{"score":75,"decision":"approve","reasoning":"稳定"}`
	require.NoError(t, repo.Insert(ctx, pool, AgentEvaluation{
		SignalID:    sigID,
		Model:       "claude-haiku-4-5-20251001",
		PromptHash:  "abcd1234",
		Score:       &score,
		Decision:    "approve",
		Reasoning:   "稳定的策略 + 价格未在极端区间",
		HistoryJSON: json.RawMessage(`{"symbol_history":[]}`),
		PromptText:  "你是一名加密货币...",
		ResponseRaw: &resp,
		LatencyMs:   1234,
		TokenIn:     &tokenIn,
		TokenOut:    &tokenOut,
		CostCents:   &cost,
	}))

	got, err := repo.LatestForSignal(ctx, pool, sigID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Score)
	assert.Equal(t, 75, *got.Score)
	assert.Equal(t, "approve", got.Decision)
	assert.Equal(t, "abcd1234", got.PromptHash)
	require.NotNil(t, got.TokenIn)
	assert.Equal(t, 1234, *got.TokenIn)
}

func TestAgentEvalRepo_InsertFailed(t *testing.T) {
	pool := testPool(t)
	sigRepo := NewSignalRepo(pool)
	repo := NewAgentEvalRepo(pool)
	ctx := context.Background()

	sigID := makeSignal(t, pool, sigRepo, time.Now().UnixMilli())

	require.NoError(t, repo.Insert(ctx, pool, AgentEvaluation{
		SignalID:    sigID,
		Model:       "claude-haiku-4-5-20251001",
		PromptHash:  "deadbeef",
		Score:       nil,
		Decision:    "failed",
		Reasoning:   "context deadline exceeded",
		HistoryJSON: json.RawMessage(`{}`),
		PromptText:  "...",
		ResponseRaw: nil,
		LatencyMs:   5001,
	}))

	got, err := repo.LatestForSignal(ctx, pool, sigID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Nil(t, got.Score)
	assert.Equal(t, "failed", got.Decision)
	assert.Nil(t, got.ResponseRaw)
}

func TestAgentEvalRepo_LatestForSignal_NoRows(t *testing.T) {
	pool := testPool(t)
	sigRepo := NewSignalRepo(pool)
	repo := NewAgentEvalRepo(pool)
	ctx := context.Background()

	sigID := makeSignal(t, pool, sigRepo, time.Now().UnixMilli())
	got, err := repo.LatestForSignal(ctx, pool, sigID)
	require.NoError(t, err)
	assert.Nil(t, got, "missing eval must return (nil, nil)")
}

func TestAgentEvalRepo_FKCascade(t *testing.T) {
	pool := testPool(t)
	sigRepo := NewSignalRepo(pool)
	repo := NewAgentEvalRepo(pool)
	ctx := context.Background()

	sigID := makeSignal(t, pool, sigRepo, time.Now().UnixMilli())
	require.NoError(t, repo.Insert(ctx, pool, AgentEvaluation{
		SignalID: sigID, Model: "m", PromptHash: "h", Decision: "approve",
		Reasoning: "x", HistoryJSON: json.RawMessage(`{}`), PromptText: "p",
		LatencyMs: 1,
	}))

	_, err := pool.Exec(ctx, `DELETE FROM signals WHERE id=$1`, sigID)
	require.NoError(t, err)

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_evaluations WHERE signal_id=$1`, sigID).Scan(&n))
	assert.Equal(t, 0, n, "delete signal must cascade to evaluations")
}

func TestAgentEvalRepo_ListSince(t *testing.T) {
	pool := testPool(t)
	sigRepo := NewSignalRepo(pool)
	repo := NewAgentEvalRepo(pool)
	ctx := context.Background()

	// 3 signals + evaluations all in the last second
	cutoff := time.Now().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		sigID := makeSignal(t, pool, sigRepo, time.Now().Add(time.Duration(i)*time.Millisecond).UnixMilli())
		require.NoError(t, repo.Insert(ctx, pool, AgentEvaluation{
			SignalID: sigID, Model: "m", PromptHash: "h", Decision: "approve",
			Reasoning: "x", HistoryJSON: json.RawMessage(`{}`), PromptText: "p",
			LatencyMs: 1,
		}))
	}

	got, err := repo.ListSince(ctx, pool, cutoff)
	require.NoError(t, err)
	assert.Len(t, got, 3)
}
