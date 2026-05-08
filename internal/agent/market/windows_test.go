package market

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// at builds a UTC time and double-checks the weekday matches expectation
// (test setup bug if not).
func at(t *testing.T, year, month, day int, weekdayWanted time.Weekday, hh, mm int) []string {
	t.Helper()
	tm := time.Date(year, time.Month(month), day, hh, mm, 0, 0, time.UTC)
	if tm.Weekday() != weekdayWanted {
		t.Fatalf("test setup bug: %s is %s, not %s", tm, tm.Weekday(), weekdayWanted)
	}
	return ActiveWindows(tm)
}

func TestActiveWindows_USDataReleaseTuesday1230(t *testing.T) {
	out := at(t, 2026, 5, 12, time.Tuesday, 12, 30)
	assert.Contains(t, out, "us_data_release_window")
}

func TestActiveWindows_USDataReleaseFriday1330(t *testing.T) {
	out := at(t, 2026, 5, 15, time.Friday, 13, 30)
	assert.Contains(t, out, "us_data_release_window")
}

func TestActiveWindows_NotUSDataReleaseOnMonday(t *testing.T) {
	out := at(t, 2026, 5, 11, time.Monday, 12, 30)
	assert.NotContains(t, out, "us_data_release_window")
}

func TestActiveWindows_USMarketOpenWeekdays(t *testing.T) {
	out := at(t, 2026, 5, 11, time.Monday, 13, 30)
	assert.Contains(t, out, "us_market_open_window")
	out = at(t, 2026, 5, 12, time.Tuesday, 14, 0)
	assert.Contains(t, out, "us_market_open_window")
}

func TestActiveWindows_NotUSMarketOpenWeekend(t *testing.T) {
	out := at(t, 2026, 5, 9, time.Saturday, 13, 30)
	assert.NotContains(t, out, "us_market_open_window")
}

func TestActiveWindows_WeekendGapMondayEarly(t *testing.T) {
	out := at(t, 2026, 5, 11, time.Monday, 2, 0)
	assert.Contains(t, out, "weekend_gap_window")
}

func TestActiveWindows_NotWeekendGapAfter4(t *testing.T) {
	out := at(t, 2026, 5, 11, time.Monday, 5, 0)
	assert.NotContains(t, out, "weekend_gap_window")
}

func TestActiveWindows_QuietTime(t *testing.T) {
	// Wednesday 03:00 UTC: hits no window.
	out := at(t, 2026, 5, 13, time.Wednesday, 3, 0)
	assert.Empty(t, out)
}

func TestActiveWindows_ConvertsLocalToUTC(t *testing.T) {
	// Same instant in different time zones must yield identical results.
	loc, _ := time.LoadLocation("America/New_York")
	utc := time.Date(2026, 5, 12, 12, 30, 0, 0, time.UTC)
	local := utc.In(loc)
	assert.Equal(t, ActiveWindows(utc), ActiveWindows(local))
}
