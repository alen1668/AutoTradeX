package macrocontext

import (
	"testing"
	"time"
)

func TestFormatRelativeText(t *testing.T) {
	cases := []struct {
		minutes int
		want    string
	}{
		{0, "正在开始"},
		{15, "还有 15 分钟"},
		{-30, "30 分钟前已过"},
		{-1, "1 分钟前已过"},
	}
	for _, c := range cases {
		got := FormatRelativeText(c.minutes)
		if got != c.want {
			t.Errorf("FormatRelativeText(%d) = %q, want %q", c.minutes, got, c.want)
		}
	}
}

func TestMinutesBetween(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 30, 0, 0, time.UTC)
	if m := MinutesBetween(now, now.Add(45*time.Minute)); m != 45 {
		t.Errorf("future: got %d want 45", m)
	}
	if m := MinutesBetween(now, now.Add(-20*time.Minute)); m != -20 {
		t.Errorf("past: got %d want -20", m)
	}
}
