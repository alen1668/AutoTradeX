//go:build integration

package eval

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseSince(t *testing.T) {
	for _, s := range AllowedSinces {
		cutoff, ok := ParseSince(s)
		require.True(t, ok, "%q must be allowed", s)
		require.False(t, cutoff.IsZero(), "%q produced zero cutoff", s)
		require.True(t, cutoff.Before(time.Now()),
			"%q cutoff must be in the past", s)
	}
	for _, s := range []string{"abc", "", "30d", "5d", "1d"} {
		_, ok := ParseSince(s)
		require.False(t, ok, "%q must NOT be allowed", s)
	}
}

func TestLoadEvalReport_EmptyDB(t *testing.T) {
	pool := newTestPool(t)
	rep, err := LoadEvalReport(context.Background(), pool, "3d")
	require.NoError(t, err)
	require.Equal(t, "3d", rep.Since)
	require.Equal(t, 0, rep.TotalSignals)
	require.Len(t, rep.Buckets, 5)
	for _, b := range rep.Buckets {
		require.Equal(t, 0, b.Signals)
		require.True(t, math.IsNaN(b.AvgPnL))
		require.True(t, math.IsNaN(b.WinRate))
	}
	require.True(t, math.IsNaN(rep.Spearman), "Spearman is NaN with <2 samples")
}

func TestLoadEvalReport_InvalidSinceFallsBackTo3d(t *testing.T) {
	pool := newTestPool(t)
	rep, err := LoadEvalReport(context.Background(), pool, "abc")
	require.NoError(t, err)
	require.Equal(t, "3d", rep.Since)
}

func TestLoadEvalReport_With30dAlsoFallsBack(t *testing.T) {
	pool := newTestPool(t)
	rep, err := LoadEvalReport(context.Background(), pool, "30d")
	require.NoError(t, err)
	require.Equal(t, "3d", rep.Since, "30d not in AllowedSinces; should fall back")
}

func TestLoadEvalReport_BucketsScoredSignal(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Seed: one signal at score=75 (→ "60-80" bucket).
	_, err := pool.Exec(ctx, `
INSERT INTO signals (strategy_id, symbol, kind, signal_price, tv_timestamp_ms,
                     raw_payload, decision, trace_id,
                     agent_score, agent_decision)
VALUES ('s1', 'BTCUSDT', 'long', 50000, $1, '{}'::jsonb, 'accepted', 'tx',
        75, 'approve')`, time.Now().UnixMilli())
	require.NoError(t, err)

	rep, err := LoadEvalReport(ctx, pool, "3d")
	require.NoError(t, err)
	require.Equal(t, 1, rep.TotalSignals)
	for _, b := range rep.Buckets {
		if b.Label == "60-80" {
			require.Equal(t, 1, b.Signals)
		} else {
			require.Equal(t, 0, b.Signals)
		}
	}
}
