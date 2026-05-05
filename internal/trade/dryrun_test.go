package trade

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDryRunTrader_PlaceMarketReturnsFilled(t *testing.T) {
	dr := NewDryRunTrader()
	res, err := dr.Place(context.Background(), OrderRequest{
		ClientOrderID:  "c1",
		Symbol:         "ETHUSDC",
		Side:           OrderSideBuy,
		Type:           OrderTypeMarket,
		Qty:            decimal.NewFromFloat(0.1),
		ReferencePrice: decimal.NewFromFloat(100),
	})
	require.NoError(t, err)
	assert.Equal(t, OrderStatusFilled, res.Status)
	assert.True(t, decimal.NewFromFloat(0.1).Equal(res.FilledQty))
	assert.True(t, decimal.NewFromFloat(100).Equal(res.AvgFillPrice))
	assert.Equal(t, "DRYRUN-c1", res.ExchangeOrderID)
}

func TestDryRunTrader_PlaceStopReturnsSubmittedNotFilled(t *testing.T) {
	dr := NewDryRunTrader()
	res, err := dr.Place(context.Background(), OrderRequest{
		ClientOrderID:  "c2",
		Symbol:         "ETHUSDC",
		Side:           OrderSideSell,
		Type:           OrderTypeStopMarket,
		Qty:            decimal.NewFromFloat(1),
		StopPrice:      decimal.NewFromFloat(95),
		ReferencePrice: decimal.NewFromFloat(100),
	})
	require.NoError(t, err)
	assert.Equal(t, OrderStatusSubmitted, res.Status,
		"stop orders are submitted, not filled, until trigger; in dry_run we never trigger")
}

func TestDryRunTrader_CancelReturnsCanceled(t *testing.T) {
	dr := NewDryRunTrader()
	require.NoError(t, dr.Cancel(context.Background(), "ETHUSDC", "c2"))
}
