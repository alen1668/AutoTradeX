// Package history projects the store layer's position_history rows down
// to the simpler scorer.HistoricalTrade view consumed by the prompt.
package history

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
	"github.com/lizhaojie/tvbot/internal/store"
)

// Querier is the minimum store surface this package needs. Concrete impl
// is *store.PositionHistoryRepo.
type Querier interface {
	ListBySymbolAndStrategy(ctx context.Context, q store.Querier, strategyID, symbol string, limit int) ([]*store.PositionHistoryRow, error)
	ListByStrategy(ctx context.Context, q store.Querier, strategyID string, limit int) ([]*store.PositionHistoryRow, error)
}

type Provider struct {
	q    Querier
	pool *pgxpool.Pool
	log  zerolog.Logger
}

func New(q Querier, pool *pgxpool.Pool) *Provider {
	return &Provider{q: q, pool: pool}
}

// WithLogger attaches a logger; failures here are non-fatal (returned
// trade slice is empty, the LLM still scores).
func (p *Provider) WithLogger(l zerolog.Logger) *Provider {
	p.log = l
	return p
}

// SymbolHistory returns the most-recent N closed trades for the given
// strategyID + symbol. DB error → log warn + return empty slice (so the
// scorer keeps going with a "0 笔" hint).
func (p *Provider) SymbolHistory(ctx context.Context, strategyID, symbol string, limit int) ([]scorer.HistoricalTrade, error) {
	rows, err := p.q.ListBySymbolAndStrategy(ctx, p.pool, strategyID, symbol, limit)
	if err != nil {
		p.log.Warn().Err(err).Msg("history: SymbolHistory query failed; returning empty")
		return nil, nil
	}
	return mapRows(rows), nil
}

// StrategyHistory returns the most-recent N closed trades for the given
// strategy across ALL symbols (gives the LLM cross-symbol context).
func (p *Provider) StrategyHistory(ctx context.Context, strategyID string, limit int) ([]scorer.HistoricalTrade, error) {
	rows, err := p.q.ListByStrategy(ctx, p.pool, strategyID, limit)
	if err != nil {
		p.log.Warn().Err(err).Msg("history: StrategyHistory query failed; returning empty")
		return nil, nil
	}
	return mapRows(rows), nil
}

func mapRows(rows []*store.PositionHistoryRow) []scorer.HistoricalTrade {
	out := make([]scorer.HistoricalTrade, 0, len(rows))
	for _, r := range rows {
		durMin := r.DurationSeconds / 60
		out = append(out, scorer.HistoricalTrade{
			OpenedAt:    r.OpenedAt,
			Symbol:      r.Symbol,
			Direction:   r.Side,
			EntryPrice:  r.EntryFillPrice,
			ExitPrice:   r.ExitFillPrice,
			PnLUSD:      r.PnLUSDC,
			DurationMin: durMin,
			ExitReason:  r.CloseReason,
		})
	}
	return out
}
