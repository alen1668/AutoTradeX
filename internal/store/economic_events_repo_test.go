//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEconomicEventsRepo_UpsertIdempotent(t *testing.T) {
	pool := testPool(t)
	repo := NewEconomicEventsRepo(pool)
	ctx := context.Background()

	now := time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC)
	ev := EconomicEventRecord{
		SourceID: "ff:abc123",
		Name:     "CPI m/m",
		Currency: "USD",
		Impact:   "High",
		StartsAt: now,
	}
	require.NoError(t, repo.Upsert(ctx, pool, ev))
	ev.Name = "CPI m/m (updated)"
	require.NoError(t, repo.Upsert(ctx, pool, ev))

	n, err := repo.Count(ctx, pool)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "UPSERT should not duplicate")
}

func TestEconomicEventsRepo_ActiveBetween(t *testing.T) {
	pool := testPool(t)
	repo := NewEconomicEventsRepo(pool)
	ctx := context.Background()

	base := time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC)
	events := []EconomicEventRecord{
		{SourceID: "ff:a", Name: "Past", Currency: "USD", Impact: "High", StartsAt: base.Add(-2 * time.Hour)},
		{SourceID: "ff:b", Name: "Hit-1", Currency: "USD", Impact: "High", StartsAt: base.Add(-30 * time.Minute)},
		{SourceID: "ff:c", Name: "Hit-2", Currency: "USD", Impact: "High", StartsAt: base.Add(30 * time.Minute)},
		{SourceID: "ff:d", Name: "Future", Currency: "USD", Impact: "High", StartsAt: base.Add(3 * time.Hour)},
	}
	for _, ev := range events {
		require.NoError(t, repo.Upsert(ctx, pool, ev))
	}
	got, err := repo.ActiveBetween(ctx, pool, base.Add(-1*time.Hour), base.Add(1*time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 2, "expected 2 in-window events, got %+v", got)
	assert.Equal(t, "Hit-1", got[0].Name)
	assert.Equal(t, "Hit-2", got[1].Name)
}
