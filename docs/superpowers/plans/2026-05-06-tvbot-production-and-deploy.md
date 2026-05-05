# TVBot Production & Deploy Plan (Plan 4 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development.

**Goal:** Take the dry_run-only bot of Plan 3 to a production-ready state. Add real Binance USDT/USDC-M perp integration (testnet + live), order reconciliation loop, startup crash-recovery, IP whitelist HTTP middleware, Dockerfile + multi-stage build, CI workflow, and a README that gets a teammate from `git clone` to running bot in under 5 minutes.

**Architecture:** Adds 3 background workers (Reconciler, DailyPnLResetter, StartupRecovery — the latter runs once at boot). Adds `BinanceTrader` adapter implementing `internal/trade.Trader`. Wires IP whitelist middleware on `/webhook/tv`. No new domain types; this plan is mostly infrastructure + ops.

**Tech Stack:** adshao/go-binance/v2, github.com/cenkalti/backoff/v4 (retries), goreleaser-style Dockerfile.

**Spec:** `docs/superpowers/specs/2026-05-05-tradingview-webhook-bot-design.md`
**Plans 1-3 must be complete.**

---

## Tasks Summary

| # | Task | Estimated effort |
|---|------|------------------|
| 1 | BinanceTrader (testnet + live) | M |
| 2 | OrderReconciler background loop | M |
| 3 | DailyPnLResetter background loop | S |
| 4 | StartupRecovery (boot reconciliation) | M |
| 5 | IP whitelist middleware | S |
| 6 | Dockerfile (multi-stage) + docker-compose with bot service | S |
| 7 | GitHub Actions CI | S |
| 8 | README with quickstart | S |

---

## Task 1: BinanceTrader

**Files:**
- Create: `internal/infrastructure/binance/trader.go`
- Create: `internal/infrastructure/binance/trader_test.go` (testnet integration test, gated by env var)
- Modify: `cmd/tvbot/main.go` (mode-based factory: dry_run/testnet/live)

The `BinanceTrader` implements `internal/trade.Trader`. It wraps `github.com/adshao/go-binance/v2/futures` for the USDT-M perpetual futures API.

### Public API

```go
type BinanceTrader struct {
    client      *futures.Client
    isTestnet   bool
    log         zerolog.Logger
    timeoutMs   int
}

func NewBinanceTrader(cfg config.BinanceConfig, apiKey, apiSecret string, mode config.BotMode, log zerolog.Logger) *BinanceTrader
func (t *BinanceTrader) Place(ctx context.Context, req trade.OrderRequest) (*trade.OrderResult, error)
func (t *BinanceTrader) Cancel(ctx context.Context, symbol, clientOrderID string) error
```

### Place semantics

Map `trade.OrderRequest.Type` to Binance:

| trade.OrderType         | Binance OrderType    | Notes |
|-------------------------|----------------------|-------|
| `MARKET`                | `MARKET`             | NewOrderRespType=RESULT for sync fill info |
| `STOP`                  | `STOP`               | requires `price` (limit) + `stopPrice` (trigger) |
| `STOP_MARKET`           | `STOP_MARKET`        | requires `stopPrice` only |
| `TAKE_PROFIT_MARKET`    | `TAKE_PROFIT_MARKET` | requires `stopPrice` only |

For MARKET: read `executedQty` and `avgPrice` from response; map status to `filled`.

For STOP / STOP_MARKET / TAKE_PROFIT_MARKET: response will be `NEW`; map to `submitted`.

`reduceOnly`: `true` for purposes `stop`, `backup_stop`, `take_profit`, `exit` (anything closing a position). Implementer MUST plumb purpose through OrderRequest. **Add `Purpose` field to `trade.OrderRequest`** and pass it from application/trade Service.

### Quantity step rounding

Before placing, fetch exchange info once at startup (cache `LotSizeFilter` per symbol). Floor quantity to the symbol's step size. **Replace** the current hardcoded `0.001` step in application/trade.Service with a lookup from BinanceTrader (or pass step via config).

