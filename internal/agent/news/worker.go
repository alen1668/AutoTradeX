package news

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/eval"
	"github.com/lizhaojie/tvbot/internal/notify"
)

type WorkerSettings struct {
	Enabled     bool
	IntervalMin int
	TopN        int
	// NotifyMinImpact gates whether worker sends a notification on each tick.
	// Empty / unrecognized → "" disables notifications entirely.
	// Values in ascending order: none < low < medium < high.
	NotifyMinImpact string
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
	notifier   notify.Notifier // nil-safe; when nil or NoOp, worker skips push
	settings   SettingsReader
	log        zerolog.Logger
}

func NewWorker(f Fetcher, c ClassifierLike, p Persistor, pub Publisher, sr SettingsReader, log zerolog.Logger) *Worker {
	return &Worker{
		fetcher: f, classifier: c, persistor: p, publisher: pub, settings: sr, log: log,
	}
}

// WithNotifier wires a notifier (WeCom / Feishu / Multi). The worker calls
// Send on success-path runs whose Impact meets the WorkerSettings threshold.
// Failure rows never trigger notifications (they're audit-only).
func (w *Worker) WithNotifier(n notify.Notifier) *Worker {
	w.notifier = n
	return w
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
	w.maybeNotify(ctx, id, c, s.NotifyMinImpact)
	w.log.Info().Str("impact", c.Impact).Int("headlines", len(headlines)).Msg("news updated")
	return nil
}

// impactRank turns the textual impact into a comparable int. Anything outside
// the known set is mapped to -1 ("never meets the threshold").
func impactRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "none":
		return 0
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	}
	return -1
}

// maybeNotify sends a Message via the wired notifier when:
//   - notifier is non-nil
//   - minImpact is a recognized threshold
//   - the current Impact rank is >= minImpact rank
//
// Notify failures are logged but never propagate (decision path is the worker's
// main job, not the side-channel push).
func (w *Worker) maybeNotify(ctx context.Context, snapshotID int64, c Classification, minImpact string) {
	if w.notifier == nil {
		return
	}
	if _, isNoOp := w.notifier.(notify.NoOp); isNoOp {
		return
	}
	threshold := impactRank(minImpact)
	if threshold < 0 {
		return // unconfigured or unrecognized — push disabled
	}
	if impactRank(c.Impact) < threshold {
		return
	}
	severity := notify.SeverityInfo
	if c.Impact == "high" {
		severity = notify.SeverityWarn
	}
	msg := notify.Message{
		Title:    "📰 加密新闻 — impact " + strings.ToUpper(c.Impact),
		Body:     c.Summary,
		Severity: severity,
		Fields: map[string]any{
			"snapshot_id": snapshotID,
			"模型":          c.LLMModel,
			"标题数":         len(c.PerHeadline),
			"详情":          "/eval/news/" + itoa(snapshotID),
		},
	}
	if err := w.notifier.Send(ctx, msg); err != nil {
		w.log.Warn().Err(err).Msg("news notifier send failed")
	}
}

func itoa(n int64) string {
	// Avoid pulling strconv just for one int64; the worker file already has
	// strings imported and Sprintf is overkill.
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
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
