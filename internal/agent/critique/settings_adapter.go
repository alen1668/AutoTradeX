package critique

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

// SettingsRepoLike is the minimum surface SettingsAdapter needs.
type SettingsRepoLike interface {
	Get(ctx context.Context, q store.Querier) (*store.Settings, error)
}

// Settings is the worker-level projection of store.Settings critique fields.
type Settings struct {
	Enabled    bool
	Model      string // empty → caller resolves fallback (e.g. scorer model)
	WindowDays int
	MinSample  int
	MaxPinned  int
	CronUTC    string
}

type SettingsAdapter struct {
	repo SettingsRepoLike
	pool *pgxpool.Pool
}

func NewSettingsAdapter(repo SettingsRepoLike, pool *pgxpool.Pool) *SettingsAdapter {
	return &SettingsAdapter{repo: repo, pool: pool}
}

func (a *SettingsAdapter) Read(ctx context.Context) (Settings, error) {
	s, err := a.repo.Get(ctx, a.pool)
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		Enabled:    s.CritiqueEnabled,
		Model:      s.CritiqueModel,
		WindowDays: s.CritiqueWindowDays,
		MinSample:  s.CritiqueMinSample,
		MaxPinned:  s.CritiqueMaxPinned,
		CronUTC:    s.CritiqueCronUTC,
	}, nil
}