For Plan 4 simplicity: **add `(t *BinanceTrader) StepSize(symbol string) (decimal.Decimal, error)`** and have application/trade.Service read it on first OpenPosition for that symbol (cached).

### Test

Integration test that requires `BINANCE_TESTNET_KEY` and `BINANCE_TESTNET_SECRET` env vars; skips otherwise. Places a tiny test order ($1 worth), waits for fill, cancels.

```go
//go:build integration

package binance

import (
    "os"
    "testing"
    // ...
)

func TestBinanceTrader_TestnetPlaceMarket(t *testing.T) {
    if os.Getenv("BINANCE_TESTNET_KEY") == "" {
        t.Skip("set BINANCE_TESTNET_KEY + BINANCE_TESTNET_SECRET to run")
    }
    // ... place 0.001 BTC market buy on BTCUSDT testnet
}
```

### Steps

- [ ] **Step 1**: Add `Purpose` field to `internal/trade.OrderRequest`. Update application/trade callers to pass it.
- [ ] **Step 2**: Implement BinanceTrader (REST only — websocket is V2)
- [ ] **Step 3**: Mode factory in main.go: switch on cfg.BotMode → DryRun / Binance(testnet) / Binance(live)
- [ ] **Step 4**: Manual test on testnet (skipped automatically without keys)
- [ ] **Step 5**: Commit

```bash
git commit -m "feat(infrastructure/binance): BinanceTrader for testnet + live perpetual futures"
```

---

## Task 2: OrderReconciler

**Files:**
- Create: `internal/application/reconcile/reconciler.go`
- Create: `internal/application/reconcile/reconciler_test.go`

Background loop. Every `cfg.Reconciler.IntervalSeconds` (default 30s):
1. Query `orders` where `status IN ('pending','submitted','partial')`
2. For each, ask `Trader.GetOrder(symbol, clientOrderID)` → real status
3. If real status differs from DB → update DB row
4. If a stop/take_profit was filled (real status = filled) → trigger close-flow on the corresponding virtual_position
5. If a protective order was canceled by exchange unexpectedly → log + alert

### Trader interface extension

Add to `internal/trade.Trader`:

```go
GetOrder(ctx context.Context, symbol, clientOrderID string) (*OrderResult, error)
```

For DryRunTrader: return the order in its last-known state (track in a map). For BinanceTrader: call futures.GetOrderService.

### Steps

- [ ] **Step 1**: Add `GetOrder` to Trader port
- [ ] **Step 2**: Implement on DryRunTrader (in-memory map) + BinanceTrader
- [ ] **Step 3**: Implement reconciler service with `Run(ctx)` that ticks every interval
- [ ] **Step 4**: Wire in main.go: launch goroutine
- [ ] **Step 5**: Tests with stub Trader
- [ ] **Step 6**: Commit `feat(application/reconcile): order status sync loop with stop-fill trigger`

---

## Task 3: DailyPnLResetter

**Files:**
- Create: `internal/application/reconcile/daily_resetter.go`

Tiny worker that runs once per minute (or so) and checks `system_state.daily_pnl_date`. If `< CURRENT_DATE` (UTC), zero out `daily_pnl_usdc` via `repo.AddDailyPnL(ctx, q, 0, now)` (which has the rollover logic baked in).

Actually: `SystemStateRepo.AddDailyPnL` already does rollover automatically when called. So this resetter just calls `AddDailyPnL(0, now)` periodically — no-op if same day, resets if new day.

Could also just be triggered organically by trade activity. Two options:
A. Always call AddDailyPnL(0, now) on each trade close — already works
B. Background ticker for guaranteed daily reset even if no trades

For MVP, A is sufficient (the rollover happens lazily when next trade lands). Skip a dedicated background worker. **Document this decision** in the reconciler package doc.

### Steps

- [ ] **Step 1**: Add note in package doc explaining lazy rollover
- [ ] **Step 2**: Verify by reading existing AddDailyPnL — it already handles date check
- [ ] **Step 3**: No code change needed; just commit a small doc-only update or skip task entirely

---

## Task 4: StartupRecovery

