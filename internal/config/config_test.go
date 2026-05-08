package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_RejectsEmptyBotMode(t *testing.T) {
	// envconfig treats set-but-empty as present (ok=true), so the explicit
	// BotMode.Valid() check is what fires here, not the required:"true" tag.
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("BOT_MODE", "")
	_, err := Load("nonexistent.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BOT_MODE")
}

func TestLoad_RejectsMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("risk:\n  max_total_leverage: [not, a, number\n"), 0o644))
	t.Setenv("BOT_MODE", "testnet")
	t.Setenv("DATABASE_URL", "postgres://x")
	_, err := Load(yamlPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "yaml parse")
}

func TestLoad_RejectsInvalidBotMode(t *testing.T) {
	t.Setenv("BOT_MODE", "wild_west")
	t.Setenv("DATABASE_URL", "postgres://x")
	_, err := Load("nonexistent.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wild_west")
}

func TestLoad_AcceptsAllValidModes(t *testing.T) {
	for _, m := range []string{"testnet", "live"} {
		t.Run(m, func(t *testing.T) {
			t.Setenv("BOT_MODE", m)
			t.Setenv("DATABASE_URL", "postgres://x")
			t.Setenv("WEBHOOK_SECRET", "s")
			t.Setenv("SESSION_SECRET", "s")
			cfg, err := Load("nonexistent.yaml")
			require.NoError(t, err)
			assert.Equal(t, BotMode(m), cfg.BotMode)
		})
	}
}

func TestLoad_MergesYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte(`
risk:
  max_total_leverage: 5.0
  max_daily_loss_usdc: 1000
ip_whitelist:
  - 10.0.0.1
binance:
  recv_window_ms: 5000
  order_timeout_ms: 3000
reconciler:
  interval_seconds: 30
notifier:
  feishu:  { enabled: false, webhook_url: "" }
  telegram: { enabled: false, bot_token: "", chat_id: "" }
`), 0o644))

	t.Setenv("BOT_MODE", "testnet")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("WEBHOOK_SECRET", "s")
	t.Setenv("SESSION_SECRET", "s")

	cfg, err := Load(yamlPath)
	require.NoError(t, err)
	assert.InDelta(t, 5.0, cfg.Risk.MaxTotalLeverage, 0.001)
	assert.Equal(t, 1000.0, cfg.Risk.MaxDailyLossUSDC)
	assert.Equal(t, []string{"10.0.0.1"}, cfg.IPWhitelist)
}
