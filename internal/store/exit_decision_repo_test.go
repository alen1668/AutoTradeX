//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func seedOpenPositionForExit(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int64 {
	t.Helper()
	seedStrategyForVP(t, ctx, pool)
	sigID := seedSignalForVP(t, ctx, pool, time.Now().UnixMilli())
	id, err := NewVirtualPositionRepo(pool).Insert(ctx, pool, VirtualPositionRow{
		StrategyID: "s", Symbol: "ETHUSDC", Side: "long",
		Qty: decimal.NewFromFloat(0.05), EntrySignalPrice: decimal.NewFromInt(100),
		EntrySignalID: sigID, Status: "open",
	})
	require.NoError(t, err)
	return id
}

func newExitRow(positionID int64) ExitDecisionRow {
	sl := decimal.NewFromFloat(2280)
	return ExitDecisionRow{
		VirtualPositionID: positionID,
		StrategyID:        "s",
		Symbol:            "ETHUSDC",
		Side:              "long",
		EntryFillPrice:    decimal.NewFromFloat(2300),
		CurrentPrice:      decimal.NewFromFloat(2330),
		Qty:               decimal.NewFromFloat(0.05),
		UnrealizedPnLUSD:  decimal.NewFromFloat(1.5),
		UnrealizedPnLPct:  decimal.NewFromFloat(0.013),
		PositionAgeSec:    1800,
		CurrentSLPrice:    &sl,
		Action:            "hold",
		Confidence:        "medium",
		Reasoning:         "ok",
		Model:             "claude-sonnet-4-6",
		PromptHash:        "abcd1234",
		Mode:              "shadow",
	}
}

func TestExitDecisionRepo_InsertAndGet(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pos := seedOpenPositionForExit(t, ctx, pool)

	repo := NewExitDecisionRepo(pool)
	id, err := repo.Insert(ctx, newExitRow(pos))
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "hold", got.Action)
	require.Equal(t, "shadow", got.Mode)
	require.Equal(t, int64(pos), got.VirtualPositionID)
}

func TestExitDecisionRepo_LastForPosition_Recent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pos := seedOpenPositionForExit(t, ctx, pool)
	repo := NewExitDecisionRepo(pool)

	_, err := repo.Insert(ctx, newExitRow(pos))
	require.NoError(t, err)

	last, err := repo.LastForPosition(ctx, pos)
	require.NoError(t, err)
	require.NotNil(t, last)
	require.WithinDuration(t, time.Now(), last.CreatedAt, time.Minute)
}

func TestExitDecisionRepo_LastForPosition_NoneReturnsNil(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	repo := NewExitDecisionRepo(pool)

	last, err := repo.LastForPosition(ctx, 999999)
	require.NoError(t, err)
	require.Nil(t, last)
}

func TestExitDecisionRepo_ListPagingAndFilter(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pos := seedOpenPositionForExit(t, ctx, pool)
	repo := NewExitDecisionRepo(pool)

	for i := 0; i < 5; i++ {
		_, err := repo.Insert(ctx, newExitRow(pos))
		require.NoError(t, err)
	}
	rows, err := repo.List(ctx, ExitDecisionListFilter{Limit: 3})
	require.NoError(t, err)
	require.Len(t, rows, 3)

	// Filter by mode
	shadow, err := repo.List(ctx, ExitDecisionListFilter{Mode: "shadow", Limit: 50})
	require.NoError(t, err)
	require.Len(t, shadow, 5)

	// Filter by mode that doesn't match → empty
	active, err := repo.List(ctx, ExitDecisionListFilter{Mode: "active", Limit: 50})
	require.NoError(t, err)
	require.Empty(t, active)
}

func TestExitDecisionRepo_PendingOutcome(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pos := seedOpenPositionForExit(t, ctx, pool)
	repo := NewExitDecisionRepo(pool)

	id, err := repo.Insert(ctx, newExitRow(pos))
	require.NoError(t, err)

	pending, err := repo.PendingOutcome(ctx, time.Now().Add(time.Hour), 50)
	require.NoError(t, err)
	require.NotEmpty(t, pending)
	require.Equal(t, id, pending[0].ID)
}

func TestExitDecisionRepo_SetExecution(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pos := seedOpenPositionForExit(t, ctx, pool)
	repo := NewExitDecisionRepo(pool)

	id, _ := repo.Insert(ctx, newExitRow(pos))
	when := time.Now()
	require.NoError(t, repo.SetExecution(ctx, id, &when, "success", ""))

	got, _ := repo.GetByID(ctx, id)
	require.NotNil(t, got.ExecutionStatus)
	require.Equal(t, "success", *got.ExecutionStatus)
	require.NotNil(t, got.ExecutedAt)
}

func TestExitDecisionRepo_SetIfHoldOutcome(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	pos := seedOpenPositionForExit(t, ctx, pool)
	repo := NewExitDecisionRepo(pool)

	id, _ := repo.Insert(ctx, newExitRow(pos))
	pct := decimal.NewFromFloat(0.012)
	label := "improved"
	require.NoError(t, repo.SetIfHoldOutcome(ctx, id, 60, &pct, &label))

	got, _ := repo.GetByID(ctx, id)
	require.NotNil(t, got.IfHoldLabel)
	require.Equal(t, "improved", *got.IfHoldLabel)
	require.NotNil(t, got.OutcomeComputedAt)
}
