package regime

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/agent/market"
	"github.com/lizhaojie/tvbot/internal/store"
)

// WorkerSettings is the subset of system_state the worker reads each tick.
type WorkerSettings struct {
	Enabled     bool
	IntervalMin int
}

// SettingsReader returns the latest WorkerSettings.
type SettingsReader interface {
	Read(ctx context.Context) (WorkerSettings, error)
}

// Repository is the subset of store.MarketRegimeRepo the worker needs.
type Repository interface {
	Insert(ctx context.Context, rec store.MarketRegimeRecord) (int64, error)
}

// Worker runs Classify on a ticker and writes results to market_regime.
type Worker struct {
	client   market.KlineClient
	repo     Repository
	settings SettingsReader
	log      zerolog.Logger
	symbol   string
	lookback int
}

func NewWorker(c market.KlineClient, repo Repository, s SettingsReader, log zerolog.Logger) *Worker {
	return &Worker{
		client:   c,
		repo:     repo,
		settings: s,
		log:      log,
		symbol:   "BTCUSDT",
		lookback: 168,
	}
}

func (w *Worker) WithSymbol(s string) *Worker { w.symbol = s; return w }

// Start blocks until ctx is cancelled. RunOnce is invoked immediately and
// then on every tick. The tick interval is re-read each cycle so settings
// changes take effect at the next boundary.
func (w *Worker) Start(ctx context.Context) {
	if err := w.RunOnce(ctx); err != nil {
		w.log.Warn().Err(err).Msg("regime initial run failed")
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
				w.log.Warn().Err(err).Msg("regime run failed")
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
		w.log.Debug().Msg("regime worker disabled, skipping")
		return nil
	}
	candles, err := w.client.Get1hOHLC(ctx, w.symbol, w.lookback)
	if err != nil {
		return err
	}
	res := Classify(candles)
	if res.Label == "" {
		w.log.Warn().Int("candles", len(candles)).Msg("regime classifier returned empty (insufficient data)")
		return nil
	}
	now := time.Now().UTC()
	_, err = w.repo.Insert(ctx, store.MarketRegimeRecord{
		MeasuredAt:    now,
		Label:         res.Label,
		TrendStrength: res.TrendStrength,
		Volatility24h: res.Volatility24h,
		VolPercentile: res.VolPercentile,
		Change24hPct:  res.Change24hPct,
		PriceRangePos: res.PriceRangePos,
		KlineCount:    res.KlineCount,
	})
	if err != nil {
		return err
	}
	w.log.Info().Str("label", res.Label).Str("trend", res.TrendStrength.String()).
		Str("vol_pctl", res.VolPercentile.String()).Msg("regime updated")
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
