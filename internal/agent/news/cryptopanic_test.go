package news

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestCryptoPanicFetcher_ParsesTopN(t *testing.T) {
	body, _ := os.ReadFile("testdata/cryptopanic_sample.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("auth_token") != "test-key" {
			http.Error(w, "missing auth_token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	f := NewCryptoPanicFetcher(srv.URL, "test-key").WithHTTPClient(srv.Client())
	headlines, err := f.Fetch(context.Background(), 5)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(headlines) != 3 {
		t.Errorf("want 3 headlines, got %d", len(headlines))
	}
	if headlines[0].Title == "" || headlines[0].URL == "" {
		t.Errorf("first headline incomplete: %+v", headlines[0])
	}
	if headlines[0].Source != "CoinDesk" {
		t.Errorf("Source: %q", headlines[0].Source)
	}
}

func TestCryptoPanicFetcher_TruncatesToTopN(t *testing.T) {
	body, _ := os.ReadFile("testdata/cryptopanic_sample.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	f := NewCryptoPanicFetcher(srv.URL, "test-key").WithHTTPClient(srv.Client())
	h, _ := f.Fetch(context.Background(), 2)
	if len(h) != 2 {
		t.Errorf("want 2 (truncated), got %d", len(h))
	}
}

func TestCryptoPanicFetcher_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	f := NewCryptoPanicFetcher(srv.URL, "k").WithHTTPClient(srv.Client())
	if _, err := f.Fetch(context.Background(), 5); err == nil {
		t.Fatal("expected error on 429")
	}
}
