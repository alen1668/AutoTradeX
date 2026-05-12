package scorer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/agent/macrocontext"
	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
)

// fixedInput is the canonical ScoreInput for golden-file tests. Stable so
// the rendered prompt is deterministic.
func fixedInput() ScoreInput {
	return ScoreInput{
		Signal: &sigpkg.Signal{
			StrategyID:    "supertrend-eth",
			Symbol:        "ETHUSDC",
			Kind:          sigpkg.KindLong,
			Price:         decimal.RequireFromString("2300.50"),
			TVTimestampMs: 1714723504000, // 2024-05-03 08:05:04 UTC
		},
		Strategy: &strategy.Strategy{
			Config: strategy.Config{ID: "supertrend-eth", Symbol: "ETHUSDC"},
		},
		SymbolHistory: []HistoricalTrade{
			{
				OpenedAt:    time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC),
				Symbol:      "ETHUSDC",
				Direction:   "long",
				EntryPrice:  decimal.RequireFromString("2280"),
				ExitPrice:   decimal.RequireFromString("2310"),
				PnLUSD:      decimal.RequireFromString("30"),
				DurationMin: 45,
				ExitReason:  "tp",
			},
		},
		StrategyHistory: []HistoricalTrade{
			{
				OpenedAt:    time.Date(2024, 5, 2, 10, 0, 0, 0, time.UTC),
				Symbol:      "BTCUSDC",
				Direction:   "short",
				EntryPrice:  decimal.RequireFromString("60000"),
				ExitPrice:   decimal.RequireFromString("59500"),
				PnLUSD:      decimal.RequireFromString("50"),
				DurationMin: 90,
				ExitReason:  "tp",
			},
		},
		Portfolio: &PortfolioSnapshot{
			TotalNotionalUSD: decimal.RequireFromString("5000"),
			DailyPnLUSD:      decimal.RequireFromString("12.50"),
			OpenPositions: []OpenPosition{
				{StrategyID: "supertrend-btc", Symbol: "BTCUSDC", Direction: "long",
					EntryPrice: decimal.RequireFromString("60100"),
					NotionalUSD: decimal.RequireFromString("3000"),
					UnrealizedPnL: decimal.RequireFromString("5")},
			},
		},
		Market: &MarketContext{
			Symbol: "ETHUSDC",
			Last24hHigh: decimal.RequireFromString("2350"), Last24hLow: decimal.RequireFromString("2250"),
			Last24hChangePct: decimal.RequireFromString("1.25"), Last1hChangePct: decimal.RequireFromString("0.30"),
			PriceVs24hRange: decimal.RequireFromString("0.55"),
			Volatility24h:   decimal.RequireFromString("0.018"),
			KlineLookback1h: []decimal.Decimal{
				decimal.RequireFromString("2280"), decimal.RequireFromString("2295"),
				decimal.RequireFromString("2300"), decimal.RequireFromString("2300.5"),
			},
		},
		HighVolWindows: []string{"us_market_open_window"},
	}
}

func TestRenderPrompt_GoldenFile(t *testing.T) {
	in := fixedInput()
	rendered, hash, err := RenderPrompt(in)
	require.NoError(t, err)
	require.NotEmpty(t, hash)
	require.Len(t, hash, 8)

	goldenPath := filepath.Join("testdata", "prompt_v1.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(goldenPath, []byte(rendered), 0644))
		t.Log("updated golden file")
		return
	}
	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden file missing — run: UPDATE_GOLDEN=1 go test ./internal/agent/scorer/... -run TestRenderPrompt_GoldenFile")
	if string(want) != rendered {
		t.Errorf("prompt diverged from golden\n--- got ---\n%s\n--- want ---\n%s", rendered, string(want))
	}
}

func TestRenderPrompt_PortfolioNil(t *testing.T) {
	in := fixedInput()
	in.Portfolio = nil
	rendered, _, err := RenderPrompt(in)
	require.NoError(t, err)
	assert.Contains(t, rendered, "仓位数据暂不可用")
}

func TestRenderPrompt_MarketNil(t *testing.T) {
	in := fixedInput()
	in.Market = nil
	rendered, _, err := RenderPrompt(in)
	require.NoError(t, err)
	assert.Contains(t, rendered, "市场数据暂不可用")
}

func TestRenderPrompt_NoHighVolWindows(t *testing.T) {
	in := fixedInput()
	in.HighVolWindows = nil
	rendered, _, err := RenderPrompt(in)
	require.NoError(t, err)
	assert.Contains(t, rendered, "非已知高波动时段")
}

func TestRenderPrompt_HashStable(t *testing.T) {
	in := fixedInput()
	_, h1, _ := RenderPrompt(in)
	_, h2, _ := RenderPrompt(in)
	assert.Equal(t, h1, h2, "same input must produce same hash")
}

