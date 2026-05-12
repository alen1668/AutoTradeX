package macrocontext

import (
	"context"
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/calendar"
	"github.com/lizhaojie/tvbot/internal/store"
)

const btcSymbol = "BTCUSDT"

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

// PerpReader returns the latest perp_metrics row for a symbol.
type PerpReader interface {
	Latest(ctx context.Context, symbol string) (*store.PerpMetricsRecord, error)
}

// SettingsForPerp returns the lookback minutes used to gate stale perp data.
type SettingsForPerp interface {
	Get(ctx context.Context) (lookbackMinutes int, err error)
}

type Reader struct {
	regime   RegimeReader
	calendar CalendarSource
	news     NewsReader
	perp     PerpReader
	settings SettingsForPerp
	now      func() time.Time
}

func NewReader(r RegimeReader, c CalendarSource, n NewsReader) *Reader {
	return &Reader{regime: r, calendar: c, news: n, now: func() time.Time { return time.Now().UTC() }}
}

func (r *Reader) WithClock(now func() time.Time) *Reader {
	r.now = now
	return r
}

// WithPerp wires the perp_metrics reader + lookback settings source.
// Without this, Load returns nil PerpSelf / PerpBTC (fail-open).
func (r *Reader) WithPerp(p PerpReader, s SettingsForPerp) *Reader {
	r.perp = p
	r.settings = s
	return r
}

// Load aggregates four sources. Any individual failure -> nil/empty field.
// Never returns an error: the scorer path must not be blocked.
// signalSymbol selects which symbol's perp metrics to load as PerpSelf;
// PerpBTC is always BTCUSDT (or aliased to PerpSelf when signal == BTC).
func (r *Reader) Load(ctx context.Context, signalSymbol string) MacroContext {
	now := r.now()
	out := MacroContext{}

	if r.regime != nil {
		ctxR, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
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
		cancel()
	}

	if r.calendar != nil {
		ctxC, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
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
		cancel()
	}

	if r.news != nil {
		ctxN, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
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
		cancel()
	}

	if r.perp != nil {
		lookback := 30
		if r.settings != nil {
			if lb, err := r.settings.Get(ctx); err == nil && lb > 0 {
				lookback = lb
			}
		}
		out.PerpSelf = r.loadPerp(ctx, signalSymbol, now, lookback)
		if signalSymbol == btcSymbol {
			out.PerpBTC = out.PerpSelf
		} else {
			out.PerpBTC = r.loadPerp(ctx, btcSymbol, now, lookback)
		}
	}

	return out
}

func (r *Reader) loadPerp(ctx context.Context, symbol string, now time.Time, lookbackMin int) *PerpSnapshot {
	if symbol == "" || r.perp == nil {
		return nil
	}
	ctxP, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	rec, err := r.perp.Latest(ctxP, symbol)
	if err != nil || rec == nil {
		return nil
	}
	stale := MinutesBetween(rec.ObservedAt, now)
	if stale > lookbackMin {
		return nil
	}
	return &PerpSnapshot{
		Symbol:             rec.Symbol,
		FundingRatePct:     rec.FundingRate.Mul(decimal.NewFromInt(100)),
		FundingLabel:       rec.FundingLabel,
		OpenInterest24hPct: rec.OpenInterest24hPct,
		OISignal:           rec.OISignal,
		Price24hPct:        rec.Price24hPct,
		TopLSRatio:         rec.TopLSRatio,
		LSLabel:            rec.LSLabel,
		MeasuredAt:         rec.ObservedAt,
		StaleMinutes:       stale,
	}
}
