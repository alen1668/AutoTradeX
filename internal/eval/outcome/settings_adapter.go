package outcome

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

// SettingsRepoLike is the minimum surface SettingsAdapter needs from
// store.SettingsRepo. Defining it here lets tests pass a fake.
type SettingsRepoLike interface {
	Get(ctx context.Context, q store.Querier) (*store.Settings, error)
}

// SettingsAdapter projects store.Settings into outcome.Config.
type SettingsAdapter struct {
	repo SettingsRepoLike
	pool *pgxpool.Pool
}

func NewSettingsAdapter(repo SettingsRepoLike, pool *pgxpool.Pool) *SettingsAdapter {
	return &SettingsAdapter{repo: repo, pool: pool}
}

// Read loads settings and returns the worker Config.
func (a *SettingsAdapter) Read(ctx context.Context) (Config, error) {
	s, err := a.repo.Get(ctx, a.pool)
	if err != nil {
		return Config{}, err
	}
	return Config{
		HorizonMin:   s.OutcomeHorizonMin,
		WinThresh:    s.OutcomeWinThresholdPct,
		LossThresh:   s.OutcomeLossThresholdPct,
		BatchSize:    s.OutcomeBatchSize,
		ScanInterval: time.Duration(s.OutcomeScanIntervalMin) * time.Minute,
		StaleCutoffH: s.OutcomeStaleCutoffH,
		// MinAgeMin is derived from HorizonMin+5 in NewWorker if zero; leave 0
	}, nil
}
