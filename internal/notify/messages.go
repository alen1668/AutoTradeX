package notify

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// nowFunc is the time source for timestamping notifications. Tests may
// override it to get deterministic output.
var nowFunc = time.Now

// withTimestamp appends `时间：YYYY-MM-DD HH:MM:SS ±HH:MM` to a notification
// body. The timezone reflects the bot process's local zone.
func withTimestamp(body string) string {
	return body + "\n时间：" + nowFunc().Format("2006-01-02 15:04:05 -07:00")
}

// Chinese-language rich notification builders. Pure functions so the
// content is easy to inspect and unit-test.

// CloseReason controls the close-message title text.
const (
	CloseReasonSignal          = "signal"           // strategy signal close
	CloseReasonStopLoss        = "stop_loss"        // protective stop fired
	CloseReasonTakeProfit      = "take_profit"     // take profit fired
	CloseReasonRecoveryOffline = "recovery_offline" // detected during startup recovery
)

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

func sideLabel(side string) string {
	switch side {
	case "long", "open_long", "exit_long":
		return "多头"
	case "short", "open_short", "exit_short":
		return "空头"
	}
	return side
}

func purposeLabel(purpose string) string {
	switch purpose {
	case "stop":
		return "止损单"
	case "backup_stop":
		return "备用止损单"
	case "take_profit":
		return "止盈单"
	case "entry":
		return "入场单"
	case "exit":
		return "出场单"
	}
	return purpose
}

// ComputePnL returns realised PnL: long → qty*(exit-entry), short → qty*(entry-exit).
func ComputePnL(side string, entryPrice, exitPrice, qty decimal.Decimal) decimal.Decimal {
	switch side {
	case "long":
		return exitPrice.Sub(entryPrice).Mul(qty)
	case "short":
		return entryPrice.Sub(exitPrice).Mul(qty)
	}
	return decimal.Zero
}

// BuildOpenMessage returns a rich CN notification for a successful open.
func BuildOpenMessage(strategyID, symbol string, leverage int, side string,
	signalPrice, entryFillPrice, qty decimal.Decimal) Message {
	notional := qty.Mul(entryFillPrice)
	slippage := entryFillPrice.Sub(signalPrice)
	body := fmt.Sprintf(
		`策略：%s
币种：%s
动作：%s
方向：%s
信号价：%s
成交价：%s
滑点：%s
数量：%s
持仓金额：%s
杠杆：%dx`,
		strategyID, symbol,
		kindLabel(side), sideLabel(side),
		signalPrice.StringFixed(2),
		entryFillPrice.StringFixed(2),
		slippage.StringFixed(4),
		qty.String(),
		notional.StringFixed(2),
		leverage,
	)
	return Message{
		Title:    "✅ 开仓成功",
		Body:     withTimestamp(body),
		Severity: SeverityInfo,
	}
}

// BuildCloseMessage returns a rich CN notification for a closed position.
// reason controls the title; pass one of the CloseReason* constants.
func BuildCloseMessage(strategyID, symbol, side, reason string,
	entryFillPrice, exitFillPrice, qty, pnl decimal.Decimal) Message {
	pnlPct := decimal.Zero
	if !entryFillPrice.IsZero() && !qty.IsZero() {
		pnlPct = pnl.Div(entryFillPrice.Mul(qty)).Mul(decimal.NewFromInt(100))
	}
	resultLabel := "盈利"
	severity := SeverityInfo
	if pnl.IsNegative() {
		resultLabel = "亏损"
		severity = SeverityWarn
	}
	body := fmt.Sprintf(
		`策略：%s
币种：%s
动作：平仓
方向：%s
入场价：%s
出场价：%s
数量：%s
%s：%s (%s%%)`,
		strategyID, symbol,
		sideLabel(side),
		entryFillPrice.StringFixed(2),
		exitFillPrice.StringFixed(2),
		qty.String(),
		resultLabel,
		pnl.StringFixed(2),
		pnlPct.StringFixed(2),
	)
	title := closeTitle(reason, !pnl.IsNegative())
	// recovery_offline is a critical alert regardless of profit/loss.
	if reason == CloseReasonRecoveryOffline {
		severity = SeverityCritical
	}
	return Message{
		Title:    title,
		Body:     withTimestamp(body),
		Severity: severity,
	}
}

func closeTitle(reason string, profit bool) string {
	switch reason {
	case CloseReasonStopLoss:
		return "🛑 止损触发"
	case CloseReasonTakeProfit:
		return "🎯 止盈触发"
	case CloseReasonRecoveryOffline:
		return "⚠️ 离线平仓 (恢复)"
	}
	if profit {
		return "✅ 平仓 盈利"
	}
	return "🔴 平仓 亏损"
}

// BuildDeniedMessage returns a rich CN notification for risk-denied signals.
func BuildDeniedMessage(strategyID, symbol, kind, ruleName, reason string) Message {
	body := fmt.Sprintf(
		`策略：%s
币种：%s
信号：%s
拒绝规则：%s
原因：%s`,
		strategyID, symbol, kindLabel(kind), ruleName, reason,
	)
	return Message{
		Title:    "⚠️ 信号被风控拒绝",
		Body:     withTimestamp(body),
		Severity: SeverityWarn,
	}
}

// BuildOpenFailedMessage describes an open that started but failed.
func BuildOpenFailedMessage(strategyID, symbol, kind, errMsg string) Message {
	body := fmt.Sprintf(
		`策略：%s
币种：%s
信号：%s
错误：%s

⚠️ 注意：入场单可能已成交但保护单失败，请去交易所确认是否有未平仓位。`,
		strategyID, symbol, kindLabel(kind), errMsg,
	)
	return Message{
		Title:    "❌ 开仓失败",
		Body:     withTimestamp(body),
		Severity: SeverityCritical,
	}
}

