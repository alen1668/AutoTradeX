package eval

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AllowedSinces is the canonical set of grayscale-report time windows
// shared by `cmd/agent-eval --since=...` and `GET /eval?since=...`.
// Anything outside this list falls back to DefaultSince.
var AllowedSinces = []string{"1h", "24h", "3d", "7d"}

// DefaultSince is the window used when no/invalid value is provided.
const DefaultSince = "3d"

// ParseSince resolves "1h" / "24h" / "3d" / "7d" to an absolute cutoff time.
// Values outside AllowedSinces return ok=false; callers should fall back to
// DefaultSince rather than erroring (matches the "silent fallback" UX
// described in the dashboard spec §3.4.2).
func ParseSince(s string) (time.Time, bool) {
	now := time.Now()
	switch s {
	case "1h":
		return now.Add(-time.Hour), true
	case "24h":
		return now.Add(-24 * time.Hour), true
	case "3d":
		return now.Add(-3 * 24 * time.Hour), true
	case "7d":
		return now.Add(-7 * 24 * time.Hour), true
	}
	return time.Time{}, false
}

// LoadEvalReport computes the grayscale-period report:
// 5 score buckets × realized-PnL plus the LLM-call health snapshot.
// Non-allowed `since` silently falls back to DefaultSince (the returned
// report.Since reflects the actual window used).
func LoadEvalReport(ctx context.Context, pool *pgxpool.Pool, since string) (EvalReport, error) {
	cutoff, ok := ParseSince(since)
	if !ok {
		since = DefaultSince
		cutoff, _ = ParseSince(since)
	}

	rep := EvalReport{
		Since:       since,
		GeneratedAt: time.Now().Unix(),
		Buckets:     emptyEvalBuckets(),
	}

	if err := loadBuckets(ctx, pool, cutoff, &rep); err != nil {
		return rep, err
	}

	scores, pnls, err := loadScoresAndPnLs(ctx, pool, cutoff)
	if err != nil {
		return rep, err
	}
	if len(scores) >= 2 {
		rep.Spearman = Spearman(scores, pnls)
	} else {
		rep.Spearman = math.NaN()
	}

	health, err := loadLLMHealth(ctx, pool, cutoff)
	if err != nil {
		return rep, err
	}
	rep.LLMHealth = health
	return rep, nil
}

// emptyEvalBuckets returns 5 zero-filled buckets in the canonical order.
func emptyEvalBuckets() []EvalBucket {
	labels := []string{"0-20", "20-40", "40-60", "60-80", "80-100"}
	out := make([]EvalBucket, 5)
	for i, l := range labels {
		out[i] = EvalBucket{Label: l, AvgPnL: math.NaN(), WinRate: math.NaN()}
	}
	return out
}

