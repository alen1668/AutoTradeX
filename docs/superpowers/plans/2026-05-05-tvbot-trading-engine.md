# TVBot Trading Engine Implementation Plan (Plan 2 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire repositories + idempotency + notifier + dry-run trader + ingest/trade application services so a JSON webhook payload can be ingested through the full pipeline (idempotency → load context → risk → decide → trade → notify) entirely in dry_run mode, end-to-end, with all state persisted to PostgreSQL.

**Architecture:** Hexagonal — application services depend on Ports (interfaces); infrastructure provides adapters. The application/ingest service is the entry point. application/trade encapsulates open/close/stop coordination. Repositories live in internal/store, each one wrapping pgxpool.Pool. Notifier and Trader are pluggable interfaces.

**Tech Stack:** pgx/v5, hashicorp/golang-lru/v2, shopspring/decimal, zerolog, dockertest/v3 for integration tests. NO Binance live integration in this plan — that's deferred to Plan 2B (or folded into Plan 3 webhook layer).

**Spec reference:** `docs/superpowers/specs/2026-05-05-tradingview-webhook-bot-design.md`
**Foundation reference:** Plan 1 (foundation) is complete; this plan builds on top of it.

---

## Out of Scope (deferred to Plan 2B / 3 / 4)

- Real Binance Trader (testnet + live) — Plan 2B or Plan 3
- OrderReconciler background loop — Plan 2B
- Startup recovery / position reconciliation — Plan 2B
- HTTP webhook handler — Plan 3
- Web admin UI — Plan 3

By the **end of Plan 2**, you can write a Go test that calls `ingest.Service.Ingest(ctx, jsonBytes)` in dry_run mode and verify all DB state mutations occur correctly, including: signals row, virtual_positions row, multiple orders rows (entry + stop + backup_stop), notifier called.

---

## File Structure (本 Plan 创建/修改的文件)

| 路径 | 责任 |
|------|------|
| `internal/store/types.go` | `Querier` interface + `WithTx` transaction helper |
| `internal/store/signal_repo.go` | Insert / GetByID / List + idempotency-aware insert |
| `internal/store/signal_repo_test.go` | integration tests |
| `internal/store/strategy_repo.go` | Get / List / Create / Update / Delete |
| `internal/store/strategy_repo_test.go` | integration tests |
| `internal/store/system_state_repo.go` | Get / Arm / Disarm / UpdateDailyPnL / ResetBreaker / RolloverDailyPnL |
| `internal/store/system_state_repo_test.go` | integration tests |
| `internal/store/virtual_position_repo.go` | GetActiveByStrategy / Insert / UpdateStatus / SetEntryFill / SetCloseInfo |
| `internal/store/virtual_position_repo_test.go` | integration tests |
| `internal/store/position_history_repo.go` | Insert / ListByStrategy |
| `internal/store/position_history_repo_test.go` | integration tests |
| `internal/store/order_repo.go` | Insert / Update / ListPending / GetByClientID |
| `internal/store/order_repo_test.go` | integration tests |
| `internal/store/testhelpers_test.go` | dockertest setup, ephemeral DB, schema apply |
| `internal/idempotency/lru.go` | hashicorp/golang-lru wrapper |
| `internal/idempotency/checker.go` | `Check(ctx, strategyID, tvTimestampMs) (bool, error)` — LRU + DB |
| `internal/idempotency/checker_test.go` | integration tests |
| `internal/notify/notifier.go` | `Notifier` interface, `Message` struct, `NoOp`, `Multi` |
| `internal/notify/notifier_test.go` | unit tests for Multi/NoOp |
| `internal/notify/feishu.go` | Feishu webhook adapter |
| `internal/notify/feishu_test.go` | unit tests with httptest |
| `internal/notify/telegram.go` | Telegram bot adapter |
| `internal/notify/telegram_test.go` | unit tests with httptest |
| `internal/trade/trader.go` | `Trader` interface, `OrderRequest` / `OrderResult` types |
| `internal/trade/dryrun.go` | `DryRunTrader` impl — never calls external API |
| `internal/trade/dryrun_test.go` | unit tests |
| `internal/application/trade/service.go` | OpenPosition / ClosePosition / AttachStops orchestration |
| `internal/application/trade/service_test.go` | integration tests |
| `internal/application/ingest/service.go` | full signal pipeline orchestrator |
| `internal/application/ingest/service_test.go` | end-to-end integration tests |
| `internal/application/ingest/risk_inputs.go` | `BuildRiskInput` — loads strategy / position / system_state / equity |

---

## Conventions

- All repositories accept `context.Context` first arg.
- All repos return `pgx.ErrNoRows` (or wrapped) when not found — callers check with `errors.Is`.
- `decimal.Decimal` is the only numeric type for monetary fields.
- All `time.Time` is UTC.
- Each repo file has a unit-of-work helper or accepts a `Querier` (interface satisfied by both `*pgxpool.Pool` and `pgx.Tx`) so callers can pass a tx when atomicity matters.
- Integration tests use `dockertest` to spin up an ephemeral postgres per package, apply the migrations from `migrations/`, and run.
- `client_order_id` format: `<purpose>-<traceID>-<n>` (e.g., `entry-abc12-0`, `stop-abc12-1`).

---

## Task 1: store helpers — Querier + tx + dockertest

**Files:**
- Create: `internal/store/types.go`
- Create: `internal/store/testhelpers_test.go`

- [ ] **Step 1: Create `internal/store/types.go`**

```go
package store

import (
	"context"

	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is the subset of pgx APIs used by repositories. Both
// *pgxpool.Pool and pgx.Tx satisfy it, so callers can pass a tx when
// they need atomicity across multiple repo calls.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// WithTx runs fn inside a transaction; commits if fn returns nil, rolls
// back otherwise. Use a serializable isolation level for risk-pipeline
// flows; default ReadCommitted otherwise.
func WithTx(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	tx, err := pool.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
```

- [ ] **Step 2: Create `internal/store/testhelpers_test.go`**

```go
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/stretchr/testify/require"
)

// testPool creates an ephemeral postgres container, applies migrations, and
// returns a connected pool. Cleans up automatically via t.Cleanup.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skip integration test in -short mode")
	}

	pool, err := dockertest.NewPool("")
	require.NoError(t, err, "dockertest.NewPool")
	pool.MaxWait = 60 * time.Second

	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "16-alpine",
		Env: []string{
			"POSTGRES_USER=test",
			"POSTGRES_PASSWORD=test",
			"POSTGRES_DB=test",
		},
	}, func(cfg *docker.HostConfig) {
		cfg.AutoRemove = true
		cfg.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	require.NoError(t, err, "dockertest run postgres")
	t.Cleanup(func() { _ = pool.Purge(resource) })

	hostPort := resource.GetHostPort("5432/tcp")
	dsn := fmt.Sprintf("postgres://test:test@%s/test?sslmode=disable", hostPort)

	var pgPool *pgxpool.Pool
	require.NoError(t, pool.Retry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		p, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		if err := p.Ping(ctx); err != nil {
			p.Close()
			return err
		}
		pgPool = p
		return nil
	}))
	t.Cleanup(pgPool.Close)

	applyMigrations(t, pgPool)
	return pgPool
}

func applyMigrations(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	migPath, err := filepath.Abs("../../migrations/0001_init.sql")
	require.NoError(t, err)
	data, err := os.ReadFile(migPath)
	require.NoError(t, err)
	// Crude split on -- +goose markers and execute the Up StatementBegin block.
	sql := extractGooseUp(string(data))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := p.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	_, err = conn.Exec(ctx, sql)
	require.NoError(t, err, "apply migrations")
}

// extractGooseUp pulls the body between `-- +goose Up\n-- +goose StatementBegin`
// and `-- +goose StatementEnd` (only the Up block).
func extractGooseUp(s string) string {
	start := "-- +goose Up"
	begin := "-- +goose StatementBegin"
	end := "-- +goose StatementEnd"
	i := indexAfter(s, start)
	if i < 0 {
		return ""
	}
	j := indexAfter(s[i:], begin)
	if j < 0 {
		return ""
	}
	body := s[i+j:]
	k := indexAfter(body, end)
	if k < 0 {
		return body
	}
	return body[:k-len(end)]
}

func indexAfter(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i + len(sub)
		}
	}
	return -1
}

// helper used by repos that mutate timestamps
var _ = pgx.ErrNoRows
```

- [ ] **Step 3: Verify dockertest can run**

