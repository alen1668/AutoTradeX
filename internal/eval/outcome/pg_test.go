package outcome

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestPGRepo_PendingEvaluations_BasicFilters seeds 4 signals + evals and
// asserts only the eligible ones return.
func TestPGRepo_PendingEvaluations_BasicFilters(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// Seed strategies (FK from virtual_positions / position_history)
	mustExec(t, pool, `INSERT INTO strategies (id, symbol, leverage, size_usdc, stop_loss_pct, max_open_usdc) VALUES ('s1','BTCUSDT',5,100,1.0,1000)`)

	// 4 signals:
	//   id=1 kind=long  → eligible (no outcome yet)
	//   id=2 kind=short → eligible
	//   id=3 kind=exit_long → SKIPPED (exit signal)
	//   id=4 kind=long  → SKIPPED (already labeled)
	now := time.Now().UTC().Add(-2 * time.Hour) // older than minAgeMin
	tvMs := now.UnixMilli()
	mustExec(t, pool, `INSERT INTO signals (id, strategy_id, symbol, kind, signal_price, tv_timestamp_ms, received_at, raw_payload, decision, trace_id) VALUES
		(1,'s1','BTCUSDT','long',      100, $1, $2, '{}', 'accepted', 't1')`, tvMs, now)
	mustExec(t, pool, `INSERT INTO signals (id, strategy_id, symbol, kind, signal_price, tv_timestamp_ms, received_at, raw_payload, decision, trace_id) VALUES
		(2,'s1','BTCUSDT','short',     200, $1, $2, '{}', 'accepted', 't2')`, tvMs+1, now)
	mustExec(t, pool, `INSERT INTO signals (id, strategy_id, symbol, kind, signal_price, tv_timestamp_ms, received_at, raw_payload, decision, trace_id) VALUES
		(3,'s1','BTCUSDT','exit_long', 100, $1, $2, '{}', 'accepted', 't3')`, tvMs+2, now)
	mustExec(t, pool, `INSERT INTO signals (id, strategy_id, symbol, kind, signal_price, tv_timestamp_ms, received_at, raw_payload, decision, trace_id) VALUES
		(4,'s1','BTCUSDT','long',      100, $1, $2, '{}', 'accepted', 't4')`, tvMs+3, now)

	mustExec(t, pool, `INSERT INTO agent_evaluations (signal_id, model, prompt_hash, decision, reasoning, history_json, prompt_text, latency_ms, created_at) VALUES
		(1, 'm', 'h', 'approve', 'r', '{}', 'p', 0, $1),
		(2, 'm', 'h', 'abandon', 'r', '{}', 'p', 0, $1),
		(3, 'm', 'h', 'approve', 'r', '{}', 'p', 0, $1),
		(4, 'm', 'h', 'approve', 'r', '{}', 'p', 0, $1)`, now)
	mustExec(t, pool, `UPDATE agent_evaluations SET outcome_label='win', outcome_horizon_min=60, outcome_computed_at=now() WHERE signal_id=4`)

	r := NewPGRepo(pool)
	rows, err := r.PendingEvaluations(ctx, 100, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 eligible rows, got %d (%+v)", len(rows), rows)
	}
	dirs := map[int64]string{}
	for _, row := range rows {
		dirs[row.SignalID] = row.Direction
	}
	if dirs[1] != "buy" {
		t.Fatalf("signal 1 (long) → want buy, got %q", dirs[1])
	}
	if dirs[2] != "sell" {
		t.Fatalf("signal 2 (short) → want sell, got %q", dirs[2])
	}
}

// TestPGRepo_PositionPnL covers both have-row and no-row paths.
// position_history is linked to signals via virtual_positions
// (vp.entry_signal_id = signal.id, ph joined via strategy_id+symbol+opened_at).
func TestPGRepo_PositionPnL(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	mustExec(t, pool, `INSERT INTO strategies (id, symbol, leverage, size_usdc, stop_loss_pct, max_open_usdc) VALUES ('s1','BTCUSDT',5,100,1.0,1000)`)
	openedAt := time.Now().UTC().Add(-1 * time.Hour)
	mustExec(t, pool, `INSERT INTO signals (id, strategy_id, symbol, kind, signal_price, tv_timestamp_ms, raw_payload, decision, trace_id) VALUES
		(1,'s1','BTCUSDT','long', 100, 1, '{}', 'accepted', 't1'),
		(2,'s1','BTCUSDT','long', 100, 2, '{}', 'accepted', 't2')`)

	// virtual_position for signal 1 with a known opened_at
	mustExec(t, pool, `INSERT INTO virtual_positions
		(strategy_id, symbol, side, qty, entry_signal_price, entry_signal_id, status, opened_at)
		VALUES ('s1','BTCUSDT','long', 1, 100, 1, 'closed', $1)`, openedAt)

	// position_history matched via strategy_id + symbol + opened_at
	mustExec(t, pool, `INSERT INTO position_history (
		strategy_id, symbol, side, qty, entry_signal_price, entry_fill_price,
		exit_signal_price, exit_fill_price, pnl_usdc, pnl_pct, fees_usdc,
		close_reason, duration_seconds, opened_at, closed_at
	) VALUES ('s1','BTCUSDT','long', 1, 100, 100, 105, 105, 42.5, 5, 0,
		'take_profit', 60, $1, now())`, openedAt)

	r := NewPGRepo(pool)
	// signal 1 has a position
	pnl, err := r.PositionPnL(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if pnl == nil || !pnl.Equal(decimal.NewFromFloat(42.5)) {
		t.Fatalf("want 42.5, got %v", pnl)
	}
	// signal 2 has no position
	pnl, err = r.PositionPnL(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if pnl != nil {
		t.Fatalf("want nil for no-position signal, got %v", pnl)
	}
}

// TestPGRepo_WriteOutcome_IdempotentOnExisting verifies that writes don't
// clobber an already-set outcome_label.
func TestPGRepo_WriteOutcome_IdempotentOnExisting(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	mustExec(t, pool, `INSERT INTO strategies (id, symbol, leverage, size_usdc, stop_loss_pct, max_open_usdc) VALUES ('s1','BTCUSDT',5,100,1.0,1000)`)
	mustExec(t, pool, `INSERT INTO signals (id, strategy_id, symbol, kind, signal_price, tv_timestamp_ms, raw_payload, decision, trace_id) VALUES (1,'s1','BTCUSDT','long', 100, 1, '{}', 'accepted', 't1')`)
	mustExec(t, pool, `INSERT INTO agent_evaluations (signal_id, model, prompt_hash, decision, reasoning, history_json, prompt_text, latency_ms) VALUES (1, 'm', 'h', 'approve', 'r', '{}', 'p', 0)`)

	r := NewPGRepo(pool)
	pnl := decimal.NewFromFloat(10)
	res := Result{Label: LabelWin, PnLUSD: &pnl, HorizonMin: 60, ComputedAt: time.Now().UTC()}
	if err := r.WriteOutcome(ctx, 1, res); err != nil {
		t.Fatal(err)
	}
	// Attempt to overwrite — should be a no-op
	res2 := Result{Label: LabelLoss, PnLUSD: &pnl, HorizonMin: 60, ComputedAt: time.Now().UTC()}
	if err := r.WriteOutcome(ctx, 1, res2); err != nil {
		t.Fatal(err)
	}

	var lab string
	if err := pool.QueryRow(ctx, `SELECT outcome_label FROM agent_evaluations WHERE signal_id=1`).Scan(&lab); err != nil {
		t.Fatal(err)
	}
	if lab != "win" {
		t.Fatalf("want label still 'win' after re-write attempt, got %q", lab)
	}
}
