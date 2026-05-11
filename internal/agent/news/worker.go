package news

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/eval"
)

type WorkerSettings struct {
	Enabled     bool
	IntervalMin int
	TopN        int
}

type SettingsReader interface {
	Read(ctx context.Context) (WorkerSettings, error)
}

type Fetcher interface {
	Fetch(ctx context.Context, topN int) ([]Headline, error)
}

type ClassifierLike interface {
	Classify(ctx context.Context, headlines []Headline) (Classification, error)
}

type Persistor interface {
	PersistSuccess(ctx context.Context, c Classification) (int64, error)
	PersistFailure(ctx context.Context, c Classification, cause error) (int64, error)
}

type Publisher interface {
	Publish(e eval.EvalEvent)
}

type Worker struct {
	fetcher    Fetcher
	classifier ClassifierLike
	persistor  Persistor
	publisher  Publisher
	settings   SettingsReader
	log        zerolog.Logger
}

func NewWorker(f Fetcher, c ClassifierLike, p Persistor, pub Publisher, sr SettingsReader, log zerolog.Logger) *Worker {
	return &Worker{
		fetcher: f, classifier: c, persistor: p, publisher: pub, settings: sr, log: log,
	}
}

func (w *Worker) Start(ctx context.Context) {
	if err := w.RunOnce(ctx); err != nil {
		w.log.Warn().Err(err).Msg("news initial run failed")
	}
	s, _ := w.settings.Read(ctx)
	interval := time.Duration(maxInt(s.IntervalMin, 1)) * time.Minute
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.RunOnce(ctx); err != nil {
				w.log.Warn().Err(err).Msg("news run failed")
			}
			if s2, err := w.settings.Read(ctx); err == nil {
				newInterval := time.Duration(maxInt(s2.IntervalMin, 1)) * time.Minute
				if newInterval != interval {
					t.Reset(newInterval)
					interval = newInterval
				}
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
		w.log.Debug().Msg("news worker disabled, skipping")
		return nil
	}
	topN := s.TopN
	if topN <= 0 {
		topN = 5
	}
	headlines, err := w.fetcher.Fetch(ctx, topN)
	if err != nil {
		c := Classification{
			MeasuredAt:       time.Now().UTC(),
			LLMModel:         "(none)",
			PerHeadlineJSON:  []byte("[]"),
			RawHeadlinesJSON: []byte("[]"),
			PromptHash:       hashPrompt(""),
		}
		id, _ := w.persistor.PersistFailure(ctx, c, err)
		w.publishNewsAlert(id, "none", c.MeasuredAt)
		return err
	}
	c, classifyErr := w.classifier.Classify(ctx, headlines)
	if classifyErr != nil {
		id, _ := w.persistor.PersistFailure(ctx, c, classifyErr)
		w.publishNewsAlert(id, "none", c.MeasuredAt)
		return classifyErr
	}
	id, err := w.persistor.PersistSuccess(ctx, c)
	if err != nil {
		w.log.Error().Err(err).Msg("news persist failed (data lost)")
		return nil
	}
	w.publishNewsAlert(id, c.Impact, c.MeasuredAt)
	w.log.Info().Str("impact", c.Impact).Int("headlines", len(headlines)).Msg("news updated")
	return nil
}

func (w *Worker) publishNewsAlert(id int64, impact string, t time.Time) {
	if w.publisher == nil {
		return
	}
	w.publisher.Publish(eval.EvalEvent{
		Kind:       "news_alert",
		SnapshotID: id,
		Impact:     impact,
		OccurredAt: t.Unix(),
	})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
