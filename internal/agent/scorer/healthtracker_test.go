package scorer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type fakeClockSrc struct{ now time.Time }

func (c *fakeClockSrc) Now() time.Time             { return c.now }
func (c *fakeClockSrc) advance(d time.Duration)    { c.now = c.now.Add(d) }
func newFakeClock(t time.Time) *fakeClockSrc       { return &fakeClockSrc{now: t} }

func TestHealthTracker_NoEntriesIsHealthy(t *testing.T) {
	h := NewHealthTracker(10 * time.Minute)
	bad, fails, total := h.IsUnhealthy()
	assert.False(t, bad)
	assert.Equal(t, 0, fails)
	assert.Equal(t, 0, total)
}

func TestHealthTracker_FewFailuresStaysHealthy(t *testing.T) {
	h := NewHealthTracker(10 * time.Minute)
	for i := 0; i < 4; i++ {
		h.RecordFailure()
	}
	bad, _, _ := h.IsUnhealthy()
	assert.False(t, bad, "fewer than 5 failures must be healthy")
}

func TestHealthTracker_FiveFailuresFiresAlert(t *testing.T) {
	h := NewHealthTracker(10 * time.Minute)
	for i := 0; i < 5; i++ {
		h.RecordFailure()
	}
	bad, fails, total := h.IsUnhealthy()
	assert.True(t, bad)
	assert.Equal(t, 5, fails)
	assert.Equal(t, 5, total)
}

func TestHealthTracker_BelowFailureRateThreshold(t *testing.T) {
	h := NewHealthTracker(10 * time.Minute)
	for i := 0; i < 5; i++ {
		h.RecordFailure()
	}
	for i := 0; i < 6; i++ {
		h.RecordSuccess()
	}
	// 5/11 ≈ 45% < 50% threshold; even with 5 failures should NOT alert.
	bad, _, _ := h.IsUnhealthy()
	assert.False(t, bad)
}

func TestHealthTracker_OldEntriesEvicted(t *testing.T) {
	fc := newFakeClock(time.Now())
	h := NewHealthTrackerWithClock(10*time.Minute, fc)
	for i := 0; i < 5; i++ {
		h.RecordFailure()
	}
	bad, _, _ := h.IsUnhealthy()
	assert.True(t, bad)

	fc.advance(11 * time.Minute)
	bad, fails, total := h.IsUnhealthy()
	assert.False(t, bad)
	assert.Equal(t, 0, fails)
	assert.Equal(t, 0, total)
}

func TestHealthTracker_ConcurrentRecords(t *testing.T) {
	h := NewHealthTracker(10 * time.Minute)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			h.RecordFailure()
		}
		done <- struct{}{}
	}()
	go func() {
		for i := 0; i < 100; i++ {
			h.RecordSuccess()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
	bad, fails, total := h.IsUnhealthy()
	assert.Equal(t, 200, total)
	assert.Equal(t, 100, fails)
	assert.True(t, bad, "100/200=50% with >=5 fails should alert")
}
