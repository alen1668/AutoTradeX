package news

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const sampleRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel>
    <title>CoinDesk</title>
    <item>
      <title>SEC sues XYZ Exchange</title>
      <link>https://example.com/sec-xyz</link>
      <description>Regulator alleges unregistered securities offering</description>
      <guid isPermaLink="false">guid-1</guid>
      <pubDate>Mon, 11 May 2026 16:30:09 +0000</pubDate>
      <dc:creator>Reporter A</dc:creator>
      <category>News</category>
    </item>
    <item>
      <title>Bitcoin price holds 65k</title>
      <link>https://example.com/btc-65k</link>
      <description>Daily analysis</description>
      <guid isPermaLink="false">guid-2</guid>
      <pubDate>Mon, 11 May 2026 15:00:00 +0000</pubDate>
      <dc:creator>Reporter B</dc:creator>
    </item>
    <item>
      <title>Third item</title>
      <link>https://example.com/3</link>
      <pubDate>Mon, 11 May 2026 14:00:00 +0000</pubDate>
    </item>
  </channel>
</rss>`

func TestCoinDeskRSSFetcher_ParsesItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleRSS))
	}))
	defer srv.Close()

	f := NewCoinDeskRSSFetcher(srv.URL).WithHTTPClient(srv.Client())
	hs, err := f.Fetch(context.Background(), 5)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(hs) != 3 {
		t.Fatalf("want 3, got %d", len(hs))
	}
	if hs[0].Title != "SEC sues XYZ Exchange" || hs[0].URL != "https://example.com/sec-xyz" {
		t.Errorf("h0: %+v", hs[0])
	}
	if hs[0].Source != "CoinDesk" {
		t.Errorf("source: %q", hs[0].Source)
	}
	if hs[0].PublishedAt.Year() != 2026 {
		t.Errorf("PublishedAt not parsed: %v", hs[0].PublishedAt)
	}
}

func TestCoinDeskRSSFetcher_TruncatesToTopN(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleRSS))
	}))
	defer srv.Close()
	f := NewCoinDeskRSSFetcher(srv.URL).WithHTTPClient(srv.Client())
	hs, _ := f.Fetch(context.Background(), 2)
	if len(hs) != 2 {
		t.Errorf("want 2 (truncated), got %d", len(hs))
	}
}

func TestCoinDeskRSSFetcher_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	f := NewCoinDeskRSSFetcher(srv.URL).WithHTTPClient(srv.Client())
	if _, err := f.Fetch(context.Background(), 5); err == nil {
		t.Fatal("expected error on 503")
	}
}
