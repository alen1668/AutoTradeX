package risk

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
)

func TestTotalLeverageRule_AllowsBelowMax(t *testing.T) {
	r := TotalLeverageRule{MaxLeverage: decimal.NewFromFloat(3.0)}
	s := mustStrategy(t, 100, 500) // notional = 500
	d, err := r.Check(context.Background(), Input{
		Signal:          &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy:        s,
		OpenNotionalSum: decimal.NewFromFloat(1000),
		AccountEquity:   decimal.NewFromFloat(1000), // (1000+500)/1000 = 1.5x < 3.0x
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestTotalLeverageRule_DeniesAboveMax(t *testing.T) {
	r := TotalLeverageRule{MaxLeverage: decimal.NewFromFloat(2.0)}
	s := mustStrategy(t, 100, 500) // notional = 500
	d, err := r.Check(context.Background(), Input{
		Signal:          &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy:        s,
		OpenNotionalSum: decimal.NewFromFloat(2000),
		AccountEquity:   decimal.NewFromFloat(1000), // (2000+500)/1000 = 2.5x > 2.0x
	})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "max_total_leverage")
}

func TestTotalLeverageRule_AllowsExitSignal(t *testing.T) {
	r := TotalLeverageRule{MaxLeverage: decimal.NewFromFloat(1.0)}
	s := mustStrategy(t, 100, 500)
	d, err := r.Check(context.Background(), Input{
		Signal:          &sigpkg.Signal{Kind: sigpkg.KindExitLong},
		Strategy:        s,
		OpenNotionalSum: decimal.NewFromFloat(99999),
		AccountEquity:   decimal.NewFromFloat(1),
	})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestTotalLeverageRule_RejectsZeroEquity(t *testing.T) {
	r := TotalLeverageRule{MaxLeverage: decimal.NewFromFloat(3.0)}
	s := mustStrategy(t, 100, 500)
	d, err := r.Check(context.Background(), Input{
		Signal:        &sigpkg.Signal{Kind: sigpkg.KindLong},
		Strategy:      s,
		AccountEquity: decimal.Zero,
	})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "equity")
}
