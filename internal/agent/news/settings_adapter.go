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
	// topN=12: 多源聚合后给 LLM 留足够覆盖面 (4 源 × 6/源 → 去重后 ~12-18 条 → 截 12)
	return &SettingsAdapter{repo: repo, pool: pool, topN: 12}
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
