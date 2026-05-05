package risk

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
)

func TestDailyLossBreaker_AllowsWhenBreakerNotTripped(t *testing.T) {
	r := DailyLossBreakerRule{MaxDailyLossUSDC: decimal.NewFromFloat(500)}
	d, err := r.Check(context.Background(), Input{
		Signal:         &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy:       mustStrategy(t, 100, 500),
		DailyPnLUSDC:   decimal.NewFromFloat(-100),
		BreakerTripped: false,
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestDailyLossBreaker_DeniesWhenBreakerTripped(t *testing.T) {
	r := DailyLossBreakerRule{MaxDailyLossUSDC: decimal.NewFromFloat(500)}
	d, err := r.Check(context.Background(), Input{
		Signal:         &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy:       mustStrategy(t, 100, 500),
		BreakerTripped: true,
	})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "breaker")
}

func TestDailyLossBreaker_DeniesWhenLossExceedsThreshold(t *testing.T) {
	r := DailyLossBreakerRule{MaxDailyLossUSDC: decimal.NewFromFloat(500)}
	d, err := r.Check(context.Background(), Input{
		Signal:       &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy:     mustStrategy(t, 100, 500),
		DailyPnLUSDC: decimal.NewFromFloat(-501), // 损失超过 500
	})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "daily loss")
}

func TestDailyLossBreaker_AllowsExitSignalEvenWhenTripped(t *testing.T) {
	// 平仓信号必须能通过，否则永远卡住
	r := DailyLossBreakerRule{MaxDailyLossUSDC: decimal.NewFromFloat(500)}
	d, err := r.Check(context.Background(), Input{
		Signal:         &sigpkg.Signal{Kind: sigpkg.KindExitLong},
		BreakerTripped: true,
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}
