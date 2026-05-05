# TVBot Foundation Implementation Plan (Plan 1 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 搭建项目骨架、配置/日志基建、PostgreSQL 迁移（含全部列中文备注）、domain 层与风控规则的纯逻辑单元测试。完成后 `go test ./...` 全绿，`make migrate` 可建好 schema。

**Architecture:** Single Go binary, Hexagonal/DDD-ish layout (domain / application / infrastructure / web). 本 plan 只覆盖 domain + risk + 基建，不接入任何外部服务（无 HTTP server、无 Binance、无 webhook）。

**Tech Stack:** Go 1.23+, chi/v5, pgx/v5, goose/v3, zerolog, envconfig, yaml.v3, testify, dockertest/v3, golang-lru/v2.

**Spec reference:** `docs/superpowers/specs/2026-05-05-tradingview-webhook-bot-design.md`

---

## File Structure (本 Plan 创建/修改的文件)

| 路径 | 责任 |
|------|------|
| `go.mod`, `go.sum` | 依赖管理 |
| `Makefile` | 常用命令封装（test / migrate / dev） |
| `.env.example` | 环境变量样板 |
| `config/config.yaml.example` | 静态配置样板 |
| `docker-compose.yml` | postgres 服务（bot 服务后续 plan 加） |
| `cmd/tvbot/main.go` | 进程入口（本 plan 仅 stub） |
| `internal/config/config.go` | 配置加载与校验 |
| `internal/config/config_test.go` | 配置测试 |
| `internal/log/log.go` | zerolog 初始化 + trace_id helper |
| `internal/log/log_test.go` | 日志测试 |
| `internal/store/db.go` | pgx pool 工厂 |
| `internal/store/db_test.go` | DB 连接集成测试（dockertest） |
| `migrations/0001_init.sql` | 全部 schema + 中文备注 |
| `internal/domain/signal/signal.go` | Signal 值对象 + JSON 解析 |
| `internal/domain/signal/signal_test.go` | 单元测试 |
| `internal/domain/strategy/strategy.go` | Strategy 实体 |
| `internal/domain/strategy/strategy_test.go` | 单元测试 |
| `internal/domain/position/position.go` | VirtualPosition + 状态枚举 |
| `internal/domain/position/decision.go` | (持仓 × 信号) → 动作 决策表 |
| `internal/domain/position/decision_test.go` | 8 种组合全覆盖 |
| `internal/domain/order/order.go` | Order + 状态机 |
| `internal/domain/order/order_test.go` | 状态转移测试 |
| `internal/risk/types.go` | RiskRule 接口、Decision 类型 |
| `internal/risk/pipeline.go` | RulePipeline 组合器 |
| `internal/risk/pipeline_test.go` | 短路逻辑测试 |
| `internal/risk/max_position.go` | 单策略最大未平仓规则 |
| `internal/risk/max_position_test.go` | 单测 |
| `internal/risk/total_leverage.go` | 总杠杆规则 |
| `internal/risk/total_leverage_test.go` | 单测 |
| `internal/risk/daily_loss_breaker.go` | 日亏熔断规则 |
| `internal/risk/daily_loss_breaker_test.go` | 单测 |
| `internal/risk/ip_whitelist.go` | IP 白名单规则 |
| `internal/risk/ip_whitelist_test.go` | 单测 |

---

## Task 1: 项目骨架与依赖

**Files:**
- Create: `go.mod`, `Makefile`, `.gitignore`（已存在则跳过）, `.env.example`, `config/config.yaml.example`, `cmd/tvbot/main.go`

- [ ] **Step 1: 初始化 Go module**

```bash
cd /Users/lizhaojie/tool/crypto
go mod init github.com/lizhaojie/tvbot
```

- [ ] **Step 2: 安装核心依赖**

```bash
go get github.com/go-chi/chi/v5
go get github.com/jackc/pgx/v5
go get github.com/jackc/pgx/v5/pgxpool
go get github.com/pressly/goose/v3
go get github.com/rs/zerolog
go get github.com/kelseyhightower/envconfig
go get gopkg.in/yaml.v3
go get github.com/alexedwards/scs/v2
go get github.com/hashicorp/golang-lru/v2
go get github.com/adshao/go-binance/v2
go get github.com/stretchr/testify
go get github.com/ory/dockertest/v3
go get github.com/google/uuid
go get golang.org/x/crypto/bcrypt
go mod tidy
```

- [ ] **Step 3: 创建 `cmd/tvbot/main.go` stub**

```go
package main

import "fmt"

func main() {
	fmt.Println("tvbot: stub - foundation plan only, real entrypoint comes in plan 2")
}
```

- [ ] **Step 4: 创建 `Makefile`**

```makefile
.PHONY: test test-v lint build run migrate-up migrate-down migrate-status pg-up pg-down

GOOSE_DRIVER ?= postgres
GOOSE_DBSTRING ?= postgres://tvbot:tvbot@localhost:5432/tvbot?sslmode=disable
MIGRATIONS_DIR := migrations

test:
	go test ./...

test-v:
	go test -race -v ./...

lint:
	go vet ./...

build:
	go build -o bin/tvbot ./cmd/tvbot

run: build
	./bin/tvbot

pg-up:
	docker compose up -d postgres

pg-down:
	docker compose down

migrate-up:
	GOOSE_DRIVER=$(GOOSE_DRIVER) GOOSE_DBSTRING="$(GOOSE_DBSTRING)" go run github.com/pressly/goose/v3/cmd/goose -dir $(MIGRATIONS_DIR) up

migrate-down:
	GOOSE_DRIVER=$(GOOSE_DRIVER) GOOSE_DBSTRING="$(GOOSE_DBSTRING)" go run github.com/pressly/goose/v3/cmd/goose -dir $(MIGRATIONS_DIR) down

migrate-status:
	GOOSE_DRIVER=$(GOOSE_DRIVER) GOOSE_DBSTRING="$(GOOSE_DBSTRING)" go run github.com/pressly/goose/v3/cmd/goose -dir $(MIGRATIONS_DIR) status
```

- [ ] **Step 5: 创建 `.env.example`**

```
BOT_MODE=dry_run
DATABASE_URL=postgres://tvbot:tvbot@localhost:5432/tvbot?sslmode=disable
HTTP_LISTEN=0.0.0.0:8080
LOG_LEVEL=debug
TZ=UTC

# Binance（plan 2 用，先留位）
BINANCE_API_KEY=
BINANCE_API_SECRET=

# 鉴权（plan 3 用）
WEBHOOK_SECRET=please-change-me
SESSION_SECRET=please-change-me-32bytes
```

- [ ] **Step 6: 创建 `config/config.yaml.example`**

```yaml
risk:
  max_total_leverage: 3.0
  max_daily_loss_usdc: 500
ip_whitelist:
  - 52.89.214.238
  - 34.212.75.30
  - 54.218.53.128
  - 52.32.178.7
  - 127.0.0.1
notifier:
  feishu:
    webhook_url: ""
    enabled: false
  telegram:
    bot_token: ""
    chat_id: ""
    enabled: false
binance:
  base_url_live:    "https://fapi.binance.com"
  base_url_testnet: "https://testnet.binancefuture.com"
  recv_window_ms:   5000
  order_timeout_ms: 3000
reconciler:
  interval_seconds: 30
```

- [ ] **Step 7: 创建 `docker-compose.yml`**

```yaml
version: "3.9"
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: tvbot
      POSTGRES_PASSWORD: tvbot
      POSTGRES_DB: tvbot
    ports: ["5432:5432"]
    volumes:
      - ./.data/pg:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U tvbot -d tvbot"]
      interval: 2s
      timeout: 5s
      retries: 30
```

- [ ] **Step 8: 验证 build 通过**

Run: `make build && ./bin/tvbot`
Expected: 输出 `tvbot: stub - foundation plan only, real entrypoint comes in plan 2`

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum Makefile .env.example config/config.yaml.example docker-compose.yml cmd/tvbot/main.go
git commit -m "chore: bootstrap project skeleton (go.mod, Makefile, docker-compose, stub main)"
```

---

## Task 2: 配置加载（envconfig + yaml）

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/config/testdata/valid.yaml`

- [ ] **Step 1: 写测试 `internal/config/config_test.go`**

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_RequiresBotMode(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("BOT_MODE", "")
	_, err := Load("nonexistent.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BOT_MODE")
}

func TestLoad_RejectsInvalidBotMode(t *testing.T) {
	t.Setenv("BOT_MODE", "wild_west")
	t.Setenv("DATABASE_URL", "postgres://x")
	_, err := Load("nonexistent.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wild_west")
}

