package portfolio

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/store"
)

type fakeRepo struct {
	active   []*store.VirtualPositionRow
	dailyPnL decimal.Decimal
	listErr  error
	pnlErr   error
}

func (f *fakeRepo) ListActive(ctx context.Context, _ store.Querier) ([]*store.VirtualPositionRow, error) {
	return f.active, f.listErr
}
func (f *fakeRepo) DailyRealizedPnL(ctx context.Context, _ store.Querier, _ time.Time) (decimal.Decimal, error) {
	return f.dailyPnL, f.pnlErr
}

func TestProvider_EmptyReturnsZero(t *testing.T) {
	p := New(&fakeRepo{}, nil)
	snap, err := p.Snapshot(context.Background())
	require.NoError(t, err)
	require.NotNil(t, snap)
	assert.True(t, snap.TotalNotionalUSD.IsZero())
	assert.Empty(t, snap.OpenPositions)
	assert.True(t, snap.DailyPnLUSD.IsZero())
}

func TestProvider_AggregatesOpenPositions(t *testing.T) {
	fr := &fakeRepo{
		active: []*store.VirtualPositionRow{
			{StrategyID: "s1", Symbol: "ETHUSDC", Side: "long",
				EntryFillPrice: decimal.NewFromInt(2300), Qty: decimal.NewFromFloat(1.5)},
			{StrategyID: "s2", Symbol: "BTCUSDC", Side: "short",
				EntryFillPrice: decimal.NewFromInt(60000), Qty: decimal.NewFromFloat(0.05)},
		},
		dailyPnL: decimal.NewFromInt(12),
	}
	p := New(fr, nil)
	snap, _ := p.Snapshot(context.Background())
	require.NotNil(t, snap)
	// notional: 2300*1.5 + 60000*0.05 = 3450 + 3000 = 6450
	assert.True(t, snap.TotalNotionalUSD.Sub(decimal.NewFromInt(6450)).Abs().LessThan(decimal.NewFromFloat(0.01)))
	assert.Equal(t, decimal.NewFromInt(12), snap.DailyPnLUSD)
	require.Len(t, snap.OpenPositions, 2)
	assert.Equal(t, "s1", snap.OpenPositions[0].StrategyID)
}

func TestProvider_FallbackToSignalPriceWhenFillUnknown(t *testing.T) {
	// "opening" status: fill price not yet known → use signal price
	fr := &fakeRepo{
		active: []*store.VirtualPositionRow{
			{StrategyID: "s1", Symbol: "ETHUSDC", Side: "long",
				EntrySignalPrice: decimal.NewFromInt(2300),
				EntryFillPrice:   decimal.Zero, // not filled yet
				Qty:              decimal.NewFromFloat(1)},
		},
	}
	p := New(fr, nil)
	snap, _ := p.Snapshot(context.Background())
	require.NotNil(t, snap)
	assert.Equal(t, "2300", snap.OpenPositions[0].EntryPrice.String())
	assert.Equal(t, "2300", snap.TotalNotionalUSD.String())
}

func TestProvider_ListActiveErrorReturnsNilNoErr(t *testing.T) {
	fr := &fakeRepo{listErr: errors.New("db down")}
	p := New(fr, nil)
	snap, err := p.Snapshot(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, snap)
}

func TestProvider_PnLErrorReturnsNilNoErr(t *testing.T) {
	fr := &fakeRepo{pnlErr: errors.New("db down")}
	p := New(fr, nil)
	snap, err := p.Snapshot(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, snap)
}
