package perpmetrics_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/perpmetrics"
)

func TestBinanceFetcher_AllEndpoints_Parse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fapi/v1/premiumIndex":
			fmt.Fprintln(w, `[{"symbol":"BTCUSDT","markPrice":"50000.10","indexPrice":"50001","estimatedSettlePrice":"50000.5","lastFundingRate":"0.00025","nextFundingTime":1715000000000,"interestRate":"0.0001","time":1714999000000}]`)
		case "/fapi/v1/openInterest":
			fmt.Fprintln(w, `{"openInterest":"12345.6","symbol":"BTCUSDT","time":1714999000000}`)
		case "/futures/data/topLongShortPositionRatio":
			fmt.Fprintln(w, `[{"symbol":"BTCUSDT","longShortRatio":"1.85","longAccount":"0.65","shortAccount":"0.35","timestamp":1714999000000}]`)
		case "/futures/data/openInterestHist":
			fmt.Fprintln(w, `[{"symbol":"BTCUSDT","sumOpenInterest":"12000.0","sumOpenInterestValue":"600000000","timestamp":1714912600000}]`)
		default:
			http.Error(w, "unknown path: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer ts.Close()

	client := futures.NewClient("k", "s")
	client.BaseURL = ts.URL
	f := perpmetrics.NewBinanceFetcher(client)
	ctx := context.Background()

	pi, err := f.PremiumIndex(ctx, "BTCUSDT")
	if err != nil {
		t.Fatalf("PremiumIndex: %v", err)
	}
	if !pi.FundingRate.Equal(decimal.NewFromFloat(0.00025)) {
		t.Errorf("FundingRate = %v, want 0.00025", pi.FundingRate)
	}
	if pi.NextFundingTime.IsZero() {
		t.Error("NextFundingTime should be populated")
	}
	if !pi.MarkPrice.Equal(decimal.NewFromFloat(50000.10)) {
		t.Errorf("MarkPrice = %v, want 50000.10", pi.MarkPrice)
	}

	oi, err := f.OpenInterest(ctx, "BTCUSDT")
	if err != nil {
		t.Fatalf("OpenInterest: %v", err)
	}
	if !oi.Current.Equal(decimal.NewFromFloat(12345.6)) {
		t.Errorf("OI current = %v, want 12345.6", oi.Current)
	}
	if !oi.Prev24h.Equal(decimal.NewFromFloat(12000)) {
		t.Errorf("OI prev24h = %v, want 12000", oi.Prev24h)
	}

	ls, err := f.TopLongShortRatio(ctx, "BTCUSDT")
	if err != nil {
		t.Fatalf("TopLongShortRatio: %v", err)
	}
	if !ls.Equal(decimal.NewFromFloat(1.85)) {
		t.Errorf("TopLS = %v, want 1.85", ls)
	}
}

func TestBinanceFetcher_HistFailureSoftFalls(t *testing.T) {
	// openInterestHist returns 500; OpenInterest method should still return
	// current OI (Prev24h = zero), letting the worker fall back to neutral OI signal.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fapi/v1/openInterest":
			fmt.Fprintln(w, `{"openInterest":"100","symbol":"NEWUSDT","time":1714999000000}`)
		case "/futures/data/openInterestHist":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.Error(w, "unknown", http.StatusNotFound)
		}
	}))
	defer ts.Close()

	client := futures.NewClient("k", "s")
	client.BaseURL = ts.URL
	f := perpmetrics.NewBinanceFetcher(client)

	oi, err := f.OpenInterest(context.Background(), "NEWUSDT")
	if err != nil {
		t.Fatalf("OpenInterest should soft-fail hist: %v", err)
	}
	if !oi.Current.Equal(decimal.NewFromInt(100)) {
		t.Errorf("Current = %v, want 100", oi.Current)
	}
	if !oi.Prev24h.IsZero() {
		t.Errorf("Prev24h = %v, want zero (hist soft-failed)", oi.Prev24h)
	}
}

func TestBinanceFetcher_PremiumIndex_ErrorPropagated(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	client := futures.NewClient("k", "s")
	client.BaseURL = ts.URL
	f := perpmetrics.NewBinanceFetcher(client)

	if _, err := f.PremiumIndex(context.Background(), "BTCUSDT"); err == nil {
		t.Error("expected error on 500 response, got nil")
	}
}