func TestLoad_AcceptsAllValidModes(t *testing.T) {
	for _, m := range []string{"dry_run", "testnet", "live"} {
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
  base_url_live: "https://fapi.binance.com"
  base_url_testnet: "https://testnet.binancefuture.com"
  recv_window_ms: 5000
  order_timeout_ms: 3000
reconciler:
  interval_seconds: 30
notifier:
  feishu:  { enabled: false, webhook_url: "" }
  telegram: { enabled: false, bot_token: "", chat_id: "" }
`), 0o644))

	t.Setenv("BOT_MODE", "dry_run")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("WEBHOOK_SECRET", "s")
	t.Setenv("SESSION_SECRET", "s")

	cfg, err := Load(yamlPath)
	require.NoError(t, err)
	assert.InDelta(t, 5.0, cfg.Risk.MaxTotalLeverage, 0.001)
	assert.Equal(t, 1000.0, cfg.Risk.MaxDailyLossUSDC)
	assert.Equal(t, []string{"10.0.0.1"}, cfg.IPWhitelist)
}
```

- [ ] **Step 2: 跑测试看到失败**

Run: `go test ./internal/config/...`
Expected: FAIL（package 不存在）

- [ ] **Step 3: 实现 `internal/config/config.go`**

```go
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
	BotMode        BotMode `envconfig:"BOT_MODE" required:"true"`
	DatabaseURL    string  `envconfig:"DATABASE_URL" required:"true"`
	HTTPListen     string  `envconfig:"HTTP_LISTEN" default:"0.0.0.0:8080"`
	LogLevel       string  `envconfig:"LOG_LEVEL" default:"info"`
	WebhookSecret  string  `envconfig:"WEBHOOK_SECRET"`
	SessionSecret  string  `envconfig:"SESSION_SECRET"`
	BinanceKey     string  `envconfig:"BINANCE_API_KEY"`
	BinanceSecret  string  `envconfig:"BINANCE_API_SECRET"`

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
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/config/... -v`
Expected: 全部 PASS（4 个 case）

- [ ] **Step 5: Commit**

```bash
git add internal/config/ go.sum
git commit -m "feat(config): load env + yaml config with bot mode validation"
```

---

## Task 3: 日志（zerolog + trace_id）

**Files:**
- Create: `internal/log/log.go`
- Create: `internal/log/log_test.go`

- [ ] **Step 1: 写测试 `internal/log/log_test.go`**

```go
package log

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWriterRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := newWith(&buf, "warn")
	logger.Info().Msg("info-msg")
	logger.Warn().Msg("warn-msg")
	out := buf.String()
	assert.NotContains(t, out, "info-msg")
	assert.Contains(t, out, "warn-msg")
}

func TestTraceIDInjectedIntoContext(t *testing.T) {
	ctx := context.Background()
	tid := "trace-abc"
	ctx = WithTraceID(ctx, tid)
	got, ok := TraceIDFrom(ctx)
	require.True(t, ok)
	assert.Equal(t, tid, got)
}

func TestFromContextEmbedsTraceID(t *testing.T) {
	var buf bytes.Buffer
	base := newWith(&buf, "debug")
	ctx := WithTraceID(context.Background(), "trace-xyz")
	logger := FromContext(ctx, base)
	logger.Info().Msg("hi")
	out := buf.String()
	assert.Contains(t, out, "trace-xyz")
}

func TestNewWith_InvalidLevelDefaultsInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := newWith(&buf, "garbage")
	assert.Equal(t, zerolog.InfoLevel, logger.GetLevel())
	_ = strings.TrimSpace(buf.String())
}
```

- [ ] **Step 2: 跑测试看到失败**

Run: `go test ./internal/log/...`
Expected: FAIL（package 不存在）

- [ ] **Step 3: 实现 `internal/log/log.go`**

```go
package log

import (
	"context"
	"io"
	"os"

	"github.com/rs/zerolog"
)

type ctxKey int

const traceIDKey ctxKey = 0

// New returns a base logger writing to stderr.
func New(level string) zerolog.Logger {
	return newWith(os.Stderr, level)
}

func newWith(w io.Writer, level string) zerolog.Logger {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil || lvl == zerolog.NoLevel {
		lvl = zerolog.InfoLevel
	}
	return zerolog.New(w).Level(lvl).With().Timestamp().Logger()
}

// WithTraceID stores a trace id on the context.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

// TraceIDFrom extracts a trace id from context.
func TraceIDFrom(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(traceIDKey).(string)
	return v, ok && v != ""
}

// FromContext returns a logger that automatically tags each log with the
// trace_id from the context (if present).
func FromContext(ctx context.Context, base zerolog.Logger) zerolog.Logger {
	if tid, ok := TraceIDFrom(ctx); ok {
		return base.With().Str("trace_id", tid).Logger()
	}
	return base
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/log/... -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/log/
git commit -m "feat(log): zerolog setup with trace_id context propagation"
```

---

## Task 4: PostgreSQL 迁移（migrations/0001_init.sql）

**重要**：所有列必须有 `COMMENT ON COLUMN` 中文备注（spec §5）。

**Files:**
- Create: `migrations/0001_init.sql`

- [ ] **Step 1: 创建 `migrations/0001_init.sql`**

```sql
-- +goose Up
-- +goose StatementBegin

-- ========== 枚举类型 ==========
CREATE TYPE signal_kind AS ENUM ('long', 'short', 'exit_long', 'exit_short');
COMMENT ON TYPE signal_kind IS '信号方向：开多/开空/平多/平空';

CREATE TYPE order_status AS ENUM ('pending','submitted','partial','filled','canceled','rejected','expired');
COMMENT ON TYPE order_status IS '订单状态';

CREATE TYPE bot_mode AS ENUM ('dry_run','testnet','live');
COMMENT ON TYPE bot_mode IS '运行模式';

-- ========== strategies ==========
CREATE TABLE strategies (
    id              TEXT PRIMARY KEY,
    symbol          TEXT NOT NULL,
    leverage        SMALLINT NOT NULL,
    size_usdc       NUMERIC(20,8) NOT NULL,
    stop_loss_pct   NUMERIC(6,3) NOT NULL,
    take_profit_pct NUMERIC(6,3),
    max_open_usdc   NUMERIC(20,8) NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
COMMENT ON TABLE strategies IS '策略配置表（Web 后台 CRUD 目标）';
COMMENT ON COLUMN strategies.id              IS '策略唯一 ID（即 webhook payload 中的 strategy_id）';
COMMENT ON COLUMN strategies.symbol          IS '交易对，如 ETHUSDC';
COMMENT ON COLUMN strategies.leverage        IS '杠杆倍数，整数 1-125';
COMMENT ON COLUMN strategies.size_usdc       IS '每次下单名义价值（USDC）';
COMMENT ON COLUMN strategies.stop_loss_pct   IS '止损百分比，例 1.500 表示 1.5%';
COMMENT ON COLUMN strategies.take_profit_pct IS '止盈百分比，可空表示不挂止盈单';
COMMENT ON COLUMN strategies.max_open_usdc   IS '该策略允许的最大未平仓金额（USDC），风控用';
COMMENT ON COLUMN strategies.enabled         IS '是否启用：禁用后该策略所有信号均被拒绝';
COMMENT ON COLUMN strategies.created_at      IS '创建时间（UTC）';
COMMENT ON COLUMN strategies.updated_at      IS '更新时间（UTC）';

-- ========== signals ==========
CREATE TABLE signals (
    id              BIGSERIAL PRIMARY KEY,
    strategy_id     TEXT NOT NULL,
    symbol          TEXT NOT NULL,
    kind            signal_kind NOT NULL,
    signal_price    NUMERIC(20,8) NOT NULL,
    tv_timestamp_ms BIGINT NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    raw_payload     JSONB NOT NULL,
    client_ip       INET,
    decision        TEXT NOT NULL,
    decision_reason TEXT,
    trace_id        TEXT NOT NULL,
    UNIQUE (strategy_id, tv_timestamp_ms)
);
COMMENT ON TABLE signals IS '收到的所有 webhook 信号（审计 + 幂等事实源）';
COMMENT ON COLUMN signals.id              IS '自增主键';
COMMENT ON COLUMN signals.strategy_id     IS '触发该信号的策略 ID（不强制外键，允许接收未注册策略的信号以审计）';
COMMENT ON COLUMN signals.symbol          IS '交易对';
COMMENT ON COLUMN signals.kind            IS '信号方向：long/short/exit_long/exit_short';
COMMENT ON COLUMN signals.signal_price    IS 'TradingView 触发时的价格';
COMMENT ON COLUMN signals.tv_timestamp_ms IS 'TradingView 触发的毫秒时间戳，用于幂等与延迟统计';
COMMENT ON COLUMN signals.received_at     IS 'bot 收到信号的时间（UTC）';
COMMENT ON COLUMN signals.raw_payload     IS 'webhook 原始 JSON 全量留底';
COMMENT ON COLUMN signals.client_ip       IS '请求来源 IP';
COMMENT ON COLUMN signals.decision        IS '处理决定：accepted/duplicate/risk_denied/invalid/disarmed';
COMMENT ON COLUMN signals.decision_reason IS '决定的原因文案（拒绝时填写具体哪条规则）';
COMMENT ON COLUMN signals.trace_id        IS '贯穿整条处理链路的 trace id';

-- ========== virtual_positions ==========
CREATE TABLE virtual_positions (
    id                   BIGSERIAL PRIMARY KEY,
    strategy_id          TEXT NOT NULL REFERENCES strategies(id),
    symbol               TEXT NOT NULL,
    side                 TEXT NOT NULL,
    qty                  NUMERIC(20,8) NOT NULL,
    entry_signal_price   NUMERIC(20,8) NOT NULL,
    entry_fill_price     NUMERIC(20,8),
    entry_signal_id      BIGINT NOT NULL REFERENCES signals(id),
    entry_order_id       BIGINT,
    stop_order_id        BIGINT,
    backup_stop_order_id BIGINT,
    take_profit_order_id BIGINT,
    status               TEXT NOT NULL,
    opened_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at            TIMESTAMPTZ
);
COMMENT ON TABLE virtual_positions IS '虚拟持仓（按策略 ID 隔离记账，多个策略可共享真实账户）';
COMMENT ON COLUMN virtual_positions.id                   IS '自增主键';
COMMENT ON COLUMN virtual_positions.strategy_id          IS '所属策略 ID';
COMMENT ON COLUMN virtual_positions.symbol               IS '交易对';
COMMENT ON COLUMN virtual_positions.side                 IS '方向：long/short';
COMMENT ON COLUMN virtual_positions.qty                  IS '持仓币数量（已按 LOT_SIZE 取整）';
COMMENT ON COLUMN virtual_positions.entry_signal_price   IS '开仓信号价（TV 触发价）';
COMMENT ON COLUMN virtual_positions.entry_fill_price     IS '开仓实际成交均价（filled 后填充）';
COMMENT ON COLUMN virtual_positions.entry_signal_id      IS '触发开仓的 signals.id';
COMMENT ON COLUMN virtual_positions.entry_order_id       IS '开仓订单 orders.id';
COMMENT ON COLUMN virtual_positions.stop_order_id        IS '主止损单 orders.id（限价止损）';
COMMENT ON COLUMN virtual_positions.backup_stop_order_id IS '兜底止损单 orders.id（市价止损）';
COMMENT ON COLUMN virtual_positions.take_profit_order_id IS '止盈单 orders.id（可空）';
COMMENT ON COLUMN virtual_positions.status               IS '状态：opening/open/closing/closed';
COMMENT ON COLUMN virtual_positions.opened_at            IS '虚拟开仓时间';
COMMENT ON COLUMN virtual_positions.closed_at            IS '平仓时间（关闭后填）';

-- 同一策略同时只能有一个活跃仓位
CREATE UNIQUE INDEX uq_virtual_positions_active
    ON virtual_positions(strategy_id)
    WHERE status IN ('opening','open','closing');

-- ========== position_history ==========
CREATE TABLE position_history (
    id                      BIGSERIAL PRIMARY KEY,
    strategy_id             TEXT NOT NULL,
    symbol                  TEXT NOT NULL,
    side                    TEXT NOT NULL,
    qty                     NUMERIC(20,8) NOT NULL,
    entry_signal_price      NUMERIC(20,8) NOT NULL,
    entry_fill_price        NUMERIC(20,8) NOT NULL,
    exit_signal_price       NUMERIC(20,8) NOT NULL,
    exit_fill_price         NUMERIC(20,8) NOT NULL,
    pnl_usdc                NUMERIC(20,8) NOT NULL,
    pnl_pct                 NUMERIC(10,4) NOT NULL,
    fees_usdc               NUMERIC(20,8) NOT NULL DEFAULT 0,
    open_signal_to_fill_ms  INT,
    close_signal_to_fill_ms INT,
    open_slippage_bp        NUMERIC(10,4),
    close_slippage_bp       NUMERIC(10,4),
    close_reason            TEXT NOT NULL,
    duration_seconds        INT NOT NULL,
    opened_at               TIMESTAMPTZ NOT NULL,
    closed_at               TIMESTAMPTZ NOT NULL
);
COMMENT ON TABLE position_history IS '平仓归档（每次完整 开+平 的快照）';
COMMENT ON COLUMN position_history.id                      IS '自增主键';
COMMENT ON COLUMN position_history.strategy_id             IS '所属策略 ID';
COMMENT ON COLUMN position_history.symbol                  IS '交易对';
COMMENT ON COLUMN position_history.side                    IS '方向：long/short';
COMMENT ON COLUMN position_history.qty                     IS '币数量';
COMMENT ON COLUMN position_history.entry_signal_price      IS '开仓信号价';
COMMENT ON COLUMN position_history.entry_fill_price        IS '开仓成交均价';
COMMENT ON COLUMN position_history.exit_signal_price       IS '平仓信号价';
COMMENT ON COLUMN position_history.exit_fill_price         IS '平仓成交均价';
COMMENT ON COLUMN position_history.pnl_usdc                IS '盈亏（USDC，已扣手续费）';
COMMENT ON COLUMN position_history.pnl_pct                 IS '盈亏百分比';
COMMENT ON COLUMN position_history.fees_usdc               IS '本笔交易手续费总额（USDC）';
COMMENT ON COLUMN position_history.open_signal_to_fill_ms  IS '开仓延迟：从信号触发到成交的毫秒数';
COMMENT ON COLUMN position_history.close_signal_to_fill_ms IS '平仓延迟：从信号触发到成交的毫秒数';
COMMENT ON COLUMN position_history.open_slippage_bp        IS '开仓滑点（基点）：(成交价-信号价)/信号价*10000';
COMMENT ON COLUMN position_history.close_slippage_bp       IS '平仓滑点（基点）';
COMMENT ON COLUMN position_history.close_reason            IS '平仓原因：signal/stop_loss/take_profit/manual';
COMMENT ON COLUMN position_history.duration_seconds        IS '持仓时长（秒）';
COMMENT ON COLUMN position_history.opened_at               IS '开仓时间';
COMMENT ON COLUMN position_history.closed_at               IS '平仓时间';

CREATE INDEX idx_position_history_strategy_closed ON position_history(strategy_id, closed_at DESC);

-- ========== orders ==========
CREATE TABLE orders (
    id                  BIGSERIAL PRIMARY KEY,
    virtual_position_id BIGINT REFERENCES virtual_positions(id),
    strategy_id         TEXT NOT NULL,
    symbol              TEXT NOT NULL,
    side                TEXT NOT NULL,
    type                TEXT NOT NULL,
    purpose             TEXT NOT NULL,
    qty                 NUMERIC(20,8) NOT NULL,
    price               NUMERIC(20,8),
    stop_price          NUMERIC(20,8),
    client_order_id     TEXT NOT NULL UNIQUE,
    exchange_order_id   TEXT,
    status              order_status NOT NULL,
    filled_qty          NUMERIC(20,8) NOT NULL DEFAULT 0,
    avg_fill_price      NUMERIC(20,8),
    fees_usdc           NUMERIC(20,8) NOT NULL DEFAULT 0,
    submitted_at        TIMESTAMPTZ,
    filled_at           TIMESTAMPTZ,
    raw_response        JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
COMMENT ON TABLE orders IS '订单：与 Binance 真实订单一对一（client_order_id 是幂等键）';
COMMENT ON COLUMN orders.id                  IS '自增主键';
COMMENT ON COLUMN orders.virtual_position_id IS '所属虚拟仓位 ID（可空：dry_run 阶段或独立挂单）';
COMMENT ON COLUMN orders.strategy_id         IS '所属策略 ID';
COMMENT ON COLUMN orders.symbol              IS '交易对';
COMMENT ON COLUMN orders.side                IS '订单方向：BUY/SELL';
COMMENT ON COLUMN orders.type                IS '订单类型：MARKET/STOP/STOP_MARKET/TAKE_PROFIT_MARKET';
COMMENT ON COLUMN orders.purpose             IS '业务用途：entry/exit/stop/backup_stop/take_profit';
COMMENT ON COLUMN orders.qty                 IS '委托币数量';
COMMENT ON COLUMN orders.price               IS '限价（仅限价类型有值）';
COMMENT ON COLUMN orders.stop_price          IS '触发价（条件单）';
COMMENT ON COLUMN orders.client_order_id     IS '本地生成的客户端订单 ID（提交给 Binance 用于幂等）';
COMMENT ON COLUMN orders.exchange_order_id   IS 'Binance 返回的订单 ID';
COMMENT ON COLUMN orders.status              IS '订单状态';
COMMENT ON COLUMN orders.filled_qty          IS '已成交币数量';
COMMENT ON COLUMN orders.avg_fill_price      IS '成交均价';
COMMENT ON COLUMN orders.fees_usdc           IS '本订单手续费（USDC）';
COMMENT ON COLUMN orders.submitted_at        IS '提交到交易所时间';
COMMENT ON COLUMN orders.filled_at           IS '完全成交时间';
COMMENT ON COLUMN orders.raw_response        IS '最近一次交易所响应留底';
COMMENT ON COLUMN orders.created_at          IS '创建时间';
COMMENT ON COLUMN orders.updated_at          IS '更新时间';

CREATE INDEX idx_orders_position ON orders(virtual_position_id);
CREATE INDEX idx_orders_status ON orders(status) WHERE status IN ('pending','submitted','partial');

-- ========== system_state ==========
CREATE TABLE system_state (
    id              SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    armed           BOOLEAN NOT NULL DEFAULT false,
    armed_at        TIMESTAMPTZ,
    armed_by        TEXT,
    daily_pnl_usdc  NUMERIC(20,8) NOT NULL DEFAULT 0,
    daily_pnl_date  DATE NOT NULL DEFAULT CURRENT_DATE,
    breaker_tripped BOOLEAN NOT NULL DEFAULT false,
    breaker_reason  TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
COMMENT ON TABLE system_state IS '系统全局状态（单行表）';
COMMENT ON COLUMN system_state.id              IS '固定 1，强制单行';
COMMENT ON COLUMN system_state.armed           IS '是否已启动交易（重启后默认 false）';
COMMENT ON COLUMN system_state.armed_at        IS '上次 arm 时间';
COMMENT ON COLUMN system_state.armed_by        IS '上次 arm 操作者';
COMMENT ON COLUMN system_state.daily_pnl_usdc  IS '当日累计盈亏（USDC）';
COMMENT ON COLUMN system_state.daily_pnl_date  IS '当日日期（用于翻天清零判断）';
COMMENT ON COLUMN system_state.breaker_tripped IS '熔断是否触发';
COMMENT ON COLUMN system_state.breaker_reason  IS '熔断原因';
COMMENT ON COLUMN system_state.updated_at      IS '更新时间';

INSERT INTO system_state (id) VALUES (1);

-- ========== users ==========
CREATE TABLE users (
    id            SMALLSERIAL PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
COMMENT ON TABLE users IS '后台用户（MVP 单用户）';
COMMENT ON COLUMN users.id            IS '自增主键';
COMMENT ON COLUMN users.username      IS '登录用户名';
COMMENT ON COLUMN users.password_hash IS 'bcrypt 哈希';
COMMENT ON COLUMN users.created_at    IS '创建时间';

-- ========== signals 索引（最后创建，依赖前面表已存在不必要，但保持紧邻表定义） ==========
CREATE INDEX idx_signals_received_at ON signals(received_at DESC);
CREATE INDEX idx_signals_strategy_id ON signals(strategy_id, received_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS system_state;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS position_history;
DROP TABLE IF EXISTS virtual_positions;
DROP TABLE IF EXISTS signals;
DROP TABLE IF EXISTS strategies;
DROP TYPE IF EXISTS bot_mode;
DROP TYPE IF EXISTS order_status;
DROP TYPE IF EXISTS signal_kind;
-- +goose StatementEnd
```

- [ ] **Step 2: 启动 Postgres**

Run: `make pg-up`
Expected: postgres 容器启动；`docker ps` 看到 healthy

- [ ] **Step 3: 跑迁移 up**

Run: `make migrate-up`
Expected:
```
2026/05/05 ... OK   0001_init.sql
goose: successfully migrated database to version: 1
```

- [ ] **Step 4: 验证表结构与备注**

Run:
```bash
docker exec -it $(docker compose ps -q postgres) \
  psql -U tvbot -d tvbot -c "\d+ strategies" \
  -c "\d+ signals" \
  -c "\d+ virtual_positions" \
  -c "\d+ position_history" \
  -c "\d+ orders" \
  -c "\d+ system_state" \
  -c "\d+ users"
```
Expected: 每张表的每一列都显示中文 Description；system_state 已有一行。

- [ ] **Step 5: 验证 down 可回滚**

Run: `make migrate-down && make migrate-status`
Expected: 0001 状态 Pending；表全部消失（`\dt` 为空）

- [ ] **Step 6: 重新 up 留作后续 task 用**

Run: `make migrate-up`

- [ ] **Step 7: Commit**

```bash
git add migrations/0001_init.sql
git commit -m "feat(db): initial schema with full Chinese column comments"
```

---

## Task 5: DB 连接（pgx pool）

**Files:**
- Create: `internal/store/db.go`
- Create: `internal/store/db_test.go`

- [ ] **Step 1: 写测试 `internal/store/db_test.go`**

```go
//go:build integration

package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPool_PingsDB(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://tvbot:tvbot@localhost:5432/tvbot?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	var one int
	err = pool.QueryRow(ctx, "SELECT 1").Scan(&one)
	require.NoError(t, err)
	assert.Equal(t, 1, one)
}
```

- [ ] **Step 2: 跑测试看到失败**

Run: `go test -tags=integration ./internal/store/...`
Expected: FAIL（package 不存在）

- [ ] **Step 3: 实现 `internal/store/db.go`**

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
```

- [ ] **Step 4: 跑测试**

Run: `make pg-up && make migrate-up && go test -tags=integration ./internal/store/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): pgx connection pool factory with ping"
```

---

## Task 6: domain/signal — Signal 值对象 + JSON 解析

**Files:**
- Create: `internal/domain/signal/signal.go`
- Create: `internal/domain/signal/signal_test.go`

- [ ] **Step 1: 写测试 `internal/domain/signal/signal_test.go`**

```go
package signal

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal" // 见 step 3 安装
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Valid(t *testing.T) {
	body := []byte(`{"strategy_id":"s1","symbol":"ETHUSDC","signal":"Long","price":"2312.14","timestamp":1714723504000,"secret":"x"}`)
	s, err := Parse(body)
	require.NoError(t, err)
	assert.Equal(t, "s1", s.StrategyID)
	assert.Equal(t, "ETHUSDC", s.Symbol)
	assert.Equal(t, KindLong, s.Kind)
	assert.True(t, s.Price.Equal(decimal.RequireFromString("2312.14")))
	assert.Equal(t, time.UnixMilli(1714723504000).UTC(), s.TVTimestamp.UTC())
	assert.Equal(t, "x", s.Secret)
}

func TestParse_KindCaseInsensitive(t *testing.T) {
	cases := map[string]Kind{
		"Long":        KindLong,
		"long":        KindLong,
		"SHORT":       KindShort,
		"Exit Long":   KindExitLong,
		"exit short":  KindExitShort,
		"EXIT LONG":   KindExitLong,
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"strategy_id": "s",
				"symbol":      "ETHUSDC",
				"signal":      raw,
				"price":       "1",
				"timestamp":   1,
				"secret":      "x",
			})
			s, err := Parse(body)
			require.NoError(t, err)
			assert.Equal(t, want, s.Kind)
		})
	}
}

