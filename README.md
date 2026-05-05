# tvbot — TradingView Webhook Auto-Trader

A single Go binary that receives TradingView strategy alerts over HTTPS, passes them through a risk pipeline, and executes USDT/USDC-M perpetual futures orders on Binance. State is persisted in PostgreSQL. An HTMX admin UI provides live visibility and system controls.

---

## Overview

- **Webhook ingestion**: TradingView POSTs a JSON payload to `/webhook/tv`. The ingest pipeline validates the HMAC secret, checks idempotency, runs risk rules, and dispatches a trade.
- **Risk pipeline**: IP whitelist, max-position-per-strategy, total-leverage cap, daily-loss circuit-breaker.
- **Traders**: DryRun (simulated fills at reference price), Testnet (Binance Futures sandbox), Live (real execution).
- **Virtual position accounting**: multi-strategy PnL tracked independently; FIFO reconciliation on partial fills.
- **Notifications**: Feishu and/or Telegram on fill/reject events.
- **Admin UI**: HTMX-powered, session-authenticated. Arm/Disarm the system, manage strategies, inspect positions and signals.

---

## Architecture

```
TradingView ──webhook──► /webhook/tv ──► [parse → idempotency → risk → decide → trade → notify]
                                                                                    │
                              ┌─ Binance Futures API ◄──── Trader (DryRun/Binance)  │
                              │
PostgreSQL ◄──── repos ◄──── application/{ingest,trade,reconcile}                  │
                                                                                    │
                                                    Notifier (Feishu / Telegram) ◄──┘
```

Key packages:

| Package | Responsibility |
|---|---|
| `internal/application/ingest` | Webhook parse, idempotency, risk pipeline orchestration |
| `internal/application/trade` | Order placement, virtual position tracking |
| `internal/application/reconcile` | Background fill reconciliation, startup recovery |
| `internal/risk` | IP whitelist, max-position, leverage cap, daily-loss breaker |
| `internal/store` | PostgreSQL repos (signals, orders, positions, strategies, system state) |
| `internal/web/admin` | HTMX admin UI handlers |
| `internal/web/middleware` | Auth session gate, IP whitelist HTTP middleware |
| `internal/infrastructure/binance` | Binance Futures REST client (live + testnet) |

---

## Quick Start (5 min)

**Prerequisites**: Go 1.23+ and Docker (or a local Postgres 16+ instance).

```bash
# Clone
git clone <repo-url> && cd crypto

# Start Postgres
make pg-up
make migrate-up

# Build the binary
make build

# Configure
cp config/config.yaml.example config/config.yaml
cp .env.example .env
$EDITOR .env   # set BOT_MODE, WEBHOOK_SECRET, SESSION_SECRET at minimum

# Create the first admin user (interactive prompt)
./bin/tvbot seed-user

# Run
./bin/tvbot
```

Then open http://localhost:8080/login in your browser.

### Docker Compose (alternative)

```bash
cp .env.example .env
$EDITOR .env   # configure secrets

# Start only postgres (run migrations manually first)
docker compose up -d postgres
make migrate-up

# Then bring up the bot
docker compose up -d bot
```

---

## TradingView Alert Setup

In your TradingView strategy, create a webhook alert pointing to:

```
https://<your-domain>/webhook/tv
```

Use this JSON message template:

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

- `strategy_id` must match a row in the `strategies` table (create via admin UI).
- `signal` values: `buy`, `sell`, `close_long`, `close_short`.
- `secret` must match `WEBHOOK_SECRET` in your `.env`.

The IP whitelist in `config/config.yaml` is pre-seeded with TradingView's published egress IPs:

```yaml
ip_whitelist:
  - 52.89.214.238
  - 34.212.75.30
  - 54.218.53.128
  - 52.32.178.7
  - 127.0.0.1   # local dev / cloudflared loopback
```

---

## Modes

