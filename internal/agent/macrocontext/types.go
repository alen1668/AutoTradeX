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
	Regime   *Regime
	Events   []Event
	News     *NewsAlert
	PerpSelf *PerpSnapshot // 信号 symbol 自身; nil 表示数据不可用
	PerpBTC  *PerpSnapshot // BTCUSDT 大盘; signal symbol == BTCUSDT 时与 PerpSelf 同指针
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

// PerpSnapshot is the prompt-friendly projection of a perp_metrics row.
// FundingRatePct is funding * 100 so the template can render "0.025%".
type PerpSnapshot struct {
	Symbol             string
	FundingRatePct     decimal.Decimal
	FundingLabel       string
	OpenInterest24hPct decimal.Decimal
	OISignal           string
	Price24hPct        decimal.Decimal
	TopLSRatio         decimal.Decimal
	LSLabel            string
	MeasuredAt         time.Time
	StaleMinutes       int
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
