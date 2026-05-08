package scorer

import (
	"sync"
	"time"
)

// clock abstracts time.Now for tests.
type clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// HealthTracker keeps a rolling window of LLM call outcomes (success vs
// failure). It powers the "LLM 持续失败" critical alert: when the failure
// rate inside the window crosses 50% AND failure count is at least 5, the
// ingest layer fires an alert (throttled to one per 10 minutes by the
// hook).
//
// All in-memory; restart resets. The alert is operational visibility —
// not something we want to lose if Postgres flaps.
type HealthTracker struct {
	window time.Duration
	clock  clock

	mu      sync.Mutex
	entries []entry
}

type entry struct {
	at      time.Time
	success bool
}

// NewHealthTracker uses real wall clock. Window of 10 minutes is the
// recommended default (matches the alert design in the spec).
func NewHealthTracker(window time.Duration) *HealthTracker {
	return &HealthTracker{window: window, clock: realClock{}}
}

// NewHealthTrackerWithClock injects a test clock.
func NewHealthTrackerWithClock(window time.Duration, c clock) *HealthTracker {
	return &HealthTracker{window: window, clock: c}
}

func (h *HealthTracker) RecordSuccess() { h.record(true) }
func (h *HealthTracker) RecordFailure() { h.record(false) }

func (h *HealthTracker) record(ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, entry{at: h.clock.Now(), success: ok})
	h.evictLocked()
}

// IsUnhealthy reports whether the tracker has crossed the alert threshold,
// plus the (failures, total) tuple inside the current window for inclusion
// in the alert message.
//
// Threshold: at least 5 failures AND >=50% failure rate.
func (h *HealthTracker) IsUnhealthy() (bad bool, failures, total int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.evictLocked()
	var fails int
	for _, e := range h.entries {
		if !e.success {
			fails++
		}
	}
	total = len(h.entries)
	failures = fails
	if fails < 5 {
		return false, fails, total
	}
	if float64(fails)/float64(total) < 0.5 {
		return false, fails, total
	}
	return true, fails, total
}

func (h *HealthTracker) evictLocked() {
	cutoff := h.clock.Now().Add(-h.window)
	i := 0
	for ; i < len(h.entries); i++ {
		if h.entries[i].at.After(cutoff) || h.entries[i].at.Equal(cutoff) {
			break
		}
	}
	if i > 0 {
		h.entries = h.entries[i:]
	}
}