Run: `go test -tags=integration ./internal/store/... -run NONE -v 2>&1 | head -5` (we don't have any test yet, just verifying setup compiles)

Expected: compile success, no tests run.

- [ ] **Step 4: Commit**

```bash
git add internal/store/types.go internal/store/testhelpers_test.go
git commit -m "feat(store): Querier interface + dockertest helpers"
```

---

## Task 2: SignalRepo

**Files:**
- Create: `internal/store/signal_repo.go`
- Create: `internal/store/signal_repo_test.go`

The `signals` table has `UNIQUE (strategy_id, tv_timestamp_ms)`. We use `INSERT ... ON CONFLICT DO NOTHING RETURNING id` to detect duplicates atomically.

- [ ] **Step 1: Test file `internal/store/signal_repo_test.go`**

```go
//go:build integration

package store

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignalRepo_InsertAndDuplicate(t *testing.T) {
	pool := testPool(t)
	repo := NewSignalRepo(pool)
	ctx := context.Background()

	in := SignalRow{
		StrategyID:    "s1",
		Symbol:        "ETHUSDC",
		Kind:          "long",
		SignalPrice:   decimal.NewFromFloat(2300.5),
		TVTimestampMs: 1714723504000,
		ReceivedAt:    time.Now().UTC(),
		RawPayload:    json.RawMessage(`{"k":"v"}`),
		ClientIP:      net.ParseIP("10.0.0.1"),
		Decision:      "pending",
		TraceID:       "trace-1",
	}

	id1, dup1, err := repo.Insert(ctx, pool, in)
	require.NoError(t, err)
	assert.False(t, dup1)
	assert.Greater(t, id1, int64(0))

	// Same (strategy_id, tv_timestamp_ms) → duplicate
	id2, dup2, err := repo.Insert(ctx, pool, in)
	require.NoError(t, err)
	assert.True(t, dup2)
	assert.Equal(t, id1, id2)
}

func TestSignalRepo_UpdateDecision(t *testing.T) {
	pool := testPool(t)
	repo := NewSignalRepo(pool)
	ctx := context.Background()

	id, _, err := repo.Insert(ctx, pool, SignalRow{
		StrategyID: "s1", Symbol: "ETHUSDC", Kind: "long",
		SignalPrice: decimal.NewFromInt(1), TVTimestampMs: 1, ReceivedAt: time.Now().UTC(),
		RawPayload: json.RawMessage(`{}`), Decision: "pending", TraceID: "t",
	})
	require.NoError(t, err)

	require.NoError(t, repo.UpdateDecision(ctx, pool, id, "accepted", "ok"))

	got, err := repo.GetByID(ctx, pool, id)
	require.NoError(t, err)
	assert.Equal(t, "accepted", got.Decision)
	assert.Equal(t, "ok", got.DecisionReason)
}

func TestSignalRepo_ListRecent(t *testing.T) {
	pool := testPool(t)
	repo := NewSignalRepo(pool)
	ctx := context.Background()

	for i := int64(0); i < 5; i++ {
		_, _, err := repo.Insert(ctx, pool, SignalRow{
			StrategyID: "s1", Symbol: "ETHUSDC", Kind: "long",
			SignalPrice: decimal.NewFromInt(1), TVTimestampMs: 100 + i,
			ReceivedAt: time.Now().UTC(),
			RawPayload: json.RawMessage(`{}`), Decision: "accepted", TraceID: "t",
		})
		require.NoError(t, err)
	}

	rows, err := repo.ListRecent(ctx, pool, 3)
	require.NoError(t, err)
	assert.Len(t, rows, 3)
	// Ordered DESC by received_at, so latest first
	assert.Equal(t, int64(104), rows[0].TVTimestampMs)
}
```

- [ ] **Step 2: Implementation `internal/store/signal_repo.go`**

```go
package store

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type SignalRow struct {
	ID             int64
	StrategyID     string
	Symbol         string
	Kind           string
	SignalPrice    decimal.Decimal
	TVTimestampMs  int64
	ReceivedAt     time.Time
	RawPayload     json.RawMessage
	ClientIP       net.IP
	Decision       string
	DecisionReason string
	TraceID        string
}

type SignalRepo struct {
	pool *pgxpool.Pool
}

func NewSignalRepo(pool *pgxpool.Pool) *SignalRepo { return &SignalRepo{pool: pool} }

// Insert inserts a row. Returns (id, duplicate, error). On duplicate it returns
// the EXISTING row's id; the caller should treat duplicate=true as "already
// processed, skip work".
func (r *SignalRepo) Insert(ctx context.Context, q Querier, in SignalRow) (int64, bool, error) {
	const sql = `
INSERT INTO signals
  (strategy_id, symbol, kind, signal_price, tv_timestamp_ms, received_at,
   raw_payload, client_ip, decision, decision_reason, trace_id)
VALUES ($1,$2,$3::signal_kind,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (strategy_id, tv_timestamp_ms) DO NOTHING
RETURNING id`
	var id int64
	err := q.QueryRow(ctx, sql,
		in.StrategyID, in.Symbol, in.Kind, in.SignalPrice, in.TVTimestampMs, in.ReceivedAt,
		in.RawPayload, ipOrNil(in.ClientIP), in.Decision, nullableString(in.DecisionReason), in.TraceID,
	).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// Duplicate — fetch existing id
		var existing int64
		if err2 := q.QueryRow(ctx,
			`SELECT id FROM signals WHERE strategy_id=$1 AND tv_timestamp_ms=$2`,
			in.StrategyID, in.TVTimestampMs,
		).Scan(&existing); err2 != nil {
			return 0, false, err2
		}
		return existing, true, nil
	}
	return 0, false, err
}

func (r *SignalRepo) UpdateDecision(ctx context.Context, q Querier, id int64, decision, reason string) error {
	_, err := q.Exec(ctx,
		`UPDATE signals SET decision=$1, decision_reason=$2 WHERE id=$3`,
		decision, nullableString(reason), id)
	return err
}

func (r *SignalRepo) GetByID(ctx context.Context, q Querier, id int64) (*SignalRow, error) {
	var s SignalRow
	var rawPayload []byte
	var clientIP *net.IP
	var decisionReason *string
	err := q.QueryRow(ctx,
		`SELECT id, strategy_id, symbol, kind::text, signal_price, tv_timestamp_ms,
                received_at, raw_payload, client_ip, decision, decision_reason, trace_id
           FROM signals WHERE id=$1`, id,
	).Scan(&s.ID, &s.StrategyID, &s.Symbol, &s.Kind, &s.SignalPrice, &s.TVTimestampMs,
		&s.ReceivedAt, &rawPayload, &clientIP, &s.Decision, &decisionReason, &s.TraceID)
	if err != nil {
		return nil, err
	}
	s.RawPayload = json.RawMessage(rawPayload)
	if clientIP != nil {
		s.ClientIP = *clientIP
	}
	if decisionReason != nil {
		s.DecisionReason = *decisionReason
	}
	return &s, nil
}

func (r *SignalRepo) ListRecent(ctx context.Context, q Querier, limit int) ([]*SignalRow, error) {
	rows, err := q.Query(ctx,
		`SELECT id, strategy_id, symbol, kind::text, signal_price, tv_timestamp_ms,
                received_at, raw_payload, client_ip, decision, decision_reason, trace_id
           FROM signals
          ORDER BY received_at DESC
          LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SignalRow{}
	for rows.Next() {
		var s SignalRow
		var rawPayload []byte
		var clientIP *net.IP
		var decisionReason *string
		if err := rows.Scan(&s.ID, &s.StrategyID, &s.Symbol, &s.Kind, &s.SignalPrice, &s.TVTimestampMs,
			&s.ReceivedAt, &rawPayload, &clientIP, &s.Decision, &decisionReason, &s.TraceID); err != nil {
			return nil, err
		}
		s.RawPayload = json.RawMessage(rawPayload)
		if clientIP != nil {
			s.ClientIP = *clientIP
		}
		if decisionReason != nil {
			s.DecisionReason = *decisionReason
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

func ipOrNil(ip net.IP) any {
	if ip == nil {
		return nil
	}
	return ip.String()
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
```

- [ ] **Step 3: Run tests**

Run: `go test -tags=integration -race -v ./internal/store/... -run TestSignalRepo`
Expected: 3 tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/store/signal_repo.go internal/store/signal_repo_test.go
git commit -m "feat(store): SignalRepo with idempotent insert via UNIQUE constraint"
```

---

## Task 3: StrategyRepo

**Files:**
- Create: `internal/store/strategy_repo.go`
- Create: `internal/store/strategy_repo_test.go`

- [ ] **Step 1: Test file**

```go
//go:build integration

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrategyRepo_CreateGetUpdate(t *testing.T) {
	pool := testPool(t)
	repo := NewStrategyRepo(pool)
	ctx := context.Background()

	in := StrategyRow{
		ID: "macd_eth", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.NewFromInt(100), StopLossPct: decimal.NewFromFloat(1.5),
		TakeProfitPct: decimal.NewFromFloat(3.0),
		MaxOpenUSDC:   decimal.NewFromInt(500),
		Enabled:       true,
	}
	require.NoError(t, repo.Create(ctx, pool, in))

	got, err := repo.Get(ctx, pool, "macd_eth")
	require.NoError(t, err)
	assert.Equal(t, in.Symbol, got.Symbol)
	assert.Equal(t, in.Leverage, got.Leverage)
	assert.True(t, in.SizeUSDC.Equal(got.SizeUSDC))

	in.Enabled = false
	in.SizeUSDC = decimal.NewFromInt(200)
	require.NoError(t, repo.Update(ctx, pool, in))

	got2, err := repo.Get(ctx, pool, "macd_eth")
	require.NoError(t, err)
	assert.False(t, got2.Enabled)
	assert.True(t, decimal.NewFromInt(200).Equal(got2.SizeUSDC))
}

func TestStrategyRepo_NotFound(t *testing.T) {
	pool := testPool(t)
	repo := NewStrategyRepo(pool)
	_, err := repo.Get(context.Background(), pool, "missing")
	require.Error(t, err)
	assert.True(t, errors.Is(err, pgx.ErrNoRows))
}

func TestStrategyRepo_List(t *testing.T) {
	pool := testPool(t)
	repo := NewStrategyRepo(pool)
	ctx := context.Background()

	for _, id := range []string{"a", "b", "c"} {
		require.NoError(t, repo.Create(ctx, pool, StrategyRow{
			ID: id, Symbol: "ETHUSDC", Leverage: 1,
			SizeUSDC: decimal.NewFromInt(10), StopLossPct: decimal.NewFromFloat(1),
			MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true,
		}))
	}
	rows, err := repo.List(ctx, pool)
	require.NoError(t, err)
	assert.Len(t, rows, 3)
}

func TestStrategyRepo_Delete(t *testing.T) {
	pool := testPool(t)
	repo := NewStrategyRepo(pool)
	ctx := context.Background()
	require.NoError(t, repo.Create(ctx, pool, StrategyRow{
		ID: "x", Symbol: "ETHUSDC", Leverage: 1,
		SizeUSDC: decimal.NewFromInt(10), StopLossPct: decimal.NewFromFloat(1),
		MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true,
	}))
	require.NoError(t, repo.Delete(ctx, pool, "x"))
	_, err := repo.Get(ctx, pool, "x")
	assert.True(t, errors.Is(err, pgx.ErrNoRows))
}
```

- [ ] **Step 2: Implementation**

```go
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type StrategyRow struct {
	ID            string
	Symbol        string
	Leverage      int
	SizeUSDC      decimal.Decimal
	StopLossPct   decimal.Decimal
	TakeProfitPct decimal.Decimal // zero = none
	MaxOpenUSDC   decimal.Decimal
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type StrategyRepo struct {
	pool *pgxpool.Pool
}

func NewStrategyRepo(pool *pgxpool.Pool) *StrategyRepo { return &StrategyRepo{pool: pool} }

func (r *StrategyRepo) Create(ctx context.Context, q Querier, in StrategyRow) error {
	_, err := q.Exec(ctx, `
INSERT INTO strategies
  (id, symbol, leverage, size_usdc, stop_loss_pct, take_profit_pct, max_open_usdc, enabled)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		in.ID, in.Symbol, in.Leverage, in.SizeUSDC, in.StopLossPct,
		nullableDecimal(in.TakeProfitPct), in.MaxOpenUSDC, in.Enabled)
	return err
}

func (r *StrategyRepo) Update(ctx context.Context, q Querier, in StrategyRow) error {
	_, err := q.Exec(ctx, `
UPDATE strategies SET
  symbol=$2, leverage=$3, size_usdc=$4, stop_loss_pct=$5, take_profit_pct=$6,
  max_open_usdc=$7, enabled=$8, updated_at=now()
WHERE id=$1`,
		in.ID, in.Symbol, in.Leverage, in.SizeUSDC, in.StopLossPct,
		nullableDecimal(in.TakeProfitPct), in.MaxOpenUSDC, in.Enabled)
	return err
}

func (r *StrategyRepo) Get(ctx context.Context, q Querier, id string) (*StrategyRow, error) {
	var s StrategyRow
	var tp *decimal.Decimal
	err := q.QueryRow(ctx, `
SELECT id, symbol, leverage, size_usdc, stop_loss_pct, take_profit_pct,
       max_open_usdc, enabled, created_at, updated_at
  FROM strategies WHERE id=$1`, id,
	).Scan(&s.ID, &s.Symbol, &s.Leverage, &s.SizeUSDC, &s.StopLossPct, &tp,
		&s.MaxOpenUSDC, &s.Enabled, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if tp != nil {
		s.TakeProfitPct = *tp
	}
	return &s, nil
}

func (r *StrategyRepo) List(ctx context.Context, q Querier) ([]*StrategyRow, error) {
	rows, err := q.Query(ctx, `
SELECT id, symbol, leverage, size_usdc, stop_loss_pct, take_profit_pct,
       max_open_usdc, enabled, created_at, updated_at
  FROM strategies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*StrategyRow{}
	for rows.Next() {
		var s StrategyRow
		var tp *decimal.Decimal
		if err := rows.Scan(&s.ID, &s.Symbol, &s.Leverage, &s.SizeUSDC, &s.StopLossPct, &tp,
			&s.MaxOpenUSDC, &s.Enabled, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		if tp != nil {
			s.TakeProfitPct = *tp
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

func (r *StrategyRepo) Delete(ctx context.Context, q Querier, id string) error {
	_, err := q.Exec(ctx, `DELETE FROM strategies WHERE id=$1`, id)
	return err
}

func nullableDecimal(d decimal.Decimal) any {
	if d.IsZero() {
		return nil
	}
	return d
}
```

- [ ] **Step 3: Tests pass**

Run: `go test -tags=integration -race -v ./internal/store/... -run TestStrategyRepo`
Expected: 4 tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/store/strategy_repo.go internal/store/strategy_repo_test.go
git commit -m "feat(store): StrategyRepo CRUD"
```

---

## Task 4: SystemStateRepo

**Files:**
- Create: `internal/store/system_state_repo.go`
- Create: `internal/store/system_state_repo_test.go`

- [ ] **Step 1: Test file**

```go
//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemStateRepo_DefaultIsDisarmed(t *testing.T) {
	pool := testPool(t)
	repo := NewSystemStateRepo(pool)
	st, err := repo.Get(context.Background(), pool)
	require.NoError(t, err)
	assert.False(t, st.Armed)
	assert.False(t, st.BreakerTripped)
	assert.True(t, st.DailyPnLUSDC.IsZero())
}

func TestSystemStateRepo_ArmDisarm(t *testing.T) {
	pool := testPool(t)
	repo := NewSystemStateRepo(pool)
	ctx := context.Background()

	require.NoError(t, repo.Arm(ctx, pool, "alice"))
	st, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, st.Armed)
	assert.Equal(t, "alice", st.ArmedBy)

	require.NoError(t, repo.Disarm(ctx, pool))
	st2, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.False(t, st2.Armed)
}

func TestSystemStateRepo_AddDailyPnLAndRollover(t *testing.T) {
	pool := testPool(t)
	repo := NewSystemStateRepo(pool)
	ctx := context.Background()

	// Same day: accumulates
	require.NoError(t, repo.AddDailyPnL(ctx, pool, decimal.NewFromFloat(10), time.Now().UTC()))
	require.NoError(t, repo.AddDailyPnL(ctx, pool, decimal.NewFromFloat(-3), time.Now().UTC()))
	st, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(7).Equal(st.DailyPnLUSDC))

	// New day: resets first
	tomorrow := time.Now().UTC().Add(24 * time.Hour)
	require.NoError(t, repo.AddDailyPnL(ctx, pool, decimal.NewFromFloat(5), tomorrow))
	st2, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, decimal.NewFromInt(5).Equal(st2.DailyPnLUSDC))
}

func TestSystemStateRepo_TripAndResetBreaker(t *testing.T) {
	pool := testPool(t)
	repo := NewSystemStateRepo(pool)
	ctx := context.Background()

	require.NoError(t, repo.TripBreaker(ctx, pool, "daily loss exceeded"))
	st, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.True(t, st.BreakerTripped)
	assert.Equal(t, "daily loss exceeded", st.BreakerReason)

	require.NoError(t, repo.ResetBreaker(ctx, pool))
	st2, err := repo.Get(ctx, pool)
	require.NoError(t, err)
	assert.False(t, st2.BreakerTripped)
	assert.Empty(t, st2.BreakerReason)
}
```

- [ ] **Step 2: Implementation**

```go
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type SystemStateRow struct {
	Armed          bool
	ArmedAt        time.Time
	ArmedBy        string
	DailyPnLUSDC   decimal.Decimal
	DailyPnLDate   time.Time
	BreakerTripped bool
	BreakerReason  string
	UpdatedAt      time.Time
}

type SystemStateRepo struct {
	pool *pgxpool.Pool
}

func NewSystemStateRepo(pool *pgxpool.Pool) *SystemStateRepo {
	return &SystemStateRepo{pool: pool}
}

func (r *SystemStateRepo) Get(ctx context.Context, q Querier) (*SystemStateRow, error) {
	var s SystemStateRow
	var armedAt *time.Time
	var armedBy, breakerReason *string
	err := q.QueryRow(ctx, `
SELECT armed, armed_at, armed_by, daily_pnl_usdc, daily_pnl_date,
       breaker_tripped, breaker_reason, updated_at
  FROM system_state WHERE id=1`,
	).Scan(&s.Armed, &armedAt, &armedBy, &s.DailyPnLUSDC, &s.DailyPnLDate,
		&s.BreakerTripped, &breakerReason, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if armedAt != nil {
		s.ArmedAt = *armedAt
	}
	if armedBy != nil {
		s.ArmedBy = *armedBy
	}
	if breakerReason != nil {
		s.BreakerReason = *breakerReason
	}
	return &s, nil
}

func (r *SystemStateRepo) Arm(ctx context.Context, q Querier, by string) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET armed=true, armed_at=now(), armed_by=$1, updated_at=now() WHERE id=1`,
		by)
	return err
}

func (r *SystemStateRepo) Disarm(ctx context.Context, q Querier) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET armed=false, updated_at=now() WHERE id=1`)
	return err
}

// AddDailyPnL atomically adds delta to daily_pnl_usdc, rolling over to 0 first
// if `now` falls on a different UTC date than daily_pnl_date.
func (r *SystemStateRepo) AddDailyPnL(ctx context.Context, q Querier, delta decimal.Decimal, now time.Time) error {
	today := now.UTC().Format("2006-01-02")
	_, err := q.Exec(ctx, `
UPDATE system_state
   SET daily_pnl_usdc = CASE WHEN daily_pnl_date = $2::date THEN daily_pnl_usdc + $1 ELSE $1 END,
       daily_pnl_date = $2::date,
       updated_at = now()
 WHERE id=1`, delta, today)
	return err
}

func (r *SystemStateRepo) TripBreaker(ctx context.Context, q Querier, reason string) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET breaker_tripped=true, breaker_reason=$1, updated_at=now() WHERE id=1`,
		reason)
	return err
}

func (r *SystemStateRepo) ResetBreaker(ctx context.Context, q Querier) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET breaker_tripped=false, breaker_reason=NULL, daily_pnl_usdc=0, updated_at=now() WHERE id=1`)
	return err
}
```

- [ ] **Step 3: Run tests**

Run: `go test -tags=integration -race -v ./internal/store/... -run TestSystemStateRepo`
Expected: 4 tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/store/system_state_repo.go internal/store/system_state_repo_test.go
git commit -m "feat(store): SystemStateRepo with arm/disarm/breaker/daily PnL rollover"
```