func TestParse_RejectsMissingFields(t *testing.T) {
	cases := []string{
		`{"symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":1,"secret":"x"}`,                    // 缺 strategy_id
		`{"strategy_id":"s","signal":"Long","price":"1","timestamp":1,"secret":"x"}`,                     // 缺 symbol
		`{"strategy_id":"s","symbol":"ETHUSDC","price":"1","timestamp":1,"secret":"x"}`,                  // 缺 signal
		`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","timestamp":1,"secret":"x"}`,              // 缺 price
		`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","secret":"x"}`,                // 缺 timestamp
		`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":1}`,               // 缺 secret
	}
	for i, body := range cases {
		t.Run("case-"+string(rune('A'+i)), func(t *testing.T) {
			_, err := Parse([]byte(body))
			require.Error(t, err)
		})
	}
}

func TestParse_RejectsBadKind(t *testing.T) {
	_, err := Parse([]byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"hodl","price":"1","timestamp":1,"secret":"x"}`))
	require.Error(t, err)
}

func TestParse_RejectsNegativePrice(t *testing.T) {
	_, err := Parse([]byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"-1","timestamp":1,"secret":"x"}`))
	require.Error(t, err)
}

func TestParse_RejectsZeroOrNegativeTimestamp(t *testing.T) {
	_, err := Parse([]byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":0,"secret":"x"}`))
	require.Error(t, err)
}

func TestParse_PriceCanBeNumeric(t *testing.T) {
	// TradingView 偶尔会发数字而非字符串
	s, err := Parse([]byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":2312.14,"timestamp":1,"secret":"x"}`))
	require.NoError(t, err)
	assert.True(t, s.Price.Equal(decimal.RequireFromString("2312.14")))
}
```

- [ ] **Step 2: 安装 decimal 库（精度敏感字段统一用它）**

```bash
go get github.com/shopspring/decimal
go mod tidy
```

- [ ] **Step 3: 跑测试看到失败**

Run: `go test ./internal/domain/signal/...`
Expected: FAIL（package 不存在）

- [ ] **Step 4: 实现 `internal/domain/signal/signal.go`**

```go
package signal

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type Kind string

const (
	KindLong      Kind = "long"
	KindShort     Kind = "short"
	KindExitLong  Kind = "exit_long"
	KindExitShort Kind = "exit_short"
)

func (k Kind) IsEntry() bool { return k == KindLong || k == KindShort }
func (k Kind) IsExit() bool  { return k == KindExitLong || k == KindExitShort }

func parseKind(s string) (Kind, error) {
	norm := strings.ToLower(strings.TrimSpace(s))
	norm = strings.ReplaceAll(norm, " ", "_")
	switch norm {
	case "long":
		return KindLong, nil
	case "short":
		return KindShort, nil
	case "exit_long":
		return KindExitLong, nil
	case "exit_short":
		return KindExitShort, nil
	}
	return "", fmt.Errorf("unknown signal kind: %q", s)
}

// Signal is the parsed, validated webhook payload.
type Signal struct {
	StrategyID  string
	Symbol      string
	Kind        Kind
	Price       decimal.Decimal
	TVTimestamp time.Time
	TVTimestampMs int64
	Secret      string
	Raw         json.RawMessage
}

// payload mirrors the wire format. price is json.Number to accept both
// "2312.14" and 2312.14.
type payload struct {
	StrategyID  *string      `json:"strategy_id"`
	Symbol      *string      `json:"symbol"`
	Signal      *string      `json:"signal"`
	Price       *json.Number `json:"price"`
	Timestamp   *int64       `json:"timestamp"`
	Secret      *string      `json:"secret"`
}

func Parse(body []byte) (*Signal, error) {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var p payload
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if p.StrategyID == nil || *p.StrategyID == "" {
		return nil, errors.New("strategy_id required")
	}
	if p.Symbol == nil || *p.Symbol == "" {
		return nil, errors.New("symbol required")
	}
	if p.Signal == nil {
		return nil, errors.New("signal required")
	}
	if p.Price == nil {
		return nil, errors.New("price required")
	}
	if p.Timestamp == nil {
		return nil, errors.New("timestamp required")
	}
	if p.Secret == nil || *p.Secret == "" {
		return nil, errors.New("secret required")
	}
	kind, err := parseKind(*p.Signal)
	if err != nil {
		return nil, err
	}
	price, err := decimal.NewFromString(string(*p.Price))
	if err != nil {
		return nil, fmt.Errorf("price invalid: %w", err)
	}
	if !price.IsPositive() {
		return nil, fmt.Errorf("price must be positive, got %s", price.String())
	}
	if *p.Timestamp <= 0 {
		return nil, fmt.Errorf("timestamp must be > 0, got %d", *p.Timestamp)
	}
	return &Signal{
		StrategyID:    *p.StrategyID,
		Symbol:        strings.ToUpper(*p.Symbol),
		Kind:          kind,
		Price:         price,
		TVTimestamp:   time.UnixMilli(*p.Timestamp).UTC(),
		TVTimestampMs: *p.Timestamp,
		Secret:        *p.Secret,
		Raw:           json.RawMessage(body),
	}, nil
}
```

- [ ] **Step 5: 跑测试**

Run: `go test ./internal/domain/signal/... -v`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add internal/domain/signal/ go.sum
git commit -m "feat(domain/signal): parse and validate TradingView webhook payload"
```

---

## Task 7: domain/strategy — Strategy 实体

**Files:**
- Create: `internal/domain/strategy/strategy.go`
- Create: `internal/domain/strategy/strategy_test.go`

- [ ] **Step 1: 写测试 `internal/domain/strategy/strategy_test.go`**

```go
package strategy

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_RejectsInvalidLeverage(t *testing.T) {
	_, err := New(Config{ID: "s", Symbol: "ETHUSDC", Leverage: 0,
		SizeUSDC: decimal.NewFromInt(10), StopLossPct: decimal.NewFromFloat(1.5),
		MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true})
	require.Error(t, err)
}

func TestNew_RejectsNonPositiveSize(t *testing.T) {
	_, err := New(Config{ID: "s", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.Zero, StopLossPct: decimal.NewFromFloat(1.5),
		MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true})
	require.Error(t, err)
}

func TestNew_RejectsNonPositiveStopLoss(t *testing.T) {
	_, err := New(Config{ID: "s", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.NewFromInt(10), StopLossPct: decimal.Zero,
		MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true})
	require.Error(t, err)
}

func TestNew_RejectsMaxOpenLessThanSize(t *testing.T) {
	_, err := New(Config{ID: "s", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.NewFromInt(100), StopLossPct: decimal.NewFromFloat(1.5),
		MaxOpenUSDC: decimal.NewFromInt(50), Enabled: true})
	require.Error(t, err)
}

func TestNew_AcceptsValid(t *testing.T) {
	s, err := New(Config{
		ID: "macd_eth_high", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.NewFromInt(100), StopLossPct: decimal.NewFromFloat(1.5),
		TakeProfitPct: decimal.NewFromFloat(3.0),
		MaxOpenUSDC: decimal.NewFromInt(500), Enabled: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "macd_eth_high", s.ID)
	assert.True(t, s.HasTakeProfit())
}

func TestNotionalAtPrice(t *testing.T) {
	s, _ := New(Config{ID: "s", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.NewFromInt(100), StopLossPct: decimal.NewFromFloat(1),
		MaxOpenUSDC: decimal.NewFromInt(500), Enabled: true})
	notional := s.NotionalUSDC()
	// notional = size_usdc * leverage = 100 * 5 = 500
	assert.True(t, notional.Equal(decimal.NewFromInt(500)))
}
```

- [ ] **Step 2: 跑测试看到失败**

Run: `go test ./internal/domain/strategy/...`
Expected: FAIL

- [ ] **Step 3: 实现 `internal/domain/strategy/strategy.go`**

```go
package strategy

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

type Config struct {
	ID            string
	Symbol        string
	Leverage      int
	SizeUSDC      decimal.Decimal
	StopLossPct   decimal.Decimal // 1.5 表示 1.5%
	TakeProfitPct decimal.Decimal // optional, zero means none
	MaxOpenUSDC   decimal.Decimal
	Enabled       bool
}

type Strategy struct {
	Config
}

func New(c Config) (*Strategy, error) {
	if c.ID == "" {
		return nil, errors.New("id required")
	}
	if c.Symbol == "" {
		return nil, errors.New("symbol required")
	}
	if c.Leverage <= 0 || c.Leverage > 125 {
		return nil, fmt.Errorf("leverage out of range: %d", c.Leverage)
	}
	if !c.SizeUSDC.IsPositive() {
		return nil, errors.New("size_usdc must be positive")
	}
	if !c.StopLossPct.IsPositive() {
		return nil, errors.New("stop_loss_pct must be positive")
	}
	if !c.MaxOpenUSDC.IsPositive() {
		return nil, errors.New("max_open_usdc must be positive")
	}
	if c.MaxOpenUSDC.LessThan(c.SizeUSDC) {
		return nil, fmt.Errorf("max_open_usdc %s < size_usdc %s",
			c.MaxOpenUSDC.String(), c.SizeUSDC.String())
	}
	return &Strategy{Config: c}, nil
}

func (s *Strategy) HasTakeProfit() bool { return s.TakeProfitPct.IsPositive() }

// NotionalUSDC returns the per-trade notional value: size * leverage.
func (s *Strategy) NotionalUSDC() decimal.Decimal {
	return s.SizeUSDC.Mul(decimal.NewFromInt(int64(s.Leverage)))
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/domain/strategy/... -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain/strategy/
git commit -m "feat(domain/strategy): strategy entity with config validation"
```

---

## Task 8: domain/position — VirtualPosition + 决策表

**Files:**
- Create: `internal/domain/position/position.go`
- Create: `internal/domain/position/decision.go`
- Create: `internal/domain/position/decision_test.go`

- [ ] **Step 1: 写测试 `internal/domain/position/decision_test.go`**

```go
package position

import (
	"testing"

	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/stretchr/testify/assert"
)

func TestDecide_AllEightCases(t *testing.T) {
	cases := []struct {
		name    string
		current *VirtualPosition // nil = 空仓
		signal  sigpkg.Kind
		want    Action
	}{
		{"empty + Long", nil, sigpkg.KindLong, ActionOpenLong},
		{"empty + Short", nil, sigpkg.KindShort, ActionOpenShort},
		{"empty + ExitLong", nil, sigpkg.KindExitLong, ActionNoOp},
		{"empty + ExitShort", nil, sigpkg.KindExitShort, ActionNoOp},
		{"long + ExitLong", &VirtualPosition{Side: SideLong, Status: StatusOpen}, sigpkg.KindExitLong, ActionClose},
		{"long + Long", &VirtualPosition{Side: SideLong, Status: StatusOpen}, sigpkg.KindLong, ActionNoOp},
		{"long + Short", &VirtualPosition{Side: SideLong, Status: StatusOpen}, sigpkg.KindShort, ActionCloseAndOpenShort},
		{"long + ExitShort", &VirtualPosition{Side: SideLong, Status: StatusOpen}, sigpkg.KindExitShort, ActionNoOp},
		{"short + ExitShort", &VirtualPosition{Side: SideShort, Status: StatusOpen}, sigpkg.KindExitShort, ActionClose},
		{"short + Short", &VirtualPosition{Side: SideShort, Status: StatusOpen}, sigpkg.KindShort, ActionNoOp},
		{"short + Long", &VirtualPosition{Side: SideShort, Status: StatusOpen}, sigpkg.KindLong, ActionCloseAndOpenLong},
		{"short + ExitLong", &VirtualPosition{Side: SideShort, Status: StatusOpen}, sigpkg.KindExitLong, ActionNoOp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.current, tc.signal)
			assert.Equal(t, tc.want, got, "case=%s", tc.name)
		})
	}
}
```

- [ ] **Step 2: 写测试 `internal/domain/position/position_test.go`**

```go
package position

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatus_TransitionsAreLinear(t *testing.T) {
	// opening -> open -> closing -> closed, no rewinds
	assert.True(t, StatusOpening.CanTransitionTo(StatusOpen))
	assert.False(t, StatusOpen.CanTransitionTo(StatusOpening))
	assert.True(t, StatusOpen.CanTransitionTo(StatusClosing))
	assert.True(t, StatusClosing.CanTransitionTo(StatusClosed))
	assert.False(t, StatusClosed.CanTransitionTo(StatusClosing))
	assert.False(t, StatusClosed.CanTransitionTo(StatusOpen))
	// 同状态不允许（必须真正前进）
	assert.False(t, StatusOpen.CanTransitionTo(StatusOpen))
}

func TestStatus_IsActive(t *testing.T) {
	assert.True(t, StatusOpening.IsActive())
	assert.True(t, StatusOpen.IsActive())
	assert.True(t, StatusClosing.IsActive())
	assert.False(t, StatusClosed.IsActive())
}
```

- [ ] **Step 3: 跑测试看到失败**

Run: `go test ./internal/domain/position/...`
Expected: FAIL

- [ ] **Step 4: 实现 `internal/domain/position/position.go`**

```go
package position

import (
	"time"

	"github.com/shopspring/decimal"
)

type Side string

const (
	SideLong  Side = "long"
	SideShort Side = "short"
)

type Status string

const (
	StatusOpening Status = "opening"
	StatusOpen    Status = "open"
	StatusClosing Status = "closing"
	StatusClosed  Status = "closed"
)

func (s Status) IsActive() bool {
	return s == StatusOpening || s == StatusOpen || s == StatusClosing
}

func (s Status) CanTransitionTo(next Status) bool {
	switch s {
	case StatusOpening:
		return next == StatusOpen || next == StatusClosed
	case StatusOpen:
		return next == StatusClosing || next == StatusClosed
	case StatusClosing:
		return next == StatusClosed
	}
	return false
}

type VirtualPosition struct {
	ID                 int64
	StrategyID         string
	Symbol             string
	Side               Side
	Qty                decimal.Decimal
	EntrySignalPrice   decimal.Decimal
	EntryFillPrice     decimal.Decimal // zero until filled
	EntrySignalID      int64
	EntryOrderID       int64
	StopOrderID        int64
	BackupStopOrderID  int64
	TakeProfitOrderID  int64
	Status             Status
	OpenedAt           time.Time
	ClosedAt           time.Time
}
```

- [ ] **Step 5: 实现 `internal/domain/position/decision.go`**

```go
package position

import sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"

type Action string

const (
	ActionNoOp              Action = "noop"
	ActionOpenLong          Action = "open_long"
	ActionOpenShort         Action = "open_short"
	ActionClose             Action = "close"
	ActionCloseAndOpenLong  Action = "close_and_open_long"
	ActionCloseAndOpenShort Action = "close_and_open_short"
)

// Decide returns the action to take given current position (nil = flat) and incoming signal.
// Implements the 8x decision table in spec §7 Step 5.
func Decide(current *VirtualPosition, sig sigpkg.Kind) Action {
	if current == nil || !current.Status.IsActive() {
		switch sig {
		case sigpkg.KindLong:
			return ActionOpenLong
		case sigpkg.KindShort:
			return ActionOpenShort
		}
		return ActionNoOp
	}
	switch current.Side {
	case SideLong:
		switch sig {
		case sigpkg.KindExitLong:
			return ActionClose
		case sigpkg.KindShort:
			return ActionCloseAndOpenShort
		}
	case SideShort:
		switch sig {
		case sigpkg.KindExitShort:
			return ActionClose
		case sigpkg.KindLong:
			return ActionCloseAndOpenLong
		}
	}
	return ActionNoOp
}
```

- [ ] **Step 6: 跑测试**

Run: `go test ./internal/domain/position/... -v`
Expected: 12 个决策 case + 状态机 case 全 PASS

- [ ] **Step 7: Commit**

```bash
git add internal/domain/position/
git commit -m "feat(domain/position): VirtualPosition state machine + 8-state decision table"
```

---

## Task 9: domain/order — Order + 状态转移

**Files:**
- Create: `internal/domain/order/order.go`
- Create: `internal/domain/order/order_test.go`

- [ ] **Step 1: 写测试 `internal/domain/order/order_test.go`**

```go
package order

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatus_LifecycleAllowed(t *testing.T) {
	// pending -> submitted -> partial -> filled
	assert.True(t, StatusPending.CanTransitionTo(StatusSubmitted))
	assert.True(t, StatusSubmitted.CanTransitionTo(StatusPartial))
	assert.True(t, StatusSubmitted.CanTransitionTo(StatusFilled))
	assert.True(t, StatusPartial.CanTransitionTo(StatusFilled))

	// terminal canceled / rejected / expired 不能再前进
	assert.False(t, StatusFilled.CanTransitionTo(StatusPartial))
	assert.False(t, StatusCanceled.CanTransitionTo(StatusFilled))
	assert.False(t, StatusRejected.CanTransitionTo(StatusFilled))
	assert.False(t, StatusExpired.CanTransitionTo(StatusFilled))

	// pending/submitted 可以被取消/拒绝
	assert.True(t, StatusPending.CanTransitionTo(StatusCanceled))
	assert.True(t, StatusSubmitted.CanTransitionTo(StatusCanceled))
	assert.True(t, StatusSubmitted.CanTransitionTo(StatusRejected))
}

func TestPurpose_String(t *testing.T) {
	assert.Equal(t, "entry", string(PurposeEntry))
	assert.Equal(t, "backup_stop", string(PurposeBackupStop))
}
```

- [ ] **Step 2: 跑测试看到失败**

Run: `go test ./internal/domain/order/...`
Expected: FAIL

- [ ] **Step 3: 实现 `internal/domain/order/order.go`**

```go
package order

import (
	"time"

	"github.com/shopspring/decimal"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusSubmitted Status = "submitted"
	StatusPartial   Status = "partial"
	StatusFilled    Status = "filled"
	StatusCanceled  Status = "canceled"
	StatusRejected  Status = "rejected"
	StatusExpired   Status = "expired"
)

func (s Status) IsTerminal() bool {
	return s == StatusFilled || s == StatusCanceled || s == StatusRejected || s == StatusExpired
}

func (s Status) CanTransitionTo(next Status) bool {
	if s.IsTerminal() {
		return false
	}
	switch s {
	case StatusPending:
		return next == StatusSubmitted || next == StatusCanceled || next == StatusRejected || next == StatusExpired
	case StatusSubmitted:
		return next == StatusPartial || next == StatusFilled || next == StatusCanceled || next == StatusRejected || next == StatusExpired
	case StatusPartial:
		return next == StatusFilled || next == StatusCanceled || next == StatusExpired
	}
	return false
}

type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

type Type string

const (
	TypeMarket            Type = "MARKET"
	TypeStop              Type = "STOP"
	TypeStopMarket        Type = "STOP_MARKET"
	TypeTakeProfitMarket  Type = "TAKE_PROFIT_MARKET"
)

type Purpose string

const (
	PurposeEntry       Purpose = "entry"
	PurposeExit        Purpose = "exit"
	PurposeStop        Purpose = "stop"
	PurposeBackupStop  Purpose = "backup_stop"
	PurposeTakeProfit  Purpose = "take_profit"
)

type Order struct {
	ID                int64
	VirtualPositionID int64
	StrategyID        string
	Symbol            string
	Side              Side
	Type              Type
	Purpose           Purpose
	Qty               decimal.Decimal
	Price             decimal.Decimal // optional
	StopPrice         decimal.Decimal // optional
	ClientOrderID     string
	ExchangeOrderID   string
	Status            Status
	FilledQty         decimal.Decimal
	AvgFillPrice      decimal.Decimal
	FeesUSDC          decimal.Decimal
	SubmittedAt       time.Time
	FilledAt          time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/domain/order/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain/order/
git commit -m "feat(domain/order): Order types + state machine"
```

---

## Task 10: risk — 接口与 Pipeline

**Files:**
- Create: `internal/risk/types.go`
- Create: `internal/risk/pipeline.go`
- Create: `internal/risk/pipeline_test.go`

- [ ] **Step 1: 写测试 `internal/risk/pipeline_test.go`**

```go
package risk

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubRule struct {
	name    string
	dec     Decision
	calledP *bool
	err     error
}

func (s *stubRule) Name() string { return s.name }
func (s *stubRule) Check(ctx context.Context, in Input) (Decision, error) {
	if s.calledP != nil {
		*s.calledP = true
	}
	return s.dec, s.err
}

func TestPipeline_AllAllow(t *testing.T) {
	p := NewPipeline(
		&stubRule{name: "a", dec: Allow()},
		&stubRule{name: "b", dec: Allow()},
	)
	d, err := p.Run(context.Background(), Input{})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestPipeline_ShortCircuitsOnDeny(t *testing.T) {
	calledThird := false
	p := NewPipeline(
		&stubRule{name: "a", dec: Allow()},
		&stubRule{name: "b", dec: Deny("rule b denied")},
		&stubRule{name: "c", dec: Allow(), calledP: &calledThird},
	)
	d, err := p.Run(context.Background(), Input{})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Equal(t, "b", d.RuleName)
	assert.Equal(t, "rule b denied", d.Reason)
	assert.False(t, calledThird, "third rule should not be called after deny")
}

func TestPipeline_RulesErrorPropagates(t *testing.T) {
	p := NewPipeline(
		&stubRule{name: "a", err: errors.New("boom")},
	)
	_, err := p.Run(context.Background(), Input{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rule a")
}
```

- [ ] **Step 2: 跑测试看到失败**

Run: `go test ./internal/risk/...`
Expected: FAIL

- [ ] **Step 3: 实现 `internal/risk/types.go`**

```go
package risk

import (
	"context"
	"net"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/domain/position"
	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
)

// Input 包含一条信号经风控判断时所需的全部上下文。
type Input struct {
	Signal           *sigpkg.Signal
	Strategy         *strategy.Strategy
	CurrentPosition  *position.VirtualPosition // 该策略当前活跃仓位（nil 即空仓）
	OpenNotionalSum  decimal.Decimal           // 全局所有活跃仓位的名义价值之和（USDC）
	AccountEquity    decimal.Decimal           // 账户权益（USDC）
	DailyPnLUSDC     decimal.Decimal           // 当日盈亏
	BreakerTripped   bool
	ClientIP         net.IP
}

type Decision struct {
	Allowed  bool
	RuleName string
	Reason   string
}

func Allow() Decision           { return Decision{Allowed: true} }
func Deny(reason string) Decision { return Decision{Allowed: false, Reason: reason} }

type Rule interface {
	Name() string
	Check(ctx context.Context, in Input) (Decision, error)
}
```

- [ ] **Step 4: 实现 `internal/risk/pipeline.go`**

```go
package risk

import (
	"context"
	"fmt"
)

type Pipeline struct {
	rules []Rule
}

func NewPipeline(rules ...Rule) *Pipeline { return &Pipeline{rules: rules} }

func (p *Pipeline) Run(ctx context.Context, in Input) (Decision, error) {
	for _, r := range p.rules {
		d, err := r.Check(ctx, in)
		if err != nil {
			return Decision{}, fmt.Errorf("rule %s: %w", r.Name(), err)
		}
		if !d.Allowed {
			d.RuleName = r.Name()
			return d, nil
		}
	}
	return Allow(), nil
}
```

- [ ] **Step 5: 跑测试**

Run: `go test ./internal/risk/... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/risk/
git commit -m "feat(risk): Rule interface + short-circuiting pipeline"
```

---

## Task 11: risk/max_position — 单策略最大未平仓金额

**Files:**
- Create: `internal/risk/max_position.go`
- Create: `internal/risk/max_position_test.go`

- [ ] **Step 1: 写测试 `internal/risk/max_position_test.go`**

```go
package risk

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
)

func mustStrategy(t *testing.T, sizeUSDC, maxOpen float64) *strategy.Strategy {
	t.Helper()
	s, err := strategy.New(strategy.Config{
		ID: "s", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC:    decimal.NewFromFloat(sizeUSDC),
		StopLossPct: decimal.NewFromFloat(1.5),
		MaxOpenUSDC: decimal.NewFromFloat(maxOpen),
		Enabled:     true,
	})
	require.NoError(t, err)
	return s
}

func TestMaxPositionRule_AllowsBelowLimit(t *testing.T) {
	r := MaxPositionRule{}
	s := mustStrategy(t, 100, 500)
	d, err := r.Check(context.Background(), Input{
		Signal:   &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy: s,
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestMaxPositionRule_DeniesNotionalAboveMaxOpen(t *testing.T) {
	r := MaxPositionRule{}
	// notional = 200 * 5 = 1000 > max_open 500
	s := mustStrategy(t, 200, 500)
	d, err := r.Check(context.Background(), Input{
		Signal:   &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy: s,
	})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "max_open_usdc")
}

func TestMaxPositionRule_AllowsExitSignal(t *testing.T) {
	// 平仓信号不受 max_open 限制
	r := MaxPositionRule{}
	s := mustStrategy(t, 200, 500)
	d, err := r.Check(context.Background(), Input{
		Signal:   &sigpkg.Signal{Kind: sigpkg.KindExitLong},
		Strategy: s,
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}
```

- [ ] **Step 2: 跑测试看到失败**

Run: `go test ./internal/risk/... -run MaxPosition`
Expected: FAIL

- [ ] **Step 3: 实现 `internal/risk/max_position.go`**

```go
package risk

import (
	"context"
	"fmt"
)

type MaxPositionRule struct{}

func (MaxPositionRule) Name() string { return "max_position" }

func (MaxPositionRule) Check(_ context.Context, in Input) (Decision, error) {
	if in.Signal == nil || !in.Signal.Kind.IsEntry() {
		return Allow(), nil
	}
	if in.Strategy == nil {
		return Deny("strategy not loaded"), nil
	}
	notional := in.Strategy.NotionalUSDC()
	if notional.GreaterThan(in.Strategy.MaxOpenUSDC) {
		return Deny(fmt.Sprintf("notional %s > strategy.max_open_usdc %s",
			notional.String(), in.Strategy.MaxOpenUSDC.String())), nil
	}
	return Allow(), nil
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/risk/... -run MaxPosition -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/risk/max_position.go internal/risk/max_position_test.go
git commit -m "feat(risk): max_position rule (per-strategy notional cap)"
```

---

## Task 12: risk/total_leverage — 总杠杆上限

**Files:**
- Create: `internal/risk/total_leverage.go`
- Create: `internal/risk/total_leverage_test.go`

- [ ] **Step 1: 写测试 `internal/risk/total_leverage_test.go`**

```go
package risk

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
)

func TestTotalLeverageRule_AllowsBelowMax(t *testing.T) {
	r := TotalLeverageRule{MaxLeverage: decimal.NewFromFloat(3.0)}
	s := mustStrategy(t, 100, 500) // notional = 500
	d, err := r.Check(context.Background(), Input{
		Signal:          &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy:        s,
		OpenNotionalSum: decimal.NewFromFloat(1000), // existing
		AccountEquity:   decimal.NewFromFloat(1000), // (1000+500)/1000 = 1.5x < 3.0x
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestTotalLeverageRule_DeniesAboveMax(t *testing.T) {
	r := TotalLeverageRule{MaxLeverage: decimal.NewFromFloat(2.0)}
	s := mustStrategy(t, 100, 500) // notional = 500
	d, err := r.Check(context.Background(), Input{
		Signal:          &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy:        s,
		OpenNotionalSum: decimal.NewFromFloat(2000),
		AccountEquity:   decimal.NewFromFloat(1000), // (2000+500)/1000 = 2.5x > 2.0x
	})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "max_total_leverage")
}

func TestTotalLeverageRule_AllowsExitSignal(t *testing.T) {
	r := TotalLeverageRule{MaxLeverage: decimal.NewFromFloat(1.0)}
	s := mustStrategy(t, 100, 500)
	d, err := r.Check(context.Background(), Input{
		Signal: &sigpkg.Signal{Kind: sigpkg.KindExitLong},
		Strategy: s,
		OpenNotionalSum: decimal.NewFromFloat(99999),
		AccountEquity:   decimal.NewFromFloat(1),
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestTotalLeverageRule_RejectsZeroEquity(t *testing.T) {
	r := TotalLeverageRule{MaxLeverage: decimal.NewFromFloat(3.0)}
	s := mustStrategy(t, 100, 500)
	d, err := r.Check(context.Background(), Input{
		Signal: &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy: s,
		AccountEquity: decimal.Zero,
	})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "equity")
}
```

- [ ] **Step 2: 跑测试看到失败**

Run: `go test ./internal/risk/... -run TotalLeverage`
Expected: FAIL

- [ ] **Step 3: 实现 `internal/risk/total_leverage.go`**

```go
package risk

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
)

type TotalLeverageRule struct {
	MaxLeverage decimal.Decimal // e.g. 3.0
}

func (r TotalLeverageRule) Name() string { return "total_leverage" }

func (r TotalLeverageRule) Check(_ context.Context, in Input) (Decision, error) {
	if in.Signal == nil || !in.Signal.Kind.IsEntry() {
		return Allow(), nil
	}
	if !in.AccountEquity.IsPositive() {
		return Deny("account equity unavailable or non-positive"), nil
	}
	added := in.Strategy.NotionalUSDC()
	totalAfter := in.OpenNotionalSum.Add(added)
	leverage := totalAfter.Div(in.AccountEquity)
	if leverage.GreaterThan(r.MaxLeverage) {
		return Deny(fmt.Sprintf("leverage %s > max_total_leverage %s",
			leverage.StringFixed(2), r.MaxLeverage.StringFixed(2))), nil
	}
	return Allow(), nil
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/risk/... -run TotalLeverage -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/risk/total_leverage.go internal/risk/total_leverage_test.go
git commit -m "feat(risk): total_leverage rule (global notional cap)"
```

---

## Task 13: risk/daily_loss_breaker — 日亏熔断

**Files:**
- Create: `internal/risk/daily_loss_breaker.go`
- Create: `internal/risk/daily_loss_breaker_test.go`

- [ ] **Step 1: 写测试 `internal/risk/daily_loss_breaker_test.go`**

```go
package risk

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
)

func TestDailyLossBreaker_AllowsWhenBreakerNotTripped(t *testing.T) {
	r := DailyLossBreakerRule{MaxDailyLossUSDC: decimal.NewFromFloat(500)}
	d, err := r.Check(context.Background(), Input{
		Signal:         &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy:       mustStrategy(t, 100, 500),
		DailyPnLUSDC:   decimal.NewFromFloat(-100),
		BreakerTripped: false,
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestDailyLossBreaker_DeniesWhenBreakerTripped(t *testing.T) {
	r := DailyLossBreakerRule{MaxDailyLossUSDC: decimal.NewFromFloat(500)}
	d, err := r.Check(context.Background(), Input{
		Signal:         &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy:       mustStrategy(t, 100, 500),
		BreakerTripped: true,
	})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "breaker")
}

func TestDailyLossBreaker_DeniesWhenLossExceedsThreshold(t *testing.T) {
	r := DailyLossBreakerRule{MaxDailyLossUSDC: decimal.NewFromFloat(500)}
	d, err := r.Check(context.Background(), Input{
		Signal:       &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy:     mustStrategy(t, 100, 500),
		DailyPnLUSDC: decimal.NewFromFloat(-501), // 损失超过 500
	})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "daily loss")
}

func TestDailyLossBreaker_AllowsExitSignalEvenWhenTripped(t *testing.T) {
	// 平仓信号必须能通过，否则永远卡住
	r := DailyLossBreakerRule{MaxDailyLossUSDC: decimal.NewFromFloat(500)}
	d, err := r.Check(context.Background(), Input{
		Signal:         &sigpkg.Signal{Kind: sigpkg.KindExitLong},
		BreakerTripped: true,
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}
```

- [ ] **Step 2: 跑测试看到失败**

Run: `go test ./internal/risk/... -run DailyLoss`
Expected: FAIL

- [ ] **Step 3: 实现 `internal/risk/daily_loss_breaker.go`**

```go
package risk

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
)

type DailyLossBreakerRule struct {
	MaxDailyLossUSDC decimal.Decimal
}

func (r DailyLossBreakerRule) Name() string { return "daily_loss_breaker" }

func (r DailyLossBreakerRule) Check(_ context.Context, in Input) (Decision, error) {
	// 平仓信号永远允许（不能让熔断卡死现有持仓）
	if in.Signal != nil && in.Signal.Kind.IsExit() {
		return Allow(), nil
	}
	if in.BreakerTripped {
		return Deny("daily loss breaker tripped"), nil
	}
	loss := in.DailyPnLUSDC.Neg() // 亏损 → 正数
	if loss.GreaterThan(r.MaxDailyLossUSDC) {
		return Deny(fmt.Sprintf("daily loss %s > max %s",
			loss.StringFixed(2), r.MaxDailyLossUSDC.StringFixed(2))), nil
	}
	return Allow(), nil
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/risk/... -run DailyLoss -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/risk/daily_loss_breaker.go internal/risk/daily_loss_breaker_test.go
git commit -m "feat(risk): daily_loss_breaker rule (max daily drawdown)"
```

---

## Task 14: risk/ip_whitelist — IP 白名单

**Files:**
- Create: `internal/risk/ip_whitelist.go`
- Create: `internal/risk/ip_whitelist_test.go`

- [ ] **Step 1: 写测试 `internal/risk/ip_whitelist_test.go`**

```go
package risk

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIPWhitelistRule_AllowsListedIP(t *testing.T) {
	r, err := NewIPWhitelistRule([]string{"127.0.0.1", "10.0.0.0/8"})
	require.NoError(t, err)
	d, err := r.Check(context.Background(), Input{ClientIP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestIPWhitelistRule_AllowsCIDRMatch(t *testing.T) {
	r, err := NewIPWhitelistRule([]string{"10.0.0.0/8"})
	require.NoError(t, err)
	d, err := r.Check(context.Background(), Input{ClientIP: net.ParseIP("10.5.6.7")})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestIPWhitelistRule_DeniesUnlisted(t *testing.T) {
	r, err := NewIPWhitelistRule([]string{"127.0.0.1"})
	require.NoError(t, err)
	d, err := r.Check(context.Background(), Input{ClientIP: net.ParseIP("8.8.8.8")})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "8.8.8.8")
}

func TestIPWhitelistRule_DeniesMissingIP(t *testing.T) {
	r, err := NewIPWhitelistRule([]string{"127.0.0.1"})
	require.NoError(t, err)
	d, err := r.Check(context.Background(), Input{})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
}

func TestIPWhitelistRule_RejectsBadCIDR(t *testing.T) {
	_, err := NewIPWhitelistRule([]string{"not-an-ip"})
	require.Error(t, err)
}
```

- [ ] **Step 2: 跑测试看到失败**

Run: `go test ./internal/risk/... -run IPWhitelist`
Expected: FAIL

- [ ] **Step 3: 实现 `internal/risk/ip_whitelist.go`**

```go
package risk

import (
	"context"
	"fmt"
	"net"
	"strings"
)

type IPWhitelistRule struct {
	exactIPs []net.IP
	cidrs    []*net.IPNet
}

func NewIPWhitelistRule(entries []string) (*IPWhitelistRule, error) {
	r := &IPWhitelistRule{}
	for _, raw := range entries {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			_, n, err := net.ParseCIDR(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid cidr %q: %w", raw, err)
			}
			r.cidrs = append(r.cidrs, n)
			continue
		}
		ip := net.ParseIP(raw)
		if ip == nil {
			return nil, fmt.Errorf("invalid ip %q", raw)
		}
		r.exactIPs = append(r.exactIPs, ip)
	}
	return r, nil
}

func (r *IPWhitelistRule) Name() string { return "ip_whitelist" }

func (r *IPWhitelistRule) Check(_ context.Context, in Input) (Decision, error) {
	if in.ClientIP == nil {
		return Deny("client ip missing"), nil
	}
	for _, ip := range r.exactIPs {
		if ip.Equal(in.ClientIP) {
			return Allow(), nil
		}
	}
	for _, c := range r.cidrs {
		if c.Contains(in.ClientIP) {
			return Allow(), nil
		}
	}
	return Deny(fmt.Sprintf("ip %s not in whitelist", in.ClientIP)), nil
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/risk/... -run IPWhitelist -v`
Expected: PASS

- [ ] **Step 5: 跑全部 risk 包测试**

Run: `go test ./internal/risk/... -v`
Expected: 所有 rule 测试 + pipeline 测试全 PASS

- [ ] **Step 6: Commit**

```bash
git add internal/risk/ip_whitelist.go internal/risk/ip_whitelist_test.go
git commit -m "feat(risk): ip_whitelist rule (exact IP + CIDR)"
```

---

## Task 15: 全量 race + lint 验证

- [ ] **Step 1: 跑全部测试 with race detector**

Run: `make test-v` （即 `go test -race -v ./...`）
Expected: 全部 PASS，无 race warning

- [ ] **Step 2: lint**

Run: `make lint`
Expected: 无 vet 报错

- [ ] **Step 3: 验证 build**

Run: `make build`
Expected: `bin/tvbot` 生成

- [ ] **Step 4: 验证 down + 重新 up 不影响测试**

```bash
make migrate-down
make migrate-up
make test-v
```
Expected: PASS

- [ ] **Step 5: 最终 commit（如果上面有任何 lint fix）**

```bash
git status   # 确认 clean，否则 commit fix
```

---

## Self-Review Checklist（已在写完后自查）

- [x] 所有 14 个决策（spec §2 表）在本 plan 都有对应任务或被声明留给后续 plan：
  - 决策 1（Binance）—— 留给 plan 2
  - 决策 2（Go 核心）—— 本 plan 用 Go ✓
  - 决策 3（MVP 范围）—— 本 plan 是其中一部分
  - 决策 4（多策略虚拟仓位）—— Task 8 的 VirtualPosition + 决策表 ✓
  - 决策 5（信号契约）—— Task 6 ✓
  - 决策 6（双重止损）—— 留给 plan 2
  - 决策 7（固定 USDC + 4 风控）—— Task 11/12/13/14 全部覆盖 ✓
  - 决策 8（本地部署）—— Task 1 docker-compose + Makefile ✓（部分）
  - 决策 9（PostgreSQL）—— Task 4 + Task 5 ✓
  - 决策 10（飞书+TG）—— 留给 plan 2
  - 决策 11/12（Web 后台）—— 留给 plan 3
  - 决策 13（双层幂等）—— 留给 plan 2
  - 决策 14（dry_run/testnet/live + arm）—— Task 2 BotMode 校验 ✓（armed 状态留 plan 2）

- [x] 无 placeholder（无 TBD、TODO、"similar to task N"）
- [x] 类型一致：`Kind`、`Action`、`Status`、`Side`、`Purpose`、`Type` 全部贯穿一致
- [x] DB schema 每列均有 `COMMENT ON COLUMN` 中文备注（Task 4）

---

## 下一步

完成 **Plan 1** 后，进入：
- **Plan 2 — Trading Engine**：idempotency + Binance SDK 抽象（dry_run/testnet/live） + ingest 流程 + trade（开/止损/平） + notify + reconcile + 启动恢复
- **Plan 3 — Web Layer**：HTTP webhook + middleware + auth + HTMX 后台 4 页
- **Plan 4 — Integration & Deploy**：docker-compose 完整版 + 集成测试 + E2E + CI

每个后续 plan 在前一个完成后单独写出。

**EOF**
