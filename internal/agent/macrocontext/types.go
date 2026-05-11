// Package macrocontext aggregates the latest market regime, calendar
// event window, and news alert into a single MacroContext that the scorer
// prompt template can render.
package macrocontext

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// MacroContext is what scorer.ScoreInput.Macro holds. Sub-fields may be nil
// when the corresponding worker has never produced data; the prompt template
// detects nil and renders "暂不可用".
type MacroContext struct {
	Regime *Regime
	Events []Event
	News   *NewsAlert
}

type Regime struct {
	Label          string
	TrendStrength  decimal.Decimal
	Volatility24h  decimal.Decimal
	VolatilityPctl decimal.Decimal
	Change24hPct   decimal.Decimal
	PriceRangePos  decimal.Decimal
	MeasuredAt     time.Time
	StaleMinutes   int
}

type Event struct {
	Name         string
	Currency     string
	Impact       string
	StartsAt     time.Time
	MinutesTo    int
	RelativeText string
}

type NewsAlert struct {
	Impact       string
	Summary      string
	Reasoning    string
	PerHeadline  []HeadlineJudgment
	MeasuredAt   time.Time
	StaleMinutes int
}

type HeadlineJudgment struct {
	Title  string `json:"title"`
	URL    string `json:"url"`
	Impact string `json:"impact"`
	Reason string `json:"reason"`
}

// MinutesBetween returns int minutes from `from` to `to`. Positive when `to` is later.
func MinutesBetween(from, to time.Time) int {
	return int(to.Sub(from) / time.Minute)
}

// FormatRelativeText renders Chinese suffix for an offset in minutes.
func FormatRelativeText(minutesTo int) string {
	switch {
	case minutesTo == 0:
		return "正在开始"
	case minutesTo > 0:
		return fmt.Sprintf("还有 %d 分钟", minutesTo)
	default:
		return fmt.Sprintf("%d 分钟前已过", -minutesTo)
	}
}