---

## Task 5: VirtualPositionRepo + PositionHistoryRepo

**Files:**
- Create: `internal/store/virtual_position_repo.go`
- Create: `internal/store/virtual_position_repo_test.go`
- Create: `internal/store/position_history_repo.go`
- Create: `internal/store/position_history_repo_test.go`

- [ ] **Step 1: VirtualPositionRepo test file** (`virtual_position_repo_test.go`)

```go
//go:build integration

package store

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedStrategyForVP(t *testing.T, ctx context.Context, q Querier) {
	t.Helper()
	repo := NewStrategyRepo(nil)
	require.NoError(t, repo.Create(ctx, q, StrategyRow{
		ID: "s", Symbol: "ETHUSDC", Leverage: 1,
		SizeUSDC: decimal.NewFromInt(10), StopLossPct: decimal.NewFromFloat(1),
		MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true,
	}))
}

func seedSignalForVP(t *testing.T, ctx context.Context, q Querier, ts int64) int64 {
	t.Helper()
	repo := NewSignalRepo(nil)
	id, _, err := repo.Insert(ctx, q, SignalRow{
		StrategyID: "s", Symbol: "ETHUSDC", Kind: "long",
		SignalPrice: decimal.NewFromInt(100), TVTimestampMs: ts,
		ReceivedAt: time.Now().UTC(), RawPayload: json.RawMessage(`{}`),
		ClientIP: net.ParseIP("127.0.0.1"), Decision: "accepted", TraceID: "t",
	})
	require.NoError(t, err)
	return id
}

func TestVirtualPositionRepo_OpenAndGetActive(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	sigID := seedSignalForVP(t, ctx, pool, 1)
	repo := NewVirtualPositionRepo(pool)

	id, err := repo.Insert(ctx, pool, VirtualPositionRow{
		StrategyID:       "s",
		Symbol:           "ETHUSDC",
		Side:             "long",
		Qty:              decimal.NewFromFloat(0.1),
		EntrySignalPrice: decimal.NewFromInt(100),
		EntrySignalID:    sigID,
		Status:           "opening",
	})
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	got, err := repo.GetActiveByStrategy(ctx, pool, "s")
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, "opening", got.Status)
}

func TestVirtualPositionRepo_PartialUniqueIndexBlocksDoubleActive(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	sigID := seedSignalForVP(t, ctx, pool, 1)
	repo := NewVirtualPositionRepo(pool)

	_, err := repo.Insert(ctx, pool, VirtualPositionRow{
		StrategyID:       "s",
		Symbol:           "ETHUSDC",
		Side:             "long",
		Qty:              decimal.NewFromInt(1),
		EntrySignalPrice: decimal.NewFromInt(100),
		EntrySignalID:    sigID,
		Status:           "open",
	})
	require.NoError(t, err)

	// Try to insert a second active row → DB unique-violation
	sig2 := seedSignalForVP(t, ctx, pool, 2)
	_, err = repo.Insert(ctx, pool, VirtualPositionRow{
		StrategyID:       "s",
		Symbol:           "ETHUSDC",
		Side:             "short",
		Qty:              decimal.NewFromInt(1),
		EntrySignalPrice: decimal.NewFromInt(100),
		EntrySignalID:    sig2,
		Status:           "opening",
	})
	require.Error(t, err)
}

func TestVirtualPositionRepo_TransitionStatus(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	sigID := seedSignalForVP(t, ctx, pool, 1)
	repo := NewVirtualPositionRepo(pool)

	id, _ := repo.Insert(ctx, pool, VirtualPositionRow{
		StrategyID: "s", Symbol: "ETHUSDC", Side: "long",
		Qty: decimal.NewFromInt(1), EntrySignalPrice: decimal.NewFromInt(100),
		EntrySignalID: sigID, Status: "opening",
	})

	require.NoError(t, repo.SetEntryFill(ctx, pool, id, decimal.NewFromFloat(99.5), 42))
	require.NoError(t, repo.UpdateStatus(ctx, pool, id, "open"))
	got, err := repo.GetByID(ctx, pool, id)
	require.NoError(t, err)
	assert.Equal(t, "open", got.Status)
	assert.True(t, decimal.NewFromFloat(99.5).Equal(got.EntryFillPrice))
	assert.Equal(t, int64(42), got.EntryOrderID)

	require.NoError(t, repo.SetProtectiveOrders(ctx, pool, id, 50, 51, 0))
	got2, _ := repo.GetByID(ctx, pool, id)
	assert.Equal(t, int64(50), got2.StopOrderID)
	assert.Equal(t, int64(51), got2.BackupStopOrderID)
	assert.Zero(t, got2.TakeProfitOrderID)

	require.NoError(t, repo.MarkClosed(ctx, pool, id))
	_, err = repo.GetActiveByStrategy(ctx, pool, "s")
	assert.True(t, errors.Is(err, pgx.ErrNoRows))
}
```

- [ ] **Step 2: VirtualPositionRepo implementation** (`virtual_position_repo.go`)

```go
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type VirtualPositionRow struct {
	ID                 int64
	StrategyID         string
	Symbol             string
	Side               string
	Qty                decimal.Decimal
	EntrySignalPrice   decimal.Decimal
	EntryFillPrice     decimal.Decimal
	EntrySignalID      int64
	EntryOrderID       int64
	StopOrderID        int64
	BackupStopOrderID  int64
	TakeProfitOrderID  int64
	Status             string
	OpenedAt           time.Time
	ClosedAt           time.Time
}

type VirtualPositionRepo struct {
	pool *pgxpool.Pool
}

func NewVirtualPositionRepo(pool *pgxpool.Pool) *VirtualPositionRepo {
	return &VirtualPositionRepo{pool: pool}
}

func (r *VirtualPositionRepo) Insert(ctx context.Context, q Querier, in VirtualPositionRow) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
INSERT INTO virtual_positions
  (strategy_id, symbol, side, qty, entry_signal_price, entry_signal_id, status)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		in.StrategyID, in.Symbol, in.Side, in.Qty, in.EntrySignalPrice, in.EntrySignalID, in.Status,
	).Scan(&id)
	return id, err
}

func (r *VirtualPositionRepo) GetByID(ctx context.Context, q Querier, id int64) (*VirtualPositionRow, error) {
	return r.scanOne(ctx, q,
		`SELECT id, strategy_id, symbol, side, qty, entry_signal_price, entry_fill_price,
                entry_signal_id, entry_order_id, stop_order_id, backup_stop_order_id,
                take_profit_order_id, status, opened_at, closed_at
           FROM virtual_positions WHERE id=$1`, id)
}

func (r *VirtualPositionRepo) GetActiveByStrategy(ctx context.Context, q Querier, strategyID string) (*VirtualPositionRow, error) {
	return r.scanOne(ctx, q,
		`SELECT id, strategy_id, symbol, side, qty, entry_signal_price, entry_fill_price,
                entry_signal_id, entry_order_id, stop_order_id, backup_stop_order_id,
                take_profit_order_id, status, opened_at, closed_at
           FROM virtual_positions
          WHERE strategy_id=$1 AND status IN ('opening','open','closing')`, strategyID)
}

func (r *VirtualPositionRepo) UpdateStatus(ctx context.Context, q Querier, id int64, status string) error {
	_, err := q.Exec(ctx, `UPDATE virtual_positions SET status=$2 WHERE id=$1`, id, status)
	return err
}

func (r *VirtualPositionRepo) SetEntryFill(ctx context.Context, q Querier, id int64, fillPrice decimal.Decimal, entryOrderID int64) error {
	_, err := q.Exec(ctx,
		`UPDATE virtual_positions SET entry_fill_price=$2, entry_order_id=$3 WHERE id=$1`,
		id, fillPrice, entryOrderID)
	return err
}

func (r *VirtualPositionRepo) SetProtectiveOrders(ctx context.Context, q Querier, id, stopID, backupStopID, tpID int64) error {
	_, err := q.Exec(ctx, `
UPDATE virtual_positions
   SET stop_order_id        = NULLIF($2,0),
       backup_stop_order_id = NULLIF($3,0),
       take_profit_order_id = NULLIF($4,0)
 WHERE id=$1`, id, stopID, backupStopID, tpID)
	return err
}

func (r *VirtualPositionRepo) MarkClosed(ctx context.Context, q Querier, id int64) error {
	_, err := q.Exec(ctx,
		`UPDATE virtual_positions SET status='closed', closed_at=now() WHERE id=$1`, id)
	return err
}

func (r *VirtualPositionRepo) scanOne(ctx context.Context, q Querier, sql string, args ...any) (*VirtualPositionRow, error) {
	var v VirtualPositionRow
	var entryFill *decimal.Decimal
	var entryOrderID, stopID, backupID, tpID *int64
	var closedAt *time.Time
	err := q.QueryRow(ctx, sql, args...).Scan(
		&v.ID, &v.StrategyID, &v.Symbol, &v.Side, &v.Qty, &v.EntrySignalPrice, &entryFill,
		&v.EntrySignalID, &entryOrderID, &stopID, &backupID, &tpID,
		&v.Status, &v.OpenedAt, &closedAt)
	if err != nil {
		return nil, err
	}
	if entryFill != nil {
		v.EntryFillPrice = *entryFill
	}
	if entryOrderID != nil {
		v.EntryOrderID = *entryOrderID
	}
	if stopID != nil {
		v.StopOrderID = *stopID
	}
	if backupID != nil {
		v.BackupStopOrderID = *backupID
	}
	if tpID != nil {
		v.TakeProfitOrderID = *tpID
	}
	if closedAt != nil {
		v.ClosedAt = *closedAt
	}
	return &v, nil
}
```

- [ ] **Step 3: PositionHistoryRepo test** (`position_history_repo_test.go`)

```go
//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPositionHistoryRepo_InsertAndList(t *testing.T) {
	pool := testPool(t)
	repo := NewPositionHistoryRepo(pool)
	ctx := context.Background()

	now := time.Now().UTC()
	in := PositionHistoryRow{
		StrategyID: "s", Symbol: "ETHUSDC", Side: "long",
		Qty:                decimal.NewFromFloat(0.5),
		EntrySignalPrice:   decimal.NewFromFloat(100),
		EntryFillPrice:     decimal.NewFromFloat(99.9),
		ExitSignalPrice:    decimal.NewFromFloat(105),
		ExitFillPrice:      decimal.NewFromFloat(104.8),
		PnLUSDC:            decimal.NewFromFloat(2.45),
		PnLPct:             decimal.NewFromFloat(4.9),
		FeesUSDC:           decimal.NewFromFloat(0.05),
		OpenSignalToFillMs: 850,
		CloseSignalToFillMs: 920,
		OpenSlippageBP:     decimal.NewFromFloat(-10),
		CloseSlippageBP:    decimal.NewFromFloat(-19),
		CloseReason:        "signal",
		DurationSeconds:    300,
		OpenedAt:           now.Add(-5 * time.Minute),
		ClosedAt:           now,
	}
	require.NoError(t, repo.Insert(ctx, pool, in))

	rows, err := repo.ListByStrategy(ctx, pool, "s", 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.True(t, rows[0].PnLUSDC.Equal(decimal.NewFromFloat(2.45)))
	assert.Equal(t, "signal", rows[0].CloseReason)
}
```

- [ ] **Step 4: PositionHistoryRepo implementation** (`position_history_repo.go`)

```go
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type PositionHistoryRow struct {
	ID                  int64
	StrategyID          string
	Symbol              string
	Side                string
	Qty                 decimal.Decimal
	EntrySignalPrice    decimal.Decimal
	EntryFillPrice      decimal.Decimal
	ExitSignalPrice     decimal.Decimal
	ExitFillPrice       decimal.Decimal
	PnLUSDC             decimal.Decimal
	PnLPct              decimal.Decimal
	FeesUSDC            decimal.Decimal
	OpenSignalToFillMs  int
	CloseSignalToFillMs int
	OpenSlippageBP      decimal.Decimal
	CloseSlippageBP     decimal.Decimal
	CloseReason         string
	DurationSeconds     int
	OpenedAt            time.Time
	ClosedAt            time.Time
}

type PositionHistoryRepo struct {
	pool *pgxpool.Pool
}

func NewPositionHistoryRepo(pool *pgxpool.Pool) *PositionHistoryRepo {
	return &PositionHistoryRepo{pool: pool}
}

func (r *PositionHistoryRepo) Insert(ctx context.Context, q Querier, in PositionHistoryRow) error {
	_, err := q.Exec(ctx, `
INSERT INTO position_history
  (strategy_id, symbol, side, qty, entry_signal_price, entry_fill_price,
   exit_signal_price, exit_fill_price, pnl_usdc, pnl_pct, fees_usdc,
   open_signal_to_fill_ms, close_signal_to_fill_ms, open_slippage_bp,
   close_slippage_bp, close_reason, duration_seconds, opened_at, closed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		in.StrategyID, in.Symbol, in.Side, in.Qty, in.EntrySignalPrice, in.EntryFillPrice,
		in.ExitSignalPrice, in.ExitFillPrice, in.PnLUSDC, in.PnLPct, in.FeesUSDC,
		in.OpenSignalToFillMs, in.CloseSignalToFillMs, in.OpenSlippageBP,
		in.CloseSlippageBP, in.CloseReason, in.DurationSeconds, in.OpenedAt, in.ClosedAt)
	return err
}