func TestRenderPrompt_HashChangesWhenInputChanges(t *testing.T) {
	in := fixedInput()
	_, h1, _ := RenderPrompt(in)
	in.Signal.Price = decimal.RequireFromString("9999")
	_, h2, _ := RenderPrompt(in)
	assert.NotEqual(t, h1, h2)
}

func TestRenderPrompt_NilSignalReturnsError(t *testing.T) {
	in := fixedInput()
	in.Signal = nil
	_, _, err := RenderPrompt(in)
	require.Error(t, err)
}

func TestRenderPrompt_NilStrategyReturnsError(t *testing.T) {
	in := fixedInput()
	in.Strategy = nil
	_, _, err := RenderPrompt(in)
	require.Error(t, err)
}

func TestRenderPromptWithTemplate_CustomTmpl(t *testing.T) {
	in := fixedInput()
	tmpl := template.Must(template.New("custom").Parse(
		"S={{.StrategyID}} P={{.Signal.Price}} TS={{.SignalTimeUTC}} N={{len .Input.SymbolHistory}}",
	))
	rendered, hash, err := RenderPromptWithTemplate(in, tmpl)
	require.NoError(t, err)
	assert.Equal(t,
		"S=supertrend-eth P=2300.5 TS=2024-05-03 08:05:04 N=1",
		rendered)
	require.Len(t, hash, 8)
}

func TestRenderPromptWithTemplate_NilSignal(t *testing.T) {
	in := fixedInput()
	in.Signal = nil
	tmpl := template.Must(template.New("x").Parse("noop"))
	_, _, err := RenderPromptWithTemplate(in, tmpl)
	require.Error(t, err)
}

func TestRenderPromptWithTemplate_NilStrategy(t *testing.T) {
	in := fixedInput()
	in.Strategy = nil
	tmpl := template.Must(template.New("x").Parse("noop"))
	_, _, err := RenderPromptWithTemplate(in, tmpl)
	require.Error(t, err)
}

func TestRenderPrompt_MacroAllPresent(t *testing.T) {
	in := fixedInput()
	in.Macro = macrocontext.MacroContext{
		Regime: &macrocontext.Regime{
			Label:          "range",
			TrendStrength:  decimal.NewFromFloat(0.1),
			VolatilityPctl: decimal.NewFromFloat(0.4),
			Change24hPct:   decimal.NewFromFloat(-1.5),
			PriceRangePos:  decimal.NewFromFloat(0.55),
			StaleMinutes:   12,
		},
		Events: []macrocontext.Event{
			{Name: "CPI m/m", Currency: "USD", Impact: "High", MinutesTo: 20, RelativeText: "还有 20 分钟"},
		},
		News: &macrocontext.NewsAlert{Impact: "high", Summary: "整体偏空", StaleMinutes: 5},
	}
	rendered, _, err := RenderPrompt(in)
	require.NoError(t, err)
	assert.Contains(t, rendered, "BTC regime: range")
	assert.Contains(t, rendered, "数据陈旧: 12 分钟前测得")
	assert.Contains(t, rendered, "CPI m/m (USD, High): 还有 20 分钟")
	assert.Contains(t, rendered, "影响等级: high")
	assert.Contains(t, rendered, "整体偏空")
}

func TestRenderPrompt_MacroAllNil_RendersUnavailable(t *testing.T) {
	in := fixedInput() // zero Macro
	rendered, _, err := RenderPrompt(in)
	require.NoError(t, err)
	for _, want := range []string{
		"BTC regime 数据暂不可用",
		"最近 ±1h 无高重要性宏观事件",
		"新闻数据暂不可用",
		"永续指标暂不可用",
	} {
		assert.Contains(t, rendered, want, "fallback line missing")
	}
}

func TestRenderPrompt_PerpSelfAndBTC(t *testing.T) {
	in := fixedInput()
	in.Macro.PerpSelf = &macrocontext.PerpSnapshot{
		Symbol:             "ETHUSDC",
		FundingRatePct:     decimal.NewFromFloat(0.025),
		FundingLabel:       "mild_long",
		OpenInterest24hPct: decimal.NewFromFloat(3.2),
		OISignal:           "new_longs",
		TopLSRatio:         decimal.NewFromFloat(1.85),
		LSLabel:            "bullish",
		StaleMinutes:       3,
	}
	in.Macro.PerpBTC = &macrocontext.PerpSnapshot{
		Symbol:             "BTCUSDT",
		FundingRatePct:     decimal.NewFromFloat(0.01),
		FundingLabel:       "neutral",
		OpenInterest24hPct: decimal.NewFromFloat(1.0),
		OISignal:           "neutral",
		TopLSRatio:         decimal.NewFromFloat(1.1),
		LSLabel:            "balanced",
		StaleMinutes:       3,
	}
	rendered, _, err := RenderPrompt(in)
	require.NoError(t, err)
	assert.Contains(t, rendered, "ETHUSDC: funding=0.0250%")
	assert.Contains(t, rendered, "[mild_long]")
	assert.Contains(t, rendered, "[new_longs]")
	assert.Contains(t, rendered, "BTCUSDT: funding=0.0100%")
	assert.Contains(t, rendered, "[neutral]")
	// stale = 3min, < 5min threshold, so no "数据 X 分钟前" suffix
	assert.NotContains(t, rendered, "(数据 3 分钟前)")
}

