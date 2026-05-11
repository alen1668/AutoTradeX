package calendar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestForexFactoryFetcher_ParsesAndFiltersHighUSD(t *testing.T) {
	body, err := os.ReadFile("testdata/ff_weekly_sample.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	f := NewForexFactoryFetcher(srv.URL).WithHTTPClient(srv.Client())
	events, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// Expect 2 High USD events. Manufacturing PMI is EUR (excluded), Retail Sales is Medium (excluded).
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(events), events)
	}
	if events[0].Name != "CPI m/m" || events[1].Name != "FOMC Statement" {
		t.Errorf("unexpected events: %+v", events)
	}
	if events[0].Impact != "High" {
		t.Errorf("Impact: %q", events[0].Impact)
	}
	if events[0].SourceID == "" {
		t.Errorf("SourceID empty")
	}
	// Verify CPI parsed as 05-13-2026 12:30 ET → UTC.
	et, _ := time.LoadLocation("America/New_York")
	want := time.Date(2026, 5, 13, 12, 30, 0, 0, et).UTC()
	if !events[0].StartsAt.Equal(want) {
		t.Errorf("StartsAt: got %v want %v", events[0].StartsAt, want)
	}
}

func TestForexFactoryFetcher_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "ff is down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := NewForexFactoryFetcher(srv.URL).WithHTTPClient(srv.Client())
	if _, err := f.Fetch(context.Background()); err == nil {
		t.Fatal("expected error on 503")
	}
}

func TestForexFactoryFetcher_BadXMLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<not-xml"))
	}))
	defer srv.Close()
	f := NewForexFactoryFetcher(srv.URL).WithHTTPClient(srv.Client())
	if _, err := f.Fetch(context.Background()); err == nil {
		t.Fatal("expected parse error")
	}
}
