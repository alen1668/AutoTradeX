package exit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lizhaojie/tvbot/internal/store"
)

type fakeSettingsRepo struct {
	s   store.Settings
	err error
}

func (f *fakeSettingsRepo) Get(_ context.Context, _ store.Querier) (*store.Settings, error) {
	if f.err != nil {
		return nil, f.err
	}
	s := f.s
	return &s, nil
}

func TestSettingsAdapter_HappyPath(t *testing.T) {
	r := &fakeSettingsRepo{s: store.Settings{
		AgentScorerModel:                  "claude-sonnet-4-6",
		ExitAgentEnabled:                  true,
		ExitAgentMode:                     "shadow",
		ExitAgentModel:                    "",
		ExitAgentScanIntervalMin:          5,
		ExitAgentMinPositionAgeSec:        60,
		ExitAgentDecisionCooldownMin:      15,
		ExitAgentRequireConfidenceForExit: "high",
		ExitAgentHorizonMin:               60,
		ExitAgentMaxConcurrent:            4,
	}}
	a := NewSettingsAdapter(r, nil)
	cfg, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !cfg.Enabled || cfg.Mode != ModeShadow {
		t.Errorf("got %+v", cfg)
	}
	if cfg.Model != "claude-sonnet-4-6" {
		t.Errorf("model fallback wrong: %q", cfg.Model)
	}
	if cfg.ScanInterval != 5*time.Minute {
		t.Errorf("scan: %v", cfg.ScanInterval)
	}
	if cfg.RequireConfidenceForExit != ConfHigh {
		t.Errorf("conf: %v", cfg.RequireConfidenceForExit)
	}
}

func TestSettingsAdapter_ExplicitModelOverride(t *testing.T) {
	r := &fakeSettingsRepo{s: store.Settings{
		AgentScorerModel: "claude-sonnet-4-6",
		ExitAgentModel:   "claude-haiku-4-5-20251001",
		ExitAgentMode:    "shadow",
	}}
	cfg, _ := NewSettingsAdapter(r, nil).Read(context.Background())
	if cfg.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("override failed: %q", cfg.Model)
	}
}

func TestSettingsAdapter_BadConfidenceFallsBackToHigh(t *testing.T) {
	r := &fakeSettingsRepo{s: store.Settings{
		ExitAgentRequireConfidenceForExit: "yolo",
	}}
	cfg, _ := NewSettingsAdapter(r, nil).Read(context.Background())
	if cfg.RequireConfidenceForExit != ConfHigh {
		t.Errorf("fallback failed: %v", cfg.RequireConfidenceForExit)
	}
}

func TestSettingsAdapter_BadModeFallsBackToShadow(t *testing.T) {
	r := &fakeSettingsRepo{s: store.Settings{
		ExitAgentMode: "yolo-ape-mode",
	}}
	cfg, _ := NewSettingsAdapter(r, nil).Read(context.Background())
	if cfg.Mode != ModeShadow {
		t.Errorf("mode fallback failed: %v", cfg.Mode)
	}
}

func TestSettingsAdapter_RepoError(t *testing.T) {
	r := &fakeSettingsRepo{err: errors.New("db down")}
	_, err := NewSettingsAdapter(r, nil).Read(context.Background())
	if err == nil {
		t.Error("want error")
	}
}
