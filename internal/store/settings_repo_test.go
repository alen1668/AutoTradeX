//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsRepo_BootstrapPopulatesNulls(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()

	require.NoError(t, repo.Bootstrap(ctx, pool,
		decimal.NewFromFloat(3.0), decimal.NewFromFloat(500),
		"https://feishu.example", true,
		"tg-token", "12345", false))

	s, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(3.0).Equal(s.MaxTotalLeverage))
	assert.True(t, decimal.NewFromFloat(500).Equal(s.MaxDailyLossUSDC))
	assert.Equal(t, "https://feishu.example", s.FeishuURL)
	assert.True(t, s.FeishuEnabled)
}

func TestSettingsRepo_BootstrapRespectsExisting(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()

	// Initial bootstrap
	require.NoError(t, repo.Bootstrap(ctx, pool,
		decimal.NewFromFloat(3.0), decimal.NewFromFloat(500),
		"https://a", true, "t1", "c1", false))

	// User changed via UI:
	require.NoError(t, repo.UpdateRisk(ctx, pool, decimal.NewFromFloat(5.0), decimal.NewFromFloat(1000)))
	require.NoError(t, repo.UpdateNotifier(ctx, pool, "https://b", false, "", "", false))

	// Restart calls Bootstrap again — must NOT overwrite user changes
	require.NoError(t, repo.Bootstrap(ctx, pool,
		decimal.NewFromFloat(99), decimal.NewFromFloat(99999),
		"https://c", true, "t99", "c99", true))

	s, _ := repo.Get(ctx, pool)
	assert.True(t, decimal.NewFromFloat(5.0).Equal(s.MaxTotalLeverage),
		"bootstrap should NOT overwrite already-set value")
	assert.True(t, decimal.NewFromFloat(1000).Equal(s.MaxDailyLossUSDC))
	assert.Equal(t, "https://b", s.FeishuURL)
}
