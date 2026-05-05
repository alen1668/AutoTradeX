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
	ModeDryRun  BotMode = "dry_run"
	ModeTestnet BotMode = "testnet"
	ModeLive    BotMode = "live"
)

func (m BotMode) Valid() bool {
	return m == ModeDryRun || m == ModeTestnet || m == ModeLive
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

type BinanceConfig struct {
	BaseURLLive    string `yaml:"base_url_live"`
	BaseURLTestnet string `yaml:"base_url_testnet"`
	RecvWindowMs   int    `yaml:"recv_window_ms"`
	OrderTimeoutMs int    `yaml:"order_timeout_ms"`
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
		return nil, fmt.Errorf("BOT_MODE invalid: %q (allowed: dry_run, testnet, live)", cfg.BotMode)
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
