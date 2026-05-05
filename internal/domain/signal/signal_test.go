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
		"Long":        KindLong,
		"long":        KindLong,
		"SHORT":       KindShort,
		"Exit Long":   KindExitLong,
		"exit short":  KindExitShort,
		"EXIT LONG":   KindExitLong,
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
		`{"symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":1,"secret":"x"}`,                    // 缺 strategy_id
		`{"strategy_id":"s","signal":"Long","price":"1","timestamp":1,"secret":"x"}`,                     // 缺 symbol
		`{"strategy_id":"s","symbol":"ETHUSDC","price":"1","timestamp":1,"secret":"x"}`,                  // 缺 signal
		`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","timestamp":1,"secret":"x"}`,              // 缺 price
		`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","secret":"x"}`,                // 缺 timestamp
		`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"1","timestamp":1}`,               // 缺 secret
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
