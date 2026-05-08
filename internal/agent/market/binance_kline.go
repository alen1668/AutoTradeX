package market

import (
	"context"
	"fmt"
	"strconv"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"
)

// BinanceKlineClient adapts adshao/go-binance's KlinesService to KlineClient.
// Reuses the *futures.Client constructed by infrastructure/binance.Trader so
// auth + base-URL (testnet vs mainnet) configuration stays in one place.
type BinanceKlineClient struct {
	client *futures.Client
}

func NewBinanceKlineClient(client *futures.Client) *BinanceKlineClient {
	return &BinanceKlineClient{client: client}
}

func (c *BinanceKlineClient) Get1hCloses(ctx context.Context, symbol string, limit int) ([]decimal.Decimal, error) {
	klines, err := c.client.NewKlinesService().
		Symbol(symbol).
		Interval("1h").
		Limit(limit).
		Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("binance klines: %w", err)
	}
	out := make([]decimal.Decimal, 0, len(klines))
	for _, k := range klines {
		f, err := strconv.ParseFloat(k.Close, 64)
		if err != nil {
			return nil, fmt.Errorf("parse close %q: %w", k.Close, err)
		}
		out = append(out, decimal.NewFromFloat(f))
	}
	return out, nil
}
