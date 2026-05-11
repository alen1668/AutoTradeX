package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func dec(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

func TestBuildOpenMessage_LongHasAllFields(t *testing.T) {
	m := BuildOpenMessage("20260506", "ETHUSDT", 10, "long", dec("2350.00"), dec("2344.13"), dec("0.426"))
	assert.Contains(t, m.Title, "开仓")
	assert.Contains(t, m.Body, "策略：20260506")
	assert.Contains(t, m.Body, "币种：ETHUSDT")
	assert.Contains(t, m.Body, "动作：开多")
	assert.Contains(t, m.Body, "方向：多头")
	assert.Contains(t, m.Body, "信号价：2350.00")
	assert.Contains(t, m.Body, "成交价：2344.13")
	assert.Contains(t, m.Body, "数量：0.426")
	assert.Contains(t, m.Body, "杠杆：10x")
	assert.Equal(t, SeverityInfo, m.Severity)
}

func TestBuildOpenMessage_NoLeadingNewline(t *testing.T) {
	m := BuildOpenMessage("20260506", "ETHUSDT", 10, "long", dec("2350"), dec("2344"), dec("0.5"))
	assert.False(t, strings.HasPrefix(m.Body, "\n"))
	assert.False(t, strings.HasPrefix(m.Body, " "))
}

func TestBuildCloseMessage_SignalProfit(t *testing.T) {
	// long, entry 2300, exit 2400, qty 1 → pnl +100
	m := BuildCloseMessage("20260506", "ETHUSDT", "long", CloseReasonSignal,
		dec("2300"), dec("2400"), dec("1"), dec("100"))
	assert.Contains(t, m.Title, "盈利")
	assert.Contains(t, m.Body, "盈利：100.00")
	assert.Equal(t, SeverityInfo, m.Severity)
}

func TestBuildCloseMessage_SignalLoss(t *testing.T) {
	m := BuildCloseMessage("20260506", "ETHUSDT", "long", CloseReasonSignal,
		dec("2400"), dec("2300"), dec("1"), dec("-100"))
	assert.Contains(t, m.Title, "亏损")
	assert.Contains(t, m.Body, "亏损：-100.00")
	assert.Equal(t, SeverityWarn, m.Severity)
}

func TestBuildCloseMessage_StopLossTitle(t *testing.T) {
	m := BuildCloseMessage("20260506", "ETHUSDT", "short", CloseReasonStopLoss,
		dec("2300"), dec("2350"), dec("0.5"), dec("-25"))
	assert.Contains(t, m.Title, "止损触发")
	assert.Contains(t, m.Body, "亏损：-25.00")
	assert.Equal(t, SeverityWarn, m.Severity)
}

func TestBuildCloseMessage_TakeProfitTitle(t *testing.T) {
	m := BuildCloseMessage("20260506", "ETHUSDT", "long", CloseReasonTakeProfit,
		dec("2300"), dec("2400"), dec("0.5"), dec("50"))
	assert.Contains(t, m.Title, "止盈触发")
	assert.Equal(t, SeverityInfo, m.Severity)
}

func TestBuildCloseMessage_RecoveryOfflineCritical(t *testing.T) {
	// Recovery is critical regardless of profit.
	m := BuildCloseMessage("20260506", "ETHUSDT", "long", CloseReasonRecoveryOffline,
		dec("2300"), dec("2400"), dec("1"), dec("100"))
	assert.Contains(t, m.Title, "离线平仓")
	assert.Contains(t, m.Title, "恢复")
	assert.Equal(t, SeverityCritical, m.Severity)
}

func TestBuildDeniedMessage_ContainsRule(t *testing.T) {
	m := BuildDeniedMessage("20260506", "ETHUSDT", "long", "max_position", "notional 1000 > limit 500")
	assert.Contains(t, m.Title, "风控")
	assert.Contains(t, m.Body, "拒绝规则：max_position")
	assert.Contains(t, m.Body, "原因：notional 1000")
	assert.Contains(t, m.Body, "信号：开多")
}

func TestBuildOpenFailedMessage_WarnsAboutGhost(t *testing.T) {
	m := BuildOpenFailedMessage("20260506", "ETHUSDT", "long", "place stop: -4015 ...")
	assert.Equal(t, SeverityCritical, m.Severity)
	assert.Contains(t, m.Body, "未平仓位")
}

func TestBuildCloseFailedMessage_Critical(t *testing.T) {
	m := BuildCloseFailedMessage("20260506", "ETHUSDT", "binance returned 5xx")
	assert.Equal(t, SeverityCritical, m.Severity)
	assert.Contains(t, m.Body, "持仓可能仍然存在")
	assert.Contains(t, m.Body, "binance returned 5xx")
}

func TestBuildProtectiveCanceledMessage(t *testing.T) {
	m := BuildProtectiveCanceledMessage("20260506", "ETHUSDT", "stop", 39, "open")
	assert.Contains(t, m.Title, "保护单被取消")
	assert.Contains(t, m.Body, "保护单：止损单")
	assert.Contains(t, m.Body, "持仓 ID：39")
	assert.Contains(t, m.Body, "持仓状态：open")
	assert.Equal(t, SeverityCritical, m.Severity)
}

func TestBuildRecoveryMismatchMessage(t *testing.T) {
	m := BuildRecoveryMismatchMessage("20260506", "ETHUSDT", "short", 39, dec("0.426"), dec("0.5"))
	assert.Contains(t, m.Title, "持仓不一致")
	assert.Contains(t, m.Body, "DB 方向：空头")
	assert.Contains(t, m.Body, "DB 数量：0.426")
	assert.Contains(t, m.Body, "交易所真实数量：0.5")
	assert.Equal(t, SeverityCritical, m.Severity)
}

func TestBuildRecoveryAnomalyMessage(t *testing.T) {
	m := BuildRecoveryAnomalyMessage(39, "binance: 5xx")
	assert.Contains(t, m.Title, "启动恢复异常")
	assert.Contains(t, m.Body, "持仓 ID：39")
	assert.Contains(t, m.Body, "错误：binance: 5xx")
	assert.Equal(t, SeverityCritical, m.Severity)
}

func TestBuildRecoveryAutoClosedNoExitPriceMessage(t *testing.T) {
	m := BuildRecoveryAutoClosedNoExitPriceMessage("20260506", "ETHUSDT", "short",
		39, dec("2340.66"), dec("0.426"))
	assert.Contains(t, m.Title, "出场价未知")
	assert.Contains(t, m.Body, "方向：空头")
	assert.Contains(t, m.Body, "入场价：2340.66")
	assert.Contains(t, m.Body, "数量：0.426")
	assert.Equal(t, SeverityCritical, m.Severity)
}

func TestKindLabelCovered(t *testing.T) {
	assert.Equal(t, "开多", kindLabel("long"))
	assert.Equal(t, "开空", kindLabel("short"))
	assert.Equal(t, "平多", kindLabel("exit_long"))
	assert.Equal(t, "平空", kindLabel("exit_short"))
}

func TestSideLabel(t *testing.T) {
	assert.Equal(t, "多头", sideLabel("long"))
	assert.Equal(t, "空头", sideLabel("short"))
	assert.Equal(t, "多头", sideLabel("exit_long"))
	assert.Equal(t, "空头", sideLabel("exit_short"))
}

func TestPurposeLabel(t *testing.T) {
	assert.Equal(t, "止损单", purposeLabel("stop"))
	assert.Equal(t, "备用止损单", purposeLabel("backup_stop"))
	assert.Equal(t, "止盈单", purposeLabel("take_profit"))
}

func TestComputePnL_Long(t *testing.T) {
	pnl := ComputePnL("long", dec("2300"), dec("2400"), dec("1"))
	assert.True(t, pnl.Equal(dec("100")))
}

func TestComputePnL_Short(t *testing.T) {
	pnl := ComputePnL("short", dec("2400"), dec("2300"), dec("1"))
	assert.True(t, pnl.Equal(dec("100")))
}

func TestComputePnL_LongLoss(t *testing.T) {
	pnl := ComputePnL("long", dec("2400"), dec("2300"), dec("1"))
	assert.True(t, pnl.Equal(dec("-100")))
}

func TestMessages_BodyEndsWithTimestamp(t *testing.T) {
	// Pin the clock so we can assert the exact line.
	frozen := time.Date(2026, 5, 8, 13, 15, 23, 0, time.FixedZone("CST", 7*3600))
	prev := nowFunc
	nowFunc = func() time.Time { return frozen }
	defer func() { nowFunc = prev }()

	cases := []Message{
		BuildOpenMessage("s", "ETHUSDT", 10, "long", dec("100"), dec("100"), dec("1")),
		BuildCloseMessage("s", "ETHUSDT", "long", CloseReasonSignal, dec("100"), dec("110"), dec("1"), dec("10")),
		BuildDeniedMessage("s", "ETHUSDT", "long", "rule", "reason"),
		BuildOpenFailedMessage("s", "ETHUSDT", "long", "err"),
		BuildCloseFailedMessage("s", "ETHUSDT", "err"),
		BuildProtectiveCanceledMessage("s", "ETHUSDT", "stop", 1, "open"),
		BuildRecoveryMismatchMessage("s", "ETHUSDT", "short", 1, dec("1"), dec("2")),
		BuildRecoveryAnomalyMessage(1, "err"),
		BuildRecoveryAutoClosedNoExitPriceMessage("s", "ETHUSDT", "short", 1, dec("100"), dec("1")),
	}
	want := "时间：2026-05-08 13:15:23 +07:00"
	for _, m := range cases {
		assert.True(t, strings.HasSuffix(m.Body, want), "body should end with %q, got: %q", want, m.Body)
	}
}

func TestBuildAgentAbandonedMessage(t *testing.T) {
	m := BuildAgentAbandonedMessage("supertrend-eth", "ETHUSDC", "long", 35, "近期连亏 3 笔,风险偏高")
	assert.Equal(t, SeverityWarn, m.Severity)
	assert.Contains(t, m.Title, "Agent 拒单")
	assert.Contains(t, m.Body, "supertrend-eth")
	assert.Contains(t, m.Body, "ETHUSDC")
	assert.Contains(t, m.Body, "35")
	assert.Contains(t, m.Body, "近期连亏")
}

func TestBuildAgentLLMUnhealthyMessage(t *testing.T) {
	m := BuildAgentLLMUnhealthyMessage(7, 10)
	assert.Equal(t, SeverityCritical, m.Severity)
	assert.Contains(t, m.Title, "LLM")
	assert.Contains(t, m.Body, "7/10")
}

func TestBuildAgentAPIKeyMissingMessage(t *testing.T) {
	m := BuildAgentAPIKeyMissingMessage()
	assert.Equal(t, SeverityCritical, m.Severity)
	assert.Contains(t, m.Title, "API key")
	assert.Contains(t, m.Body, "/settings")
}

func TestReplayRunFailedMessage(t *testing.T) {
	m := ReplayRunFailedMessage(42, "all 3 samples failed (model=claude-sonnet-4-6)")
	assert.Equal(t, "Replay run #42 failed", m.Title)
	assert.Equal(t, SeverityCritical, m.Severity)
	assert.Contains(t, m.Body, "all 3 samples failed")
	assert.Contains(t, m.Body, "/eval/replays/42")
	assert.Equal(t, int64(42), m.Fields["run_id"])
}
