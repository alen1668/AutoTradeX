package outcome

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// ExitDecisionForOutcome is a slim view of one agent_exit_decisions row
// needed for if-hold backfill. The producer is the wireup-side adapter
// that projects store.ExitDecisionRow → ExitDecisionForOutcome.
//
// ActualPnLPct is realised PnL pct for active non-hold decisions; nil
// otherwise (shadow mode, or hold action). The worker passes it through
// to ComputeIfHold which uses nil-actual to mean "leave Label unset".
type ExitDecisionForOutcome struct {
	ID           int64
	Symbol       string
	Side         string
	EntryPrice   decimal.Decimal
	Action       string
	Mode         string
	DecisionTime time.Time
	ActualPnLPct *decimal.Decimal
}

type PendingExitReader interface {
	ListPending(ctx context.Context, olderThan time.Time, limit int) ([]ExitDecisionForOutcome, error)
}

type KlineCloseFetcher interface {
	CounterfactClose(ctx context.Context, symbol string, t time.Time) (*decimal.Decimal, error)
}

type ExitOutcomeWriter interface {
	SetIfHoldOutcome(ctx context.Context, id int64, horizonMin int, pct *decimal.Decimal, label *string) error
}

type ExitOutcomeWorker struct {
	r          PendingExitReader
	klines     KlineCloseFetcher
	w          ExitOutcomeWriter
	horizonMin int
	staleH     int
	log        zerolog.Logger
}

func NewExitOutcomeWorker(r PendingExitReader, k KlineCloseFetcher, w ExitOutcomeWriter, horizonMin, staleH int, log zerolog.Logger) *ExitOutcomeWorker {
	return &ExitOutcomeWorker{r: r, klines: k, w: w, horizonMin: horizonMin, staleH: staleH, log: log}
}

func (w *ExitOutcomeWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	for {
		if err := w.RunOnce(ctx); err != nil {
			w.log.Warn().Err(err).Msg("exit_outcome: RunOnce failed")
		}
		t := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// RunOnce scans pending exit decisions older than (now - horizon),
// fetches the counterfact close, and backfills if-hold + label. Hold
// decisions and shadow-mode decisions get the if-hold pct only (Label
// stays nil — no comparable baseline).
func (w *ExitOutcomeWorker) RunOnce(ctx context.Context) error {
	cutoff := time.Now().Add(-time.Duration(w.horizonMin) * time.Minute)
	rows, err := w.r.ListPending(ctx, cutoff, 200)
	if err != nil {
		return err
	}
	for _, r := range rows {
		target := r.DecisionTime.Add(time.Duration(w.horizonMin) * time.Minute)
		close, err := w.klines.CounterfactClose(ctx, r.Symbol, target)
		if err != nil {
			w.log.Warn().Err(err).Int64("id", r.ID).Msg("exit_outcome: kline fetch failed")
			continue
		}
		in := IfHoldInput{
			Symbol: r.Symbol, Side: r.Side, EntryPrice: r.EntryPrice,
			DecisionTime: r.DecisionTime, HorizonMin: w.horizonMin,
			CounterfactPrice: close, ActualPnLUSDPct: r.ActualPnLPct,
		}
		// Hold decisions in active mode + all shadow decisions: leave Label nil.
		if r.Action == "hold" || r.Mode == "shadow" {
			in.ActualPnLUSDPct = nil
		}
		res := ComputeIfHold(in)
		if res.Pending {
			// Stale check: if older than staleH, mark as 'unavailable' once
			// so we don't keep retrying forever.
			if time.Since(r.DecisionTime) > time.Duration(w.staleH)*time.Hour {
				lbl := "unavailable"
				_ = w.w.SetIfHoldOutcome(ctx, r.ID, w.horizonMin, nil, &lbl)
			}
			continue
		}
		_ = w.w.SetIfHoldOutcome(ctx, r.ID, w.horizonMin, &res.IfHoldPnLPct, res.Label)
	}
	return nil
}
