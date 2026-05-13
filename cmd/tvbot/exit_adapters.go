package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/exit"
	"github.com/lizhaojie/tvbot/internal/agent/market"
	"github.com/lizhaojie/tvbot/internal/store"
)

// exitPriceProvider is exit.PriceProvider on top of market.Provider —
// returns the latest 1h close as a "current price". Cheap (provider
// caches 30 s per symbol), accuracy ~1m drift acceptable for Exit Agent
// decisions which run every 5 min.
type exitPriceProvider struct{ p *market.Provider }

func (a exitPriceProvider) Price(ctx context.Context, symbol string) (decimal.Decimal, error) {
	if a.p == nil {
		return decimal.Zero, fmt.Errorf("market provider unavailable")
	}
	mc, err := a.p.GetContext(ctx, symbol)
	if err != nil || mc == nil {
		return decimal.Zero, fmt.Errorf("price unavailable for %s", symbol)
	}
	if len(mc.KlineLookback1h) == 0 {
		return decimal.Zero, fmt.Errorf("kline lookback empty for %s", symbol)
	}
	return mc.KlineLookback1h[len(mc.KlineLookback1h)-1], nil
}

// exitKlineProvider is exit.KlineProvider on top of market.Provider's
// 1h closes. Renders a simple newline-separated text block; total length
// stays well under the prompt budget even with 60 entries.
type exitKlineProvider struct{ p *market.Provider }

func (a exitKlineProvider) Snapshot(ctx context.Context, symbol string) (string, error) {
	if a.p == nil {
		return "", fmt.Errorf("market provider unavailable")
	}
	mc, err := a.p.GetContext(ctx, symbol)
	if err != nil || mc == nil {
		return "", fmt.Errorf("kline unavailable for %s", symbol)
	}
	closes := mc.KlineLookback1h
	if len(closes) == 0 {
		return "", fmt.Errorf("kline lookback empty for %s", symbol)
	}
	last := closes[len(closes)-1]
	var sb strings.Builder
	fmt.Fprintf(&sb, "1h 最新收盘: %s\n", last.StringFixed(4))
	fmt.Fprintf(&sb, "24h 变化: %s%%    24h 高/低: %s / %s\n",
		mc.Last24hChangePct.StringFixed(2),
		mc.Last24hHigh.StringFixed(4),
		mc.Last24hLow.StringFixed(4))
	fmt.Fprintf(&sb, "1h 振幅: %s%%    24h 波动率: %s\n",
		mc.Last1hChangePct.StringFixed(2),
		mc.Volatility24h.StringFixed(4))
	from := 0
	if len(closes) > 24 {
		from = len(closes) - 24
	}
	fmt.Fprintf(&sb, "最近 %d 根 1h 收盘: ", len(closes)-from)
	for i := from; i < len(closes); i++ {
		if i > from {
			sb.WriteString(", ")
		}
		sb.WriteString(closes[i].StringFixed(4))
	}
	return sb.String(), nil
}

// exitHistoricalProvider aggregates the last 7d of agent_evaluations
// for the (strategy_id, symbol) pair, reading outcome_label/pct columns
// added in migration 0013.
//
// Joins agent_evaluations → signals to filter by strategy_id & symbol.
type exitHistoricalProvider struct{ pool *pgxpool.Pool }

func (a exitHistoricalProvider) Stats(ctx context.Context, strategyID, symbol string) (exit.HistoricalStats, error) {
	if a.pool == nil {
		return exit.HistoricalStats{}, fmt.Errorf("pool nil")
	}
	row := a.pool.QueryRow(ctx, `
SELECT
  COUNT(*)::int                                                                AS sample_size,
  COALESCE(AVG((outcome_label='win')::int)::numeric, 0)                         AS win_rate,
  COALESCE(AVG(outcome_pnl_pct) FILTER (WHERE outcome_label='win'), 0)          AS avg_win_pct,
  COALESCE(AVG(outcome_pnl_pct) FILTER (WHERE outcome_label='loss'), 0)         AS avg_loss_pct,
  COALESCE(AVG(outcome_horizon_min)::int, 0)                                    AS avg_hold_min
FROM agent_evaluations e
JOIN signals s ON s.id = e.signal_id
WHERE s.strategy_id = $1
  AND s.symbol      = $2
  AND e.created_at  >= now() - interval '7 days'
  AND e.outcome_label IN ('win','loss','flat')
`, strategyID, symbol)
	var stats exit.HistoricalStats
	if err := row.Scan(&stats.SampleSize, &stats.WinRate, &stats.AvgWinPct, &stats.AvgLossPct, &stats.AvgHoldMinutes); err != nil {
		return exit.HistoricalStats{}, err
	}
	return stats, nil
}

// exitPinnedProvider wraps store.CritiqueRepo.PinnedPatterns for the
// exit prompt. Critique patterns are shared across entry (scorer) and
// exit prompts so operators only need one place to manage them.
type exitPinnedProvider struct{ repo *store.CritiqueRepo }

func (a exitPinnedProvider) List(ctx context.Context, limit int) ([]exit.PinnedPattern, error) {
	rows, err := a.repo.PinnedPatterns(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]exit.PinnedPattern, 0, len(rows))
	for _, r := range rows {
		out = append(out, exit.PinnedPattern{Title: r.Title, Suggestion: r.Suggestion})
	}
	return out, nil
}

// noopExitExecutor refuses every action. Task 15 swaps this with the
// real trade.ExitOrchestrator binding. As long as exit_agent_mode=shadow
// the worker never calls Executor; this no-op only exists so the type
// system is satisfied.
type noopExitExecutor struct{}

func (noopExitExecutor) TightenSL(_ context.Context, _ int64, _ decimal.Decimal) error {
	return fmt.Errorf("exit: no orchestrator wired yet (shadow only)")
}
func (noopExitExecutor) TakePartial(_ context.Context, _ int64, _ decimal.Decimal) error {
	return fmt.Errorf("exit: no orchestrator wired yet (shadow only)")
}
func (noopExitExecutor) ExitNow(_ context.Context, _ int64) error {
	return fmt.Errorf("exit: no orchestrator wired yet (shadow only)")
}

// exitRecorderAdapter satisfies exit.ExecutionRecorder via ExitDecisionRepo.
type exitRecorderAdapter struct{ repo *store.ExitDecisionRepo }

func (a exitRecorderAdapter) SetExecution(ctx context.Context, decisionID int64, executedAt *time.Time, status string, errMsg string) error {
	return a.repo.SetExecution(ctx, decisionID, executedAt, status, errMsg)
}