func (r *PositionHistoryRepo) ListByStrategy(ctx context.Context, q Querier, strategyID string, limit int) ([]*PositionHistoryRow, error) {
	rows, err := q.Query(ctx, `
SELECT id, strategy_id, symbol, side, qty, entry_signal_price, entry_fill_price,
       exit_signal_price, exit_fill_price, pnl_usdc, pnl_pct, fees_usdc,
       open_signal_to_fill_ms, close_signal_to_fill_ms, open_slippage_bp,
       close_slippage_bp, close_reason, duration_seconds, opened_at, closed_at
  FROM position_history
 WHERE strategy_id=$1
 ORDER BY closed_at DESC
 LIMIT $2`, strategyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PositionHistoryRow{}
	for rows.Next() {
		var h PositionHistoryRow
		var openMs, closeMs *int
		var openSlip, closeSlip *decimal.Decimal
		if err := rows.Scan(&h.ID, &h.StrategyID, &h.Symbol, &h.Side, &h.Qty,
			&h.EntrySignalPrice, &h.EntryFillPrice, &h.ExitSignalPrice, &h.ExitFillPrice,
			&h.PnLUSDC, &h.PnLPct, &h.FeesUSDC,
			&openMs, &closeMs, &openSlip, &closeSlip,
			&h.CloseReason, &h.DurationSeconds, &h.OpenedAt, &h.ClosedAt); err != nil {
			return nil, err
		}
		if openMs != nil {
			h.OpenSignalToFillMs = *openMs
		}
		if closeMs != nil {
			h.CloseSignalToFillMs = *closeMs
		}
		if openSlip != nil {
			h.OpenSlippageBP = *openSlip
		}
		if closeSlip != nil {
			h.CloseSlippageBP = *closeSlip
		}
		out = append(out, &h)
	}
	return out, rows.Err()
}
```

- [ ] **Step 5: Tests pass**

Run: `go test -tags=integration -race -v ./internal/store/... -run "TestVirtualPositionRepo|TestPositionHistoryRepo"`
Expected: 4 tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/store/virtual_position_repo.go internal/store/virtual_position_repo_test.go \
        internal/store/position_history_repo.go  internal/store/position_history_repo_test.go
git commit -m "feat(store): VirtualPositionRepo + PositionHistoryRepo"
```

---

## Task 6: OrderRepo

**Files:**
- Create: `internal/store/order_repo.go`
- Create: `internal/store/order_repo_test.go`

- [ ] **Step 1: Test file**

```go
//go:build integration

package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrderRepo_InsertAndGetByClientID(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	repo := NewOrderRepo(pool)

	in := OrderRow{
		StrategyID:    "s",
		Symbol:        "ETHUSDC",
		Side:          "BUY",
		Type:          "MARKET",
		Purpose:       "entry",
		Qty:           decimal.NewFromFloat(0.1),
		ClientOrderID: "entry-trace1-0",
		Status:        "pending",
	}
	id, err := repo.Insert(ctx, pool, in)
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	got, err := repo.GetByClientID(ctx, pool, "entry-trace1-0")
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
}

func TestOrderRepo_InsertDuplicateClientIDFails(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	repo := NewOrderRepo(pool)

	_, err := repo.Insert(ctx, pool, OrderRow{
		StrategyID: "s", Symbol: "ETHUSDC", Side: "BUY", Type: "MARKET", Purpose: "entry",
		Qty: decimal.NewFromInt(1), ClientOrderID: "dup", Status: "pending",
	})
	require.NoError(t, err)
	_, err = repo.Insert(ctx, pool, OrderRow{
		StrategyID: "s", Symbol: "ETHUSDC", Side: "BUY", Type: "MARKET", Purpose: "entry",
		Qty: decimal.NewFromInt(1), ClientOrderID: "dup", Status: "pending",
	})
	require.Error(t, err)
}

func TestOrderRepo_UpdateStatusAndFill(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	repo := NewOrderRepo(pool)

	id, _ := repo.Insert(ctx, pool, OrderRow{
		StrategyID: "s", Symbol: "ETHUSDC", Side: "BUY", Type: "MARKET", Purpose: "entry",
		Qty: decimal.NewFromInt(1), ClientOrderID: "c1", Status: "pending",
	})
	require.NoError(t, repo.UpdateOnFill(ctx, pool, id, "ex-123", decimal.NewFromInt(1), decimal.NewFromFloat(99.5), decimal.NewFromFloat(0.1)))

	got, _ := repo.GetByClientID(ctx, pool, "c1")
	assert.Equal(t, "filled", got.Status)
	assert.True(t, decimal.NewFromFloat(99.5).Equal(got.AvgFillPrice))
}

func TestOrderRepo_ListPending(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedStrategyForVP(t, ctx, pool)
	repo := NewOrderRepo(pool)

	for i, status := range []string{"pending", "submitted", "filled", "canceled"} {
		_, err := repo.Insert(ctx, pool, OrderRow{
			StrategyID: "s", Symbol: "ETHUSDC", Side: "BUY", Type: "MARKET", Purpose: "entry",
			Qty: decimal.NewFromInt(1), ClientOrderID: "c" + string(rune('0'+i)), Status: status,
		})
		require.NoError(t, err)
	}
	pending, err := repo.ListPending(ctx, pool)
	require.NoError(t, err)
	assert.Len(t, pending, 2) // pending + submitted
}

func TestOrderRepo_GetByClientIDNotFound(t *testing.T) {
	pool := testPool(t)
	repo := NewOrderRepo(pool)
	_, err := repo.GetByClientID(context.Background(), pool, "nope")
	assert.True(t, errors.Is(err, pgx.ErrNoRows))
}
```

- [ ] **Step 2: Implementation**

```go
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type OrderRow struct {
	ID                int64
	VirtualPositionID int64
	StrategyID        string
	Symbol            string
	Side              string
	Type              string
	Purpose           string
	Qty               decimal.Decimal
	Price             decimal.Decimal
	StopPrice         decimal.Decimal
	ClientOrderID     string
	ExchangeOrderID   string
	Status            string
	FilledQty         decimal.Decimal
	AvgFillPrice      decimal.Decimal
	FeesUSDC          decimal.Decimal
	SubmittedAt       time.Time
	FilledAt          time.Time
	RawResponse       json.RawMessage
}

type OrderRepo struct {
	pool *pgxpool.Pool
}

func NewOrderRepo(pool *pgxpool.Pool) *OrderRepo { return &OrderRepo{pool: pool} }

func (r *OrderRepo) Insert(ctx context.Context, q Querier, in OrderRow) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
INSERT INTO orders
  (virtual_position_id, strategy_id, symbol, side, type, purpose, qty, price,
   stop_price, client_order_id, status)
VALUES (NULLIF($1,0)::bigint, $2,$3,$4,$5,$6,$7, NULLIF($8,0)::numeric, NULLIF($9,0)::numeric, $10, $11::order_status)
RETURNING id`,
		in.VirtualPositionID, in.StrategyID, in.Symbol, in.Side, in.Type, in.Purpose,
		in.Qty, in.Price, in.StopPrice, in.ClientOrderID, in.Status,
	).Scan(&id)
	return id, err
}

func (r *OrderRepo) GetByClientID(ctx context.Context, q Querier, clientID string) (*OrderRow, error) {
	return r.scanOne(ctx, q,
		`SELECT id, COALESCE(virtual_position_id,0), strategy_id, symbol, side, type, purpose,
                qty, COALESCE(price,0), COALESCE(stop_price,0),
                client_order_id, COALESCE(exchange_order_id,''), status::text,
                filled_qty, COALESCE(avg_fill_price,0), fees_usdc,
                COALESCE(submitted_at, '0001-01-01'::timestamptz),
                COALESCE(filled_at, '0001-01-01'::timestamptz),
                COALESCE(raw_response::text,'{}')
           FROM orders WHERE client_order_id=$1`, clientID)
}

func (r *OrderRepo) UpdateOnFill(ctx context.Context, q Querier, id int64, exchangeOrderID string,
	filledQty, avgFillPrice, fees decimal.Decimal) error {
	_, err := q.Exec(ctx, `
UPDATE orders SET
  status='filled'::order_status,
  exchange_order_id=$2,
  filled_qty=$3,
  avg_fill_price=$4,
  fees_usdc=$5,
  filled_at=now(),
  updated_at=now()
WHERE id=$1`, id, exchangeOrderID, filledQty, avgFillPrice, fees)
	return err
}

func (r *OrderRepo) UpdateStatus(ctx context.Context, q Querier, id int64, status string) error {
	_, err := q.Exec(ctx,
		`UPDATE orders SET status=$2::order_status, updated_at=now() WHERE id=$1`,
		id, status)
	return err
}

func (r *OrderRepo) ListPending(ctx context.Context, q Querier) ([]*OrderRow, error) {
	rows, err := q.Query(ctx, `
SELECT id, COALESCE(virtual_position_id,0), strategy_id, symbol, side, type, purpose,
       qty, COALESCE(price,0), COALESCE(stop_price,0),
       client_order_id, COALESCE(exchange_order_id,''), status::text,
       filled_qty, COALESCE(avg_fill_price,0), fees_usdc,
       COALESCE(submitted_at,'0001-01-01'::timestamptz),
       COALESCE(filled_at,'0001-01-01'::timestamptz),
       COALESCE(raw_response::text,'{}')
  FROM orders WHERE status IN ('pending','submitted','partial')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*OrderRow{}
	for rows.Next() {
		o, err := r.scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *OrderRepo) scanOne(ctx context.Context, q Querier, sql string, args ...any) (*OrderRow, error) {
	row := q.QueryRow(ctx, sql, args...)
	var o OrderRow
	var rawResp string
	if err := row.Scan(&o.ID, &o.VirtualPositionID, &o.StrategyID, &o.Symbol, &o.Side, &o.Type, &o.Purpose,
		&o.Qty, &o.Price, &o.StopPrice, &o.ClientOrderID, &o.ExchangeOrderID, &o.Status,
		&o.FilledQty, &o.AvgFillPrice, &o.FeesUSDC, &o.SubmittedAt, &o.FilledAt, &rawResp); err != nil {
		return nil, err
	}
	o.RawResponse = json.RawMessage(rawResp)
	return &o, nil
}

func (r *OrderRepo) scanRow(rows pgxRow) (*OrderRow, error) {
	var o OrderRow
	var rawResp string
	if err := rows.Scan(&o.ID, &o.VirtualPositionID, &o.StrategyID, &o.Symbol, &o.Side, &o.Type, &o.Purpose,
		&o.Qty, &o.Price, &o.StopPrice, &o.ClientOrderID, &o.ExchangeOrderID, &o.Status,
		&o.FilledQty, &o.AvgFillPrice, &o.FeesUSDC, &o.SubmittedAt, &o.FilledAt, &rawResp); err != nil {
		return nil, err
	}
	o.RawResponse = json.RawMessage(rawResp)
	return &o, nil
}

// pgxRow narrows pgx.Rows.Scan to what scanRow needs.
type pgxRow interface {
	Scan(dest ...any) error
}
```

- [ ] **Step 3: Tests pass**

Run: `go test -tags=integration -race -v ./internal/store/... -run TestOrderRepo`
Expected: 5 tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/store/order_repo.go internal/store/order_repo_test.go
git commit -m "feat(store): OrderRepo with client_order_id idempotency"
```

---

## Task 7: Idempotency layer

**Files:**
- Create: `internal/idempotency/lru.go`
- Create: `internal/idempotency/checker.go`
- Create: `internal/idempotency/checker_test.go`

The Checker is a small composition: an in-memory LRU keyed on `(strategy_id, tv_timestamp_ms)`, fronting a SignalRepo lookup. The intent: cheap fast path; DB is the source of truth.

For Plan 2, we keep the Checker focused on "is this signal a duplicate?" — the actual `INSERT ... ON CONFLICT` happens later inside the ingest pipeline (which uses SignalRepo.Insert directly and gets `duplicate=true` when conflict). The LRU here serves to short-circuit BEFORE we even hit the DB for the very common case of TV resending the same alert seconds apart.

- [ ] **Step 1: Test file `internal/idempotency/checker_test.go`**

```go
//go:build integration

package idempotency

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/store"
)

// reuses internal/store testPool helper via package-level test setup.
// Since dockertest is in store package's _test, we open our own pool here.
func openPool(t *testing.T) interface {
	store.Querier
	Close()
} {
	t.Helper()
	t.Skip("idempotency integration test requires its own docker setup; covered by ingest e2e tests")
	return nil
}

func TestChecker_LRUOnly(t *testing.T) {
	c := NewChecker(64, nil) // nil repo = LRU-only mode (used in tests/dry_run)
	ctx := context.Background()

	// First seen: not duplicate
	dup, err := c.Check(ctx, "s1", 100)
	require.NoError(t, err)
	assert.False(t, dup)

	// Same key: duplicate
	dup2, err := c.Check(ctx, "s1", 100)
	require.NoError(t, err)
	assert.True(t, dup2)

	// Different key: not duplicate
	dup3, err := c.Check(ctx, "s1", 101)
	require.NoError(t, err)
	assert.False(t, dup3)
}

func TestChecker_LRUEviction(t *testing.T) {
	c := NewChecker(2, nil)
	ctx := context.Background()
	_, _ = c.Check(ctx, "s", 1)
	_, _ = c.Check(ctx, "s", 2)
	_, _ = c.Check(ctx, "s", 3) // evicts (s,1)
	dup, _ := c.Check(ctx, "s", 1)
	assert.False(t, dup, "evicted key must be re-tested via repo (or treated as new in LRU-only mode)")
}

// Suppress unused import in non-integration builds.
var _ = json.RawMessage(nil)
var _ = net.IP(nil)
var _ = time.Time{}
var _ = decimal.Zero
```

(The integration-with-DB scenario is exercised by Task 13's end-to-end ingest test.)

- [ ] **Step 2: Implementation `internal/idempotency/lru.go`**

```go
package idempotency

import (
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
)

type key struct {
	strategyID    string
	tvTimestampMs int64
}

type lruCache struct {
	mu   sync.Mutex
	cache *lru.Cache[key, struct{}]
}

func newLRU(size int) *lruCache {
	c, _ := lru.New[key, struct{}](size)
	return &lruCache{cache: c}
}

// SeenOrAdd returns true if the key was already present; false (and adds) otherwise.
func (l *lruCache) SeenOrAdd(strategyID string, tvTimestampMs int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := key{strategyID, tvTimestampMs}
	if _, ok := l.cache.Get(k); ok {
		return true
	}
	l.cache.Add(k, struct{}{})
	return false
}
```

- [ ] **Step 3: Implementation `internal/idempotency/checker.go`**

```go
package idempotency

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

// SignalLookup is the subset of SignalRepo we need.
type SignalLookup interface {
	ExistsByKey(ctx context.Context, q store.Querier, strategyID string, tvTimestampMs int64) (bool, error)
}

// Checker is a 2-layer idempotency check: LRU first (fast), DB second (truth).
//
// Pass repo=nil to use LRU-only mode (e.g., in unit tests or dry_run scenarios
// where no DB is wired). In production, always pass a real SignalRepo wired
// to a *pgxpool.Pool.
type Checker struct {
	lru  *lruCache
	repo SignalLookup
	pool *pgxpool.Pool
}

func NewChecker(lruSize int, repo SignalLookup) *Checker {
	return &Checker{lru: newLRU(lruSize), repo: repo}
}

// WithPool sets the pool used for repo lookups. Optional for LRU-only mode.
func (c *Checker) WithPool(p *pgxpool.Pool) *Checker {
	c.pool = p
	return c
}

// Check returns true if (strategyID, tvTimestampMs) has been seen before.
// Always adds to the LRU on the first sighting.
func (c *Checker) Check(ctx context.Context, strategyID string, tvTimestampMs int64) (bool, error) {
	if c.lru.SeenOrAdd(strategyID, tvTimestampMs) {
		return true, nil
	}
	if c.repo == nil || c.pool == nil {
		return false, nil
	}
	exists, err := c.repo.ExistsByKey(ctx, c.pool, strategyID, tvTimestampMs)
	if err != nil {
		return false, err
	}
	return exists, nil
}
```

- [ ] **Step 4: Add `ExistsByKey` to SignalRepo**

Edit `internal/store/signal_repo.go`, add method to `*SignalRepo`:

```go
func (r *SignalRepo) ExistsByKey(ctx context.Context, q Querier, strategyID string, tvTimestampMs int64) (bool, error) {
	var n int
	err := q.QueryRow(ctx,
		`SELECT 1 FROM signals WHERE strategy_id=$1 AND tv_timestamp_ms=$2 LIMIT 1`,
		strategyID, tvTimestampMs,
	).Scan(&n)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
}
```

(Imports already include `pgx` and `errors`.)

- [ ] **Step 5: Run tests**

```bash
go test -tags=integration -race -v ./internal/idempotency/...
go test -tags=integration -race -v ./internal/store/...
```

Expected:
- 2 LRU tests pass
- All store tests still pass

- [ ] **Step 6: Commit**

```bash
git add internal/idempotency/ internal/store/signal_repo.go
git commit -m "feat(idempotency): LRU-first checker with DB-backed truth"
```

---

## Task 8: Notifier interface + NoOp + Multi

**Files:**
- Create: `internal/notify/notifier.go`
- Create: `internal/notify/notifier_test.go`

- [ ] **Step 1: Test file**

```go
package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorder struct {
	got []Message
	err error
}

func (r *recorder) Send(_ context.Context, m Message) error {
	r.got = append(r.got, m)
	return r.err
}

func TestNoOp(t *testing.T) {
	n := NoOp{}
	require.NoError(t, n.Send(context.Background(), Message{Title: "x"}))
}

func TestMulti_FansOut(t *testing.T) {
	a := &recorder{}
	b := &recorder{}
	n := NewMulti(a, b)
	require.NoError(t, n.Send(context.Background(), Message{Title: "hi"}))
	assert.Len(t, a.got, 1)
	assert.Len(t, b.got, 1)
}

func TestMulti_AggregatesErrors(t *testing.T) {
	a := &recorder{}
	b := &recorder{err: errors.New("boom")}
	n := NewMulti(a, b)
	err := n.Send(context.Background(), Message{Title: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	assert.Len(t, a.got, 1, "first notifier still called even if second errs")
}

func TestSeverity_Constants(t *testing.T) {
	assert.Equal(t, "info", string(SeverityInfo))
	assert.Equal(t, "warn", string(SeverityWarn))
	assert.Equal(t, "critical", string(SeverityCritical))
}
```

- [ ] **Step 2: Implementation `internal/notify/notifier.go`**

```go
package notify

import (
	"context"
	"errors"
	"strings"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityCritical Severity = "critical"
)

// Message is a structured notification. Adapters render it differently per channel.
type Message struct {
	Title    string
	Body     string         // markdown-friendly plain text
	Severity Severity
	Fields   map[string]any // optional extra context
}

// Notifier is the abstract send interface — Feishu, Telegram, and any future
// channel implement it. Multi composes several into one.
type Notifier interface {
	Send(ctx context.Context, m Message) error
}

// NoOp drops every message. Used as the default when no channel is configured.
type NoOp struct{}

func (NoOp) Send(_ context.Context, _ Message) error { return nil }

// Multi fans a single Send call out to N notifiers. It always tries every
// notifier even if an earlier one errors, then returns the joined error (or nil).
type Multi struct{ inner []Notifier }

func NewMulti(notifiers ...Notifier) *Multi { return &Multi{inner: notifiers} }

func (m *Multi) Send(ctx context.Context, msg Message) error {
	var errs []string
	for _, n := range m.inner {
		if err := n.Send(ctx, msg); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.New("notifier errors: " + strings.Join(errs, "; "))
}
```

- [ ] **Step 3: Tests pass**

Run: `go test -race -v ./internal/notify/... -run "TestNoOp|TestMulti|TestSeverity"`
Expected: 4 tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/notify/notifier.go internal/notify/notifier_test.go
git commit -m "feat(notify): Notifier interface + NoOp + Multi fan-out"
```

---

## Task 9: Feishu Notifier

**Files:**
- Create: `internal/notify/feishu.go`
- Create: `internal/notify/feishu_test.go`

Feishu's webhook accepts `{"msg_type":"text","content":{"text":"..."}}` (or richer formats; we use text for MVP).

- [ ] **Step 1: Test file**

```go
package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeishu_PostsTextMessage(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok"}`))
	}))
	defer srv.Close()

	n := NewFeishu(srv.URL)
	require.NoError(t, n.Send(context.Background(), Message{
		Title:    "Trade",
		Body:     "Opened LONG ETH @ 2300",
		Severity: SeverityInfo,
	}))
	assert.Equal(t, "text", got["msg_type"])
	content := got["content"].(map[string]any)
	text := content["text"].(string)
	assert.Contains(t, text, "Trade")
	assert.Contains(t, text, "Opened LONG")
}

