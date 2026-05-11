package main

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

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
