package ingest

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/domain/strategy"
	"github.com/lizhaojie/tvbot/internal/notify"
)

// Chinese-language rich notification builders. Kept as pure functions so the
// content is easy to inspect and unit-test.

// kindLabel maps internal Kind to user-facing CN action label.
func kindLabel(kind string) string {
	switch kind {
	case "long":
		return "开多"
	case "short":
		return "开空"
	case "exit_long":
		return "平多"
	case "exit_short":
		return "平空"
	}
	return kind
}

// BuildOpenMessage returns a rich CN notification for a successful open.
// Notional = qty * entryFillPrice (the actual margin × leverage value).
func BuildOpenMessage(strat *strategy.Strategy, side string, signalPrice, entryFillPrice, qty decimal.Decimal) notify.Message {
	notional := qty.Mul(entryFillPrice)
	slippage := entryFillPrice.Sub(signalPrice)
	body := fmt.Sprintf(
		`策略：%s
币种：%s
动作：%s
方向：%s
信号价：%s USDT
成交价：%s USDT
滑点：%s USDT
数量：%s
名义金额：%s USDT
杠杆：%dx`,
		strat.ID, strat.Symbol,
		kindLabel(side), sideLabel(side),
		signalPrice.StringFixed(2),
		entryFillPrice.StringFixed(2),
		slippage.StringFixed(4),
		qty.String(),
		notional.StringFixed(2),
		strat.Leverage,
	)
	return notify.Message{
		Title:    "✅ 开仓成功",
		Body:     body,
		Severity: notify.SeverityInfo,
	}
}

// BuildCloseMessage returns a rich CN notification for a successful close.
func BuildCloseMessage(strat *strategy.Strategy, side string, entryFillPrice, exitFillPrice, qty, pnl decimal.Decimal) notify.Message {
	pnlPct := decimal.Zero
	if !entryFillPrice.IsZero() {
		// Sign of P&L direction depends on side: long profits when exit>entry,
		// short profits when exit<entry. PnL value passed in already reflects this.
		pnlPct = pnl.Div(entryFillPrice.Mul(qty)).Mul(decimal.NewFromInt(100))
	}
	emoji := "✅"
	resultLabel := "盈利"
	if pnl.IsNegative() {
		emoji = "🔴"
		resultLabel = "亏损"
	}
	body := fmt.Sprintf(
		`策略：%s
币种：%s
动作：平仓
方向：%s
入场价：%s USDT
出场价：%s USDT
数量：%s
%s：%s USDT (%s%%)`,
		strat.ID, strat.Symbol,
		sideLabel(side),
		entryFillPrice.StringFixed(2),
		exitFillPrice.StringFixed(2),
		qty.String(),
		resultLabel,
		pnl.StringFixed(2),
		pnlPct.StringFixed(2),
	)
	severity := notify.SeverityInfo
	if pnl.IsNegative() {
		severity = notify.SeverityWarn
	}
	return notify.Message{
		Title:    emoji + " 平仓 " + resultLabel,
		Body:     body,
		Severity: severity,
	}
}

// BuildDeniedMessage returns a rich CN notification for risk-denied signals.
func BuildDeniedMessage(strategyID, symbol, kind, ruleName, reason string) notify.Message {
	body := fmt.Sprintf(
		`策略：%s
币种：%s
信号：%s
拒绝规则：%s
原因：%s`,
		strategyID, symbol, kindLabel(kind), ruleName, reason,
	)
	return notify.Message{
		Title:    "⚠️ 信号被风控拒绝",
		Body:     body,
		Severity: notify.SeverityWarn,
	}
}

// BuildOpenFailedMessage describes an open that started but failed (e.g. when
// the entry fill went through but a protective order errored).
func BuildOpenFailedMessage(strategyID, symbol, kind, errMsg string) notify.Message {
	body := fmt.Sprintf(
		`策略：%s
币种：%s
信号：%s
错误：%s

⚠️ 注意：入场单可能已成交但保护单失败，请去交易所确认是否有未平仓位。`,
		strategyID, symbol, kindLabel(kind), errMsg,
	)
	return notify.Message{
		Title:    "❌ 开仓失败",
		Body:     body,
		Severity: notify.SeverityCritical,
	}
}

// BuildCloseFailedMessage describes a close that errored.
func BuildCloseFailedMessage(strategyID, symbol, errMsg string) notify.Message {
	body := fmt.Sprintf(
		`策略：%s
币种：%s
错误：%s

⚠️ 持仓可能仍然存在，请去交易所确认。`,
		strategyID, symbol, errMsg,
	)
	return notify.Message{
		Title:    "❌ 平仓失败",
		Body:     body,
		Severity: notify.SeverityCritical,
	}
}

// sideLabel maps long/short to CN labels for display.
func sideLabel(side string) string {
	switch side {
	case "long", "open_long":
		return "多头"
	case "short", "open_short":
		return "空头"
	case "exit_long":
		return "多头"
	case "exit_short":
		return "空头"
	}
	return side
}
