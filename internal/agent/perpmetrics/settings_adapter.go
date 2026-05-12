package perpmetrics

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

// WorkerSettings is the subset of store.Settings the worker reads each tick.
type WorkerSettings struct {
	Enabled         bool
	LookbackMinutes int
}

// SettingsRepoLike is the minimum surface SettingsAdapter needs from
// store.SettingsRepo. Defining it here lets tests pass a fake.
type SettingsRepoLike interface {
	Get(ctx context.Context, q store.Querier) (*store.Settings, error)
}

// SettingsAdapter projects store.Settings into WorkerSettings.
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
	return WorkerSettings{
		Enabled:         s.PerpMetricsEnabled,
		LookbackMinutes: s.PerpMetricsLookbackMinutes,
	}, nil
}
