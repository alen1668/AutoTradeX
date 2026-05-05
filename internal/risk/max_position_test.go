package risk

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
)

func mustStrategy(t *testing.T, sizeUSDC, maxOpen float64) *strategy.Strategy {
	t.Helper()
	s, err := strategy.New(strategy.Config{
		ID: "s", Symbol: "ETHUSDC", Leverage: 5,
		SizeUSDC:    decimal.NewFromFloat(sizeUSDC),
		StopLossPct: decimal.NewFromFloat(1.5),
		MaxOpenUSDC: decimal.NewFromFloat(maxOpen),
		Enabled:     true,
	})
	require.NoError(t, err)
	return s
}

func TestMaxPositionRule_AllowsBelowLimit(t *testing.T) {
	r := MaxPositionRule{}
	s := mustStrategy(t, 100, 500)
	d, err := r.Check(context.Background(), Input{
		Signal:   &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy: s,
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestMaxPositionRule_DeniesNotionalAboveMaxOpen(t *testing.T) {
	r := MaxPositionRule{}
	// notional = 200 * 5 = 1000 > max_open 500
	s := mustStrategy(t, 200, 500)
	d, err := r.Check(context.Background(), Input{
		Signal:   &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy: s,
	})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "max_open_usdc")
}

func TestMaxPositionRule_AllowsExitSignal(t *testing.T) {
	// 平仓信号不受 max_open 限制
	r := MaxPositionRule{}
	s := mustStrategy(t, 200, 500)
	d, err := r.Check(context.Background(), Input{
		Signal:   &sigpkg.Signal{Kind: sigpkg.KindExitLong},
		Strategy: s,
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}
