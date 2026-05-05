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

func TestOrderRepo_InsertAndGetByClientID(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	repo := NewOrderRepo(pool)

	in := OrderRow{
		StrategyID:    "s",
		Symbol:        "ETHUSDC",
		Side:          "BUY",
		Type:          "MARKET",
		Purpose:       "entry",
		Qty:           decimal.NewFromFloat(0.1),
		ClientOrderID: "entry-trace1-0",
		Status:        "pending",
	}
	id, err := repo.Insert(ctx, pool, in)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	got, err := repo.GetByClientID(ctx, pool, "entry-trace1-0")
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
}

func TestOrderRepo_InsertDuplicateClientIDFails(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	repo := NewOrderRepo(pool)

	_, err := repo.Insert(ctx, pool, OrderRow{
		StrategyID: "s", Symbol: "ETHUSDC", Side: "BUY", Type: "MARKET", Purpose: "entry",
		Qty: decimal.NewFromInt(1), ClientOrderID: "dup", Status: "pending",
	})
	require.NoError(t, err)
	_, err = repo.Insert(ctx, pool, OrderRow{
		StrategyID: "s", Symbol: "ETHUSDC", Side: "BUY", Type: "MARKET", Purpose: "entry",
		Qty: decimal.NewFromInt(1), ClientOrderID: "dup", Status: "pending",
	})
	require.Error(t, err)
}

func TestOrderRepo_UpdateStatusAndFill(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	repo := NewOrderRepo(pool)

	id, _ := repo.Insert(ctx, pool, OrderRow{
		StrategyID: "s", Symbol: "ETHUSDC", Side: "BUY", Type: "MARKET", Purpose: "entry",
		Qty: decimal.NewFromInt(1), ClientOrderID: "c1", Status: "pending",
	})
	require.NoError(t, repo.UpdateOnFill(ctx, pool, id, "ex-123", decimal.NewFromInt(1), decimal.NewFromFloat(99.5), decimal.NewFromFloat(0.1)))

	got, _ := repo.GetByClientID(ctx, pool, "c1")
	assert.Equal(t, "filled", got.Status)
	assert.True(t, decimal.NewFromFloat(99.5).Equal(got.AvgFillPrice))
}

func TestOrderRepo_ListPending(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	repo := NewOrderRepo(pool)

	for i, status := range []string{"pending", "submitted", "filled", "canceled"} {
		_, err := repo.Insert(ctx, pool, OrderRow{
			StrategyID: "s", Symbol: "ETHUSDC", Side: "BUY", Type: "MARKET", Purpose: "entry",
			Qty: decimal.NewFromInt(1), ClientOrderID: "c" + string(rune('0'+i)), Status: status,
		})
		require.NoError(t, err)
	}
	pending, err := repo.ListPending(ctx, pool)
	require.NoError(t, err)
	assert.Len(t, pending, 2) // pending + submitted
}

func TestOrderRepo_GetByClientIDNotFound(t *testing.T) {
	pool := testPool(t)
	repo := NewOrderRepo(pool)
	_, err := repo.GetByClientID(context.Background(), pool, "nope")
	assert.True(t, errors.Is(err, pgx.ErrNoRows))
}
