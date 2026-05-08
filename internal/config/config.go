// Package config loads runtime config from environment variables and an
// optional YAML overlay.
//
// Env-only fields (secrets, DB URL, HTTP listen, etc.) and YAML-only fields
// (risk thresholds, ip whitelist, notifier endpoints, Binance URLs,
// reconciler tuning) are partitioned: NO field has both an `envconfig` and
// a `yaml` tag. Adding both to the same field would produce confusing
// precedence (yaml currently wins because it loads second).
//
// Config carries plaintext secrets (WebhookSecret, SessionSecret, BinanceKey,
// BinanceSecret, Notifier tokens). Never log a Config value directly.
package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v3"
)

type BotMode string

const (
	ModeTestnet BotMode = "testnet"
	ModeLive    BotMode = "live"
)

func (m BotMode) Valid() bool {
	return m == ModeTestnet || m == ModeLive
}

type Config struct {
	// 来自 env
	BotMode       BotMode `envconfig:"BOT_MODE" required:"true"`
	DatabaseURL   string  `envconfig:"DATABASE_URL" required:"true"`
	HTTPListen    string  `envconfig:"HTTP_LISTEN" default:"0.0.0.0:8080"`
	LogLevel      string  `envconfig:"LOG_LEVEL" default:"info"`
	WebhookSecret string  `envconfig:"WEBHOOK_SECRET"`
	SessionSecret string  `envconfig:"SESSION_SECRET"`
	BinanceKey    string  `envconfig:"BINANCE_API_KEY"`
	BinanceSecret string  `envconfig:"BINANCE_API_SECRET"`

	// 来自 yaml（在 Load 中合并）
	Risk        RiskConfig       `yaml:"risk"`
	IPWhitelist []string         `yaml:"ip_whitelist"`
	Notifier    NotifierConfig   `yaml:"notifier"`
	Binance     BinanceConfig    `yaml:"binance"`
	Reconciler  ReconcilerConfig `yaml:"reconciler"`
}

type RiskConfig struct {
	MaxTotalLeverage float64 `yaml:"max_total_leverage"`
	MaxDailyLossUSDC float64 `yaml:"max_daily_loss_usdc"`
}

type NotifierConfig struct {
	Feishu   FeishuConfig   `yaml:"feishu"`
	Telegram TelegramConfig `yaml:"telegram"`
}

type FeishuConfig struct {
	WebhookURL string `yaml:"webhook_url"`
	Enabled    bool   `yaml:"enabled"`
}

type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
	Enabled  bool   `yaml:"enabled"`
}

// BinanceConfig holds tunables for the Binance USDT-M perp adapter.
//
// Note: base URLs are NOT configured here. The adshao/go-binance/v2 SDK
// internally selects between live (https://fapi.binance.com) and testnet
// (https://testnet.binancefuture.com) via its package-global `futures.UseTestnet`
// boolean, driven by the BOT_MODE env var. Adding a third endpoint such as
// demo-fapi.binance.com would require bypassing the SDK switch.
type BinanceConfig struct {
	RecvWindowMs   int `yaml:"recv_window_ms"`
	OrderTimeoutMs int `yaml:"order_timeout_ms"`
}

type ReconcilerConfig struct {
	IntervalSeconds int `yaml:"interval_seconds"`
}

func Load(yamlPath string) (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("env: %w", err)
	}
	if !cfg.BotMode.Valid() {
		return nil, fmt.Errorf("BOT_MODE invalid: %q (allowed: testnet, live)", cfg.BotMode)
	}
	if data, err := os.ReadFile(yamlPath); err == nil {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("yaml parse %s: %w", yamlPath, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("yaml read %s: %w", yamlPath, err)
	}
	return &cfg, nil
}
