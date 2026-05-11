//go:build integration

package eval

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStore_CreateAndGetRun(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	id, err := s.CreateRun(ctx, ReplayRun{
		SinceWindow:  "7d",
		SinceCutoff:  time.Now().Add(-7 * 24 * time.Hour).Unix(),
		MaxN:         100,
		Concurrency:  3,
		Model:        "claude-sonnet-4-6",
		PromptText:   "hello {{ .Symbol }}",
		PromptSHA256: "abc123",
		Status:       "pending",
	})
	require.NoError(t, err)
	require.Greater(t, id, int64(0))

	got, err := s.GetRun(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "7d", got.SinceWindow)
	require.Equal(t, "pending", got.Status)
	require.Equal(t, "claude-sonnet-4-6", got.Model)
}

func TestStore_GetRun_NotFound(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	got, err := s.GetRun(context.Background(), 99999)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestStore_ListRuns_Cursor(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	var ids []int64
	for i := 0; i < 5; i++ {
		id, err := s.CreateRun(ctx, ReplayRun{
			SinceWindow:  "7d",
			SinceCutoff:  time.Now().Unix(),
			Model:        "m",
			PromptText:   "p",
			PromptSHA256: "h",
			Status:       "done",
		})
		require.NoError(t, err)
		ids = append(ids, id)
	}
	page1, next, err := s.ListRuns(ctx, 0, 2)
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.Equal(t, ids[4], page1[0].ID) // newest first
	require.Equal(t, ids[3], page1[1].ID)
	require.Equal(t, ids[3], next)

	page2, _, err := s.ListRuns(ctx, next, 2)
	require.NoError(t, err)
	require.Len(t, page2, 2)
	require.Equal(t, ids[2], page2[0].ID)
}

func TestStore_MarkRunDone_WithSummary(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	id, _ := s.CreateRun(ctx, ReplayRun{
		SinceWindow:  "7d",
		SinceCutoff:  time.Now().Unix(),
		Model:        "m",
		PromptText:   "p",
		PromptSHA256: "h",
		Status:       "running",
	})
	rep := ReplayReport{SampleSize: 42, V1Spearman: 0.3, V2Spearman: 0.4}
	require.NoError(t, s.MarkRunDone(ctx, id, &rep, 42, 0))

	got, _ := s.GetRun(ctx, id)
	require.Equal(t, "done", got.Status)
	require.NotNil(t, got.Summary)
	require.Equal(t, 42, got.Summary.SampleSize)

	raw, _ := json.Marshal(got.Summary)
	require.Contains(t, string(raw), `"sample_size":42`)
}

func TestStore_InsertRow_UniqueConstraint(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	// Seed: insert a signals row so the FK passes.
	var sigID int64
	err := pool.QueryRow(ctx, `
INSERT INTO signals (strategy_id, symbol, kind, signal_price, tv_timestamp_ms,
                     raw_payload, decision, trace_id)
VALUES ('s1', 'BTCUSDT', 'long', 50000, $1, '{}'::jsonb, 'accepted', 'tx1')
RETURNING id`, time.Now().UnixMilli()).Scan(&sigID)
	require.NoError(t, err)

	runID, err := s.CreateRun(ctx, ReplayRun{
		SinceWindow: "7d", SinceCutoff: time.Now().Unix(), Model: "m",
		PromptText: "p", PromptSHA256: "h", Status: "running",
	})
	require.NoError(t, err)

	pnl := 12.5
	row := ReplayRow{SignalID: sigID, NewScore: 75, NewDecision: "approve",
		OldScore: 30, OldDecision: "abandon", PnLUSDC: &pnl}
	require.NoError(t, s.InsertRow(ctx, runID, row))

	// Duplicate (run_id, signal_id) must fail.
	err = s.InsertRow(ctx, runID, row)
	require.Error(t, err, "expected UNIQUE violation on (run_id, signal_id)")
}

func TestStore_ListStaleRunning(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	id, _ := s.CreateRun(ctx, ReplayRun{
		SinceWindow:  "7d",
		SinceCutoff:  time.Now().Unix(),
		Model:        "m",
		PromptText:   "p",
		PromptSHA256: "h",
		Status:       "running",
	})
	// Backdate started_at past 1h threshold.
	_, err := pool.Exec(ctx,
		`UPDATE replay_runs SET started_at = now() - interval '2 hours' WHERE id = $1`, id)
	require.NoError(t, err)

	stale, err := s.ListStaleRunning(ctx, time.Hour)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	require.Equal(t, id, stale[0].ID)

	// A fresh running run (started 5 minutes ago) must NOT appear.
	freshID, _ := s.CreateRun(ctx, ReplayRun{
		SinceWindow: "7d", SinceCutoff: time.Now().Unix(), Model: "m",
		PromptText: "p", PromptSHA256: "h", Status: "running",
	})
	_, _ = pool.Exec(ctx,
		`UPDATE replay_runs SET started_at = now() - interval '5 minutes' WHERE id = $1`, freshID)
	stale, _ = s.ListStaleRunning(ctx, time.Hour)
	require.Len(t, stale, 1, "fresh running run must not be listed")
}

func TestStore_ListRows_OrderByDeltaScore(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	// Seed three signals.
	sigIDs := make([]int64, 3)
	for i := 0; i < 3; i++ {
		err := pool.QueryRow(ctx, `
INSERT INTO signals (strategy_id, symbol, kind, signal_price, tv_timestamp_ms,
                     raw_payload, decision, trace_id)
VALUES ('s', 'BTCUSDT', 'long', 50000, $1, '{}'::jsonb, 'accepted', $2)
RETURNING id`, time.Now().UnixMilli()+int64(i), "tx"+string(rune('a'+i))).Scan(&sigIDs[i])
		require.NoError(t, err)
	}

	runID, _ := s.CreateRun(ctx, ReplayRun{
		SinceWindow: "7d", SinceCutoff: time.Now().Unix(), Model: "m",
		PromptText: "p", PromptSHA256: "h", Status: "running",
	})

	// Insert rows with varying |delta|: 5, 30, 80.
	require.NoError(t, s.InsertRow(ctx, runID,
		ReplayRow{SignalID: sigIDs[0], OldScore: 50, NewScore: 55, OldDecision: "approve", NewDecision: "approve"}))
	require.NoError(t, s.InsertRow(ctx, runID,
		ReplayRow{SignalID: sigIDs[1], OldScore: 10, NewScore: 90, OldDecision: "abandon", NewDecision: "approve"}))
	require.NoError(t, s.InsertRow(ctx, runID,
		ReplayRow{SignalID: sigIDs[2], OldScore: 70, NewScore: 40, OldDecision: "approve", NewDecision: "abandon"}))

	rows, err := s.ListRows(ctx, runID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	// |90-10|=80 > |70-40|=30 > |55-50|=5
	require.Equal(t, sigIDs[1], rows[0].SignalID)
	require.Equal(t, sigIDs[2], rows[1].SignalID)
	require.Equal(t, sigIDs[0], rows[2].SignalID)
}
