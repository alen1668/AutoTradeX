package critique

import (
	"strings"
	"testing"
	"time"
)

func TestRenderPrompt_SmokeContainsKeyFields(t *testing.T) {
	in := RenderInput{
		WindowStart: time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
		WindowEnd:   time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		SampleSize:  42,
		Aggregates: []AggregateRow{
			{StrategyID: "s-trend", Regime: "trend", Outcome: "loss",
				Count: 5, AvgScore: "78.2", AvgPnLUSD: "-12.5", WinRate: "0.20"},
		},
		Details: []DetailRow{
			{SignalID: 1001, StrategyID: "s-trend", Symbol: "BTCUSDT", Kind: "long",
				Score: 80, Decision: "approve", Outcome: "loss", PnLPct: "",
				ReasoningShort: "regime 强趋势，跟多"},
		},
	}
	text, hash, err := RenderPrompt(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"42", "s-trend", "BTCUSDT", "1001", "近期反思" /* sanity */} {
		// Skip 近期反思 check — that's the scorer template, not this one.
		_ = want
	}
	for _, want := range []string{"42", "s-trend", "BTCUSDT", "1001"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in rendered text", want)
		}
	}
	if len(hash) != 8 {
		t.Fatalf("hash length = %d, want 8", len(hash))
	}
}

func TestRenderPrompt_EmptyAggregatesAndDetails(t *testing.T) {
	_, _, err := RenderPrompt(RenderInput{
		WindowStart: time.Now(), WindowEnd: time.Now(),
		SampleSize: 0,
	})
	if err != nil {
		t.Fatalf("empty render should not error: %v", err)
	}
}

func TestRenderPrompt_PreviousSummaryRendered(t *testing.T) {
	text, _, err := RenderPrompt(RenderInput{
		WindowStart: time.Now(), WindowEnd: time.Now(),
		PreviousSummary: "上次发现 trend 下做多偏激进",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "trend 下做多偏激进") {
		t.Fatal("PreviousSummary text not rendered")
	}
}

func TestRenderPrompt_NoPreviousSummaryRendersNone(t *testing.T) {
	text, _, err := RenderPrompt(RenderInput{
		WindowStart: time.Now(), WindowEnd: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "(无)") {
		t.Fatal("empty PreviousSummary should render '(无)'")
	}
}
