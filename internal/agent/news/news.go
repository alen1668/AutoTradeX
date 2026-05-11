// Package news fetches crypto news (CryptoPanic), classifies it via LLM,
// and persists results to news_snapshots.
package news

import "time"

// Headline is one row from the upstream feed, normalized for the LLM prompt.
type Headline struct {
	ExternalID  int64
	Title       string
	URL         string
	Source      string
	PublishedAt time.Time
	Raw         map[string]any // entire upstream object, for raw_headlines JSONB
}