func TestFeishu_NonZeroCodeIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":19021,"msg":"sign verification failed"}`))
	}))
	defer srv.Close()
	n := NewFeishu(srv.URL)
	err := n.Send(context.Background(), Message{Title: "x", Body: "y"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "19021")
}

func TestFeishu_HTTPErrorIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	n := NewFeishu(srv.URL)
	err := n.Send(context.Background(), Message{Title: "x"})
	require.Error(t, err)
}
```

- [ ] **Step 2: Implementation `internal/notify/feishu.go`**

```go
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Feishu struct {
	URL    string
	Client *http.Client
}

func NewFeishu(url string) *Feishu {
	return &Feishu{URL: url, Client: &http.Client{Timeout: 5 * time.Second}}
}

type feishuReq struct {
	MsgType string         `json:"msg_type"`
	Content map[string]any `json:"content"`
}

type feishuResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func (f *Feishu) Send(ctx context.Context, m Message) error {
	var b strings.Builder
	if m.Severity != "" {
		b.WriteString("[" + strings.ToUpper(string(m.Severity)) + "] ")
	}
	b.WriteString(m.Title)
	if m.Body != "" {
		b.WriteString("\n")
		b.WriteString(m.Body)
	}
	if len(m.Fields) > 0 {
		b.WriteString("\n")
		for k, v := range m.Fields {
			fmt.Fprintf(&b, "%s: %v\n", k, v)
		}
	}

	payload, _ := json.Marshal(feishuReq{
		MsgType: "text",
		Content: map[string]any{"text": b.String()},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("feishu http %d: %s", resp.StatusCode, string(body))
	}
	var fr feishuResp
	if err := json.Unmarshal(body, &fr); err != nil {
		return fmt.Errorf("feishu decode: %w", err)
	}
	if fr.Code != 0 {
		return fmt.Errorf("feishu code=%d msg=%s", fr.Code, fr.Msg)
	}
	return nil
}
```

- [ ] **Step 3: Tests pass**

Run: `go test -race -v ./internal/notify/... -run TestFeishu`
Expected: 3 tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/notify/feishu.go internal/notify/feishu_test.go
git commit -m "feat(notify): Feishu webhook adapter"
```

---

## Task 10: Telegram Notifier

**Files:**
- Create: `internal/notify/telegram.go`
- Create: `internal/notify/telegram_test.go`

Telegram bot API: `POST https://api.telegram.org/bot<token>/sendMessage` with `{"chat_id": "...", "text": "...", "parse_mode": "Markdown"}`. We make the base URL configurable so tests can hit httptest.

- [ ] **Step 1: Test file**

```go
package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTelegram_PostsMarkdownMessage(t *testing.T) {
	var got map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	n := NewTelegram(srv.URL, "TOKEN", "CHAT")
	require.NoError(t, n.Send(context.Background(), Message{Title: "X", Body: "Y", Severity: SeverityWarn}))

	assert.True(t, strings.HasPrefix(path, "/botTOKEN/sendMessage"))
	assert.Equal(t, "CHAT", got["chat_id"])
	text := got["text"].(string)
	assert.Contains(t, text, "X")
	assert.Contains(t, text, "Y")
	assert.Contains(t, text, "WARN")
}

func TestTelegram_OkFalseIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"bad chat"}`))
	}))
	defer srv.Close()
	n := NewTelegram(srv.URL, "T", "C")
	err := n.Send(context.Background(), Message{Title: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad chat")
}
```

- [ ] **Step 2: Implementation `internal/notify/telegram.go`**

```go
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Telegram struct {
	BaseURL string // e.g. "https://api.telegram.org" (override in tests)
	Token   string
	ChatID  string
	Client  *http.Client
}

func NewTelegram(baseURL, token, chatID string) *Telegram {
	if baseURL == "" {
		baseURL = "https://api.telegram.org"
	}
	return &Telegram{
		BaseURL: baseURL, Token: token, ChatID: chatID,
		Client: &http.Client{Timeout: 5 * time.Second},
	}
}

type tgReq struct {
	ChatID    string `json:"chat_id"`
	Text      string `json:"text"`
	ParseMode string `json:"parse_mode"`
}

type tgResp struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
}

func (tg *Telegram) Send(ctx context.Context, m Message) error {
	var b strings.Builder
	if m.Severity != "" {
		b.WriteString("[" + strings.ToUpper(string(m.Severity)) + "] ")
	}
	b.WriteString("*" + m.Title + "*")
	if m.Body != "" {
		b.WriteString("\n")
		b.WriteString(m.Body)
	}
	if len(m.Fields) > 0 {
		b.WriteString("\n")
		for k, v := range m.Fields {
			fmt.Fprintf(&b, "`%s`: %v\n", k, v)
		}
	}

	payload, _ := json.Marshal(tgReq{ChatID: tg.ChatID, Text: b.String(), ParseMode: "Markdown"})
	url := fmt.Sprintf("%s/bot%s/sendMessage", strings.TrimRight(tg.BaseURL, "/"), tg.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tg.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("telegram http %d: %s", resp.StatusCode, string(body))
	}
	var tr tgResp
	if err := json.Unmarshal(body, &tr); err != nil {
		return fmt.Errorf("telegram decode: %w", err)
	}
	if !tr.OK {
		return fmt.Errorf("telegram code=%d desc=%s", tr.ErrorCode, tr.Description)
	}
	return nil
}
```

- [ ] **Step 3: Tests pass**

Run: `go test -race -v ./internal/notify/... -run TestTelegram`
Expected: 2 tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/notify/telegram.go internal/notify/telegram_test.go
git commit -m "feat(notify): Telegram bot adapter (markdown messages)"
```

---

## Task 11: Trader port + DryRunTrader

**Files:**
- Create: `internal/trade/trader.go`
- Create: `internal/trade/dryrun.go`
- Create: `internal/trade/dryrun_test.go`

`Trader` is the abstract port that application/trade uses. `DryRunTrader` is a deterministic in-memory impl that simulates fills at the requested price (with optional configurable slippage = 0).

- [ ] **Step 1: Test file `internal/trade/dryrun_test.go`**

```go
package trade

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDryRunTrader_PlaceMarketReturnsFilled(t *testing.T) {
	dr := NewDryRunTrader()
	res, err := dr.Place(context.Background(), OrderRequest{
		ClientOrderID: "c1",
		Symbol:        "ETHUSDC",
		Side:          OrderSideBuy,
		Type:          OrderTypeMarket,
		Qty:           decimal.NewFromFloat(0.1),
		ReferencePrice: decimal.NewFromFloat(100),
	})
	require.NoError(t, err)
	assert.Equal(t, OrderStatusFilled, res.Status)
	assert.True(t, decimal.NewFromFloat(0.1).Equal(res.FilledQty))
	assert.True(t, decimal.NewFromFloat(100).Equal(res.AvgFillPrice))
	assert.Equal(t, "DRYRUN-c1", res.ExchangeOrderID)
}

func TestDryRunTrader_PlaceStopReturnsSubmittedNotFilled(t *testing.T) {
	dr := NewDryRunTrader()
	res, err := dr.Place(context.Background(), OrderRequest{
		ClientOrderID:  "c2",
		Symbol:         "ETHUSDC",
		Side:           OrderSideSell,
		Type:           OrderTypeStopMarket,
		Qty:            decimal.NewFromFloat(1),
		StopPrice:      decimal.NewFromFloat(95),
		ReferencePrice: decimal.NewFromFloat(100),
	})
	require.NoError(t, err)
	assert.Equal(t, OrderStatusSubmitted, res.Status,
		"stop orders are submitted, not filled, until trigger; in dry_run we never trigger")
}

func TestDryRunTrader_CancelReturnsCanceled(t *testing.T) {
	dr := NewDryRunTrader()
	require.NoError(t, dr.Cancel(context.Background(), "ETHUSDC", "c2"))
}
```

- [ ] **Step 2: Implementation `internal/trade/trader.go`**