// loadBuckets populates rep.Buckets, rep.TotalSignals, rep.TotalTrades from
// signals JOINed to position_history via virtual_positions.
func loadBuckets(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time, rep *EvalReport) error {
	const q = `
SELECT
  CASE
    WHEN s.agent_score < 20 THEN '0-20'
    WHEN s.agent_score < 40 THEN '20-40'
    WHEN s.agent_score < 60 THEN '40-60'
    WHEN s.agent_score < 80 THEN '60-80'
    ELSE '80-100'
  END AS bucket,
  COUNT(*)                                                AS n_signals,
  COUNT(ph.id)                                            AS n_trades,
  COUNT(*) FILTER (WHERE ph.pnl_usdc > 0)                 AS wins,
  COALESCE(SUM(ph.pnl_usdc), 0)::float8                   AS sum_pnl
FROM signals s
LEFT JOIN virtual_positions vp ON vp.entry_signal_id = s.id
LEFT JOIN position_history ph
       ON ph.strategy_id = vp.strategy_id
      AND ph.symbol      = vp.symbol
      AND ph.opened_at   = vp.opened_at
WHERE s.agent_score IS NOT NULL
  AND s.received_at  >= $1
GROUP BY bucket`
	rows, err := pool.Query(ctx, q, cutoff)
	if err != nil {
		return err
	}
	defer rows.Close()
	bucketMap := map[string]EvalBucket{}
	for rows.Next() {
		var b EvalBucket
		if err := rows.Scan(&b.Label, &b.Signals, &b.Trades, &b.Wins, &b.SumPnL); err != nil {
			return err
		}
		if b.Trades > 0 {
			b.AvgPnL = b.SumPnL / float64(b.Trades)
			b.WinRate = float64(b.Wins) / float64(b.Trades) * 100
		} else {
			b.AvgPnL = math.NaN()
			b.WinRate = math.NaN()
		}
		bucketMap[b.Label] = b
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i, label := range []string{"0-20", "20-40", "40-60", "60-80", "80-100"} {
		if b, ok := bucketMap[label]; ok {
			rep.Buckets[i] = b
		}
	}
	for _, b := range rep.Buckets {
		rep.TotalSignals += b.Signals
		rep.TotalTrades += b.Trades
	}
	return nil
}

// loadScoresAndPnLs returns per-signal (agent_score, pnl_usdc) pairs for
// signals in the window that produced a closed trade. Used by Spearman.
func loadScoresAndPnLs(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time) ([]int, []float64, error) {
	rows, err := pool.Query(ctx, `
SELECT s.agent_score, ph.pnl_usdc::float8
FROM signals s
JOIN virtual_positions vp ON vp.entry_signal_id = s.id
JOIN position_history ph ON ph.strategy_id = vp.strategy_id
                        AND ph.symbol      = vp.symbol
                        AND ph.opened_at   = vp.opened_at
WHERE s.agent_score IS NOT NULL
  AND s.received_at  >= $1
  AND ph.pnl_usdc    IS NOT NULL`, cutoff)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var scores []int
	var pnls []float64
	for rows.Next() {
		var sc int
		var p float64
		if err := rows.Scan(&sc, &p); err != nil {
			return nil, nil, err
		}
		scores = append(scores, sc)
		pnls = append(pnls, p)
	}
	return scores, pnls, rows.Err()
}

// loadLLMHealth aggregates agent_evaluations in the window. agent_evaluations
// has no error_message column (commit 0007 schema), so TopFailReasons stays
// empty for Phase 1 — adding a structured failure-reason column is a
// later migration if/when we want it.
func loadLLMHealth(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time) (LLMHealth, error) {
	var h LLMHealth
	err := pool.QueryRow(ctx, `
SELECT
  COUNT(*),
  COUNT(*) FILTER (WHERE decision='failed'),
  COALESCE(AVG(latency_ms), 0)::int,
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms), 0)::int
FROM agent_evaluations
WHERE created_at >= $1`, cutoff).Scan(
		&h.TotalCalls, &h.FailedCalls, &h.AvgLatencyMs, &h.P95LatencyMs)
	if err != nil {
		return h, err
	}
	if h.TotalCalls > 0 {
		h.FailureRate = float64(h.FailedCalls) / float64(h.TotalCalls) * 100
	}
	return h, nil
}

// LoadInitSnapshot returns the server-rendered initial state injected into
// /eval as window.EVAL_INIT. Covers the last 24h so the front-end's
// rolling window has data on first paint. Cheap: 3 SQL queries.
func LoadInitSnapshot(ctx context.Context, pool *pgxpool.Pool) (InitData, error) {
	cutoff := time.Now().Add(-24 * time.Hour)
	out := InitData{}

	// 1) Per-signal score points.
	scoreRows, err := pool.Query(ctx, `
SELECT extract(epoch from e.created_at)::bigint, e.score, e.decision, s.symbol
  FROM agent_evaluations e
  JOIN signals s ON s.id = e.signal_id
 WHERE e.created_at >= $1 AND e.score IS NOT NULL
 ORDER BY e.created_at ASC`, cutoff)
	if err != nil {
		return out, fmt.Errorf("init scores: %w", err)
	}
	defer scoreRows.Close()
	for scoreRows.Next() {
		var p ScorePoint
		if err := scoreRows.Scan(&p.T, &p.Score, &p.Decision, &p.Symbol); err != nil {
			return out, err
		}
		out.Scores = append(out.Scores, p)
		switch {
		case p.Score < 20:
			out.Buckets[0]++
		case p.Score < 40:
			out.Buckets[1]++
		case p.Score < 60:
			out.Buckets[2]++
		case p.Score < 80:
			out.Buckets[3]++
		default:
			out.Buckets[4]++
		}
	}
	if err := scoreRows.Err(); err != nil {
		return out, err
	}

	// 2) Hourly LLM success rate.
	llmRows, err := pool.Query(ctx, `
SELECT extract(epoch from date_trunc('hour', created_at))::bigint  AS t,
       COUNT(*)                                                    AS total,
       COUNT(*) FILTER (WHERE decision <> 'failed')                AS successful
  FROM agent_evaluations
 WHERE created_at >= $1
 GROUP BY t
 ORDER BY t ASC`, cutoff)
	if err != nil {
		return out, fmt.Errorf("init llm: %w", err)
	}
	defer llmRows.Close()
	for llmRows.Next() {
		var p LLMRatePoint
		if err := llmRows.Scan(&p.T, &p.Total, &p.Successful); err != nil {
			return out, err
		}
		out.LLM = append(out.LLM, p)
	}
	if err := llmRows.Err(); err != nil {
		return out, err
	}

	// 3) Cumulative PnL.
	pnlRows, err := pool.Query(ctx, `
SELECT extract(epoch from closed_at)::bigint, pnl_usdc::float8
  FROM position_history
 WHERE closed_at >= $1 AND pnl_usdc IS NOT NULL
 ORDER BY closed_at ASC`, cutoff)
	if err != nil {
		return out, fmt.Errorf("init pnl: %w", err)
	}
	defer pnlRows.Close()
	var cum float64
	for pnlRows.Next() {
		var t int64
		var pnl float64
		if err := pnlRows.Scan(&t, &pnl); err != nil {
			return out, err
		}
		cum += pnl
		out.PnL = append(out.PnL, PnLPoint{T: t, Cum: cum})
	}
	if err := pnlRows.Err(); err != nil {
		return out, err
	}

	return out, nil
}