| Mode | Description |
|---|---|
| `dry_run` | No exchange calls; fills simulated at the signal price |
| `testnet` | Executes on Binance Futures testnet (https://testnet.binancefuture.com) |
| `live` | Real money. Requires `BINANCE_API_KEY` + `BINANCE_API_SECRET` |

Set via `BOT_MODE` env var. The binary prints the active mode at startup.

---

## Operations

### Arming and Disarming

The system starts **disarmed**. Entry signals are rejected until you click **启动交易** in the admin UI (or POST `/system/arm`). This is a defense-in-depth measure — every restart requires a deliberate re-arm.

To disarm without restart: click **停止交易** or POST `/system/disarm`.

### Daily PnL Circuit Breaker

When `system_state.daily_pnl_usdc` drops below `-max_daily_loss_usdc` (configured in `config/config.yaml` under `risk.max_daily_loss_usdc`), all entry signals are automatically rejected. Exit signals (close) are still processed.

To reset the breaker manually: POST `/system/breaker/reset` or use the admin UI button.

The breaker resets automatically at UTC midnight.

### Background Reconciler

A goroutine polls open orders every `reconciler.interval_seconds` (default 30s) and syncs fill status from the exchange. On startup, a recovery pass closes any orphaned open orders.

---

## Tests

```bash
# Unit tests (no external deps)
go test -race ./...

# Integration tests (requires Postgres — uses dockertest, spins up its own container)
go test -tags=integration -race ./...

# Live testnet smoke test (requires Binance testnet keys)
BINANCE_API_KEY=... BINANCE_API_SECRET=... \
  go test -tags=integration_binance ./internal/infrastructure/binance/...
```

CI runs unit + integration tests on every push via GitHub Actions (`.github/workflows/ci.yml`).

---

## Configuration Reference

### Environment variables (`.env`)

| Variable | Required | Default | Description |
|---|---|---|---|
| `BOT_MODE` | yes | — | `dry_run`, `testnet`, or `live` |
| `DATABASE_URL` | yes | — | PostgreSQL connection string |
| `WEBHOOK_SECRET` | yes | — | HMAC secret shared with TradingView |
| `SESSION_SECRET` | yes | — | 32+ char secret for admin session cookies |
| `HTTP_LISTEN` | no | `0.0.0.0:8080` | TCP listen address |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn`, `error` |
| `BINANCE_API_KEY` | live/testnet | — | Binance API key |
| `BINANCE_API_SECRET` | live/testnet | — | Binance API secret |

### YAML (`config/config.yaml`)

| Key | Default | Description |
|---|---|---|
| `risk.max_total_leverage` | `3.0` | Rejects signals that would push aggregate leverage above this multiple |
| `risk.max_daily_loss_usdc` | `500` | Daily-loss breaker threshold (USDC) |
| `ip_whitelist` | TradingView IPs + 127.0.0.1 | CIDR/IP list for `/webhook/tv`; empty = allow all |
| `reconciler.interval_seconds` | `30` | Fill-reconciliation polling interval |

---

## Deployment

### Docker Compose

```bash
cp .env.example .env
$EDITOR .env   # configure all secrets

docker compose up -d postgres
make migrate-up         # apply DB schema
docker compose up -d bot
```

### Bare metal (systemd)

```bash
make build
sudo cp bin/tvbot /usr/local/bin/tvbot
# create /etc/tvbot.env with all env vars
sudo systemctl enable --now tvbot
```

### HTTPS (required by TradingView)

TradingView will only POST to HTTPS endpoints. Options:

- **Cloudflare Tunnel** (`cloudflared`) — zero-cost, no public IP required. The bot listens on `localhost:8080`; cloudflared exposes it via your domain.
- **Caddy** — add `reverse_proxy localhost:8080` in your Caddyfile; Caddy handles Let's Encrypt automatically.
- **Cloud load balancer** — terminate TLS at the LB, forward HTTP to the bot.

When running behind a loopback tunnel (cloudflared), `X-Forwarded-For` is trusted for IP extraction because `RemoteAddr` will be `127.0.0.1`.

---

## Architecture (Deep Dive)

See the full design document:
[`docs/superpowers/specs/2026-05-05-tradingview-webhook-bot-design.md`](docs/superpowers/specs/2026-05-05-tradingview-webhook-bot-design.md)

---

## License

MIT — see [LICENSE](LICENSE).
