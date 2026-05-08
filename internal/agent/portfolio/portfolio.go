// Package portfolio aggregates the bot's currently-open positions and
// today's realized PnL into a single PortfolioSnapshot that the agent
// scorer feeds the LLM.
package portfolio

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
	"github.com/lizhaojie/tvbot/internal/store"
)

// Repo is the minimum store surface this provider needs. The two methods
// come from PositionHistoryRepo and VirtualPositionRepo; the Provider
// receives an adapter (defined in cmd/tvbot/main.go) that satisfies this
// interface.
type Repo interface {
	ListActive(ctx context.Context, q store.Querier) ([]*store.VirtualPositionRow, error)
	DailyRealizedPnL(ctx context.Context, q store.Querier, day time.Time) (decimal.Decimal, error)
}

type Provider struct {
	repo Repo
	pool *pgxpool.Pool
	log  zerolog.Logger
}

func New(repo Repo, pool *pgxpool.Pool) *Provider {
	return &Provider{repo: repo, pool: pool}
}

// WithLogger enables warn logging on degraded paths.
func (p *Provider) WithLogger(l zerolog.Logger) *Provider {
	p.log = l
	return p
}

// Snapshot pulls active virtual positions and today's realized PnL.
// Failure (DB error) returns (nil, nil) — the agent layer is degradable:
// the prompt notes "仓位数据暂不可用" and the LLM still scores from the
// remaining inputs.
//
// V1 leaves UnrealizedPnL at zero. Computing it would require a live mark
// price for each open symbol; the spec parks that as future work because
// it doesn't dominate the LLM judgment and adds another exchange call
// per signal.
func (p *Provider) Snapshot(ctx context.Context) (*scorer.PortfolioSnapshot, error) {
	vps, err := p.repo.ListActive(ctx, p.pool)
	if err != nil {
		p.log.Warn().Err(err).Msg("portfolio: ListActive failed; returning nil")
		return nil, nil
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	pnl, err := p.repo.DailyRealizedPnL(ctx, p.pool, today)
	if err != nil {
		p.log.Warn().Err(err).Msg("portfolio: DailyRealizedPnL failed; returning nil")
		return nil, nil
	}

	var totalNotional decimal.Decimal
	out := make([]scorer.OpenPosition, 0, len(vps))
	for _, vp := range vps {
		// Use entry fill price when known; otherwise the entry signal price.
		// fill is decimal.Zero by default — IsZero() catches the not-yet-filled case.
		entry := vp.EntryFillPrice
		if entry.IsZero() {
			entry = vp.EntrySignalPrice
		}
		notional := entry.Mul(vp.Qty)
		totalNotional = totalNotional.Add(notional)
		out = append(out, scorer.OpenPosition{
			StrategyID:    vp.StrategyID,
			Symbol:        vp.Symbol,
			Direction:     vp.Side,
			EntryPrice:    entry,
			NotionalUSD:   notional,
			UnrealizedPnL: decimal.Zero,
		})
	}

	return &scorer.PortfolioSnapshot{
		TotalNotionalUSD: totalNotional,
		OpenPositions:    out,
		DailyPnLUSD:      pnl,
	}, nil
}
