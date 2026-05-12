package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/market"
	"github.com/lizhaojie/tvbot/internal/agent/perpmetrics"
	"github.com/lizhaojie/tvbot/internal/store"
)

// regimeRepoAdapter wraps store.MarketRegimeRepo + *pgxpool.Pool to satisfy
// regime.Repository (which expects an Insert that doesn't take a Querier).
type regimeRepoAdapter struct {
	repo *store.MarketRegimeRepo
	pool *pgxpool.Pool
}

func (a regimeRepoAdapter) Insert(ctx context.Context, rec store.MarketRegimeRecord) (int64, error) {
	return a.repo.Insert(ctx, a.pool, rec)
}

// newsRepoAdapter is the equivalent for news.Repository.
type newsRepoAdapter struct {
	repo *store.NewsSnapshotsRepo
	pool *pgxpool.Pool
}

func (a newsRepoAdapter) Insert(ctx context.Context, rec store.NewsSnapshotRecord) (int64, error) {
	return a.repo.Insert(ctx, a.pool, rec)
}

// perpStoreAdapter projects store.PerpMetricsRepo into perpmetrics.Store.
type perpStoreAdapter struct {
	repo *store.PerpMetricsRepo
	pool *pgxpool.Pool
}

func (a perpStoreAdapter) Insert(ctx context.Context, s perpmetrics.Snapshot) error {
	_, err := a.repo.Insert(ctx, a.pool, store.PerpMetricsRecord{
		Symbol:             s.Symbol,
		ObservedAt:         s.ObservedAt,
		FundingRate:        s.FundingRate,
		NextFundingTime:    s.NextFundingTime,
		MarkPrice:          s.MarkPrice,
		OpenInterest:       s.OpenInterest,
		OpenInterest24hPct: s.OpenInterest24hPct,
		Price24hPct:        s.Price24hPct,
		TopLSRatio:         s.TopLSRatio,
		FundingLabel:       s.FundingLabel,
		OISignal:           s.OISignal,
		LSLabel:            s.LSLabel,
	})
	return err
}

// perpKlineAdapter satisfies perpmetrics.KlineSource via market.Provider's
// 24h context. The provider already caches per-symbol for 30s so the perp
// worker doesn't double-call binance kline.
type perpKlineAdapter struct {
	provider *market.Provider
}

func (a perpKlineAdapter) Price24hPct(ctx context.Context, symbol string) (decimal.Decimal, error) {
	if a.provider == nil {
		return decimal.Zero, fmt.Errorf("kline provider unavailable")
	}
	mc, err := a.provider.GetContext(ctx, symbol)
	if err != nil || mc == nil {
		return decimal.Zero, fmt.Errorf("market context unavailable for %s", symbol)
	}
	return mc.Last24hChangePct, nil
}

// perpSymbolsAdapter returns distinct symbols for enabled, non-archived
// strategies. BTCUSDT is added by the worker; this adapter just yields the
// strategy roster.
type perpSymbolsAdapter struct {
	strategyRepo *store.StrategyRepo
	pool         *pgxpool.Pool
}

func (a perpSymbolsAdapter) ActiveSymbols(ctx context.Context) ([]string, error) {
	rows, err := a.strategyRepo.List(ctx, a.pool, false) // archived=false → active strategies
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row == nil || !row.Enabled {
			continue
		}
		if seen[row.Symbol] {
			continue
		}
		seen[row.Symbol] = true
		out = append(out, row.Symbol)
	}
	return out, nil
}