**Files:**
- Create: `internal/application/reconcile/startup_recovery.go`
- Create: `internal/application/reconcile/startup_recovery_test.go`

Runs once at process start, before HTTP server begins accepting requests. For each `virtual_positions` row where `status IN ('opening','open','closing')`:

1. Query Binance: real position size for `(symbol)` from `positionRisk` API
2. Query Binance: open orders for `(symbol)` filtered by `clientOrderId IN (entry_order, stop_order, backup_stop, take_profit)` from DB
3. Reconcile:
   - If real position qty == DB qty AND all 3 protective orders open → status='open' (no change)
   - If real position qty == DB qty AND missing protective order(s) → place the missing one(s); high-priority alert
   - If real position qty != DB qty → flag the row, mark system_state armed=false, alert (manual intervention required)
   - If real position qty == 0 (no position) AND DB says active → assume stop fired offline; calculate PnL from last fill data; mark closed
4. Always: at end of recovery, ensure `system_state.armed=false` (forces operator confirmation before resuming)

### Steps

- [ ] **Step 1**: Add `GetPositionRisk(ctx, symbol)` to Trader port (returns qty, side, entry price)
- [ ] **Step 2**: Implement on BinanceTrader; DryRunTrader returns zero qty
- [ ] **Step 3**: Implement startup_recovery.go with the 4-case decision tree
- [ ] **Step 4**: Wire in main.go before `srv.Start(ctx)` — fail-fast if recovery errors
- [ ] **Step 5**: Tests with stub Trader covering each of the 4 cases
- [ ] **Step 6**: Commit `feat(application/reconcile): startup recovery with position reconciliation`

---

## Task 5: IP whitelist middleware

**Files:**
- Create: `internal/web/middleware/ip_whitelist.go`
- Create: `internal/web/middleware/ip_whitelist_test.go`
- Modify: `cmd/tvbot/main.go` (apply middleware to /webhook/tv)

Wraps `risk.IPWhitelistRule.Check` as HTTP middleware. Returns 403 + JSON for blocked IPs. Reads client IP via the same logic as the webhook handler.

```go
func IPWhitelist(rule *risk.IPWhitelistRule) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ip := webhook.ClientIP(r) // export from webhook package or duplicate here
            dec, _ := rule.Check(r.Context(), risk.Input{ClientIP: ip})
            if !dec.Allowed {
                http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

Apply only to `/webhook/tv`:

```go
r.Route("/webhook", func(r chi.Router) {
    r.Use(middleware.IPWhitelist(ipRule))
    r.Post("/tv", webhookHandler.Post)
})
```

### Steps

- [ ] **Step 1**: Export `ClientIP` from `internal/web/webhook/handler.go` (rename `clientIP` → `ClientIP`)
- [ ] **Step 2**: Implement middleware
- [ ] **Step 3**: Wire in main.go
- [ ] **Step 4**: Unit tests
- [ ] **Step 5**: Commit `feat(web/middleware): IP whitelist enforcement on /webhook/tv`

---

## Task 6: Dockerfile + docker-compose

**Files:**
- Create: `Dockerfile`
- Modify: `docker-compose.yml` (add `bot` service)

Multi-stage build. Builder uses `golang:1.26-alpine`, copies source, runs `go build -trimpath -ldflags="-s -w" -o tvbot ./cmd/tvbot`. Runtime uses `alpine:3.20` with just the binary, ca-certificates, and tzdata.

```dockerfile
FROM golang:1.26-alpine AS builder
RUN apk add --no-cache build-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/tvbot ./cmd/tvbot

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/tvbot /app/tvbot
COPY migrations /app/migrations
COPY config/config.yaml.example /app/config/config.yaml.example
EXPOSE 8080
ENV TZ=UTC
ENTRYPOINT ["/app/tvbot"]
```

`docker-compose.yml` — add bot service:

```yaml
services:
  postgres:
    # ... (unchanged)
  bot:
    build: .
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      BOT_MODE: ${BOT_MODE:-dry_run}
      DATABASE_URL: postgres://tvbot:tvbot@postgres:5432/tvbot?sslmode=disable
      WEBHOOK_SECRET: ${WEBHOOK_SECRET:-please-change-me}
      SESSION_SECRET: ${SESSION_SECRET:-please-change-me-please-change-me}
      HTTP_LISTEN: 0.0.0.0:8080
      LOG_LEVEL: info
      BINANCE_API_KEY: ${BINANCE_API_KEY:-}
      BINANCE_API_SECRET: ${BINANCE_API_SECRET:-}
    ports: ["8080:8080"]
    volumes:
      - ./config:/app/config:ro
    restart: unless-stopped