// BuildCloseFailedMessage describes a close that errored.
func BuildCloseFailedMessage(strategyID, symbol, errMsg string) Message {
	body := fmt.Sprintf(
		`策略：%s
币种：%s
错误：%s

⚠️ 持仓可能仍然存在，请去交易所确认。`,
		strategyID, symbol, errMsg,
	)
	return Message{
		Title:    "❌ 平仓失败",
		Body:     withTimestamp(body),
		Severity: SeverityCritical,
	}
}

// BuildProtectiveCanceledMessage describes a protective order canceled by the
// exchange while the virtual position is still active.
func BuildProtectiveCanceledMessage(strategyID, symbol, purpose string,
	vpID int64, vpStatus string) Message {
	body := fmt.Sprintf(
		`策略：%s
币种：%s
保护单：%s
持仓 ID：%d
持仓状态：%s

⚠️ 保护单被交易所取消但仓位仍未平。请立即手工核对并补挂保护单。`,
		strategyID, symbol, purposeLabel(purpose), vpID, vpStatus,
	)
	return Message{
		Title:    "⚠️ 保护单被取消",
		Body:     withTimestamp(body),
		Severity: SeverityCritical,
	}
}

// BuildRecoveryMismatchMessage describes a startup-recovery qty mismatch.
func BuildRecoveryMismatchMessage(strategyID, symbol, dbSide string,
	vpID int64, dbQty, realQty decimal.Decimal) Message {
	body := fmt.Sprintf(
		`策略：%s
币种：%s
持仓 ID：%d
DB 方向：%s
DB 数量：%s
交易所真实数量：%s

⚠️ 数据库与交易所不一致，已强制 disarm。请手工核对后再 arm。`,
		strategyID, symbol, vpID, sideLabel(dbSide), dbQty.String(), realQty.String(),
	)
	return Message{
		Title:    "⚠️ 启动恢复 持仓不一致",
		Body:     withTimestamp(body),
		Severity: SeverityCritical,
	}
}

// BuildRecoveryAnomalyMessage describes a startup-recovery error for one VP.
func BuildRecoveryAnomalyMessage(vpID int64, errMsg string) Message {
	body := fmt.Sprintf(
		`持仓 ID：%d
错误：%s

⚠️ 启动恢复无法对账此持仓。已强制 disarm。请手工核对后再 arm。`,
		vpID, errMsg,
	)
	return Message{
		Title:    "❌ 启动恢复异常",
		Body:     withTimestamp(body),
		Severity: SeverityCritical,
	}
}

// BuildRecoveryAutoClosedNoExitPriceMessage describes an offline close where
// no protective fill could be located, so exit price/PnL are unknown.
func BuildRecoveryAutoClosedNoExitPriceMessage(strategyID, symbol, side string,
	vpID int64, entryFillPrice, qty decimal.Decimal) Message {
	body := fmt.Sprintf(
		`策略：%s
币种：%s
持仓 ID：%d
方向：%s
入场价：%s
数量：%s

⚠️ 离线期间仓位被关闭(可能手工平仓)，无法获取出场价与盈亏。请到交易所历史记录核对。`,
		strategyID, symbol, vpID,
		sideLabel(side),
		entryFillPrice.StringFixed(2),
		qty.String(),
	)
	return Message{
		Title:    "⚠️ 离线平仓 出场价未知 (恢复)",
		Body:     withTimestamp(body),
		Severity: SeverityCritical,
	}
}

// BuildAgentAbandonedMessage — agent 真正拒单时的飞书 / Telegram 通知 (warn 级).
// 仅在 score < threshold 且 dry_run=false 时发送; dry_run 期 agent 想拒
// 但仍下单的情况不发通知 (噪声太大,该数据靠 /signals UI 复盘).
func BuildAgentAbandonedMessage(strategyID, symbol, kind string, score int, reasoning string) Message {
	body := fmt.Sprintf(
		`策略：%s
币种：%s
方向：%s
分数：%d (低于阈值)
理由：%s`,
		strategyID, symbol, kindLabel(kind), score, reasoning,
	)
	return Message{
		Title:    "🤖 Agent 拒单",
		Body:     withTimestamp(body),
		Severity: SeverityWarn,
	}
}

// BuildAgentLLMUnhealthyMessage — LLM 调用滚动失败率超阈值. 调用方需自行节流
// (10 分钟最多 1 条).
func BuildAgentLLMUnhealthyMessage(failures, total int) Message {
	body := fmt.Sprintf(
		`最近 10 分钟 LLM 调用失败 %d/%d 次。
Agent 当前依 fail_mode 设置兜底 (open=放行, closed=拒单)。
请检查 LLM API key、配额、网络。`,
		failures, total,
	)
	return Message{
		Title:    "⚠️ LLM 持续失败",
		Body:     withTimestamp(body),
		Severity: SeverityCritical,
	}
}

// BuildAgentAPIKeyMissingMessage — agent 启用了但 LLM API key 配置异常.
// 调用方需自行节流 (每天最多 1 条).
func BuildAgentAPIKeyMissingMessage() Message {
	body := `Agent 评分已启用,但 LLM API key 缺失或无效 (401)。
请到 /settings 页 「AI 评分」 区块重新配置。
Agent 当前按 fail_mode 兜底,不影响交易底线但等同未接 agent。`
	return Message{
		Title:    "🚨 LLM API key 配置异常",
		Body:     withTimestamp(body),
		Severity: SeverityCritical,
	}
}
