package exit

import (
	"context"
	"fmt"
	"strings"

	"github.com/lizhaojie/tvbot/internal/agent/macrocontext"
)

// KlineProvider returns a pre-formatted multi-period text block for
// inclusion in the prompt.
type KlineProvider interface {
	Snapshot(ctx context.Context, symbol string) (string, error)
}

// MacroLoader fronts macrocontext.Reader. Defined as an interface so
// tests can substitute without spinning up the real Reader.
type MacroLoader interface {
	Load(ctx context.Context, symbol string) macrocontext.MacroContext
}

// HistoricalProvider returns the strategy×symbol outcome stats from
// agent_evaluations over the last 7 days.
type HistoricalProvider interface {
	Stats(ctx context.Context, strategyID, symbol string) (HistoricalStats, error)
}

// PinnedProvider returns the up-to-N pinned critique patterns.
type PinnedProvider interface {
	List(ctx context.Context, limit int) ([]PinnedPattern, error)
}

type DefaultContextProvider struct {
	klines     KlineProvider
	macro      MacroLoader
	historical HistoricalProvider
	pinned     PinnedProvider
	maxPinned  int
}

func NewDefaultContextProvider(k KlineProvider, m MacroLoader, h HistoricalProvider, p PinnedProvider, maxPinned int) *DefaultContextProvider {
	if maxPinned <= 0 {
		maxPinned = 5
	}
	return &DefaultContextProvider{klines: k, macro: m, historical: h, pinned: p, maxPinned: maxPinned}
}

// Build assembles all sub-context. Each sub-source failure degrades to a
// "暂不可用"/empty placeholder rather than aborting; Worker still calls
// Decide so audit trail captures the LLM's behaviour even with degraded
// inputs.
func (b *DefaultContextProvider) Build(ctx context.Context, p PositionSnapshot) (Input, error) {
	in := Input{}

	if b.klines != nil {
		if k, err := b.klines.Snapshot(ctx, p.Symbol); err == nil {
			in.KlineSnapshot = k
		} else {
			in.KlineSnapshot = "(K 线暂不可用)"
		}
	} else {
		in.KlineSnapshot = "(K 线暂不可用)"
	}

	if b.macro != nil {
		in.Macro = mapMacro(b.macro.Load(ctx, p.Symbol))
	} else {
		in.Macro = emptyMacro()
	}

	if b.historical != nil {
		if h, err := b.historical.Stats(ctx, p.StrategyID, p.Symbol); err == nil {
			in.Historical = h
		}
	}

	if b.pinned != nil {
		if pinned, err := b.pinned.List(ctx, b.maxPinned); err == nil {
			in.Pinned = pinned
		}
	}

	return in, nil
}

func emptyMacro() MacroBundle {
	return MacroBundle{Regime: "(暂不可用)", PerpFunding: "n/a", PerpOIDelta: "n/a",
		UpcomingEvents: "(无)", NewsImpact: "none", NewsSummary: "n/a"}
}

// mapMacro adapts macrocontext.MacroContext (5 sub-fields, each may be
// nil) to MacroBundle. Designed to never produce empty strings — the
// prompt template can render whatever we put here directly.
func mapMacro(mc macrocontext.MacroContext) MacroBundle {
	out := MacroBundle{
		Regime:         "(暂不可用)",
		PerpFunding:    "n/a",
		PerpOIDelta:    "n/a",
		UpcomingEvents: "(无)",
		NewsImpact:     "none",
		NewsSummary:    "n/a",
	}
	if mc.Regime != nil {
		out.Regime = mc.Regime.Label
	}
	if mc.PerpSelf != nil {
		out.PerpFunding = mc.PerpSelf.FundingRatePct.StringFixed(4) + "%"
		out.PerpOIDelta = mc.PerpSelf.OpenInterest24hPct.StringFixed(2) + "% (24h)"
	}
	if len(mc.Events) > 0 {
		parts := make([]string, 0, len(mc.Events))
		for _, e := range mc.Events {
			parts = append(parts, fmt.Sprintf("%s(%s, %s)", e.Name, e.Impact, e.RelativeText))
		}
		out.UpcomingEvents = strings.Join(parts, "; ")
	}
	if mc.News != nil {
		if mc.News.Impact != "" {
			out.NewsImpact = mc.News.Impact
		}
		if mc.News.Summary != "" {
			out.NewsSummary = mc.News.Summary
		}
	}
	return out
}
