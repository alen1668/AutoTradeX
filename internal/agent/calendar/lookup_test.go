package calendar

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubLookupSrc struct {
	events  []Event
	err     error
	gotFrom time.Time
	gotTo   time.Time
}

func (s *stubLookupSrc) ActiveBetween(ctx context.Context, from, to time.Time) ([]Event, error) {
	s.gotFrom, s.gotTo = from, to
	return s.events, s.err
}

func TestActiveEventsAt_DelegatesWithPlusMinusOneHour(t *testing.T) {
	src := &stubLookupSrc{events: []Event{{Name: "X", StartsAt: time.Now()}}}
	now := time.Date(2026, 5, 13, 13, 30, 0, 0, time.UTC)
	got, err := ActiveEventsAt(context.Background(), src, now)
	if err != nil {
		t.Fatalf("ActiveEventsAt: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d", len(got))
	}
	if !src.gotFrom.Equal(now.Add(-time.Hour)) || !src.gotTo.Equal(now.Add(time.Hour)) {
		t.Errorf("window: from=%v to=%v", src.gotFrom, src.gotTo)
	}
}

func TestActiveEventsAt_PropagatesError(t *testing.T) {
	src := &stubLookupSrc{err: errors.New("db")}
	if _, err := ActiveEventsAt(context.Background(), src, time.Now()); err == nil {
		t.Fatal("expected error")
	}
}