func TestRenderPrompt_PerpSelfStale_AppendsAgeSuffix(t *testing.T) {
	in := fixedInput()
	in.Macro.PerpSelf = &macrocontext.PerpSnapshot{
		Symbol: "ETHUSDC", FundingRatePct: decimal.NewFromFloat(0.025),
		FundingLabel: "mild_long", OISignal: "neutral", LSLabel: "balanced",
		TopLSRatio: decimal.NewFromFloat(1.0), StaleMinutes: 18,
	}
	rendered, _, err := RenderPrompt(in)
	require.NoError(t, err)
	assert.Contains(t, rendered, "(数据 18 分钟前)")
}

func TestRenderPrompt_BTCSignalDoesNotDuplicateRow(t *testing.T) {
	in := fixedInput()
	in.Signal.Symbol = "BTCUSDT"
	btc := &macrocontext.PerpSnapshot{
		Symbol: "BTCUSDT", FundingRatePct: decimal.NewFromFloat(0.01),
		FundingLabel: "neutral", OISignal: "neutral", LSLabel: "balanced",
		TopLSRatio: decimal.NewFromFloat(1.0),
	}
	in.Macro.PerpSelf = btc
	in.Macro.PerpBTC = btc // same pointer (Reader aliases for BTC signals)
	rendered, _, err := RenderPrompt(in)
	require.NoError(t, err)
	// "BTCUSDT: funding=" appears at most once
	count := 0
	for i := 0; i+8 < len(rendered); i++ {
		if rendered[i:i+8] == "BTCUSDT:" {
			count++
		}
	}
	assert.Equal(t, 1, count, "BTCUSDT row should appear exactly once for BTC signal; rendered:\n%s", rendered)
}

func TestRenderPrompt_PerpBTCOnly_NoPerpSelf(t *testing.T) {
	in := fixedInput()
	in.Macro.PerpSelf = nil
	in.Macro.PerpBTC = &macrocontext.PerpSnapshot{
		Symbol: "BTCUSDT", FundingRatePct: decimal.NewFromFloat(0.05),
		FundingLabel: "mild_long", OISignal: "neutral", LSLabel: "balanced",
		TopLSRatio: decimal.NewFromFloat(1.0),
	}
	rendered, _, err := RenderPrompt(in)
	require.NoError(t, err)
	assert.Contains(t, rendered, "BTCUSDT: funding=0.0500%")
	assert.NotContains(t, rendered, "永续指标暂不可用")
}

// minimalScoreInput builds the smallest valid ScoreInput (non-nil Signal +
// Strategy; everything else zero/nil). Used by PinnedPatterns tests.
func minimalScoreInput() ScoreInput {
	return ScoreInput{
		Signal: &sigpkg.Signal{
			StrategyID:    "test-strat",
			Symbol:        "BTCUSDT",
			Kind:          sigpkg.KindLong,
			Price:         decimal.RequireFromString("50000"),
			TVTimestampMs: 1714723504000,
		},
		Strategy: &strategy.Strategy{
			Config: strategy.Config{ID: "test-strat", Symbol: "BTCUSDT"},
		},
	}
}

func TestRenderPrompt_PinnedPatterns_Rendered(t *testing.T) {
	in := minimalScoreInput()
	in.PinnedPatterns = []PinnedPattern{
		{Title: "trend 高估做多", SuggestionForPrompt: "trend + funding>0.05 扣 15 分"},
		{Title: "波动率极端区间慎入", SuggestionForPrompt: "vol_pctl>0.9 扣 10 分"},
	}
	text, _, err := RenderPrompt(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "trend + funding>0.05 扣 15 分") {
		t.Fatal("first pinned pattern suggestion missing")
	}
	if !strings.Contains(text, "vol_pctl>0.9 扣 10 分") {
		t.Fatal("second pinned pattern suggestion missing")
	}
	if !strings.Contains(text, "近期反思") {
		t.Fatal("section header missing")
	}
}

func TestRenderPrompt_PinnedPatterns_EmptyRendersNone(t *testing.T) {
	in := minimalScoreInput()
	text, _, err := RenderPrompt(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "近期反思") || !strings.Contains(text, "(无)") {
		t.Fatal("empty pinned should render '近期反思' section with '(无)'")
	}
}
