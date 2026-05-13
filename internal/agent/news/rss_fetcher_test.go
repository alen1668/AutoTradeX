package news

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const mwSampleRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>MarketWatch Top Stories</title>
    <item>
      <title>Record outflow from iShares MSCI South Korea ETF</title>
      <link>https://example.com/mw/korea-etf</link>
      <description>Largest single-week withdrawal ever recorded</description>
      <pubDate>Tue, 12 May 2026 14:00:00 +0000</pubDate>
    </item>
    <item>
      <title>US April CPI prints 3.8%, hottest since 2023</title>
      <link>https://example.com/mw/cpi-apr</link>
      <pubDate>Tue, 12 May 2026 13:35:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

func TestRSSFetcher_ParsesAndStampsSourceLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(mwSampleRSS))
	}))
	defer srv.Close()

	f := NewRSSFetcher("marketwatch", srv.URL, "MarketWatch").WithHTTPClient(srv.Client())
	hs, err := f.Fetch(context.Background(), 5)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(hs) != 2 {
		t.Fatalf("want 2 headlines, got %d", len(hs))
	}
	if hs[0].Source != "MarketWatch" {
		t.Errorf("source label not stamped: %q", hs[0].Source)
	}
	if hs[0].Title != "Record outflow from iShares MSCI South Korea ETF" {
		t.Errorf("title parse: %q", hs[0].Title)
	}
	if hs[0].URL != "https://example.com/mw/korea-etf" {
		t.Errorf("url parse: %q", hs[0].URL)
	}
	if hs[0].PublishedAt.Year() != 2026 {
		t.Errorf("pubdate parse: %v", hs[0].PublishedAt)
	}
}

func TestRSSFetcher_TopNTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mwSampleRSS))
	}))
	defer srv.Close()
	f := NewRSSFetcher("marketwatch", srv.URL, "MarketWatch").WithHTTPClient(srv.Client())
	hs, _ := f.Fetch(context.Background(), 1)
	if len(hs) != 1 {
		t.Errorf("want 1, got %d", len(hs))
	}
}

func TestRSSFetcher_HTTPErrorIncludesName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	f := NewRSSFetcher("marketwatch", srv.URL, "MarketWatch").WithHTTPClient(srv.Client())
	_, err := f.Fetch(context.Background(), 5)
	if err == nil {
		t.Fatal("expected error on 503")
	}
	if !strings.Contains(err.Error(), "marketwatch") {
		t.Errorf("error should mention source name, got: %v", err)
	}
}
