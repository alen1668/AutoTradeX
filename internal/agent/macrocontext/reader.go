package macrocontext

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lizhaojie/tvbot/internal/agent/calendar"
	"github.com/lizhaojie/tvbot/internal/store"
)

// RegimeReader returns the latest market_regime row.
type RegimeReader interface {
	Latest(ctx context.Context) (*store.MarketRegimeRecord, error)
}

// CalendarSource queries events within a window.
type CalendarSource interface {
	ActiveBetween(ctx context.Context, from, to time.Time) ([]calendar.Event, error)
}

// NewsReader returns the latest news_snapshots row.
type NewsReader interface {
	Latest(ctx context.Context) (*store.NewsSnapshotRecord, error)
}

type Reader struct {
	regime   RegimeReader
	calendar CalendarSource
	news     NewsReader
	now      func() time.Time
}

func NewReader(r RegimeReader, c CalendarSource, n NewsReader) *Reader {
	return &Reader{regime: r, calendar: c, news: n, now: func() time.Time { return time.Now().UTC() }}
}

func (r *Reader) WithClock(now func() time.Time) *Reader {
	r.now = now
	return r
}

// Load aggregates three sources. Any individual failure -> nil/empty field.
// Never returns error: the scorer path must not be blocked.
func (r *Reader) Load(ctx context.Context) MacroContext {
	now := r.now()
	out := MacroContext{}

	if r.regime != nil {
		ctxR, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		if rec, err := r.regime.Latest(ctxR); err == nil && rec != nil {
			out.Regime = &Regime{
				Label:          rec.Label,
				TrendStrength:  rec.TrendStrength,
				Volatility24h:  rec.Volatility24h,
				VolatilityPctl: rec.VolPercentile,
				Change24hPct:   rec.Change24hPct,
				PriceRangePos:  rec.PriceRangePos,
				MeasuredAt:     rec.MeasuredAt,
				StaleMinutes:   MinutesBetween(rec.MeasuredAt, now),
			}
		}
	}

	if r.calendar != nil {
		ctxC, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		if events, err := r.calendar.ActiveBetween(ctxC, now.Add(-time.Hour), now.Add(time.Hour)); err == nil {
			out.Events = make([]Event, 0, len(events))
			for _, e := range events {
				mt := MinutesBetween(now, e.StartsAt)
				out.Events = append(out.Events, Event{
					Name:         e.Name,
					Currency:     e.Currency,
					Impact:       e.Impact,
					StartsAt:     e.StartsAt,
					MinutesTo:    mt,
					RelativeText: FormatRelativeText(mt),
				})
			}
		}
	}

	if r.news != nil {
		ctxN, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		if rec, err := r.news.Latest(ctxN); err == nil && rec != nil {
			na := &NewsAlert{
				Impact:       rec.Impact,
				Summary:      rec.Summary,
				Reasoning:    rec.Reasoning,
				MeasuredAt:   rec.MeasuredAt,
				StaleMinutes: MinutesBetween(rec.MeasuredAt, now),
			}
			if len(rec.PerHeadline) > 0 {
				var ph []HeadlineJudgment
				if err := json.Unmarshal(rec.PerHeadline, &ph); err == nil {
					na.PerHeadline = ph
				}
			}
			out.News = na
		}
	}
	return out
}
