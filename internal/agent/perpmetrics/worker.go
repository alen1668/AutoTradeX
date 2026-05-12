package perpmetrics

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// KlineSource gives 24h price % change for a symbol. Worker uses it for
// OI signal cross-reference.
type KlineSource interface {
	Price24hPct(ctx context.Context, symbol string) (decimal.Decimal, error)
}

// Store persists Snapshots.
type Store interface {
	Insert(ctx context.Context, s Snapshot) error
}

// SymbolSource returns distinct symbols for active (enabled, !archived) strategies.
type SymbolSource interface {
	ActiveSymbols(ctx context.Context) ([]string, error)
}

// SettingsReader returns the latest WorkerSettings.
type SettingsReader interface {
	Read(ctx context.Context) (WorkerSettings, error)
}

const (
	btcSymbol       = "BTCUSDT"
	defaultInterval = 5 * time.Minute
)

type Worker struct {
	fetcher  Fetcher
	klines   KlineSource
	store    Store
	symbols  SymbolSource
	settings SettingsReader
	log      zerolog.Logger
	interval time.Duration
	now      func() time.Time
}

func NewWorker(f Fetcher, k KlineSource, s Store, syms SymbolSource, set SettingsReader, log zerolog.Logger) *Worker {
	return &Worker{
		fetcher: f, klines: k, store: s, symbols: syms, settings: set,
		log: log, interval: defaultInterval,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (w *Worker) WithInterval(d time.Duration) *Worker { w.interval = d; return w }

func (w *Worker) WithClock(now func() time.Time) *Worker {
	w.now = now
	return w
}

// Start blocks until ctx is cancelled. Runs RunOnce immediately, then on every
// w.interval tick. settings.Enabled is re-checked per tick (hot toggle).
func (w *Worker) Start(ctx context.Context) {
	if err := w.RunOnce(ctx); err != nil {
		w.log.Warn().Err(err).Msg("perp initial run failed")
	}
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.RunOnce(ctx); err != nil {
				w.log.Warn().Err(err).Msg("perp run failed")
			}
		}
	}
}

// RunOnce fetches one snapshot per (active symbol ∪ BTCUSDT). Per-symbol
// failures are logged but don't abort the loop.
func (w *Worker) RunOnce(ctx context.Context) error {
	s, err := w.settings.Read(ctx)
	if err != nil {
		return err
	}
	if !s.Enabled {
		w.log.Debug().Msg("perp worker disabled, skipping")
		return nil
	}
	actives, err := w.symbols.ActiveSymbols(ctx)
	if err != nil {
		w.log.Warn().Err(err).Msg("perp symbols query failed; falling back to BTCUSDT only")
		actives = nil
	}
	symbols := uniqWithBTC(actives)

	nowT := w.now()
	okCount, failCount := 0, 0
	for _, sym := range symbols {
		if err := w.fetchOne(ctx, sym, nowT); err != nil {
			failCount++
			w.log.Warn().Err(err).Str("symbol", sym).Msg("perp fetch one failed")
			continue
		}
		okCount++
	}
	w.log.Info().Int("ok", okCount).Int("fail", failCount).Int("total", len(symbols)).Msg("perp tick complete")
	return nil
}

func (w *Worker) fetchOne(ctx context.Context, symbol string, now time.Time) error {
	premium, err := w.fetcher.PremiumIndex(ctx, symbol)
	if err != nil {
		return err
	}
	oi, err := w.fetcher.OpenInterest(ctx, symbol)
	if err != nil {
		return err
	}
	ls, err := w.fetcher.TopLongShortRatio(ctx, symbol)
	if err != nil {
		return err
	}
	price24h, err := w.klines.Price24hPct(ctx, symbol)
	if err != nil {
		w.log.Debug().Err(err).Str("symbol", symbol).
			Msg("price24h unavailable; OI signal will be neutral")
		price24h = decimal.Zero
	}

	oi24hPct := decimal.Zero
	if !oi.Prev24h.IsZero() {
		oi24hPct = oi.Current.Sub(oi.Prev24h).Div(oi.Prev24h).Mul(decimal.NewFromInt(100))
	}

	snap := Snapshot{
		Symbol:             symbol,
		ObservedAt:         now,
		FundingRate:        premium.FundingRate,
		NextFundingTime:    premium.NextFundingTime,
		MarkPrice:          premium.MarkPrice,
		OpenInterest:       oi.Current,
		OpenInterest24hPct: oi24hPct,
		Price24hPct:        price24h,
		TopLSRatio:         ls,
		FundingLabel:       FundingLabel(premium.FundingRate),
		OISignal:           OISignal(oi24hPct, price24h),
		LSLabel:            LSLabel(ls),
	}
	return w.store.Insert(ctx, snap)
}

func uniqWithBTC(actives []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(actives)+1)
	for _, s := range actives {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if !seen[btcSymbol] {
		out = append(out, btcSymbol)
	}
	return out
}
