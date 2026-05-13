package news

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

type stubFetcher struct {
	headlines []Headline
	err       error
	called    int
}

func (s *stubFetcher) Fetch(ctx context.Context, topN int) ([]Headline, error) {
	s.called++
	if s.err != nil {
		return nil, s.err
	}
	if topN < len(s.headlines) {
		return s.headlines[:topN], nil
	}
	return s.headlines, nil
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestMultiFetcher_MergesAndSortsByPublishedAt(t *testing.T) {
	a := &stubFetcher{headlines: []Headline{
		{Title: "older", URL: "https://example.com/1", Source: "A", PublishedAt: mustTime("2026-05-12T10:00:00Z")},
		{Title: "newest", URL: "https://example.com/3", Source: "A", PublishedAt: mustTime("2026-05-12T14:00:00Z")},
	}}
	b := &stubFetcher{headlines: []Headline{
		{Title: "middle", URL: "https://example.com/2", Source: "B", PublishedAt: mustTime("2026-05-12T12:00:00Z")},
	}}
	mf := NewMultiFetcher([]Fetcher{a, b}, zerolog.Nop())
	hs, err := mf.Fetch(context.Background(), 10)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(hs) != 3 {
		t.Fatalf("want 3 headlines, got %d", len(hs))
	}
	if hs[0].Title != "newest" || hs[1].Title != "middle" || hs[2].Title != "older" {
		t.Errorf("sort wrong: %v %v %v", hs[0].Title, hs[1].Title, hs[2].Title)
	}
}

func TestMultiFetcher_DedupesByURL(t *testing.T) {
	a := &stubFetcher{headlines: []Headline{
		{Title: "CoinDesk version", URL: "https://shared.example/cpi", Source: "CoinDesk", PublishedAt: mustTime("2026-05-12T13:00:00Z")},
	}}
	b := &stubFetcher{headlines: []Headline{
		{Title: "MarketWatch version", URL: "https://shared.example/cpi", Source: "MarketWatch", PublishedAt: mustTime("2026-05-12T13:30:00Z")},
		{Title: "Unique", URL: "https://shared.example/korea-etf", Source: "MarketWatch", PublishedAt: mustTime("2026-05-12T12:00:00Z")},
	}}
	mf := NewMultiFetcher([]Fetcher{a, b}, zerolog.Nop())
	hs, err := mf.Fetch(context.Background(), 10)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(hs) != 2 {
		t.Fatalf("want 2 (deduped), got %d", len(hs))
	}
	foundUnique := false
	for _, h := range hs {
		if h.Title == "Unique" {
			foundUnique = true
		}
	}
	if !foundUnique {
		t.Errorf("Unique headline lost in dedup")
	}
}

func TestMultiFetcher_PartialFailure_KeepsSuccessfulSources(t *testing.T) {
	good := &stubFetcher{headlines: []Headline{
		{Title: "from-good", URL: "https://example.com/good", PublishedAt: mustTime("2026-05-12T10:00:00Z")},
	}}
	bad := &stubFetcher{err: errors.New("network down")}
	mf := NewMultiFetcher([]Fetcher{good, bad}, zerolog.Nop())
	hs, err := mf.Fetch(context.Background(), 10)
	if err != nil {
		t.Fatalf("Fetch should not error on partial fail: %v", err)
	}
	if len(hs) != 1 || hs[0].Title != "from-good" {
		t.Errorf("expected only 'from-good', got %+v", hs)
	}
}

func TestMultiFetcher_AllFailed_ReturnsError(t *testing.T) {
	bad1 := &stubFetcher{err: errors.New("e1")}
	bad2 := &stubFetcher{err: errors.New("e2")}
	mf := NewMultiFetcher([]Fetcher{bad1, bad2}, zerolog.Nop())
	_, err := mf.Fetch(context.Background(), 10)
	if err == nil {
		t.Fatal("expected error when all sources fail")
	}
}

func TestMultiFetcher_TopNTruncates(t *testing.T) {
	a := &stubFetcher{headlines: []Headline{
		{Title: "h1", URL: "u1", PublishedAt: mustTime("2026-05-12T10:00:00Z")},
		{Title: "h2", URL: "u2", PublishedAt: mustTime("2026-05-12T11:00:00Z")},
		{Title: "h3", URL: "u3", PublishedAt: mustTime("2026-05-12T12:00:00Z")},
	}}
	mf := NewMultiFetcher([]Fetcher{a}, zerolog.Nop())
	hs, _ := mf.Fetch(context.Background(), 2)
	if len(hs) != 2 {
		t.Errorf("want 2 (top-n), got %d", len(hs))
	}
	if hs[0].Title != "h3" || hs[1].Title != "h2" {
		t.Errorf("expected newest two: %v", hs)
	}
}

func TestMultiFetcher_PerSourceTopNRequest(t *testing.T) {
	srcs := []Fetcher{
		&stubFetcher{}, &stubFetcher{}, &stubFetcher{}, &stubFetcher{},
	}
	mf := NewMultiFetcher(srcs, zerolog.Nop())
	_, _ = mf.Fetch(context.Background(), 12)
	for i, s := range srcs {
		stub := s.(*stubFetcher)
		if stub.called != 1 {
			t.Errorf("source %d called %d times, want 1", i, stub.called)
		}
	}
}
