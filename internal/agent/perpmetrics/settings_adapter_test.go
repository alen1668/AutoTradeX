package perpmetrics_test

import (
	"context"
	"testing"

	"github.com/lizhaojie/tvbot/internal/agent/perpmetrics"
	"github.com/lizhaojie/tvbot/internal/store"
)

type fakeSettingsRepo struct {
	s *store.Settings
}

func (f *fakeSettingsRepo) Get(ctx context.Context, q store.Querier) (*store.Settings, error) {
	return f.s, nil
}

func TestSettingsAdapter_Read(t *testing.T) {
	s := &store.Settings{PerpMetricsEnabled: true, PerpMetricsLookbackMinutes: 45}
	a := perpmetrics.NewSettingsAdapter(&fakeSettingsRepo{s: s}, nil)
	got, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.Enabled || got.LookbackMinutes != 45 {
		t.Errorf("got %+v, want Enabled=true Lookback=45", got)
	}
}

func TestSettingsAdapter_Disabled(t *testing.T) {
	s := &store.Settings{PerpMetricsEnabled: false, PerpMetricsLookbackMinutes: 30}
	a := perpmetrics.NewSettingsAdapter(&fakeSettingsRepo{s: s}, nil)
	got, _ := a.Read(context.Background())
	if got.Enabled {
		t.Errorf("got enabled=true, want false")
	}
}
