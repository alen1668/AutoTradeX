package macrocontext

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

// regimeRepoAdapter pulls *pgxpool.Pool inside so callers don't thread it.
type regimeRepoAdapter struct {
	repo *store.MarketRegimeRepo
	pool *pgxpool.Pool
}

// WrapRegimeRepo returns a RegimeReader bound to the supplied pool.
func WrapRegimeRepo(repo *store.MarketRegimeRepo, pool *pgxpool.Pool) RegimeReader {
	return &regimeRepoAdapter{repo: repo, pool: pool}
}

func (a *regimeRepoAdapter) Latest(ctx context.Context) (*store.MarketRegimeRecord, error) {
	return a.repo.Latest(ctx, a.pool)
}

type newsRepoAdapter struct {
	repo *store.NewsSnapshotsRepo
	pool *pgxpool.Pool
}

// WrapNewsRepo returns a NewsReader bound to the supplied pool.
func WrapNewsRepo(repo *store.NewsSnapshotsRepo, pool *pgxpool.Pool) NewsReader {
	return &newsRepoAdapter{repo: repo, pool: pool}
}

func (a *newsRepoAdapter) Latest(ctx context.Context) (*store.NewsSnapshotRecord, error) {
	return a.repo.Latest(ctx, a.pool)
}

type perpRepoAdapter struct {
	repo *store.PerpMetricsRepo
	pool *pgxpool.Pool
}

// WrapPerpRepo returns a PerpReader bound to the supplied pool.
func WrapPerpRepo(repo *store.PerpMetricsRepo, pool *pgxpool.Pool) PerpReader {
	return &perpRepoAdapter{repo: repo, pool: pool}
}

func (a *perpRepoAdapter) Latest(ctx context.Context, symbol string) (*store.PerpMetricsRecord, error) {
	return a.repo.Latest(ctx, a.pool, symbol)
}

// settingsForPerpAdapter projects store.SettingsRepo into SettingsForPerp.
type settingsForPerpAdapter struct {
	repo *store.SettingsRepo
	pool *pgxpool.Pool
}

// WrapSettingsForPerp returns a SettingsForPerp bound to the supplied pool.
func WrapSettingsForPerp(repo *store.SettingsRepo, pool *pgxpool.Pool) SettingsForPerp {
	return &settingsForPerpAdapter{repo: repo, pool: pool}
}

func (a *settingsForPerpAdapter) Get(ctx context.Context) (int, error) {
	s, err := a.repo.Get(ctx, a.pool)
	if err != nil {
		return 0, err
	}
	return s.PerpMetricsLookbackMinutes, nil
}
