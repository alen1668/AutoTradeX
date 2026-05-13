package exit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

// SettingsRepoLike is the slice of *store.SettingsRepo SettingsAdapter
// needs. Defined here for test substitutability.
type SettingsRepoLike interface {
	Get(ctx context.Context, q store.Querier) (*store.Settings, error)
}

type SettingsAdapter struct {
	repo SettingsRepoLike
	pool *pgxpool.Pool
}

func NewSettingsAdapter(repo SettingsRepoLike, pool *pgxpool.Pool) *SettingsAdapter {
	return &SettingsAdapter{repo: repo, pool: pool}
}

// Read translates store.Settings into exit.Config. Falls back:
//   - Model: empty → AgentScorerModel
//   - RequireConfidenceForExit: invalid → ConfHigh (safest default).
//   - Mode: invalid → ModeShadow (safest default).
//
// Numeric fields are taken at face value; the migration default-clamps
// at the DB layer.
func (a *SettingsAdapter) Read(ctx context.Context) (Config, error) {
	s, err := a.repo.Get(ctx, a.pool)
	if err != nil {
		return Config{}, err
	}
	model := s.ExitAgentModel
	if model == "" {
		model = s.AgentScorerModel
	}
	conf := Confidence(s.ExitAgentRequireConfidenceForExit)
	if !conf.IsValid() {
		conf = ConfHigh
	}
	mode := Mode(s.ExitAgentMode)
	if mode != ModeShadow && mode != ModeActive {
		mode = ModeShadow
	}
	return Config{
		Enabled:                  s.ExitAgentEnabled,
		Mode:                     mode,
		Model:                    model,
		ScanInterval:             time.Duration(s.ExitAgentScanIntervalMin) * time.Minute,
		MinPositionAge:           time.Duration(s.ExitAgentMinPositionAgeSec) * time.Second,
		DecisionCooldown:         time.Duration(s.ExitAgentDecisionCooldownMin) * time.Minute,
		RequireConfidenceForExit: conf,
		HorizonMin:               s.ExitAgentHorizonMin,
		MaxConcurrent:            s.ExitAgentMaxConcurrent,
	}, nil
}
