package exit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

// OpenPositionsReader returns all eligible open positions with current
// price already populated. Worker filters further by age/cooldown.
type OpenPositionsReader interface {
	ListOpen(ctx context.Context) ([]PositionSnapshot, error)
}

// ContextProvider builds the Input.Macro / Historical / KlineSnapshot /
// Pinned for one position. Failures degrade gracefully (caller continues
// with whatever fields it returned).
type ContextProvider interface {
	Build(ctx context.Context, p PositionSnapshot) (Input, error)
}

type Decider interface {
	Decide(ctx context.Context, in Input) (Decision, DecisionMeta, error)
}

type Store interface {
	Insert(ctx context.Context, p PositionSnapshot, d Decision, m DecisionMeta, mode Mode) (int64, error)
}

type SettingsReader interface {
	Read(ctx context.Context) (Config, error)
}

type CooldownReader interface {
	// LastDecisionAt returns the most recent decision timestamp for the
	// position, or nil if none. Worker compares to cfg.DecisionCooldown.
	LastDecisionAt(ctx context.Context, positionID int64) (*time.Time, error)
}

type Executor interface {
	TightenSL(ctx context.Context, positionID int64, newPrice decimal.Decimal) error
	TakePartial(ctx context.Context, positionID int64, pct decimal.Decimal) error
	ExitNow(ctx context.Context, positionID int64) error
}

// ExecutionRecorder writes back the post-execute status to the decision
// row. Worker.execute calls it once per non-shadow non-hold decision.
// nil-safe: shadow-only deployments may pass nil.
type ExecutionRecorder interface {
	SetExecution(ctx context.Context, decisionID int64, executedAt *time.Time, status string, errMsg string) error
}

type WorkerDeps struct {
	Reader   OpenPositionsReader
	Ctx      ContextProvider
	Decider  Decider
	Store    Store
	Settings SettingsReader
	Cooldown CooldownReader
	Executor Executor
	Recorder ExecutionRecorder // optional; nil → no execution writeback
	Log      zerolog.Logger
}

type Worker struct {
	d        WorkerDeps
	inflight sync.Map // positionID → struct{}
}

func NewWorker(d WorkerDeps) *Worker { return &Worker{d: d} }

