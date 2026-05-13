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
	title, severity := newsTitle(c.Impact, c.Direction)
	// 通知保持精简: 仅 title (含方向 emoji) + body (LLM summary)。
	// 不带 snapshot_id / 模型 / 标题数 / 详情链接等调试字段(用户反馈这些是噪音)。
	msg := notify.Message{
		Title:    title,
		Body:     c.Summary,
		Severity: severity,
	}
	if err := w.notifier.Send(ctx, msg); err != nil {
		w.log.Warn().Err(err).Msg("news notifier send failed")
	}
}

// newsTitle 把 (impact, direction) 二维渲染成醒目的中文标题 + 严重级。
// 设计目标:用户在飞书/Telegram 一眼看出"利好/利空 + 重要程度",不需要看正文。
//
//	高 利好 → 🚀🟢🟢🟢 重大利好｜加密新闻
//	高 利空 → ⚠️🔴🔴🔴 重大利空｜加密新闻
//	中 利好 → 📈🟢🟢 利好｜加密新闻
//	中 利空 → 📉🔴🔴 利空｜加密新闻
//	中 分歧 → ⚖️🟠🟠 多空分歧｜加密新闻
//	低 利好 → 🟢 偏多｜加密新闻
//	低 利空 → 🔴 偏空｜加密新闻
//	其它   → ℹ️ 信息性｜加密新闻
//
// 高 impact 默认 SeverityWarn (飞书会用红色卡片 / Telegram 用警报样式)。
func newsTitle(impact, direction string) (string, notify.Severity) {
	imp := strings.ToLower(strings.TrimSpace(impact))
	dir := strings.ToLower(strings.TrimSpace(direction))
	sev := notify.SeverityInfo
	if imp == "high" {
		sev = notify.SeverityWarn
	}

	switch imp {
	case "high":
		switch dir {
		case "bullish":
			return "🚀🟢🟢🟢 重大利好｜加密新闻", sev
		case "bearish":
			return "⚠️🔴🔴🔴 重大利空｜加密新闻", sev
		case "mixed":
			return "⚖️🟠🟠🟠 重大事件 · 多空分歧｜加密新闻", sev
		}
		return "❗ 重大事件 · 信息性｜加密新闻", sev
	case "medium":
		switch dir {
		case "bullish":
			return "📈🟢🟢 利好｜加密新闻", sev
		case "bearish":
			return "📉🔴🔴 利空｜加密新闻", sev
		case "mixed":
			return "⚖️🟠 多空分歧｜加密新闻", sev
		}
		return "ℹ️ 信息性更新｜加密新闻", sev
	case "low":
		switch dir {
		case "bullish":
			return "🟢 偏多｜加密新闻", sev
		case "bearish":
			return "🔴 偏空｜加密新闻", sev
		}
		return "ℹ️ 一般动态｜加密新闻", sev
	}
	return "📰 加密新闻", sev
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
