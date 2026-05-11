package calendar

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

func TestCalendarSettingsAdapter_ReadProjects(t *testing.T) {
	repo := &fakeSettingsRepo{s: &store.Settings{CalendarEnabled: true}}
	a := NewSettingsAdapter(repo, nil)
	got, _ := a.Read(context.Background())
	if !got.Enabled {
		t.Errorf("Enabled not projected: %+v", got)
	}
}

func TestCalendarSettingsAdapter_PropagatesError(t *testing.T) {
	a := NewSettingsAdapter(&fakeSettingsRepo{err: errors.New("x")}, nil)
	if _, err := a.Read(context.Background()); err == nil {
		t.Fatal("want error")
	}
}
