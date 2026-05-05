# tvbot — TradingView Webhook 自动交易机器人

一个单 Go 二进制程序，接收 TradingView 策略告警（通过 HTTPS webhook），经过风控流水线处理后，在币安 USDT/USDC-M 永续合约上执行交易订单。状态持久化存储于 PostgreSQL。内置 HTMX 管理后台，提供实时可视化和系统控制功能。

---

## 概览 (Overview)

- **Webhook 接收**：TradingView 向 `/webhook/tv` 发送 JSON 载荷。接收流水线验证 HMAC 签名、检查幂等性、执行风控规则，并下发交易指令。
- **风控流水线**：IP 白名单、单策略最大仓位限制、总杠杆上限、日亏熔断。
- **交易模式**：DryRun（以信号价格模拟成交）、Testnet（币安合约测试网）、Live（真实执行）。
- **虚拟持仓核算**：多策略盈亏独立追踪，部分成交时按 FIFO 逻辑对账。
- **通知**：在成交/拒绝事件时推送飞书和/或 Telegram 消息。
- **管理后台**：基于 HTMX，需登录鉴权。支持启动/停止系统、管理策略、查看持仓和信号。

---

## 系统架构 (Architecture)

```
TradingView ──webhook──► /webhook/tv ──► [parse → idempotency → risk → decide → trade → notify]
                                                                                    │
                              ┌─ Binance Futures API ◄──── Trader (DryRun/Binance)  │
                              │
PostgreSQL ◄──── repos ◄──── application/{ingest,trade,reconcile}                  │
                                                                                    │
                                                    Notifier (Feishu / Telegram) ◄──┘
```

核心包说明：

| 包路径 | 职责 |
|---|---|
| `internal/application/ingest` | Webhook 解析、幂等检查、风控流水线编排 |
| `internal/application/trade` | 下单、虚拟持仓追踪 |
| `internal/application/reconcile` | 后台成交对账、启动时恢复 |
| `internal/risk` | IP 白名单、最大仓位、杠杆上限、日亏熔断 |
| `internal/store` | PostgreSQL 数据访问层（信号、订单、持仓、策略、系统状态） |
| `internal/web/admin` | HTMX 管理后台处理器 |
| `internal/web/middleware` | 会话鉴权中间件、IP 白名单 HTTP 中间件 |
| `internal/infrastructure/binance` | 币安合约 REST 客户端（live + testnet） |

---

## 快速开始 (Quick Start, 5 分钟)

**前置条件**：Go 1.23+ 和 Docker（或本地 Postgres 16+ 实例）。

```bash
# 克隆仓库
git clone <repo-url> && cd crypto

# 启动 Postgres
make pg-up
make migrate-up

# 编译二进制
make build

# 配置
cp config/config.yaml.example config/config.yaml
cp .env.example .env
$EDITOR .env   # set BOT_MODE, WEBHOOK_SECRET, SESSION_SECRET at minimum

# 创建第一个管理员账号（交互式提示）
./bin/tvbot seed-user

# 运行
./bin/tvbot
```

然后在浏览器中打开 http://localhost:8080/login。

### Docker Compose（另一种方式）

```bash
cp .env.example .env
$EDITOR .env   # configure secrets

# 仅启动 postgres（先手动执行迁移）
docker compose up -d postgres
make migrate-up

# 再启动 bot
docker compose up -d bot
```

---

## TradingView 告警配置 (TradingView Alert Setup)

在您的 TradingView 策略中，创建一个指向以下地址的 webhook 告警：

```
https://<your-domain>/webhook/tv
```

使用如下 JSON 消息模板：

```json
{
  "strategy_id": "macd_eth_high",
  "symbol": "{{ticker}}",
  "signal": "{{strategy.order.action}}",
  "price": "{{close}}",
  "timestamp": {{time}},
  "secret": "<your-webhook-secret>"
}
```

- `strategy_id` 必须与 `strategies` 表中的某一行匹配（通过管理后台创建）。
- `signal` 取值：`buy`、`sell`、`close_long`、`close_short`。
- `secret` 必须与 `.env` 中的 `WEBHOOK_SECRET` 一致。

`config/config.yaml` 中的 IP 白名单已预置 TradingView 官方出口 IP：

```yaml
ip_whitelist:
  - 52.89.214.238
  - 34.212.75.30
  - 54.218.53.128
  - 52.32.178.7
  - 127.0.0.1   # local dev / cloudflared loopback
```

---

## 运行模式 (Modes)

| 模式 | 说明 |
|---|---|
| `dry_run` | 不调用交易所接口，按信号价格模拟成交 |
| `testnet` | 在币安合约测试网执行（https://testnet.binancefuture.com） |
| `live` | 真实资金交易，需要配置 `BINANCE_API_KEY` + `BINANCE_API_SECRET` |

通过 `BOT_MODE` 环境变量设置，程序启动时会打印当前模式。

