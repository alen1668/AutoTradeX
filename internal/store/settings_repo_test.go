//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSettingsRepo_UpdateBinance(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()

	require.NoError(t, repo.UpdateBinance(ctx, pool, "KEY-123", "SECRET-456"))
	s, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.Equal(t, "KEY-123", s.BinanceAPIKey)
	assert.Equal(t, "SECRET-456", s.BinanceAPISecret)

	// Empty string updates → NULL → empty string in struct
	require.NoError(t, repo.UpdateBinance(ctx, pool, "", ""))
	s2, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.Empty(t, s2.BinanceAPIKey)
	assert.Empty(t, s2.BinanceAPISecret)
}

func TestSettingsRepo_BootstrapPopulatesNulls(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()

	require.NoError(t, repo.Bootstrap(ctx, pool,
		decimal.NewFromFloat(3.0), decimal.NewFromFloat(500),
		"https://feishu.example", true,
		"tg-token", "12345", false,
		"", "",
		"mysecret", []string{"127.0.0.1", "10.0.0.0/8"},
		30, 5000, 3000,
	))

	s, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromFloat(3.0).Equal(s.MaxTotalLeverage))
	assert.True(t, decimal.NewFromFloat(500).Equal(s.MaxDailyLossUSDC))
	assert.Equal(t, "https://feishu.example", s.FeishuURL)
	assert.True(t, s.FeishuEnabled)
	assert.Equal(t, "mysecret", s.WebhookSecret)
	assert.Equal(t, []string{"127.0.0.1", "10.0.0.0/8"}, s.IPWhitelist)
	assert.Equal(t, 30, s.ReconcilerIntervalSeconds)
	assert.Equal(t, 5000, s.BinanceRecvWindowMs)
	assert.Equal(t, 3000, s.BinanceOrderTimeoutMs)
}

func TestSettingsRepo_BootstrapRespectsExisting(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()

	// Initial bootstrap
	require.NoError(t, repo.Bootstrap(ctx, pool,
		decimal.NewFromFloat(3.0), decimal.NewFromFloat(500),
		"https://a", true, "t1", "c1", false,
		"key1", "secret1",
		"sec1", []string{"127.0.0.1"}, 30, 5000, 3000,
	))

	// User changed via UI:
	require.NoError(t, repo.UpdateRisk(ctx, pool, decimal.NewFromFloat(5.0), decimal.NewFromFloat(1000)))
	require.NoError(t, repo.UpdateNotifier(ctx, pool, "https://b", false, "", "", false))
	require.NoError(t, repo.UpdateIPWhitelist(ctx, pool, []string{"192.168.1.0/24"}))
	require.NoError(t, repo.UpdateWebhookSecret(ctx, pool, "newsecret"))
	require.NoError(t, repo.UpdateReconciler(ctx, pool, 60))
	require.NoError(t, repo.UpdateBinanceTuning(ctx, pool, 8000, 5000))

	// Restart calls Bootstrap again — must NOT overwrite user changes
	require.NoError(t, repo.Bootstrap(ctx, pool,
		decimal.NewFromFloat(99), decimal.NewFromFloat(99999),
		"https://c", true, "t99", "c99", true,
		"key99", "secret99",
		"oldsecret", []string{"10.0.0.0/8"}, 10, 1000, 1000,
	))

	s, _ := repo.Get(ctx, pool)
	assert.True(t, decimal.NewFromFloat(5.0).Equal(s.MaxTotalLeverage),
		"bootstrap should NOT overwrite already-set value")
	assert.True(t, decimal.NewFromFloat(1000).Equal(s.MaxDailyLossUSDC))
	assert.Equal(t, "https://b", s.FeishuURL)
	assert.Equal(t, "newsecret", s.WebhookSecret, "bootstrap must not overwrite UI-set webhook secret")
	assert.Equal(t, []string{"192.168.1.0/24"}, s.IPWhitelist, "bootstrap must not overwrite UI-set IP whitelist")
	assert.Equal(t, 60, s.ReconcilerIntervalSeconds, "bootstrap must not overwrite UI-set reconciler interval")
	assert.Equal(t, 8000, s.BinanceRecvWindowMs)
	assert.Equal(t, 5000, s.BinanceOrderTimeoutMs)
}

func TestSettingsRepo_IPWhitelistRoundTrip(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()

	// Initially nil → returns empty slice via COALESCE
	s, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.Empty(t, s.IPWhitelist)

	// Set whitelist
	entries := []string{"52.89.214.238", "10.0.0.0/8", "127.0.0.1"}
	require.NoError(t, repo.UpdateIPWhitelist(ctx, pool, entries))
	s2, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.Equal(t, entries, s2.IPWhitelist)

	// Clear whitelist
	require.NoError(t, repo.UpdateIPWhitelist(ctx, pool, []string{}))
	s3, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.Empty(t, s3.IPWhitelist)
}

func TestSettingsRepo_WebhookSecretRoundTrip(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()

	require.NoError(t, repo.UpdateWebhookSecret(ctx, pool, "abc123"))
	s, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.Equal(t, "abc123", s.WebhookSecret)

	// Empty → NULL → empty string
	require.NoError(t, repo.UpdateWebhookSecret(ctx, pool, ""))
	s2, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.Empty(t, s2.WebhookSecret)
}