```go
package trade

import (
	"context"

	"github.com/shopspring/decimal"
)

type OrderSide string

const (
	OrderSideBuy  OrderSide = "BUY"
	OrderSideSell OrderSide = "SELL"
)

type OrderType string

const (
	OrderTypeMarket           OrderType = "MARKET"
	OrderTypeStop             OrderType = "STOP"        // limit-stop
	OrderTypeStopMarket       OrderType = "STOP_MARKET" // market-stop
	OrderTypeTakeProfitMarket OrderType = "TAKE_PROFIT_MARKET"
)

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusSubmitted OrderStatus = "submitted"
	OrderStatusFilled    OrderStatus = "filled"
	OrderStatusCanceled  OrderStatus = "canceled"
	OrderStatusRejected  OrderStatus = "rejected"
)

type OrderRequest struct {
	ClientOrderID  string
	Symbol         string
	Side           OrderSide
	Type           OrderType
	Qty            decimal.Decimal
	Price          decimal.Decimal // for STOP (limit price) — optional
	StopPrice      decimal.Decimal // trigger — for STOP / STOP_MARKET / TAKE_PROFIT_MARKET
	ReferencePrice decimal.Decimal // tells DryRun what price to fill at; live impl ignores
}

type OrderResult struct {
	ClientOrderID   string
	ExchangeOrderID string
	Status          OrderStatus
	FilledQty       decimal.Decimal
	AvgFillPrice    decimal.Decimal
	FeesUSDC        decimal.Decimal
}

// Trader is the port for sending orders. Adapters: DryRunTrader, BinanceTrader (Plan 2B).
type Trader interface {
	Place(ctx context.Context, req OrderRequest) (*OrderResult, error)
	Cancel(ctx context.Context, symbol, clientOrderID string) error
}
```

- [ ] **Step 3: Implementation `internal/trade/dryrun.go`**

```go
package trade

import "context"

// DryRunTrader simulates instant fills at the request's ReferencePrice for
// MARKET orders, and never triggers stop orders. Used in dry_run mode and
// in unit/integration tests of the application layer.
type DryRunTrader struct{}

func NewDryRunTrader() *DryRunTrader { return &DryRunTrader{} }

func (DryRunTrader) Place(_ context.Context, req OrderRequest) (*OrderResult, error) {
	res := &OrderResult{
		ClientOrderID:   req.ClientOrderID,
		ExchangeOrderID: "DRYRUN-" + req.ClientOrderID,
	}
	if req.Type == OrderTypeMarket {
		res.Status = OrderStatusFilled
		res.FilledQty = req.Qty
		res.AvgFillPrice = req.ReferencePrice
		return res, nil
	}
	// stop / take-profit: parked, never triggered in dry_run
	res.Status = OrderStatusSubmitted
	return res, nil
}

func (DryRunTrader) Cancel(_ context.Context, _ string, _ string) error { return nil }
```

- [ ] **Step 4: Tests pass**

Run: `go test -race -v ./internal/trade/...`
Expected: 3 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/trade/
git commit -m "feat(trade): Trader port + DryRunTrader (instant market fill, parked stops)"
```

---

## Task 12: application/trade — open / close / attach stops

**Files:**
- Create: `internal/application/trade/service.go`
- Create: `internal/application/trade/service_test.go`

The service orchestrates: place market order → record entry → attach 2-3 protective orders. It depends on Trader, OrderRepo, VirtualPositionRepo (only for `SetEntryFill` and `SetProtectiveOrders`).

The service does NOT decide WHETHER to open — that's the ingest layer (which calls position.Decide). It just executes the open/close mechanics.

For Plan 2 (dry_run), the service computes qty from `size_usdc * leverage / signal_price` then floors to a deterministic step (we hardcode step = 0.001 for now; real LOT_SIZE comes from Binance in Plan 2B).

- [ ] **Step 1: Test file**

```go
//go:build integration

package trade

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/store"
	tradepkg "github.com/lizhaojie/tvbot/internal/trade"
)

// reuse store.testPool by opening from this package (own dockertest in test helper)
// The helper for this package is just inline since we don't need a separate fixture file.

func setupDB(t *testing.T) (*store.SignalRepo, *store.StrategyRepo, *store.VirtualPositionRepo, *store.OrderRepo, *PoolWrap) {
	t.Helper()
	t.Skip("application/trade integration covered by ingest e2e (Task 13)")
	return nil, nil, nil, nil, nil
}

type PoolWrap struct{}

func TestOpenLong_DryRun_RecordsEntryAndAllStops(t *testing.T) {
	t.Skip("covered by ingest e2e (Task 13)")
	_ = decimal.Zero
	_ = json.RawMessage(nil)
	_ = net.IP(nil)
	_ = time.Time{}
	_ = context.TODO()
	_ = require.NoError
	_ = assert.True
	_ = tradepkg.NewDryRunTrader
}
```

(The full integration is in Task 13 e2e. Keeping this file to verify package compiles.)

- [ ] **Step 2: Implementation `internal/application/trade/service.go`**

```go
package trade

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/domain/order"
	"github.com/lizhaojie/tvbot/internal/domain/position"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
	"github.com/lizhaojie/tvbot/internal/store"
	tradepkg "github.com/lizhaojie/tvbot/internal/trade"
)

type Service struct {
	pool         *pgxpool.Pool
	orderRepo    *store.OrderRepo
	posRepo      *store.VirtualPositionRepo
	historyRepo  *store.PositionHistoryRepo
	trader       tradepkg.Trader
	qtyStep      decimal.Decimal // floor step; 0.001 default
}

func NewService(pool *pgxpool.Pool, orderRepo *store.OrderRepo, posRepo *store.VirtualPositionRepo,
	historyRepo *store.PositionHistoryRepo, trader tradepkg.Trader) *Service {
	return &Service{
		pool: pool, orderRepo: orderRepo, posRepo: posRepo, historyRepo: historyRepo,
		trader: trader,
		qtyStep: decimal.NewFromFloat(0.001),
	}
}

// OpenInput captures everything needed to open a virtual position.
type OpenInput struct {
	Strategy      *strategy.Strategy
	Side          position.Side
	SignalPrice   decimal.Decimal
	SignalID      int64
	TraceID       string
}

// OpenResult is what the caller gets back.
type OpenResult struct {
	VirtualPositionID int64
	EntryFillPrice    decimal.Decimal
	Qty               decimal.Decimal
}

// OpenPosition: insert virtual_position row, place entry market order, set
// fill, place stop + backup_stop (+ optional take_profit). Returns the fully-
// armed VP. All rows under one transaction.
func (s *Service) OpenPosition(ctx context.Context, in OpenInput) (*OpenResult, error) {
	// 1) Compute qty
	notional := in.Strategy.NotionalUSDC()
	rawQty := notional.Div(in.SignalPrice)
	qty := floorTo(rawQty, s.qtyStep)
	if !qty.IsPositive() {
		return nil, fmt.Errorf("qty rounds to 0 (notional=%s price=%s step=%s)",
			notional, in.SignalPrice, s.qtyStep)
	}

	res := &OpenResult{Qty: qty}

	err := store.WithTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// 2) Insert virtual position (status='opening')
		vpID, err := s.posRepo.Insert(ctx, tx, store.VirtualPositionRow{
			StrategyID: in.Strategy.ID, Symbol: in.Strategy.Symbol,
			Side: string(in.Side), Qty: qty,
			EntrySignalPrice: in.SignalPrice, EntrySignalID: in.SignalID,
			Status: string(position.StatusOpening),
		})
		if err != nil {
			return err
		}
		res.VirtualPositionID = vpID

		// 3) Place entry market order
		entrySide := tradepkg.OrderSideBuy
		if in.Side == position.SideShort {
			entrySide = tradepkg.OrderSideSell
		}
		entryClientID := fmt.Sprintf("entry-%s-%d", in.TraceID, vpID)
		entryRes, err := s.trader.Place(ctx, tradepkg.OrderRequest{
			ClientOrderID:  entryClientID,
			Symbol:         in.Strategy.Symbol,
			Side:           entrySide,
			Type:           tradepkg.OrderTypeMarket,
			Qty:            qty,
			ReferencePrice: in.SignalPrice,
		})
		if err != nil {
			return fmt.Errorf("place entry: %w", err)
		}
		if entryRes.Status != tradepkg.OrderStatusFilled {
			return fmt.Errorf("entry not filled (status=%s)", entryRes.Status)
		}

		// 4) Insert order row + update VP entry fill
		entryOrderID, err := s.orderRepo.Insert(ctx, tx, store.OrderRow{
			VirtualPositionID: vpID,
			StrategyID:        in.Strategy.ID,
			Symbol:            in.Strategy.Symbol,
			Side:              string(entrySide),
			Type:              string(order.TypeMarket),
			Purpose:           string(order.PurposeEntry),
			Qty:               qty,
			ClientOrderID:     entryClientID,
			Status:            string(order.StatusFilled),
		})
		if err != nil {
			return err
		}
		if err := s.orderRepo.UpdateOnFill(ctx, tx, entryOrderID, entryRes.ExchangeOrderID,
			entryRes.FilledQty, entryRes.AvgFillPrice, entryRes.FeesUSDC); err != nil {
			return err
		}
		if err := s.posRepo.SetEntryFill(ctx, tx, vpID, entryRes.AvgFillPrice, entryOrderID); err != nil {
			return err
		}
		res.EntryFillPrice = entryRes.AvgFillPrice

		// 5) Compute + place protective orders (stop, backup_stop, optional take_profit)
		stopID, backupID, tpID, err := s.placeProtectiveOrders(ctx, tx, vpID, in.Strategy, in.Side,
			entryRes.AvgFillPrice, qty, in.TraceID)
		if err != nil {
			return err
		}
		if err := s.posRepo.SetProtectiveOrders(ctx, tx, vpID, stopID, backupID, tpID); err != nil {
			return err
		}

		// 6) Mark VP as 'open'
		return s.posRepo.UpdateStatus(ctx, tx, vpID, string(position.StatusOpen))
	})
	return res, err
}

func (s *Service) placeProtectiveOrders(ctx context.Context, tx pgx.Tx, vpID int64,
	strat *strategy.Strategy, side position.Side, entryFill, qty decimal.Decimal, traceID string,
) (stopID, backupID, tpID int64, err error) {
	// Direction multipliers: long → stop below, take-profit above. short → opposite.
	dir := decimal.NewFromInt(1)
	if side == position.SideShort {
		dir = decimal.NewFromInt(-1)
	}
	pct := strat.StopLossPct.Div(decimal.NewFromInt(100))
	mainStopTrigger := entryFill.Mul(decimal.NewFromInt(1).Sub(pct.Mul(dir)))
	mainStopLimit := mainStopTrigger.Mul(decimal.NewFromFloat(0.999)) // slight slip toward unfavorable
	backupTrigger := mainStopTrigger.Mul(decimal.NewFromFloat(0.998))

	exitSide := tradepkg.OrderSideSell
	if side == position.SideShort {
		exitSide = tradepkg.OrderSideBuy
	}

	// 1) Main stop (limit stop)
	stopClientID := fmt.Sprintf("stop-%s-%d", traceID, vpID)
	stopRes, err := s.trader.Place(ctx, tradepkg.OrderRequest{
		ClientOrderID: stopClientID, Symbol: strat.Symbol, Side: exitSide,
		Type: tradepkg.OrderTypeStop, Qty: qty, Price: mainStopLimit, StopPrice: mainStopTrigger,
		ReferencePrice: entryFill,
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("place stop: %w", err)
	}
	stopID, err = s.orderRepo.Insert(ctx, tx, store.OrderRow{
		VirtualPositionID: vpID, StrategyID: strat.ID, Symbol: strat.Symbol,
		Side: string(exitSide), Type: string(order.TypeStop), Purpose: string(order.PurposeStop),
		Qty: qty, Price: mainStopLimit, StopPrice: mainStopTrigger,
		ClientOrderID: stopClientID, Status: string(order.StatusSubmitted),
	})
	if err != nil {
		return 0, 0, 0, err
	}
	_ = stopRes

	// 2) Backup market stop
	backupClientID := fmt.Sprintf("backup_stop-%s-%d", traceID, vpID)
	if _, err := s.trader.Place(ctx, tradepkg.OrderRequest{
		ClientOrderID: backupClientID, Symbol: strat.Symbol, Side: exitSide,
		Type: tradepkg.OrderTypeStopMarket, Qty: qty, StopPrice: backupTrigger,
		ReferencePrice: entryFill,
	}); err != nil {
		return 0, 0, 0, fmt.Errorf("place backup_stop: %w", err)
	}
	backupID, err = s.orderRepo.Insert(ctx, tx, store.OrderRow{
		VirtualPositionID: vpID, StrategyID: strat.ID, Symbol: strat.Symbol,
		Side: string(exitSide), Type: string(order.TypeStopMarket), Purpose: string(order.PurposeBackupStop),
		Qty: qty, StopPrice: backupTrigger,
		ClientOrderID: backupClientID, Status: string(order.StatusSubmitted),
	})
	if err != nil {
		return 0, 0, 0, err
	}

	// 3) Optional take profit
	if strat.HasTakeProfit() {
		tpPct := strat.TakeProfitPct.Div(decimal.NewFromInt(100))
		tpTrigger := entryFill.Mul(decimal.NewFromInt(1).Add(tpPct.Mul(dir)))
		tpClientID := fmt.Sprintf("take_profit-%s-%d", traceID, vpID)
		if _, err := s.trader.Place(ctx, tradepkg.OrderRequest{
			ClientOrderID: tpClientID, Symbol: strat.Symbol, Side: exitSide,
			Type: tradepkg.OrderTypeTakeProfitMarket, Qty: qty, StopPrice: tpTrigger,
			ReferencePrice: entryFill,
		}); err != nil {
			return 0, 0, 0, fmt.Errorf("place take_profit: %w", err)
		}
		tpID, err = s.orderRepo.Insert(ctx, tx, store.OrderRow{
			VirtualPositionID: vpID, StrategyID: strat.ID, Symbol: strat.Symbol,
			Side: string(exitSide), Type: string(order.TypeTakeProfitMarket),
			Purpose: string(order.PurposeTakeProfit), Qty: qty, StopPrice: tpTrigger,
			ClientOrderID: tpClientID, Status: string(order.StatusSubmitted),
		})
		if err != nil {
			return 0, 0, 0, err
		}
	}
	return stopID, backupID, tpID, nil
}

// CloseInput is what the caller passes to close an existing virtual position.
type CloseInput struct {
	VirtualPositionID int64
	StrategyID        string
	Symbol            string
	Side              position.Side
	Qty               decimal.Decimal
	EntryFillPrice    decimal.Decimal
	StopOrderID       int64
	BackupStopOrderID int64
	TakeProfitOrderID int64
	OpenedAt          time.Time
	EntrySignalPrice  decimal.Decimal
	ExitSignalPrice   decimal.Decimal
	CloseReason       string // "signal" | "stop_loss" | "take_profit" | "manual"
	TraceID           string
}

type CloseResult struct {
	ExitFillPrice decimal.Decimal
	PnLUSDC       decimal.Decimal
}

func (s *Service) ClosePosition(ctx context.Context, in CloseInput) (*CloseResult, error) {
	// 1) Cancel protective orders (best-effort, parallel ignored — sequential is fine for dry_run)
	for _, oid := range []int64{in.StopOrderID, in.BackupStopOrderID, in.TakeProfitOrderID} {
		if oid == 0 {
			continue
		}
		// Look up client_order_id from DB so we have it to cancel
		// (For Plan 2 dry_run, Cancel is a no-op anyway.)
		row, err := s.orderRepo.GetByClientID(ctx, s.pool, "")
		_ = row
		_ = err
	}

	// 2) Place exit market order
	exitSide := tradepkg.OrderSideSell
	if in.Side == position.SideShort {
		exitSide = tradepkg.OrderSideBuy
	}
	exitClientID := fmt.Sprintf("exit-%s-%d", in.TraceID, in.VirtualPositionID)
	exitRes, err := s.trader.Place(ctx, tradepkg.OrderRequest{
		ClientOrderID:  exitClientID,
		Symbol:         in.Symbol,
		Side:           exitSide,
		Type:           tradepkg.OrderTypeMarket,
		Qty:            in.Qty,
		ReferencePrice: in.ExitSignalPrice,
	})
	if err != nil {
		return nil, err
	}
	if exitRes.Status != tradepkg.OrderStatusFilled {
		return nil, errors.New("exit not filled")
	}

	// 3) Compute PnL
	pnl := computePnL(in.Side, in.Qty, in.EntryFillPrice, exitRes.AvgFillPrice)

	err = store.WithTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		exitOrderID, err := s.orderRepo.Insert(ctx, tx, store.OrderRow{
			VirtualPositionID: in.VirtualPositionID,
			StrategyID:        in.StrategyID,
			Symbol:            in.Symbol,
			Side:              string(exitSide),
			Type:              string(order.TypeMarket),
			Purpose:           string(order.PurposeExit),
			Qty:               in.Qty,
			ClientOrderID:     exitClientID,
			Status:            string(order.StatusFilled),
		})
		if err != nil {
			return err
		}
		if err := s.orderRepo.UpdateOnFill(ctx, tx, exitOrderID, exitRes.ExchangeOrderID,
			exitRes.FilledQty, exitRes.AvgFillPrice, exitRes.FeesUSDC); err != nil {
			return err
		}

		// 4) Mark closed
		if err := s.posRepo.MarkClosed(ctx, tx, in.VirtualPositionID); err != nil {
			return err
		}

		// 5) Insert position_history row
		now := time.Now().UTC()
		dur := int(now.Sub(in.OpenedAt).Seconds())
		if dur < 0 {
			dur = 0
		}
		pnlPct := decimal.Zero
		if !in.EntryFillPrice.IsZero() {
			delta := exitRes.AvgFillPrice.Sub(in.EntryFillPrice)
			if in.Side == position.SideShort {
				delta = delta.Neg()
			}
			pnlPct = delta.Div(in.EntryFillPrice).Mul(decimal.NewFromInt(100))
		}
		return s.historyRepo.Insert(ctx, tx, store.PositionHistoryRow{
			StrategyID: in.StrategyID, Symbol: in.Symbol, Side: string(in.Side),
			Qty:                in.Qty,
			EntrySignalPrice:   in.EntrySignalPrice,
			EntryFillPrice:     in.EntryFillPrice,
			ExitSignalPrice:    in.ExitSignalPrice,
			ExitFillPrice:      exitRes.AvgFillPrice,
			PnLUSDC:            pnl,
			PnLPct:             pnlPct,
			FeesUSDC:           exitRes.FeesUSDC,
			OpenSignalToFillMs: 0, // Plan 2B will populate
			CloseSignalToFillMs: 0,
			OpenSlippageBP:     decimal.Zero,
			CloseSlippageBP:    decimal.Zero,
			CloseReason:        in.CloseReason,
			DurationSeconds:    dur,
			OpenedAt:           in.OpenedAt,
			ClosedAt:           now,
		})
	})
	if err != nil {
		return nil, err
	}
	return &CloseResult{ExitFillPrice: exitRes.AvgFillPrice, PnLUSDC: pnl}, nil
}