```

### Steps

- [ ] **Step 1**: Write Dockerfile
- [ ] **Step 2**: `docker build -t tvbot .` succeeds
- [ ] **Step 3**: Update docker-compose.yml
- [ ] **Step 4**: `docker compose up -d` brings bot + postgres up; healthz responds
- [ ] **Step 5**: Commit `feat(deploy): Dockerfile + docker-compose with bot service`

---

## Task 7: GitHub Actions CI

**Files:**
- Create: `.github/workflows/ci.yml`

Two jobs: test (with postgres service container) + build (verifies docker build).

```yaml
name: CI
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    services:
      postgres:
        image: postgres:16-alpine
        env:
          POSTGRES_USER: tvbot
          POSTGRES_PASSWORD: tvbot
          POSTGRES_DB: tvbot
        ports: ["5432:5432"]
        options: >-
          --health-cmd "pg_isready -U tvbot"
          --health-interval 5s --health-timeout 5s --health-retries 10
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.26' }
      - run: go vet ./...
      - run: go test -race ./...
      - name: integration tests
        env:
          TEST_DATABASE_URL: postgres://tvbot:tvbot@localhost:5432/tvbot?sslmode=disable
        run: go test -tags=integration -race ./...

  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: docker build -t tvbot:ci .
```

### Steps

- [ ] **Step 1**: Write workflow file
- [ ] **Step 2**: Push branch, observe Actions tab
- [ ] **Step 3**: Iterate if any failure
- [ ] **Step 4**: Commit `ci: add GitHub Actions test + build workflow`

---

## Task 8: README

**Files:**
- Create: `README.md`

Sections:
1. **What it is** — TradingView webhook → Binance perp auto-trader, single binary, ~3000 LOC Go
2. **Requirements** — Go 1.23+, Docker (or Postgres 16+ locally)
3. **Quick start (5 min)** — clone, copy env example, `make pg-up && make migrate-up && make build && ./bin/tvbot seed-user && ./bin/tvbot`
4. **Configuration** — env vars + config.yaml with examples
5. **TradingView setup** — alert message template
6. **Running modes** — dry_run vs testnet vs live; warning about armed state
7. **Architecture** — link to spec doc + brief module overview
8. **Tests** — unit vs integration; how to run
9. **Deployment** — docker-compose for self-hosted; Cloudflare Tunnel for HTTPS exposure
10. **License** — MIT or whatever the user prefers

### Steps

- [ ] **Step 1**: Write README.md
- [ ] **Step 2**: Commit `docs: add README with quickstart and operations guide`

---

## Final Verification

```bash
go vet ./...
go test -race ./...
go test -tags=integration -race ./...
docker build -t tvbot:final .
docker compose up -d
sleep 10
curl http://localhost:8080/healthz   # ok
docker compose down
```

All green = Plan 4 done.

---

## Self-Review

- [ ] BinanceTrader satisfies `trade.Trader`?
- [ ] All Trader methods used by application code have implementations on both DryRun and Binance?
- [ ] Reconciler doesn't loop forever on transient errors (uses backoff)?
- [ ] Startup recovery FAILS LOUD on inconsistencies (doesn't silently armed-disarm and forget)?
- [ ] IP whitelist middleware doesn't double-extract IP (uses same `ClientIP` function as webhook handler)?
- [ ] Dockerfile produces a binary <30MB?
- [ ] CI runs test + build in <5 min?
- [ ] README enables a fresh dev to get the bot running in <5 min from `git clone`?

---

**EOF**
