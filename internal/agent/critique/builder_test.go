package critique

import (
	"context"
	"testing"
	"time"
)

func TestPGDataReader_AggregatesAndDetails(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	now := time.Now().UTC()

	mustExec(t, pool, `INSERT INTO strategies (id, symbol, leverage, size_usdc, stop_loss_pct, max_open_usdc) VALUES ('s-trend','BTCUSDT',5,100,1.0,1000)`)
	// 4 signals: 3 with win/loss/flat outcomes (eligible), 1 unlabeled (ignored).
	// tv_timestamp_ms must be unique per (strategy_id, tv_timestamp_ms) constraint.
	for i := int64(1); i <= 4; i++ {
		mustExec(t, pool, `INSERT INTO signals (id, strategy_id, symbol, kind, signal_price, tv_timestamp_ms, raw_payload, decision, trace_id)
			VALUES ($1, 's-trend', 'BTCUSDT', 'long', 100, $2, '{}', 'accepted', $3)`,
			i, now.UnixMilli()+i, "t"+itoa(i))
	}
	mustExec(t, pool, `INSERT INTO agent_evaluations (signal_id, model, prompt_hash, score, decision, reasoning, history_json, prompt_text, latency_ms, created_at, outcome_label, outcome_pnl_pct) VALUES
		(1, 'm', 'h', 70, 'approve', '理由 A', '{}', 'p', 0, $1, 'win',  0.005),
		(2, 'm', 'h', 60, 'approve', '理由 B', '{}', 'p', 0, $1, 'loss', -0.006),
		(3, 'm', 'h', 50, 'abandon', '理由 C', '{}', 'p', 0, $1, 'flat', 0.001),
		(4, 'm', 'h', 40, 'approve', '理由 D', '{}', 'p', 0, $1, NULL,   NULL)`, now)

	r := NewPGDataReader(pool)

	// Aggregates: 3 outcome buckets (win/loss/flat) for strategy s-trend.
	aggs, err := r.Aggregates(ctx, now.Add(-time.Hour), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(aggs) != 3 {
		t.Fatalf("want 3 aggregate rows (win/loss/flat), got %d: %+v", len(aggs), aggs)
	}
	for _, a := range aggs {
		if a.StrategyID != "s-trend" {
			t.Fatalf("unexpected strategy: %s", a.StrategyID)
		}
		if a.Count != 1 {
			t.Fatalf("each bucket should have 1 row, got %d for %s", a.Count, a.Outcome)
		}
	}

	// Details: 3 labeled rows (4 is unlabeled → excluded).
	dets, err := r.Details(ctx, now.Add(-time.Hour), now.Add(time.Hour), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(dets) != 3 {
		t.Fatalf("want 3 detail rows, got %d", len(dets))
	}
}

func TestPGDataReader_PreviousSummary(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	now := time.Now().UTC()

	r := NewPGDataReader(pool)
	// No critiques yet → empty.
	s, err := r.PreviousSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Fatalf("expected empty, got %q", s)
	}

	// Insert one done critique with summary.
	mustExec(t, pool, `INSERT INTO agent_critiques
		(window_start, window_end, sample_size, model, prompt_hash, summary, status)
		VALUES ($1, $1, 10, 'm', 'h', '过去 7 天 trend 下做多偏激进', 'done')`, now)
	s, err = r.PreviousSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s != "过去 7 天 trend 下做多偏激进" {
		t.Fatalf("got %q", s)
	}

	// Insert a newer 'failed' critique — PreviousSummary should still return
	// the older 'done' one because status filter is strict.
	mustExec(t, pool, `INSERT INTO agent_critiques
		(window_start, window_end, sample_size, model, prompt_hash, summary, status, error_message)
		VALUES ($1, $1, 5, 'm', 'h', 'this should not be returned', 'failed', 'llm error')`, now.Add(time.Minute))
	s, err = r.PreviousSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s != "过去 7 天 trend 下做多偏激进" {
		t.Fatalf("status filter broke; got %q", s)
	}
}

// itoa is a tiny helper for inline int→string within INSERTs.
func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	buf := [20]byte{}
	n := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}
