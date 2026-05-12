package critique

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type countingRunner struct{ n atomic.Int32 }

func (c *countingRunner) Run(context.Context) error {
	c.n.Add(1)
	return nil
}

func TestWorker_TryRun_DoubleClickCollapses(t *testing.T) {
	r := &countingRunner{}
	w := &Worker{agent: r}
	w.TryRun(context.Background())
	w.TryRun(context.Background()) // within 5min window → skipped
	if got := r.n.Load(); got != 1 {
		t.Fatalf("expected 1 run, got %d", got)
	}
}

func TestWorker_TryRun_AllowAfterWindow(t *testing.T) {
	r := &countingRunner{}
	w := &Worker{agent: r}
	w.TryRun(context.Background())
	// Pretend the previous run was long ago.
	w.mu.Lock()
	w.lastRun = time.Now().Add(-10 * time.Minute)
	w.mu.Unlock()
	w.TryRun(context.Background())
	if got := r.n.Load(); got != 2 {
		t.Fatalf("expected 2 runs after window, got %d", got)
	}
}

func TestParseDailyCron(t *testing.T) {
	cases := []struct {
		in           string
		wantH, wantM int
		wantOK       bool
	}{
		{"0 4 * * *", 4, 0, true},
		{"30 6 * * *", 6, 30, true},
		{"59 23 * * *", 23, 59, true},
		{"", 0, 0, false},
		{"0 4 1 * *", 0, 0, false},  // day-of-month restriction not supported
		{"60 0 * * *", 0, 0, false}, // minute out of range
		{"0 24 * * *", 0, 0, false}, // hour out of range
		{"abc def * * *", 0, 0, false},
	}
	for _, c := range cases {
		h, m, ok := parseDailyCron(c.in)
		if ok != c.wantOK || (ok && (h != c.wantH || m != c.wantM)) {
			t.Errorf("parseDailyCron(%q) = (%d,%d,%v), want (%d,%d,%v)",
				c.in, h, m, ok, c.wantH, c.wantM, c.wantOK)
		}
	}
}

func TestNextDailyAt_FutureToday(t *testing.T) {
	now := time.Date(2026, 5, 12, 3, 0, 0, 0, time.UTC)
	got := nextDailyAt(now, 4, 0)
	want := time.Date(2026, 5, 12, 4, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestNextDailyAt_RollsToTomorrow(t *testing.T) {
	now := time.Date(2026, 5, 12, 4, 30, 0, 0, time.UTC)
	got := nextDailyAt(now, 4, 0)
	want := time.Date(2026, 5, 13, 4, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
