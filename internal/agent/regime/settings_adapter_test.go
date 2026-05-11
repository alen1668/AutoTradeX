package regime

import (
	"context"
	"errors"
	"testing"

	"github.com/lizhaojie/tvbot/internal/store"
)

type fakeSettingsRepo struct {
	s   *store.Settings
	err error
}

func (f *fakeSettingsRepo) Get(ctx context.Context, q store.Querier) (*store.Settings, error) {
	return f.s, f.err
}

func TestSettingsAdapter_ReadProjectsRegimeFields(t *testing.T) {
	repo := &fakeSettingsRepo{s: &store.Settings{RegimeEnabled: true, RegimeIntervalMin: 45}}
	a := NewSettingsAdapter(repo, nil)
	got, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.Enabled || got.IntervalMin != 45 {
		t.Errorf("got %+v", got)
	}
}

func TestSettingsAdapter_PropagatesError(t *testing.T) {
	repo := &fakeSettingsRepo{err: errors.New("db down")}
	a := NewSettingsAdapter(repo, nil)
	if _, err := a.Read(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
