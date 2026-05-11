//go:build integration

package eval

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// seedEvaluation inserts one row into agent_evaluations + signals for cost-
// estimator tests. Returns the new signal id.
func seedEvaluation(t *testing.T, ctx context.Context, pool *pgxpool.Pool, model string, score, tokenIn, tokenOut int, receivedAt time.Time) int64 {
	t.Helper()
	var sigID int64
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO signals (strategy_id, symbol, kind, signal_price, tv_timestamp_ms,
                     raw_payload, decision, trace_id, agent_score, received_at)
VALUES ('s', 'BTCUSDT', 'long', 50000, $1, '{}'::jsonb, 'accepted', $2, $3, $4)
RETURNING id`,
		time.Now().UnixMilli(),
		"tx"+model+time.Now().Format("150405.000000000"),
		score, receivedAt,
	).Scan(&sigID))

	_, err := pool.Exec(ctx, `
INSERT INTO agent_evaluations (signal_id, model, prompt_hash, score, decision,
                                reasoning, history_json, prompt_text,
                                latency_ms, token_in, token_out, created_at)
VALUES ($1, $2, 'h', $3, 'approve', 'ok', '{}'::jsonb, 'pp', 100, $4, $5, $6)`,
		sigID, model, score, tokenIn, tokenOut, receivedAt)
	require.NoError(t, err)
	return sigID
}

func TestEstimateCost_FromHistory(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	now := time.Now()

	for i := 0; i < 11; i++ {
		// 11 signals all in 1h window so SampleCount=11 from query.
		seedEvaluation(t, ctx, pool, "claude-sonnet-4-6", 50, 2000, 500, now.Add(-30*time.Minute))
	}

	est, err := EstimateCost(ctx, pool, "1h", "claude-sonnet-4-6")
	require.NoError(t, err)
	require.Equal(t, 11, est.SampleCount)
	require.Equal(t, 2000, est.AvgTokenIn)
	require.Equal(t, 500, est.AvgTokenOut)
	require.Equal(t, "history", est.Source)
	// 11 × (2000 × 3/1M + 500 × 15/1M) = 11 × 0.0135 = 0.1485
	require.InDelta(t, 0.1485, est.TotalUSD, 1e-6)
}

func TestEstimateCost_FallbackWhenHistoryShort(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	now := time.Now()

	// Only 4 history rows for the model (< 5 threshold). seedEvaluation
	// inserts both a signal and an evaluation; SampleCount = 4.
	for i := 0; i < 4; i++ {
		seedEvaluation(t, ctx, pool, "claude-sonnet-4-6", 50, 99999, 99999, now.Add(-30*time.Minute))
	}

	est, err := EstimateCost(ctx, pool, "1h", "claude-sonnet-4-6")
	require.NoError(t, err)
	require.Equal(t, "fallback", est.Source)
	require.Equal(t, 3500, est.AvgTokenIn) // fallback value, not 99999
	require.Equal(t, 800, est.AvgTokenOut)
	require.Equal(t, 4, est.SampleCount)
}

func TestEstimateCost_UnknownModel(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	_, err := EstimateCost(ctx, pool, "1h", "gpt-9000")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown model")
}
