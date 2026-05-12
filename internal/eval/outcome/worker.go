package outcome

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// Config controls the outcome backfiller worker. Loaded from system_state
// settings; defaults applied in NewWorker for any zero value.
type Config struct {
	HorizonMin   int
	WinThresh    decimal.Decimal
	LossThresh   decimal.Decimal
	BatchSize    int
	ScanInterval time.Duration
	StaleCutoffH int
	MinAgeMin    int // age cutoff for PendingEvaluations; defaults to HorizonMin+5
}

// Worker periodically scans agent_evaluations rows lacking an outcome
// label and computes one using PendingReader (approve path) +
// KlineFetcher (abandon counterfactual path). Per-row failures log
// warn and continue; transient query failures bubble up from RunOnce.
type Worker struct {
	pending PendingReader
	kline   KlineFetcher
	writer  Writer
	cfg     Config
	log     zerolog.Logger
}

func NewWorker(pending PendingReader, kline KlineFetcher, writer Writer, cfg Config, log *zerolog.Logger) *Worker {
	z := zerolog.Nop()
	if log != nil {
		z = *log
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 200
	}
	if cfg.ScanInterval <= 0 {
		cfg.ScanInterval = 5 * time.Minute
	}
	if cfg.StaleCutoffH <= 0 {
		cfg.StaleCutoffH = 24
	}
	if cfg.MinAgeMin <= 0 {
		cfg.MinAgeMin = cfg.HorizonMin + 5
	}
	return &Worker{pending: pending, kline: kline, writer: writer, cfg: cfg, log: z}
}

// Start blocks until ctx is done. Runs a catch-up pass immediately,
// then on every ScanInterval tick.
func (w *Worker) Start(ctx context.Context) {
	if err := w.RunOnce(ctx); err != nil {
		w.log.Warn().Err(err).Msg("outcome: catch-up run failed (will retry)")
	}
	t := time.NewTicker(w.cfg.ScanInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.RunOnce(ctx); err != nil {
				w.log.Warn().Err(err).Msg("outcome: scheduled run failed (will retry)")
			}
		}
	}
}

// RunOnce processes one batch. Per-row failures log warn and continue;
// a failure to FETCH the batch itself is returned to the caller.
func (w *Worker) RunOnce(ctx context.Context) error {
	rows, err := w.pending.PendingEvaluations(ctx, w.cfg.BatchSize, w.cfg.MinAgeMin)
	if err != nil {
		return err
	}
	for _, row := range rows {
		in := Input{
			SignalID:     row.SignalID,
			Symbol:       row.Symbol,
			Direction:    row.Direction,
			SignalPrice:  row.SignalPrice,
			SignalTime:   row.SignalTime,
			HorizonMin:   w.cfg.HorizonMin,
			WinThresh:    w.cfg.WinThresh,
			LossThresh:   w.cfg.LossThresh,
			Now:          time.Now().UTC(),
			StaleCutoffH: w.cfg.StaleCutoffH,
		}
		// Approve path: realized PnL on the position opened by this signal.
		if pnl, err := w.pending.PositionPnL(ctx, row.SignalID); err != nil {
			w.log.Warn().Err(err).Int64("signal", row.SignalID).Msg("outcome: PositionPnL failed")
		} else if pnl != nil {
			in.ActualPnLUSD = pnl
		}
		// Abandon path: counterfactual close at signal_time + horizon.
		if in.ActualPnLUSD == nil && w.kline != nil {
			target := row.SignalTime.Add(time.Duration(w.cfg.HorizonMin) * time.Minute)
			if px, err := w.kline.CounterfactClose(ctx, row.Symbol, target); err != nil {
				w.log.Warn().Err(err).Int64("signal", row.SignalID).Msg("outcome: CounterfactClose failed")
			} else if px != nil {
				in.CounterfactPrice = px
			}
		}
		res := Compute(in)
		if res.Label == LabelPending {
			continue // not enough info yet; leave row NULL for next sweep
		}
		if err := w.writer.WriteOutcome(ctx, row.SignalID, res); err != nil {
			w.log.Warn().Err(err).Int64("signal", row.SignalID).Msg("outcome: WriteOutcome failed")
		}
	}
	return nil
}
