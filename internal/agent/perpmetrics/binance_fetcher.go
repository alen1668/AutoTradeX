package perpmetrics

import (
	"context"
	"fmt"
	"time"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"
)

// PremiumIndexResult is the subset of binance PremiumIndex we use.
type PremiumIndexResult struct {
	FundingRate     decimal.Decimal
	NextFundingTime time.Time
	MarkPrice       decimal.Decimal
}

// OpenInterestResult holds current and a ~24h-ago reference OI for computing
// 24h % change. Current is always populated; Prev24h may be zero when binance
// has no historical bucket for the symbol (new listings or hist API failure).
type OpenInterestResult struct {
	Current decimal.Decimal
	Prev24h decimal.Decimal
}

// Fetcher is what the worker depends on. Tests substitute a fake to drive
// failure cases without standing up an httptest server.
type Fetcher interface {
	PremiumIndex(ctx context.Context, symbol string) (PremiumIndexResult, error)
	OpenInterest(ctx context.Context, symbol string) (OpenInterestResult, error)
	TopLongShortRatio(ctx context.Context, symbol string) (decimal.Decimal, error)
}

// BinanceFetcher implements Fetcher backed by adshao/go-binance/v2.
type BinanceFetcher struct {
	client *futures.Client
}

func NewBinanceFetcher(client *futures.Client) *BinanceFetcher {
	return &BinanceFetcher{client: client}
}

func (f *BinanceFetcher) PremiumIndex(ctx context.Context, symbol string) (PremiumIndexResult, error) {
	list, err := f.client.NewPremiumIndexService().Symbol(symbol).Do(ctx)
	if err != nil {
		return PremiumIndexResult{}, err
	}
	if len(list) == 0 {
		return PremiumIndexResult{}, fmt.Errorf("premiumIndex(%s): empty response", symbol)
	}
	p := list[0]
	rate, err := decimal.NewFromString(p.LastFundingRate)
	if err != nil {
		return PremiumIndexResult{}, fmt.Errorf("parse lastFundingRate %q: %w", p.LastFundingRate, err)
	}
	mark, _ := decimal.NewFromString(p.MarkPrice)
	return PremiumIndexResult{
		FundingRate:     rate,
		NextFundingTime: time.UnixMilli(p.NextFundingTime).UTC(),
		MarkPrice:       mark,
	}, nil
}

func (f *BinanceFetcher) OpenInterest(ctx context.Context, symbol string) (OpenInterestResult, error) {
	cur, err := f.client.NewGetOpenInterestService().Symbol(symbol).Do(ctx)
	if err != nil {
		return OpenInterestResult{}, err
	}
	curOI, err := decimal.NewFromString(cur.OpenInterest)
	if err != nil {
		return OpenInterestResult{}, fmt.Errorf("parse openInterest %q: %w", cur.OpenInterest, err)
	}
	// ~24h-ago via openInterestHist period=1h limit=25. Take the oldest bar.
	hist, err := f.client.NewOpenInterestStatisticsService().
		Symbol(symbol).Period("1h").Limit(25).Do(ctx)
	if err != nil {
		return OpenInterestResult{Current: curOI}, nil
	}
	if len(hist) == 0 {
		return OpenInterestResult{Current: curOI}, nil
	}
	prev, _ := decimal.NewFromString(hist[0].SumOpenInterest)
	return OpenInterestResult{Current: curOI, Prev24h: prev}, nil
}

func (f *BinanceFetcher) TopLongShortRatio(ctx context.Context, symbol string) (decimal.Decimal, error) {
	list, err := f.client.NewTopLongShortPositionRatioService().
		Symbol(symbol).Period("5m").Limit(1).Do(ctx)
	if err != nil {
		return decimal.Decimal{}, err
	}
	if len(list) == 0 {
		return decimal.Decimal{}, fmt.Errorf("topLongShortPositionRatio(%s): empty response", symbol)
	}
	last := list[len(list)-1]
	r, err := decimal.NewFromString(last.LongShortRatio)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("parse longShortRatio %q: %w", last.LongShortRatio, err)
	}
	return r, nil
}
