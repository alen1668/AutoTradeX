package regime

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

// SettingsRepoLike is the minimum surface SettingsAdapter needs from
// store.SettingsRepo. Defining it here lets tests pass a fake.
type SettingsRepoLike interface {
	Get(ctx context.Context, q store.Querier) (*store.Settings, error)
}

// SettingsAdapter projects store.Settings into the worker-specific
// WorkerSettings.
type SettingsAdapter struct {
	repo SettingsRepoLike
	pool *pgxpool.Pool
}

func NewSettingsAdapter(repo SettingsRepoLike, pool *pgxpool.Pool) *SettingsAdapter {
	return &SettingsAdapter{repo: repo, pool: pool}
}

func (a *SettingsAdapter) Read(ctx context.Context) (WorkerSettings, error) {
	s, err := a.repo.Get(ctx, a.pool)
	if err != nil {
		return WorkerSettings{}, err
	}
	return WorkerSettings{Enabled: s.RegimeEnabled, IntervalMin: s.RegimeIntervalMin}, nil
}