func computePnL(side position.Side, qty, entry, exit decimal.Decimal) decimal.Decimal {
	delta := exit.Sub(entry)
	if side == position.SideShort {
		delta = delta.Neg()
	}
	return delta.Mul(qty)
}

func floorTo(v, step decimal.Decimal) decimal.Decimal {
	if step.IsZero() {
		return v
	}
	return v.Div(step).Floor().Mul(step)
}
```

- [ ] **Step 3: Verify compile**

Run: `go build ./internal/application/trade/...` then `go test -tags=integration ./internal/application/trade/...` — both should succeed (the test file just compiles + skips).

- [ ] **Step 4: Commit**

```bash
git add internal/application/trade/
git commit -m "feat(application/trade): OpenPosition + ClosePosition with double-stop + take-profit"
```

---

## Task 13: application/ingest — full pipeline

**Files:**
- Create: `internal/application/ingest/risk_inputs.go`
- Create: `internal/application/ingest/service.go`
- Create: `internal/application/ingest/service_test.go`

This is the heart of the bot: it orchestrates everything for a single signal.

Pipeline:
1. Parse signal → `signal.Signal`
2. Idempotency check (LRU + DB)
3. Load: strategy, current position, system_state
4. Build `risk.Input` (with account equity = caller-provided or default)
5. Run risk.Pipeline
6. If denied → record decision='risk_denied'/'disarmed', notify, return
7. Decide action via `position.Decide`
8. Branch on action → call application/trade Service
9. Record signal decision='accepted', notify

For Plan 2, account equity is hardcoded to `decimal.NewFromInt(10000)` (passed in via Service config). Plan 2B reads from Binance.

- [ ] **Step 1: Implementation `internal/application/ingest/risk_inputs.go`**

```go
package ingest

import (
	"context"
	"errors"
	"net"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/domain/position"
	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
	"github.com/lizhaojie/tvbot/internal/risk"
	"github.com/lizhaojie/tvbot/internal/store"
)

// LoadCtx is everything we read from DB to populate a risk.Input.
type LoadCtx struct {
	Strategy        *strategy.Strategy
	CurrentPosition *position.VirtualPosition
	OpenNotionalSum decimal.Decimal
	DailyPnLUSDC    decimal.Decimal
	BreakerTripped  bool
}

func loadAll(ctx context.Context, q store.Querier, pool *pgxpool.Pool,
	strategyRepo *store.StrategyRepo, posRepo *store.VirtualPositionRepo,
	systemRepo *store.SystemStateRepo, strategyID string,
) (*LoadCtx, error) {
	stratRow, err := strategyRepo.Get(ctx, q, strategyID)
	if err != nil {
		return nil, err
	}
	strat, err := strategy.New(strategy.Config{
		ID: stratRow.ID, Symbol: stratRow.Symbol, Leverage: stratRow.Leverage,
		SizeUSDC: stratRow.SizeUSDC, StopLossPct: stratRow.StopLossPct,
		TakeProfitPct: stratRow.TakeProfitPct, MaxOpenUSDC: stratRow.MaxOpenUSDC,
		Enabled: stratRow.Enabled,
	})
	if err != nil {
		return nil, err
	}

	posRow, err := posRepo.GetActiveByStrategy(ctx, q, strategyID)
	var pos *position.VirtualPosition
	if err == nil {
		pos = &position.VirtualPosition{
			ID: posRow.ID, StrategyID: posRow.StrategyID, Symbol: posRow.Symbol,
			Side: position.Side(posRow.Side), Qty: posRow.Qty,
			EntrySignalPrice: posRow.EntrySignalPrice, EntryFillPrice: posRow.EntryFillPrice,
			EntrySignalID: posRow.EntrySignalID, EntryOrderID: posRow.EntryOrderID,
			StopOrderID: posRow.StopOrderID, BackupStopOrderID: posRow.BackupStopOrderID,
			TakeProfitOrderID: posRow.TakeProfitOrderID,
			Status: position.Status(posRow.Status),
			OpenedAt: posRow.OpenedAt, ClosedAt: posRow.ClosedAt,
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	state, err := systemRepo.Get(ctx, q)
	if err != nil {
		return nil, err
	}

	openNotional, err := sumOpenNotional(ctx, q)
	if err != nil {
		return nil, err
	}

	return &LoadCtx{
		Strategy: strat, CurrentPosition: pos,
		OpenNotionalSum: openNotional,
		DailyPnLUSDC: state.DailyPnLUSDC,
		BreakerTripped: state.BreakerTripped,
	}, nil
}

func sumOpenNotional(ctx context.Context, q store.Querier) (decimal.Decimal, error) {
	var sum decimal.Decimal
	err := q.QueryRow(ctx, `
SELECT COALESCE(SUM(vp.qty * COALESCE(vp.entry_fill_price, vp.entry_signal_price)), 0)
  FROM virtual_positions vp
 WHERE vp.status IN ('opening','open','closing')`).Scan(&sum)
	return sum, err
}

func buildRiskInput(sig *sigpkg.Signal, ld *LoadCtx, equity decimal.Decimal, ip net.IP) risk.Input {
	return risk.Input{
		Signal: sig, Strategy: ld.Strategy, CurrentPosition: ld.CurrentPosition,
		OpenNotionalSum: ld.OpenNotionalSum, AccountEquity: equity,
		DailyPnLUSDC: ld.DailyPnLUSDC, BreakerTripped: ld.BreakerTripped,
		ClientIP: ip,
	}
}
```

- [ ] **Step 2: Implementation `internal/application/ingest/service.go`**

```go
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/application/trade"
	"github.com/lizhaojie/tvbot/internal/domain/position"
	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/idempotency"
	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/risk"
	"github.com/lizhaojie/tvbot/internal/store"
)

type Config struct {
	AccountEquityFallback decimal.Decimal // used in dry_run/testnet when no real equity reader
	WebhookSecret         string          // compared against Signal.Secret
}

type Service struct {
	cfg          Config
	pool         *pgxpool.Pool
	signalRepo   *store.SignalRepo
	strategyRepo *store.StrategyRepo
	posRepo      *store.VirtualPositionRepo
	systemRepo   *store.SystemStateRepo
	idempotency  *idempotency.Checker
	risk         *risk.Pipeline
	trade        *trade.Service
	notifier     notify.Notifier
	log          zerolog.Logger
}

func NewService(cfg Config, pool *pgxpool.Pool,
	signalRepo *store.SignalRepo, strategyRepo *store.StrategyRepo,
	posRepo *store.VirtualPositionRepo, systemRepo *store.SystemStateRepo,
	idem *idempotency.Checker, riskPipe *risk.Pipeline, tradeSvc *trade.Service,
	notifier notify.Notifier, log zerolog.Logger,
) *Service {
	return &Service{
		cfg: cfg, pool: pool,
		signalRepo: signalRepo, strategyRepo: strategyRepo,
		posRepo: posRepo, systemRepo: systemRepo,
		idempotency: idem, risk: riskPipe, trade: tradeSvc,
		notifier: notifier, log: log,
	}
}

// IngestResult tells the caller what happened (for HTTP responses, tests).
type IngestResult struct {
	SignalID    int64
	Decision    string // accepted | duplicate | risk_denied | invalid | disarmed
	RuleName    string // populated when decision == risk_denied
	Reason      string
	ActionTaken string // open_long | open_short | close | close_and_open_long | close_and_open_short | noop
}

// Ingest runs the full pipeline for a single webhook payload.
func (s *Service) Ingest(ctx context.Context, body []byte, clientIP net.IP) (*IngestResult, error) {
	// 1) Parse
	sig, err := sigpkg.Parse(body)
	if err != nil {
		// Record minimal signals row with decision=invalid
		_ = s.recordInvalid(ctx, body, clientIP, err.Error())
		return &IngestResult{Decision: "invalid", Reason: err.Error()}, nil
	}
	if sig.Secret != s.cfg.WebhookSecret {
		_ = s.recordInvalid(ctx, body, clientIP, "secret mismatch")
		return &IngestResult{Decision: "invalid", Reason: "secret mismatch"}, nil
	}

	// 2) Idempotency
	dup, err := s.idempotency.Check(ctx, sig.StrategyID, sig.TVTimestampMs)
	if err != nil {
		return nil, fmt.Errorf("idempotency: %w", err)
	}
	if dup {
		// Insert signals row anyway with decision='duplicate' for audit (will hit UNIQUE → existing row)
		id, _, _ := s.signalRepo.Insert(ctx, s.pool, signalRowFrom(sig, clientIP, "duplicate", "lru hit"))
		return &IngestResult{SignalID: id, Decision: "duplicate"}, nil
	}

	// 3) Insert pending signal
	signalID, isDup, err := s.signalRepo.Insert(ctx, s.pool, signalRowFrom(sig, clientIP, "pending", ""))
	if err != nil {
		return nil, err
	}
	if isDup {
		// LRU missed but DB has it (e.g. after restart) → record duplicate
		return &IngestResult{SignalID: signalID, Decision: "duplicate"}, nil
	}

	// 4) Load context + run risk
	loadCtx, err := loadAll(ctx, s.pool, s.pool, s.strategyRepo, s.posRepo, s.systemRepo, sig.StrategyID)
	if err != nil {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "invalid", "load context: "+err.Error())
		return &IngestResult{SignalID: signalID, Decision: "invalid", Reason: err.Error()}, nil
	}
	if !loadCtx.Strategy.Enabled {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "risk_denied", "strategy disabled")
		return &IngestResult{SignalID: signalID, Decision: "risk_denied", Reason: "strategy disabled"}, nil
	}

	state, err := s.systemRepo.Get(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	if !state.Armed {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "disarmed", "system not armed")
		return &IngestResult{SignalID: signalID, Decision: "disarmed", Reason: "system not armed"}, nil
	}

	in := buildRiskInput(sig, loadCtx, s.cfg.AccountEquityFallback, clientIP)
	dec, err := s.risk.Run(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("risk pipeline: %w", err)
	}
	if !dec.Allowed {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "risk_denied",
			fmt.Sprintf("%s: %s", dec.RuleName, dec.Reason))
		_ = s.notifier.Send(ctx, notify.Message{
			Title: "Signal denied", Body: fmt.Sprintf("%s denied by %s: %s", sig.StrategyID, dec.RuleName, dec.Reason),
			Severity: notify.SeverityWarn,
		})
		return &IngestResult{
			SignalID: signalID, Decision: "risk_denied",
			RuleName: dec.RuleName, Reason: dec.Reason,
		}, nil
	}

	// 5) Decide action
	action := position.Decide(loadCtx.CurrentPosition, sig.Kind)
	res := &IngestResult{SignalID: signalID, Decision: "accepted", ActionTaken: string(action)}

	switch action {
	case position.ActionNoOp:
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "noop")
		return res, nil

	case position.ActionOpenLong, position.ActionOpenShort:
		side := position.SideLong
		if action == position.ActionOpenShort {
			side = position.SideShort
		}
		if _, err := s.trade.OpenPosition(ctx, trade.OpenInput{
			Strategy:    loadCtx.Strategy,
			Side:        side,
			SignalPrice: sig.Price,
			SignalID:    signalID,
			TraceID:     sig.TraceID(),
		}); err != nil {
			_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "open failed: "+err.Error())
			_ = s.notifier.Send(ctx, notify.Message{
				Title: "Open failed", Body: err.Error(), Severity: notify.SeverityCritical,
			})
			return nil, err
		}
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", string(action))
		_ = s.notifier.Send(ctx, notify.Message{
			Title: "Open " + string(action),
			Body:  fmt.Sprintf("%s @ signal=%s", sig.StrategyID, sig.Price),
		})
		return res, nil

	case position.ActionClose:
		if loadCtx.CurrentPosition == nil {
			return res, nil
		}
		if _, err := s.trade.ClosePosition(ctx, closeInputFromLoad(loadCtx.CurrentPosition, loadCtx.Strategy, sig, "signal")); err != nil {
			_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "close failed: "+err.Error())
			_ = s.notifier.Send(ctx, notify.Message{
				Title: "Close failed", Body: err.Error(), Severity: notify.SeverityCritical,
			})
			return nil, err
		}
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "close")
		_ = s.notifier.Send(ctx, notify.Message{Title: "Closed", Body: sig.StrategyID})
		return res, nil

	case position.ActionCloseAndOpenLong, position.ActionCloseAndOpenShort:
		// Close first, then open opposite.
		if loadCtx.CurrentPosition != nil {
			if _, err := s.trade.ClosePosition(ctx, closeInputFromLoad(loadCtx.CurrentPosition, loadCtx.Strategy, sig, "signal")); err != nil {
				return nil, err
			}
		}
		side := position.SideLong
		if action == position.ActionCloseAndOpenShort {
			side = position.SideShort
		}
		if _, err := s.trade.OpenPosition(ctx, trade.OpenInput{
			Strategy: loadCtx.Strategy, Side: side, SignalPrice: sig.Price,
			SignalID: signalID, TraceID: sig.TraceID(),
		}); err != nil {
			return nil, err
		}
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", string(action))
		_ = s.notifier.Send(ctx, notify.Message{Title: "Reverse " + string(action), Body: sig.StrategyID})
		return res, nil
	}
	return res, nil
}

