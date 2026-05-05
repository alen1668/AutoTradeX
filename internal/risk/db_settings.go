package risk

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/store"
)

// DBSettingsProvider reads current settings from the database on each call.
type DBSettingsProvider struct {
	pool *pgxpool.Pool
	repo *store.SettingsRepo
}

func NewDBSettingsProvider(pool *pgxpool.Pool, repo *store.SettingsRepo) *DBSettingsProvider {
	return &DBSettingsProvider{pool: pool, repo: repo}
}

func (p *DBSettingsProvider) Get(ctx context.Context) (decimal.Decimal, decimal.Decimal, error) {
	s, err := p.repo.Get(ctx, p.pool)
	if err != nil {
		return decimal.Zero, decimal.Zero, err
	}
	return s.MaxTotalLeverage, s.MaxDailyLossUSDC, nil
}
