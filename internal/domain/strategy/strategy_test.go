package strategy

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_RejectsInvalidLeverage(t *testing.T) {
	_, err := New(Config{ID: "s", Symbol: "ETHUSDC", Leverage: 0,
		SizeUSDC: decimal.NewFromInt(10), StopLossPct: decimal.NewFromFloat(1.5),
		MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true})
	require.Error(t, err)
}

func TestNew_RejectsNonPositiveSize(t *testing.T) {
	_, err := New(Config{ID: "s", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.Zero, StopLossPct: decimal.NewFromFloat(1.5),
		MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true})
	require.Error(t, err)
}

func TestNew_RejectsNonPositiveStopLoss(t *testing.T) {
	_, err := New(Config{ID: "s", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.NewFromInt(10), StopLossPct: decimal.Zero,
		MaxOpenUSDC: decimal.NewFromInt(100), Enabled: true})
	require.Error(t, err)
}

func TestNew_RejectsMaxOpenLessThanSize(t *testing.T) {
	_, err := New(Config{ID: "s", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.NewFromInt(100), StopLossPct: decimal.NewFromFloat(1.5),
		MaxOpenUSDC: decimal.NewFromInt(50), Enabled: true})
	require.Error(t, err)
}

func TestNew_AcceptsValid(t *testing.T) {
	s, err := New(Config{
		ID: "macd_eth_high", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.NewFromInt(100), StopLossPct: decimal.NewFromFloat(1.5),
		TakeProfitPct: decimal.NewFromFloat(3.0),
		MaxOpenUSDC: decimal.NewFromInt(500), Enabled: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "macd_eth_high", s.ID)
	assert.True(t, s.HasTakeProfit())
}

func TestNotionalAtPrice(t *testing.T) {
	s, _ := New(Config{ID: "s", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC: decimal.NewFromInt(100), StopLossPct: decimal.NewFromFloat(1),
		MaxOpenUSDC: decimal.NewFromInt(500), Enabled: true})
	notional := s.NotionalUSDC()
	// notional = size_usdc * leverage = 100 * 5 = 500
	assert.True(t, notional.Equal(decimal.NewFromInt(500)))
}