// Start blocks until ctx done. Reads cfg every iteration so toggle takes
// effect without restart.
func (w *Worker) Start(ctx context.Context) {
	for {
		cfg, err := w.d.Settings.Read(ctx)
		if err != nil {
			w.d.Log.Warn().Err(err).Msg("exit: settings read failed; sleep 60s")
			if !sleepCtx(ctx, time.Minute) {
				return
			}
			continue
		}
		interval := cfg.ScanInterval
		if interval <= 0 {
			interval = 5 * time.Minute
		}
		if cfg.Enabled {
			if err := w.RunOnce(ctx); err != nil {
				w.d.Log.Error().Err(err).Msg("exit: RunOnce failed")
			}
		}
		if !sleepCtx(ctx, interval) {
			return
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// RunOnce performs one scan-and-decide pass. Idempotent; safe to call
// from cron + manual triggers.
func (w *Worker) RunOnce(ctx context.Context) error {
	cfg, err := w.d.Settings.Read(ctx)
	if err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	if !cfg.Enabled {
		return nil
	}

	positions, err := w.d.Reader.ListOpen(ctx)
	if err != nil {
		return fmt.Errorf("list open: %w", err)
	}

	conc := cfg.MaxConcurrent
	if conc < 1 {
		conc = 1
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for _, p := range positions {
		p := p
		if !w.eligible(ctx, p, cfg) {
			continue
		}
		if _, busy := w.inflight.LoadOrStore(p.VirtualPositionID, struct{}{}); busy {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer w.inflight.Delete(p.VirtualPositionID)
			w.processOne(ctx, p, cfg)
		}()
	}
	wg.Wait()
	return nil
}

func (w *Worker) eligible(ctx context.Context, p PositionSnapshot, cfg Config) bool {
	if p.PositionAge < cfg.MinPositionAge {
		return false
	}
	last, err := w.d.Cooldown.LastDecisionAt(ctx, p.VirtualPositionID)
	if err == nil && last != nil && time.Since(*last) < cfg.DecisionCooldown {
		return false
	}
	return true
}

func (w *Worker) processOne(ctx context.Context, p PositionSnapshot, cfg Config) {
	in, err := w.d.Ctx.Build(ctx, p)
	if err != nil {
		w.d.Log.Warn().Err(err).Int64("pos", p.VirtualPositionID).Msg("exit: ctx build degraded")
		// continue with whatever fields Build returned (may be empty Input)
	}
	in.Position = p

	d, meta, err := w.d.Decider.Decide(ctx, in)
	if err != nil {
		// fail-open: log + record an explicit hold row so audit trail is intact
		fallback := Decision{Action: ActionHold, Confidence: ConfLow,
			Reasoning: fmt.Sprintf("[llm_error] %s", err.Error())}
		_, _ = w.d.Store.Insert(ctx, p, fallback,
			DecisionMeta{Model: cfg.Model, PromptHash: PromptHash()}, cfg.Mode)
		return
	}

	// Constraint checks: rewrite to hold if violated, mark in reasoning.
	if violation := checkConstraints(p, d, cfg); violation != "" {
		d = Decision{Action: ActionHold, Confidence: d.Confidence,
			Reasoning: fmt.Sprintf("[constraint_violated:%s] %s", violation, d.Reasoning)}
	}

	id, err := w.d.Store.Insert(ctx, p, d, meta, cfg.Mode)
	if err != nil {
		w.d.Log.Error().Err(err).Int64("pos", p.VirtualPositionID).Msg("exit: store insert failed")
		return
	}

	if cfg.Mode != ModeActive || d.Action == ActionHold {
		return
	}
	w.execute(ctx, id, p, d)
}

func checkConstraints(p PositionSnapshot, d Decision, cfg Config) string {
	switch d.Action {
	case ActionTightenSL:
		if d.ProposedSLPrice == nil {
			return "missing_proposed_sl"
		}
		if p.CurrentSLPrice != nil {
			// long: new must be > current; short: new must be < current
			if p.Side == "long" && d.ProposedSLPrice.LessThanOrEqual(*p.CurrentSLPrice) {
				return "sl_not_tighter"
			}
			if p.Side == "short" && d.ProposedSLPrice.GreaterThanOrEqual(*p.CurrentSLPrice) {
				return "sl_not_tighter"
			}
		}
	case ActionExitNow:
		if d.Confidence.Rank() < cfg.RequireConfidenceForExit.Rank() {
			return "confidence_below_threshold"
		}
	case ActionTakePartial:
		if d.PartialPct == nil {
			return "missing_partial_pct"
		}
		if d.PartialPct.LessThanOrEqual(decimal.Zero) || d.PartialPct.GreaterThan(decimal.NewFromFloat(0.5)) {
			return "partial_pct_out_of_range"
		}
	}
	return ""
}

func (w *Worker) execute(ctx context.Context, decID int64, p PositionSnapshot, d Decision) {
	if w.d.Executor == nil {
		return
	}
	var err error
	switch d.Action {
	case ActionTightenSL:
		err = w.d.Executor.TightenSL(ctx, p.VirtualPositionID, *d.ProposedSLPrice)
	case ActionTakePartial:
		err = w.d.Executor.TakePartial(ctx, p.VirtualPositionID, *d.PartialPct)
	case ActionExitNow:
		err = w.d.Executor.ExitNow(ctx, p.VirtualPositionID)
	default:
		return
	}
	now := time.Now()
	status := "success"
	errMsg := ""
	if err != nil {
		status = "failed"
		errMsg = err.Error()
		w.d.Log.Warn().Err(err).Int64("dec", decID).Msg("exit: executor failed")
	}
	if w.d.Recorder != nil {
		if rerr := w.d.Recorder.SetExecution(ctx, decID, &now, status, errMsg); rerr != nil {
			w.d.Log.Warn().Err(rerr).Int64("dec", decID).Msg("exit: SetExecution failed")
		}
	}
}
