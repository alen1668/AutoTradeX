package ingest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"

	"github.com/lizhaojie/tvbot/internal/agent/history"
	"github.com/lizhaojie/tvbot/internal/agent/macrocontext"
	"github.com/lizhaojie/tvbot/internal/agent/market"
	"github.com/lizhaojie/tvbot/internal/agent/portfolio"
	"github.com/lizhaojie/tvbot/internal/agent/scorer"
	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
	"github.com/lizhaojie/tvbot/internal/eval"
	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/store"
)

// AgentHook bundles the agent scoring layer's dependencies + per-instance
// alert throttle state. cmd/tvbot/main.go constructs one and passes it
// to NewService; tests substitute a hook with scorerOverride set.
//
// Any nil dependency is safe — the hook degrades gracefully (e.g. no
// market provider → prompt notes "市场数据暂不可用"). A nil *AgentHook
// itself means "agent layer not wired" (etcc skipped entirely).
type AgentHook struct {
	scorerFactory *scorer.Factory
	historyProv   *history.Provider
	portfolioProv *portfolio.Provider
	marketProv    *market.Provider

	publisher   eval.Publisher // nil-safe; Phase 3 wires in cmd/tvbot/main.go
	macroReader MacroReader    // nil-safe; injected by cmd/tvbot/main.go

	scorerOverride scorer.Scorer // tests inject a stub here

	llmAlertMu       sync.Mutex
	llmAlertLastSent time.Time
	keyAlertMu       sync.Mutex
	keyAlertLastSent time.Time
}

// MacroReader is the surface AgentHook depends on for fetching macro context.
// macrocontext.Reader implements this; tests can substitute a stub.
type MacroReader interface {
	Load(ctx context.Context) macrocontext.MacroContext
}

// NewAgentHook is the production constructor. Pass nil for any provider
// you don't want wired (the hook will skip that section of the prompt).
func NewAgentHook(f *scorer.Factory, hp *history.Provider, pp *portfolio.Provider, mp *market.Provider) *AgentHook {
	return &AgentHook{scorerFactory: f, historyProv: hp, portfolioProv: pp, marketProv: mp}
}

// WithPublisher wires the Phase 3 SSE broker into the hook. Returns h for
// builder-style chaining. nil is accepted and turns publish into a no-op.
func (h *AgentHook) WithPublisher(p eval.Publisher) *AgentHook {
	h.publisher = p
	return h
}

// WithMacroReader wires the macrocontext reader. nil is accepted: the
// prompt then renders all macro sections as "暂不可用" via the template
// fallback branches.
func (h *AgentHook) WithMacroReader(r MacroReader) *AgentHook {
	h.macroReader = r
	return h
}

// publishScoreEvent fires an agent_score EvalEvent if a publisher is wired.
// Called from evaluate() once a verdict is reached. Never blocks (Broker
// is non-blocking) and never affects the trading decision.
func (h *AgentHook) publishScoreEvent(signalID int64, symbol string, score *int, decision string, latencyMs int) {
	if h == nil || h.publisher == nil {
		return
	}
	h.publisher.Publish(eval.EvalEvent{
		Kind:       "agent_score",
		SignalID:   signalID,
		Symbol:     symbol,
		AgentScore: score,
		Decision:   decision,
		LatencyMs:  latencyMs,
		OccurredAt: time.Now().Unix(),
	})
}

// agentVerdict tells the ingest service what to do after agent scoring.
// Action is the only field service.go cares about for branching; the rest
// are for persistence + notifications.
type agentVerdict struct {
	Action    string // "trade" | "abandon" | "skipped" (skipped = scorer disabled / not wired)
	Score     int    // -1 when N/A or LLM failed
	Decision  string // approve | abandon | failed | "" (skipped)
	DryRun    bool
	Reasoning string
}