---

## 运维操作 (Operations)

### 启动与停止 (Arming and Disarming)

系统启动后默认处于**停止状态**。在管理后台点击**启动交易**（或 POST `/system/arm`）之前，所有入场信号均被拒绝。这是一项纵深防御机制——每次重启后都需要主动重新启动。

停止交易无需重启：点击**停止交易**或 POST `/system/disarm`。

### 日亏熔断 (Daily PnL Circuit Breaker)

当 `system_state.daily_pnl_usdc` 跌破 `-max_daily_loss_usdc`（在 `config/config.yaml` 的 `risk.max_daily_loss_usdc` 中配置）时，所有入场信号自动被拒绝，平仓信号（close）仍正常处理。

手动重置熔断：POST `/system/breaker/reset` 或使用管理后台按钮。

熔断在 UTC 午夜自动重置。

### 后台对账 (Background Reconciler)

一个 goroutine 每隔 `reconciler.interval_seconds`（默认 30 秒）轮询未结订单，从交易所同步成交状态。启动时会执行一次恢复扫描，关闭所有孤立的未结订单。

---

## 测试 (Tests)

```bash
# 单元测试（无外部依赖）
go test -race ./...

# 集成测试（需要 Postgres —— 使用 dockertest 自动拉起容器）
go test -tags=integration -race ./...

# 实盘测试网冒烟测试（需要币安测试网密钥）
BINANCE_API_KEY=... BINANCE_API_SECRET=... \
  go test -tags=integration_binance ./internal/infrastructure/binance/...
```

CI 在每次推送时通过 GitHub Actions（`.github/workflows/ci.yml`）运行单元测试和集成测试。

---

## 配置参考 (Configuration Reference)

### 环境变量（`.env`）

| 变量名 | 是否必须 | 默认值 | 说明 |
|---|---|---|---|
| `BOT_MODE` | 是 | — | `dry_run`、`testnet` 或 `live` |
| `DATABASE_URL` | 是 | — | PostgreSQL 连接字符串 |
| `WEBHOOK_SECRET` | 是 | — | 与 TradingView 共享的 HMAC 密钥 |
| `SESSION_SECRET` | 是 | — | 管理后台会话 Cookie 的密钥，须 32 字符以上 |
| `HTTP_LISTEN` | 否 | `0.0.0.0:8080` | TCP 监听地址 |
| `LOG_LEVEL` | 否 | `info` | `debug`、`info`、`warn`、`error` |
| `BINANCE_API_KEY` | live/testnet | — | 币安 API Key（首次启动时写入 DB；后续可在 /settings 页面修改） |
| `BINANCE_API_SECRET` | live/testnet | — | 币安 API Secret（同上） |

### YAML 配置（`config/config.yaml`）

| 配置项 | 默认值 | 说明 |
|---|---|---|
| `risk.max_total_leverage` | `3.0` | 所有活跃仓位名义价值之和 ÷ 账户权益不得超过此倍数 |
| `risk.max_daily_loss_usdc` | `500` | 日亏熔断阈值（USDC） |
| `ip_whitelist` | TradingView 官方 IP + 127.0.0.1 | `/webhook/tv` 的 CIDR/IP 白名单；为空则允许所有来源 |
| `reconciler.interval_seconds` | `30` | 成交对账轮询间隔（秒） |

---

## 部署 (Deployment)

### Docker Compose

```bash
cp .env.example .env
$EDITOR .env   # configure all secrets

docker compose up -d postgres
make migrate-up         # apply DB schema
docker compose up -d bot
```

### 裸机部署（systemd）

```bash
make build
sudo cp bin/tvbot /usr/local/bin/tvbot
# create /etc/tvbot.env with all env vars
sudo systemctl enable --now tvbot
```

### HTTPS（TradingView 要求）

TradingView 仅向 HTTPS 地址推送告警。推荐方案：

- **Cloudflare Tunnel**（`cloudflared`）——零成本，无需公网 IP。Bot 监听 `localhost:8080`，cloudflared 通过您的域名对外暴露。
- **Caddy**——在 Caddyfile 中添加 `reverse_proxy localhost:8080`，Caddy 自动申请 Let's Encrypt 证书。
- **云负载均衡器**——在负载均衡器处终止 TLS，将 HTTP 请求转发给 Bot。

在 cloudflared 等回环隧道后运行时，IP 提取依赖 `X-Forwarded-For` 头（因为此时 `RemoteAddr` 为 `127.0.0.1`）。

---

## 架构深度解析 (Architecture Deep Dive)

完整设计文档请参阅：
[`docs/superpowers/specs/2026-05-05-tradingview-webhook-bot-design.md`](docs/superpowers/specs/2026-05-05-tradingview-webhook-bot-design.md)

---

## 许可证 (License)

MIT — 详见 [LICENSE](LICENSE)。
