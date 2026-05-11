package news

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultCoinDeskRSSURL is the standard CoinDesk feed. Public, no auth needed.
const DefaultCoinDeskRSSURL = "https://www.coindesk.com/arc/outboundfeeds/rss/"

// CoinDeskRSSFetcher implements Fetcher against an RSS 2.0 feed (CoinDesk,
// Cointelegraph and most crypto outlets follow the same schema). No API key
// is required.
type CoinDeskRSSFetcher struct {
	url    string
	client *http.Client
}

func NewCoinDeskRSSFetcher(url string) *CoinDeskRSSFetcher {
	return &CoinDeskRSSFetcher{
		url:    url,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (f *CoinDeskRSSFetcher) WithHTTPClient(c *http.Client) *CoinDeskRSSFetcher {
	f.client = c
	return f
}

type rssFeed struct {
	XMLName xml.Name  `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	GUID        string   `xml:"guid"`
	PubDate     string   `xml:"pubDate"`
	Creator     string   `xml:"http://purl.org/dc/elements/1.1/ creator"`
	Categories  []string `xml:"category"`
}

func (f *CoinDeskRSSFetcher) Fetch(ctx context.Context, topN int) ([]Headline, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rss GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("rss GET: status %d body=%q", resp.StatusCode, string(body))
	}
	var feed rssFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("rss decode: %w", err)
	}
	out := make([]Headline, 0, topN)
	for _, item := range feed.Channel.Items {
		published := parseRFC1123(item.PubDate)
		h := Headline{
			Title:       item.Title,
			URL:         item.Link,
			Source:      "CoinDesk",
			PublishedAt: published,
			Raw: map[string]any{
				"title":        item.Title,
				"link":         item.Link,
				"guid":         item.GUID,
				"pub_date":     item.PubDate,
				"creator":      item.Creator,
				"description":  item.Description,
				"categories":   item.Categories,
				"source_title": "CoinDesk",
			},
		}
		out = append(out, h)
		if len(out) >= topN {
			break
		}
	}
	return out, nil
}

// parseRFC1123 accepts the RSS standard layout. Returns zero time on failure;
// callers shouldn't drop the headline just because the timestamp is unparsed.
func parseRFC1123(s string) time.Time {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
