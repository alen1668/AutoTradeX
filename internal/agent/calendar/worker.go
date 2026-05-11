package calendar

import (
	"context"
	"time"

	"github.com/rs/zerolog"
)

type WorkerSettings struct {
	Enabled bool
}

type SettingsReader interface {
	Read(ctx context.Context) (WorkerSettings, error)
}

type Fetcher interface {
	Fetch(ctx context.Context) ([]Event, error)
}

type Sink interface {
	SaveBatch(ctx context.Context, events []Event) error
}

type Worker struct {
	fetcher  Fetcher
	sink     Sink
	settings SettingsReader
	log      zerolog.Logger
	interval time.Duration
}

func NewWorker(f Fetcher, s Sink, sr SettingsReader, log zerolog.Logger) *Worker {
	return &Worker{
		fetcher:  f,
		sink:     s,
		settings: sr,
		log:      log,
		interval: 24 * time.Hour,
	}
}

func (w *Worker) WithInterval(d time.Duration) *Worker {
	w.interval = d
	return w
}

func (w *Worker) Start(ctx context.Context) {
	if err := w.RunOnce(ctx); err != nil {
		w.log.Warn().Err(err).Msg("calendar initial run failed")
	}
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.RunOnce(ctx); err != nil {
				w.log.Warn().Err(err).Msg("calendar run failed")
			}
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) error {
	s, err := w.settings.Read(ctx)
	if err != nil {
		return err
	}
	if !s.Enabled {
		w.log.Debug().Msg("calendar worker disabled, skipping")
		return nil
	}
	events, err := w.fetcher.Fetch(ctx)
	if err != nil {
		return err
	}
	if err := w.sink.SaveBatch(ctx, events); err != nil {
		return err
	}
	w.log.Info().Int("count", len(events)).Msg("calendar refreshed")
	return nil
}
