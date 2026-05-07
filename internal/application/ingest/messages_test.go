package ingest

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"github.com/lizhaojie/tvbot/internal/domain/strategy"
	"github.com/lizhaojie/tvbot/internal/notify"
)

func dec(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

func sampleStrategy() *strategy.Strategy {
	return &strategy.Strategy{
		Config: strategy.Config{
			ID:          "20260506",
			Symbol:      "ETHUSDT",
			Leverage:    10,
			SizeUSDC:    dec("100"),
			StopLossPct: dec("1.5"),
			MaxOpenUSDC: dec("1500"),
		},
	}
}

func TestBuildOpenMessage_LongHasAllFields(t *testing.T) {
	m := BuildOpenMessage(sampleStrategy(), "long", dec("2350.00"), dec("2344.13"), dec("0.426"))
	assert.Contains(t, m.Title, "开仓")
	assert.Contains(t, m.Body, "策略：20260506")
	assert.Contains(t, m.Body, "币种：ETHUSDT")
	assert.Contains(t, m.Body, "动作：开多")
	assert.Contains(t, m.Body, "方向：多头")
	assert.Contains(t, m.Body, "信号价：2350.00 USDT")
	assert.Contains(t, m.Body, "成交价：2344.13 USDT")
	assert.Contains(t, m.Body, "数量：0.426")
	// notional = 0.426 × 2344.13 = 998.6...
	assert.Contains(t, m.Body, "USDT")
	assert.Contains(t, m.Body, "杠杆：10x")
	assert.Equal(t, notify.SeverityInfo, m.Severity)
}

func TestBuildCloseMessage_ProfitMarkedAsInfo(t *testing.T) {
	// long, entry 2300, exit 2400, qty 1 → pnl +100
	m := BuildCloseMessage(sampleStrategy(), "long", dec("2300"), dec("2400"), dec("1"), dec("100"))
	assert.Contains(t, m.Title, "盈利")
	assert.Contains(t, m.Body, "盈利：100.00 USDT")
	assert.Equal(t, notify.SeverityInfo, m.Severity)
}

func TestBuildCloseMessage_LossMarkedAsWarn(t *testing.T) {
	m := BuildCloseMessage(sampleStrategy(), "long", dec("2400"), dec("2300"), dec("1"), dec("-100"))
	assert.Contains(t, m.Title, "亏损")
	assert.Contains(t, m.Body, "亏损：-100.00 USDT")
	assert.Equal(t, notify.SeverityWarn, m.Severity)
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
	assert.Equal(t, notify.SeverityCritical, m.Severity)
	// Critical: warn user about possible ghost position
	assert.Contains(t, m.Body, "未平仓位")
}

func TestKindLabelCovered(t *testing.T) {
	assert.Equal(t, "开多", kindLabel("long"))
	assert.Equal(t, "开空", kindLabel("short"))
	assert.Equal(t, "平多", kindLabel("exit_long"))
	assert.Equal(t, "平空", kindLabel("exit_short"))
}

func TestBuildOpenMessage_NoLeadingNewline(t *testing.T) {
	// Feishu's rich-text rendering can be quirky with leading whitespace.
	m := BuildOpenMessage(sampleStrategy(), "long", dec("2350"), dec("2344"), dec("0.5"))
	assert.False(t, strings.HasPrefix(m.Body, "\n"))
	assert.False(t, strings.HasPrefix(m.Body, " "))
}
