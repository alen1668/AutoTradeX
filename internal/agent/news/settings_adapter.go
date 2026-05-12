package news

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

type SettingsRepoLike interface {
	Get(ctx context.Context, q store.Querier) (*store.Settings, error)
}

type SettingsAdapter struct {
	repo SettingsRepoLike
	pool *pgxpool.Pool
	topN int
}

func NewSettingsAdapter(repo SettingsRepoLike, pool *pgxpool.Pool) *SettingsAdapter {
	return &SettingsAdapter{repo: repo, pool: pool, topN: 5}
}

func (a *SettingsAdapter) Read(ctx context.Context) (WorkerSettings, error) {
	s, err := a.repo.Get(ctx, a.pool)
	if err != nil {
		return WorkerSettings{}, err
	}
	return WorkerSettings{
		Enabled:         s.NewsEnabled,
		IntervalMin:     s.NewsIntervalMin,
		TopN:            a.topN,
		NotifyMinImpact: s.NewsNotifyMinImpact,
	}, nil
}
