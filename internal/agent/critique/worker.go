package critique

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// runner is the minimal surface Worker invokes — Agent.Run satisfies it.
// Extracted as an interface so worker_test.go can substitute a counter.
type runner interface {
	Run(ctx context.Context) error
}

// Worker schedules Agent.Run on a daily cron + serves manual triggers.
// Idempotency: at most one Run() in flight; refuses to re-run within 5
// minutes of the previous run completing.
type Worker struct {
	agent    runner
	settings *SettingsAdapter
	log      zerolog.Logger

	mu      sync.Mutex
	running bool
	lastRun time.Time
}

func NewWorker(agent runner, settings *SettingsAdapter, log zerolog.Logger) *Worker {
	return &Worker{agent: agent, settings: settings, log: log}
}

// Start blocks until ctx is done. Loads settings once at start.
// Schedules the next daily-cron firing and listens on manualCh between.
func (w *Worker) Start(ctx context.Context, manualCh <-chan struct{}) {
	s, err := w.settings.Read(ctx)
	if err != nil {
		w.log.Error().Err(err).Msg("critique: settings read failed; worker exiting")
		return
	}
	if !s.Enabled {
		w.log.Info().Msg("critique disabled by settings; manual triggers still honored")
	}

	for {
		// Determine the next scheduled tick. If disabled or unparseable, sleep 24h.
		var nextAt time.Time
		hh, mm, ok := parseDailyCron(s.CronUTC)
		if !ok {
			hh, mm = 4, 0
		}
		if s.Enabled {
			nextAt = nextDailyAt(time.Now().UTC(), hh, mm)
		} else {
			nextAt = time.Now().UTC().Add(24 * time.Hour) // tick anyway to re-read settings
		}
		w.log.Debug().Time("next", nextAt).Msg("critique: next scheduled run")
		timer := time.NewTimer(time.Until(nextAt))

		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if s.Enabled {
				w.TryRun(ctx)
			}
			// Refresh settings each cycle so toggling Enabled / changing cron
			// takes effect without restart.
			if ns, err := w.settings.Read(ctx); err == nil {
				s = ns
			}
		case <-manualCh:
			timer.Stop()
			w.TryRun(ctx)
		}
	}
}

// TryRun runs at most one critique at a time AND enforces a 5-minute
// idempotency window after each completion.
func (w *Worker) TryRun(ctx context.Context) {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		w.log.Info().Msg("critique: already running, skipping")
		return
	}
	if !w.lastRun.IsZero() && time.Since(w.lastRun) < 5*time.Minute {
		w.mu.Unlock()
		w.log.Info().Msg("critique: idempotency window (5min) — skipping")
		return
	}
	w.running = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.running = false
		w.lastRun = time.Now()
		w.mu.Unlock()
	}()
	if err := w.agent.Run(ctx); err != nil {
		w.log.Warn().Err(err).Msg("critique: run returned error")
	}
}

// parseDailyCron parses "M H * * *" returning (hour, minute, ok). Other
// forms are not supported. Empty string returns (0, 0, false).
func parseDailyCron(expr string) (int, int, bool) {
	parts := strings.Fields(strings.TrimSpace(expr))
	if len(parts) != 5 {
		return 0, 0, false
	}
	// We only support the "every day at HH:MM" form; the last 3 fields
	// must all be "*".
	for _, p := range parts[2:] {
		if p != "*" {
			return 0, 0, false
		}
	}
	mm, err1 := strconv.Atoi(parts[0])
	hh, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

// nextDailyAt returns the next time at the given UTC hour:minute. If
// that time is already past for today, returns tomorrow's instance.
func nextDailyAt(now time.Time, hh, mm int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, time.UTC)
	if !t.After(now) {
		t = t.Add(24 * time.Hour)
	}
	return t
}