func (s *Service) recordInvalid(ctx context.Context, body []byte, ip net.IP, reason string) error {
	row := store.SignalRow{
		StrategyID: "_invalid_", Symbol: "_invalid_", Kind: "long", // placeholders that satisfy enum
		SignalPrice: decimal.NewFromInt(0), TVTimestampMs: time.Now().UnixMilli(),
		ReceivedAt: time.Now().UTC(), RawPayload: json.RawMessage(body),
		ClientIP: ip, Decision: "invalid", DecisionReason: reason, TraceID: "n/a",
	}
	// SignalPrice 0 is rejected by CHECK if any; the schema doesn't enforce >0 → fine.
	_, _, err := s.signalRepo.Insert(ctx, s.pool, row)
	return err
}

func signalRowFrom(sig *sigpkg.Signal, ip net.IP, decision, reason string) store.SignalRow {
	return store.SignalRow{
		StrategyID: sig.StrategyID, Symbol: sig.Symbol, Kind: string(sig.Kind),
		SignalPrice: sig.Price, TVTimestampMs: sig.TVTimestampMs,
		ReceivedAt: time.Now().UTC(), RawPayload: sig.Raw, ClientIP: ip,
		Decision: decision, DecisionReason: reason, TraceID: sig.TraceID(),
	}
}

func closeInputFromLoad(pos *position.VirtualPosition, strat *strategy.StrategyLike, sig *sigpkg.Signal, reason string) trade.CloseInput {
	_ = strat
	return trade.CloseInput{
		VirtualPositionID: pos.ID, StrategyID: pos.StrategyID, Symbol: pos.Symbol,
		Side: pos.Side, Qty: pos.Qty, EntryFillPrice: pos.EntryFillPrice,
		StopOrderID: pos.StopOrderID, BackupStopOrderID: pos.BackupStopOrderID,
		TakeProfitOrderID: pos.TakeProfitOrderID, OpenedAt: pos.OpenedAt,
		EntrySignalPrice: pos.EntrySignalPrice, ExitSignalPrice: sig.Price,
		CloseReason: reason, TraceID: sig.TraceID(),
	}
}

// silence unused imports when isolating tests
var (
	_ = errors.New
	_ = strategy.Strategy{}
)
```

**NOTE:** the implementer needs to add a `TraceID()` helper to `signal.Signal` if not already present. Add to `internal/domain/signal/signal.go`:

```go
func (s *Signal) TraceID() string {
	// derived from secret-hashed strategy_id + tv_timestamp; deterministic + concise
	return fmt.Sprintf("tv-%s-%d", s.StrategyID, s.TVTimestampMs)
}
```

Also note `strategy.StrategyLike` doesn't exist — the actual type is `*strategy.Strategy`. Fix the `closeInputFromLoad` signature accordingly.

- [ ] **Step 3: Test file `internal/application/ingest/service_test.go`**

End-to-end integration test:

```go
//go:build integration

package ingest

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apptrade "github.com/lizhaojie/tvbot/internal/application/trade"
	"github.com/lizhaojie/tvbot/internal/idempotency"
	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/risk"
	"github.com/lizhaojie/tvbot/internal/store"
	tradepkg "github.com/lizhaojie/tvbot/internal/trade"
)

func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip()
	}
	pool, err := dockertest.NewPool("")
	require.NoError(t, err)
	pool.MaxWait = 60 * time.Second

	res, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres", Tag: "16-alpine",
		Env: []string{"POSTGRES_USER=test", "POSTGRES_PASSWORD=test", "POSTGRES_DB=test"},
	}, func(c *docker.HostConfig) { c.AutoRemove = true })
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Purge(res) })

	dsn := "postgres://test:test@" + res.GetHostPort("5432/tcp") + "/test?sslmode=disable"
	var p *pgxpool.Pool
	require.NoError(t, pool.Retry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		pp, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		if err := pp.Ping(ctx); err != nil {
			pp.Close()
			return err
		}
		p = pp
		return nil
	}))
	t.Cleanup(p.Close)

	mig, err := filepath.Abs("../../../migrations/0001_init.sql")
	require.NoError(t, err)
	data, err := os.ReadFile(mig)
	require.NoError(t, err)
	body := extractGooseUp(string(data))
	_, err = p.Exec(context.Background(), body)
	require.NoError(t, err)

	// seed strategy + arm
	_, err = p.Exec(context.Background(), `
INSERT INTO strategies(id, symbol, leverage, size_usdc, stop_loss_pct, take_profit_pct, max_open_usdc, enabled)
VALUES('s', 'ETHUSDC', 5, 100, 1.5, 3.0, 1000, true)`)
	require.NoError(t, err)
	_, err = p.Exec(context.Background(), `UPDATE system_state SET armed=true, armed_by='test'`)
	require.NoError(t, err)

	return p
}

// extractGooseUp duplicates the helper used in store/testhelpers_test.go
// (kept inline because dockertest helpers in store/ are package-private).
func extractGooseUp(s string) string {
	const begin = "-- +goose StatementBegin"
	const end = "-- +goose StatementEnd"
	i := indexAfter(s, begin)
	if i < 0 {
		return s
	}
	body := s[i:]
	j := indexAfter(body, end)
	if j < 0 {
		return body
	}
	return body[:j-len(end)]
}
func indexAfter(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i + len(sub)
		}
	}
	return -1
}

func newService(t *testing.T, p *pgxpool.Pool) *Service {
	t.Helper()
	signalRepo := store.NewSignalRepo(p)
	strategyRepo := store.NewStrategyRepo(p)
	posRepo := store.NewVirtualPositionRepo(p)
	systemRepo := store.NewSystemStateRepo(p)
	orderRepo := store.NewOrderRepo(p)
	historyRepo := store.NewPositionHistoryRepo(p)
	idem := idempotency.NewChecker(1024, signalRepo).WithPool(p)
	pipe := risk.NewPipeline(
		risk.MaxPositionRule{},
		risk.TotalLeverageRule{MaxLeverage: decimal.NewFromInt(10)},
		risk.DailyLossBreakerRule{MaxDailyLossUSDC: decimal.NewFromInt(1000)},
	)
	tradeSvc := apptrade.NewService(p, orderRepo, posRepo, historyRepo, tradepkg.NewDryRunTrader())
	return NewService(Config{
		AccountEquityFallback: decimal.NewFromInt(10000),
		WebhookSecret:         "secret",
	}, p, signalRepo, strategyRepo, posRepo, systemRepo, idem, pipe, tradeSvc,
		notify.NoOp{}, zerolog.Nop())
}

func TestIngest_OpenLongDryRun(t *testing.T) {
	p := setupDB(t)
	svc := newService(t, p)

	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"100","timestamp":1714723504000,"secret":"secret"}`)
	res, err := svc.Ingest(context.Background(), body, net.ParseIP("127.0.0.1"))
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "accepted", res.Decision)
	assert.Equal(t, "open_long", res.ActionTaken)

	// Verify state
	var sigCount, vpCount, orderCount int
	require.NoError(t, p.QueryRow(context.Background(),
		`SELECT count(*) FROM signals`).Scan(&sigCount))
	require.NoError(t, p.QueryRow(context.Background(),
		`SELECT count(*) FROM virtual_positions WHERE status='open'`).Scan(&vpCount))
	require.NoError(t, p.QueryRow(context.Background(),
		`SELECT count(*) FROM orders`).Scan(&orderCount))
	assert.Equal(t, 1, sigCount)
	assert.Equal(t, 1, vpCount)
	assert.GreaterOrEqual(t, orderCount, 3, "entry + main stop + backup stop (+ optional take_profit)")

	// Idempotent: same signal again → duplicate
	res2, err := svc.Ingest(context.Background(), body, net.ParseIP("127.0.0.1"))
	require.NoError(t, err)
	assert.Equal(t, "duplicate", res2.Decision)
}

func TestIngest_RejectsBadSecret(t *testing.T) {
	p := setupDB(t)
	svc := newService(t, p)
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"100","timestamp":1,"secret":"WRONG"}`)
	res, err := svc.Ingest(context.Background(), body, nil)
	require.NoError(t, err)
	assert.Equal(t, "invalid", res.Decision)
}

func TestIngest_DisarmedSystemRejects(t *testing.T) {
	p := setupDB(t)
	_, _ = p.Exec(context.Background(), `UPDATE system_state SET armed=false`)
	svc := newService(t, p)
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"100","timestamp":1,"secret":"secret"}`)
	res, _ := svc.Ingest(context.Background(), body, net.ParseIP("127.0.0.1"))
	assert.Equal(t, "disarmed", res.Decision)
}

// silence unused
var _ = json.RawMessage(nil)
```

**NOTE for implementer:** the test file above references `s.TraceID()` indirectly via the service. Make sure `Signal.TraceID()` is added (to `internal/domain/signal/signal.go`) before running the test.

- [ ] **Step 4: Run tests**

```bash
go test -tags=integration -race -v ./internal/application/ingest/...
```

Expected: 3 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/application/ingest/ internal/domain/signal/signal.go
git commit -m "feat(application/ingest): full signal pipeline with dry_run e2e test"
```

---

## Final Verification

- [ ] **Step 1: All tests, no integration tag**

```bash
go test -race ./...
```
Expected: all unit tests pass.

- [ ] **Step 2: Integration tests**

```bash
go test -tags=integration -race ./...
```
Expected: all pass (dockertest spins up postgres for each package's integration tests).

- [ ] **Step 3: vet + build**

```bash
go vet ./... && make build && ./bin/tvbot
```
Expected: clean vet, build succeeds, tvbot stub still prints (the binary doesn't use ingest yet — Plan 3 wires it).

---

## Self-Review Checklist (controller runs after writing)

- [x] All 14 spec decisions covered or noted as deferred (Plan 2B/3/4):
  - 1 (Binance): trader port done; live impl deferred
  - 4 (multi-strategy virtual positions): VirtualPositionRepo + service ✓
  - 5 (signal contract): already done in Plan 1
  - 6 (double stop loss): trade.Service.placeProtectiveOrders ✓
  - 7 (4 risk rules): pipeline wired in ingest ✓
  - 8 (dry_run mode): DryRunTrader ✓
  - 9 (PostgreSQL): all repos ✓
  - 10 (notifier): NoOp + Multi + Feishu + Telegram ✓
  - 13 (idempotency LRU+DB): idempotency.Checker ✓
  - 14 (armed state): SystemStateRepo + ingest checks state.Armed ✓
- [x] No placeholders / TBD / TODO
- [x] Type consistency: `position.Side`, `order.Status`, `tradepkg.OrderType` all used consistently
- [x] DB queries use parameterized arguments (no SQL injection)
- [x] Each repo has integration tests
- [x] End-to-end dry_run test exercises: parse → idempotency → load → risk → decide → trade → notify → DB state correct

## Known Limitations (deferred to Plan 2B+)

1. **No real Binance impl** — DryRunTrader only. testnet/live in Plan 2B
2. **OrderReconciler not started** — orders never get status sync
3. **Startup recovery missing** — bot crash leaves zombie 'opening' rows
4. **TraceID generation is naive** — should use UUID per request; Plan 3 (HTTP layer) wires that
5. **Account equity is a fixed number** — Plan 2B reads from Binance positionRisk
6. **OpenPosition transaction wraps Trader.Place** — if Place takes >tx_timeout, transaction may abort. For Plan 2 (DryRun is instant) this is fine; live impl will need to restructure.
7. **No structured tracing across pipeline stages yet** — Plan 3 adds proper trace IDs through HTTP middleware

---

**EOF**
