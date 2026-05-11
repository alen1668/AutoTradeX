package market

import (
	"context"
	"fmt"
	"strconv"
	"time"

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

func (c *BinanceKlineClient) Get1hOHLC(ctx context.Context, symbol string, limit int) ([]Candle, error) {
	klines, err := c.client.NewKlinesService().
		Symbol(symbol).
		Interval("1h").
		Limit(limit).
		Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("binance klines: %w", err)
	}
	out := make([]Candle, 0, len(klines))
	for _, k := range klines {
		candle, err := klineToCandle(k)
		if err != nil {
			return nil, err
		}
		out = append(out, candle)
	}
	return out, nil
}

func klineToCandle(k *futures.Kline) (Candle, error) {
	parse := func(name, s string) (decimal.Decimal, error) {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return decimal.Decimal{}, fmt.Errorf("parse %s %q: %w", name, s, err)
		}
		return decimal.NewFromFloat(f), nil
	}
	o, err := parse("open", k.Open)
	if err != nil {
		return Candle{}, err
	}
	h, err := parse("high", k.High)
	if err != nil {
		return Candle{}, err
	}
	l, err := parse("low", k.Low)
	if err != nil {
		return Candle{}, err
	}
	cl, err := parse("close", k.Close)
	if err != nil {
		return Candle{}, err
	}
	return Candle{
		OpenTime: time.Unix(k.OpenTime/1000, 0).UTC(),
		Open:     o, High: h, Low: l, Close: cl,
	}, nil
}
