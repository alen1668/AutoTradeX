package signal

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_Valid(t *testing.T) {
	body := []byte(`{"strategy_id":"s1","symbol":"ETHUSDC","signal":"Long","price":"2312.14","timestamp":1714723504000,"secret":"x"}`)
	s, err := Parse(body)
	require.NoError(t, err)
	assert.Equal(t, "s1", s.StrategyID)
	assert.Equal(t, "ETHUSDC", s.Symbol)
	assert.Equal(t, KindLong, s.Kind)
	assert.True(t, s.Price.Equal(decimal.RequireFromString("2312.14")))
	assert.Equal(t, time.UnixMilli(1714723504000).UTC(), s.TVTimestamp.UTC())
	assert.Equal(t, "x", s.Secret)
}

func TestParse_KindCaseInsensitive(t *testing.T) {
	cases := map[string]Kind{
		"Long":       KindLong,
		"long":       KindLong,
		"SHORT":      KindShort,
		"Exit Long":  KindExitLong,
		"exit short": KindExitShort,
		"EXIT LONG":  KindExitLong,
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{
				"strategy_id": "s",
				"symbol":      "ETHUSDC",
				"signal":      raw,
				"price":       "1",
				"timestamp":   1,
				"secret":      "x",
			})
			s, err := Parse(body)
			require.NoError(t, err)
			assert.Equal(t, want, s.Kind)
		})
	}
}

func TestParse_RejectsMissingFields(t *testing.T) {
	cases := []string{
		`{"symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":1,"secret":"x"}`,       // 缺 strategy_id
		`{"strategy_id":"s","signal":"Long","price":"1","timestamp":1,"secret":"x"}`,        // 缺 symbol
		`{"strategy_id":"s","symbol":"ETHUSDC","price":"1","timestamp":1,"secret":"x"}`,     // 缺 signal
		`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","timestamp":1,"secret":"x"}`, // 缺 price
		`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","secret":"x"}`,   // 缺 timestamp
		`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":1}`,  // 缺 secret
	}
	for i, body := range cases {
		t.Run("case-"+string(rune('A'+i)), func(t *testing.T) {
			_, err := Parse([]byte(body))
			require.Error(t, err)
		})
	}
}

func TestParse_RejectsBadKind(t *testing.T) {
	_, err := Parse([]byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"hodl","price":"1","timestamp":1,"secret":"x"}`))
	require.Error(t, err)
}

func TestParse_RejectsNegativePrice(t *testing.T) {
	_, err := Parse([]byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"-1","timestamp":1,"secret":"x"}`))
	require.Error(t, err)
}