// evaluate runs agent scoring and applies dry_run / fail_mode to produce
// a verdict. NEVER returns an error: any failure is mapped onto the
// verdict's fields so service.go has a single decision point.
func (h *AgentHook) evaluate(
	ctx context.Context,
	pool *pgxpool.Pool,
	log zerolog.Logger,
	notifier notify.Notifier,
	settings *store.Settings,
	sig *sigpkg.Signal,
	strat *strategy.Strategy,
	signalID int64,
) agentVerdict {
	if h == nil || !settings.AgentScorerEnabled {
		return agentVerdict{Action: "trade", Score: -1, DryRun: settings.AgentScorerDryRun}
	}

	in := h.assembleInput(ctx, log, settings, sig, strat)

	var sc scorer.Scorer
	if h.scorerOverride != nil {
		sc = h.scorerOverride
	} else if h.scorerFactory != nil {
		sc = h.scorerFactory.WithSignal(signalID, settings.AgentScorerModel, settings.AgentScorerTimeoutMs)
	} else {
		// hook constructed without a scorer factory and no override —
		// treat as not wired.
		return agentVerdict{Action: "trade", Score: -1, DryRun: settings.AgentScorerDryRun}
	}

	res, _ := sc.Score(ctx, in)

	// Phase 3: push agent_score event to any SSE subscribers. Fire-and-forget.
	var scorePtr *int
	if res.Score >= 0 {
		s := res.Score
		scorePtr = &s
	}
	h.publishScoreEvent(signalID, sig.Symbol, scorePtr, res.Decision, res.LatencyMs)

	h.maybeAlertUnhealthy(ctx, notifier)

	if res.Decision == "failed" {
		if settings.LLMAPIKey == "" {
			h.maybeAlertKeyMissing(ctx, notifier)
		}
		if settings.AgentScorerFailMode == "closed" {
			return agentVerdict{
				Action: "abandon", Score: -1, Decision: "failed",
				DryRun: settings.AgentScorerDryRun,
				Reasoning: "LLM 失败,fail_mode=closed: " + res.Reasoning,
			}
		}
		return agentVerdict{
			Action: "trade", Score: -1, Decision: "failed",
			DryRun: settings.AgentScorerDryRun, Reasoning: res.Reasoning,
		}
	}

	if res.Score >= settings.AgentScorerThreshold {
		return agentVerdict{
			Action: "trade", Score: res.Score, Decision: "approve",
			DryRun: settings.AgentScorerDryRun, Reasoning: res.Reasoning,
		}
	}
	// score < threshold
	if settings.AgentScorerDryRun {
		return agentVerdict{
			Action: "trade", Score: res.Score, Decision: "abandon",
			DryRun: true, Reasoning: res.Reasoning,
		}
	}
	return agentVerdict{
		Action: "abandon", Score: res.Score, Decision: "abandon",
		DryRun: false, Reasoning: res.Reasoning,
	}
}

// assembleInput pulls history / portfolio / market in parallel. Any
// individual provider failure leaves the relevant ScoreInput field at
// its zero value (nil for Portfolio/Market, empty slices for histories).
func (h *AgentHook) assembleInput(
	ctx context.Context,
	log zerolog.Logger,
	settings *store.Settings,
	sig *sigpkg.Signal, strat *strategy.Strategy,
) scorer.ScoreInput {
	in := scorer.ScoreInput{
		Signal:         sig,
		Strategy:       strat,
		HighVolWindows: market.ActiveWindows(time.Now()),
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		if h.historyProv != nil {
			in.SymbolHistory, _ = h.historyProv.SymbolHistory(gctx, sig.StrategyID, sig.Symbol, settings.AgentScorerHistoryLimit)
			in.StrategyHistory, _ = h.historyProv.StrategyHistory(gctx, sig.StrategyID, settings.AgentScorerHistoryLimit)
		}
		return nil
	})
	g.Go(func() error {
		if h.portfolioProv != nil {
			in.Portfolio, _ = h.portfolioProv.Snapshot(gctx)
		}
		return nil
	})
	g.Go(func() error {
		if h.marketProv != nil {
			mc, _ := h.marketProv.GetContext(gctx, sig.Symbol)
			if mc != nil {
				in.Market = &scorer.MarketContext{
					Symbol:           mc.Symbol,
					Last24hHigh:      mc.Last24hHigh,
					Last24hLow:       mc.Last24hLow,
					Last24hChangePct: mc.Last24hChangePct,
					Last1hChangePct:  mc.Last1hChangePct,
					PriceVs24hRange:  mc.PriceVs24hRange,
					Volatility24h:    mc.Volatility24h,
					KlineLookback1h:  mc.KlineLookback1h,
				}
			}
		}
		return nil
	})
	g.Go(func() error {
		if h.macroReader != nil {
			in.Macro = h.macroReader.Load(gctx)
		}
		return nil
	})
	_ = g.Wait()
	return in
}

func (h *AgentHook) maybeAlertUnhealthy(ctx context.Context, n notify.Notifier) {
	if h.scorerFactory == nil {
		return
	}
	bad, fails, total := h.scorerFactory.Health().IsUnhealthy()
	if !bad {
		return
	}
	h.llmAlertMu.Lock()
	defer h.llmAlertMu.Unlock()
	if time.Since(h.llmAlertLastSent) < 10*time.Minute {
		return
	}
	h.llmAlertLastSent = time.Now()
	_ = n.Send(ctx, notify.BuildAgentLLMUnhealthyMessage(fails, total))
}

func (h *AgentHook) maybeAlertKeyMissing(ctx context.Context, n notify.Notifier) {
	h.keyAlertMu.Lock()
	defer h.keyAlertMu.Unlock()
	if time.Since(h.keyAlertLastSent) < 24*time.Hour {
		return
	}
	h.keyAlertLastSent = time.Now()
	_ = n.Send(ctx, notify.BuildAgentAPIKeyMissingMessage())
}

// abandonReason is the string written to signals.decision_reason when
// agent rejects.
func abandonReason(v agentVerdict) string {
	if v.Decision == "failed" {
		return "agent_failed: " + v.Reasoning
	}
	return fmt.Sprintf("agent_score=%d: %s", v.Score, v.Reasoning)
}
