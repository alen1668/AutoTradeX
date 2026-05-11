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