func TestParse_RejectsZeroOrNegativeTimestamp(t *testing.T) {
	_, err := Parse([]byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":0,"secret":"x"}`))
	require.Error(t, err)
}

func TestParse_PriceCanBeNumeric(t *testing.T) {
	// TradingView 偶尔会发数字而非字符串
	s, err := Parse([]byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":2312.14,"timestamp":1,"secret":"x"}`))
	require.NoError(t, err)
	assert.True(t, s.Price.Equal(decimal.RequireFromString("2312.14")))
}

func TestParse_TimestampISO8601(t *testing.T) {
	// TradingView 的 {{time}} 占位符输出 RFC3339 字符串，例如 "2026-05-06T15:29:00Z"
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":"2026-05-06T15:29:00Z","secret":"x"}`)
	s, err := Parse(body)
	require.NoError(t, err)
	want := time.Date(2026, 5, 6, 15, 29, 0, 0, time.UTC)
	assert.Equal(t, want, s.TVTimestamp.UTC())
	assert.Equal(t, want.UnixMilli(), s.TVTimestampMs)
}

func TestParse_TimestampISO8601WithOffset(t *testing.T) {
	// RFC3339 允许带时区偏移
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":"2026-05-06T17:29:00+02:00","secret":"x"}`)
	s, err := Parse(body)
	require.NoError(t, err)
	want := time.Date(2026, 5, 6, 15, 29, 0, 0, time.UTC)
	assert.Equal(t, want.UnixMilli(), s.TVTimestampMs)
}

func TestParse_TimestampRejectsGarbageString(t *testing.T) {
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":"not-a-date","secret":"x"}`)
	_, err := Parse(body)
	require.Error(t, err)
}

func TestParse_TimestampRejectsMissingTimezone(t *testing.T) {
	// RFC3339 要求时区，纯 datetime 不接受（避免歧义）
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":"2026-05-06T15:29:00","secret":"x"}`)
	_, err := Parse(body)
	require.Error(t, err)
}

func TestParse_TimestampRejectsNegativeISO(t *testing.T) {
	// 1970 之前的时间 → UnixMilli < 0 → 风控应拒绝
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":"1969-12-31T23:00:00Z","secret":"x"}`)
	_, err := Parse(body)
	require.Error(t, err)
}

func TestParse_BuyWithPositiveSizeIsLong(t *testing.T) {
	// TradingView strategy 告警: action=buy, position_size 是下单后的仓位
	// buy + 正仓 → 开多
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDT","signal":"buy","position_size":"1.5","price":"2300","timestamp":1,"secret":"x"}`)
	s, err := Parse(body)
	require.NoError(t, err)
	assert.Equal(t, KindLong, s.Kind)
}

func TestParse_BuyWithZeroSizeIsExitShort(t *testing.T) {
	// buy + 仓位归零 → 平空
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDT","signal":"buy","position_size":"0","price":"2300","timestamp":1,"secret":"x"}`)
	s, err := Parse(body)
	require.NoError(t, err)
	assert.Equal(t, KindExitShort, s.Kind)
}

func TestParse_SellWithNegativeSizeIsShort(t *testing.T) {
	// sell + 负仓 → 开空
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDT","signal":"sell","position_size":"-2.0","price":"2300","timestamp":1,"secret":"x"}`)
	s, err := Parse(body)
	require.NoError(t, err)
	assert.Equal(t, KindShort, s.Kind)
}

func TestParse_SellWithZeroSizeIsExitLong(t *testing.T) {
	// sell + 仓位归零 → 平多
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDT","signal":"sell","position_size":"0","price":"2300","timestamp":1,"secret":"x"}`)
	s, err := Parse(body)
	require.NoError(t, err)
	assert.Equal(t, KindExitLong, s.Kind)
}

func TestParse_BuyWithNegativeSizeRejected(t *testing.T) {
	// 不合理：buy 不可能让仓位变成负的
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDT","signal":"buy","position_size":"-1","price":"2300","timestamp":1,"secret":"x"}`)
	_, err := Parse(body)
	require.Error(t, err)
}

func TestParse_SellWithPositiveSizeRejected(t *testing.T) {
	// 不合理：sell 不可能让仓位变成正的
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDT","signal":"sell","position_size":"1","price":"2300","timestamp":1,"secret":"x"}`)
	_, err := Parse(body)
	require.Error(t, err)
}

func TestParse_BuyWithoutPositionSizeRejected(t *testing.T) {
	// signal=buy 但缺 position_size 无法消歧
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDT","signal":"buy","price":"2300","timestamp":1,"secret":"x"}`)
	_, err := Parse(body)
	require.Error(t, err)
}

func TestParse_BuySellCaseInsensitive(t *testing.T) {
	cases := map[string]Kind{
		"Buy":  KindLong,
		"BUY":  KindLong,
		"Sell": KindExitLong,
		"SELL": KindExitLong,
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			size := "1.5"
			if want == KindExitLong {
				size = "0"
			}
			body, _ := json.Marshal(map[string]any{
				"strategy_id":   "s",
				"symbol":        "ETHUSDT",
				"signal":        raw,
				"position_size": size,
				"price":         "2300",
				"timestamp":     1,
				"secret":        "x",
			})
			s, err := Parse(body)
			require.NoError(t, err)
			assert.Equal(t, want, s.Kind)
		})
	}
}

func TestParse_LongStillWorksWithoutPositionSize(t *testing.T) {
	// 向后兼容：显式 kind 不需要 position_size
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":1,"secret":"x"}`)
	s, err := Parse(body)
	require.NoError(t, err)
	assert.Equal(t, KindLong, s.Kind)
}