func TestSettingsRepo_AgentDefaults(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	s, err := repo.Get(context.Background(), pool)
	require.NoError(t, err)
	assert.False(t, s.AgentScorerEnabled)
	assert.Equal(t, "claude-sonnet-4-6", s.AgentScorerModel)
	assert.Equal(t, 60, s.AgentScorerThreshold)
	assert.Equal(t, 5000, s.AgentScorerTimeoutMs)
	assert.Equal(t, 20, s.AgentScorerHistoryLimit)
	assert.Equal(t, "open", s.AgentScorerFailMode)
	assert.True(t, s.AgentScorerDryRun)
	assert.Equal(t, "anthropic", s.LLMAPIProvider)
	assert.Empty(t, s.LLMAPIKey)
	assert.Empty(t, s.LLMAPIBaseURL)
}

func TestSettingsRepo_UpdateAgentScorer(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()
	err := repo.UpdateAgentScorer(ctx, pool,
		true, "claude-haiku-4-5-20251001", 70, 6000, 30, "closed", false)
	require.NoError(t, err)

	s, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, s.AgentScorerEnabled)
	assert.Equal(t, 70, s.AgentScorerThreshold)
	assert.Equal(t, 6000, s.AgentScorerTimeoutMs)
	assert.Equal(t, 30, s.AgentScorerHistoryLimit)
	assert.Equal(t, "closed", s.AgentScorerFailMode)
	assert.False(t, s.AgentScorerDryRun)
}

func TestSettingsRepo_SetAgentScorerEnabled(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()
	require.NoError(t, repo.SetAgentScorerEnabled(ctx, pool, true))
	s, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, s.AgentScorerEnabled)

	require.NoError(t, repo.SetAgentScorerEnabled(ctx, pool, false))
	s2, _ := repo.Get(ctx, pool)
	assert.False(t, s2.AgentScorerEnabled)
}

func TestSettingsRepo_UpdateLLMAPI_EmptyKeyPreservesExisting(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()
	require.NoError(t, repo.UpdateLLMAPI(ctx, pool, "anthropic", "sk-test-1", ""))
	s1, _ := repo.Get(ctx, pool)
	assert.Equal(t, "sk-test-1", s1.LLMAPIKey)

	// Empty key on second update preserves existing key.
	require.NoError(t, repo.UpdateLLMAPI(ctx, pool, "anthropic", "", "https://example.com"))
	s2, _ := repo.Get(ctx, pool)
	assert.Equal(t, "sk-test-1", s2.LLMAPIKey)
	assert.Equal(t, "https://example.com", s2.LLMAPIBaseURL)
}

func TestSettingsRepo_MacroDefaults(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	s, err := repo.Get(context.Background(), pool)
	require.NoError(t, err)
	assert.False(t, s.RegimeEnabled)
	assert.Equal(t, 30, s.RegimeIntervalMin)
	assert.False(t, s.CalendarEnabled)
	assert.False(t, s.NewsEnabled)
	assert.Equal(t, 15, s.NewsIntervalMin)
	assert.Empty(t, s.NewsAPIKey)
	assert.Equal(t, "claude-haiku-4-5-20251001", s.NewsLLMModel)
}

func TestSettingsRepo_UpdateMacro(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()
	require.NoError(t, repo.UpdateMacro(ctx, pool,
		true, 45, false, true, 10, "test-cryptopanic-key", "claude-sonnet-4-6"))

	s, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, s.RegimeEnabled)
	assert.False(t, s.CalendarEnabled)
	assert.True(t, s.NewsEnabled)
	assert.Equal(t, 45, s.RegimeIntervalMin)
	assert.Equal(t, 10, s.NewsIntervalMin)
	assert.Equal(t, "test-cryptopanic-key", s.NewsAPIKey)
	assert.Equal(t, "claude-sonnet-4-6", s.NewsLLMModel)
}

func TestSettingsRepo_SetMacroFlags(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()
	require.NoError(t, repo.SetRegimeEnabled(ctx, pool, true))
	require.NoError(t, repo.SetCalendarEnabled(ctx, pool, true))
	require.NoError(t, repo.SetNewsEnabled(ctx, pool, true))
	s, _ := repo.Get(ctx, pool)
	assert.True(t, s.RegimeEnabled)
	assert.True(t, s.CalendarEnabled)
	assert.True(t, s.NewsEnabled)
}

func TestSettingsRepo_PerpMetricsDefaults(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	s, err := repo.Get(context.Background(), pool)
	require.NoError(t, err)
	assert.False(t, s.PerpMetricsEnabled, "default should be false (off)")
	assert.Equal(t, 30, s.PerpMetricsLookbackMinutes, "default should be 30 minutes")
}

func TestSettingsRepo_UpdatePerpMetrics(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()
	require.NoError(t, repo.UpdatePerpMetrics(ctx, pool, true, 45))
	s, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, s.PerpMetricsEnabled)
	assert.Equal(t, 45, s.PerpMetricsLookbackMinutes)
}

func TestSettingsRepo_SetPerpMetricsEnabled(t *testing.T) {
	pool := testPool(t)
	repo := NewSettingsRepo(pool)
	ctx := context.Background()
	require.NoError(t, repo.SetPerpMetricsEnabled(ctx, pool, true))
	s, _ := repo.Get(ctx, pool)
	assert.True(t, s.PerpMetricsEnabled)
}
