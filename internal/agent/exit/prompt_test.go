package exit

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func sampleInput(t *testing.T) Input {
	t.Helper()
	sl := decimal.NewFromFloat(2280)
	tp := decimal.NewFromFloat(2400)
	return Input{
		Position: PositionSnapshot{
			VirtualPositionID: 42,
			StrategyID:        "macd_eth_long",
			Symbol:            "ETHUSDC",
			Side:              "long",
			EntryFillPrice:    decimal.NewFromFloat(2300),
			CurrentPrice:      decimal.NewFromFloat(2330),
			Qty:               decimal.NewFromFloat(0.05),
			UnrealizedPnLUSD:  decimal.NewFromFloat(1.5),
			UnrealizedPnLPct:  decimal.NewFromFloat(1.30),
			PositionAge:       30 * time.Minute,
			CurrentSLPrice:    &sl,
			CurrentTPPrice:    &tp,
		},
		KlineSnapshot: "1m close: 2330 2329 2328 ...",
		Macro: MacroBundle{
			Regime:         "trend",
			PerpFunding:    "+0.024%",
			PerpOIDelta:    "+5.2% (24h)",
			UpcomingEvents: "(无)",
			NewsImpact:     "low",
			NewsSummary:    "无重大事件",
		},
		Historical: HistoricalStats{
			SampleSize: 42, WinRate: decimal.NewFromFloat(0.55),
			AvgWinPct: decimal.NewFromFloat(1.8), AvgLossPct: decimal.NewFromFloat(-1.2),
			AvgHoldMinutes: 90,
		},
		Pinned: []PinnedPattern{
			{Title: "trend+高 funding 高估", Suggestion: "做多扣 15 分"},
		},
	}
}

func TestRenderPrompt_ContainsAllSections(t *testing.T) {
	out, err := RenderPrompt(sampleInput(t))
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	for _, want := range []string{
		"macd_eth_long", "ETHUSDC", "long",
		"2300", "2330", "1.3",
		"30",                   // age min
		"2280", "2400",         // sl/tp
		"trend", "+0.024%",
		"(无)",                  // upcoming events
		"trend+高 funding 高估", // pinned title
		`"action":`, `"hold"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in prompt:\n%s", want, out)
		}
	}
}

func TestRenderPrompt_NoPinnedPatterns(t *testing.T) {
	in := sampleInput(t)
	in.Pinned = nil
	out, err := RenderPrompt(in)
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	if !strings.Contains(out, "【人工钉选的近期反思】\n(无)") {
		t.Errorf("nil-pinned branch not rendered as (无):\n%s", out)
	}
}

func TestRenderPrompt_NilSL(t *testing.T) {
	in := sampleInput(t)
	in.Position.CurrentSLPrice = nil
	out, err := RenderPrompt(in)
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	if !strings.Contains(out, "当前止损价: (未挂)") {
		t.Errorf("nil sl branch not rendered:\n%s", out)
	}
}

func TestPromptHash_StableAcrossRenders(t *testing.T) {
	h1 := PromptHash()
	h2 := PromptHash()
	if h1 != h2 {
		t.Errorf("unstable: %s vs %s", h1, h2)
	}
	if len(h1) != 8 {
		t.Errorf("want 8-char hash, got %q", h1)
	}
}
