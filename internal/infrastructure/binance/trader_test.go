//go:build integration_binance

package binance

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/config"
	"github.com/lizhaojie/tvbot/internal/trade"
)

func TestTestnetMarketOrderRoundTrip(t *testing.T) {
	key := os.Getenv("BINANCE_TESTNET_KEY")
	if key == "" {
		t.Skip("set BINANCE_TESTNET_KEY + BINANCE_TESTNET_SECRET")
	}
	secret := os.Getenv("BINANCE_TESTNET_SECRET")
	tr := New(config.BinanceConfig{
		OrderTimeoutMs: 10000,
	}, key, secret, config.ModeTestnet, zerolog.Nop())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	step, err := tr.StepSize(ctx, "BTCUSDT")
	require.NoError(t, err)
	require.True(t, step.IsPositive())

	res, err := tr.Place(ctx, trade.OrderRequest{
		ClientOrderID:  "tvbot-test-" + time.Now().UTC().Format("150405"),
		Symbol:         "BTCUSDT",
		Side:           trade.OrderSideBuy,
		Type:           trade.OrderTypeMarket,
		Qty:            decimal.NewFromFloat(0.001),
		ReferencePrice: decimal.NewFromInt(50000),
		Purpose:        "entry",
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.ExchangeOrderID)
}
