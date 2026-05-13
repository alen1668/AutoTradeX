package news

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	DefaultCoinDeskRSSURL     = "https://www.coindesk.com/arc/outboundfeeds/rss/"
	DefaultMarketWatchRSSURL  = "https://feeds.marketwatch.com/marketwatch/topstories/"
	DefaultCNBCMarketsRSSURL  = "https://www.cnbc.com/id/100003114/device/rss/rss.html"
	DefaultYahooFinanceRSSURL = "https://finance.yahoo.com/news/rssindex"
)

// RSSFetcher 是参数化的 RSS 2.0 抓取器,兼容 CoinDesk / MarketWatch / CNBC /
// Yahoo Finance / Cointelegraph 等所有标准 RSS 2.0 feed。
type RSSFetcher struct {
	name        string
	url         string
	sourceLabel string
	client      *http.Client
}

func NewRSSFetcher(name, url, sourceLabel string) *RSSFetcher {
	return &RSSFetcher{
		name:        name,
		url:         url,
		sourceLabel: sourceLabel,
		client:      &http.Client{Timeout: 15 * time.Second},
	}
}

func (f *RSSFetcher) WithHTTPClient(c *http.Client) *RSSFetcher {
	f.client = c
	return f
}

func (f *RSSFetcher) Name() string { return f.name }

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
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

func (f *RSSFetcher) Fetch(ctx context.Context, topN int) ([]Headline, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (tvbot-news-fetcher)")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s GET: %w", f.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s GET: status %d body=%q", f.name, resp.StatusCode, string(body))
	}
	var feed rssFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, fmt.Errorf("%s decode: %w", f.name, err)
	}
	out := make([]Headline, 0, topN)
	for _, item := range feed.Channel.Items {
		published := parseRFC1123(item.PubDate)
		h := Headline{
			Title:       item.Title,
			URL:         item.Link,
			Source:      f.sourceLabel,
			PublishedAt: published,
			Raw: map[string]any{
				"title":        item.Title,
				"link":         item.Link,
				"guid":         item.GUID,
				"pub_date":     item.PubDate,
				"creator":      item.Creator,
				"description":  item.Description,
				"categories":   item.Categories,
				"source_title": f.sourceLabel,
			},
		}
		out = append(out, h)
		if len(out) >= topN {
			break
		}
	}
	return out, nil
}

// parseRFC1123 accepts the RSS 2.0 standard layout 和 ISO 8601(Yahoo Finance 用)。
// Returns zero time on failure; callers shouldn't drop the headline just because
// the timestamp is unparsed.
func parseRFC1123(s string) time.Time {
	for _, layout := range []string{
		time.RFC1123Z, time.RFC1123,
		time.RFC822Z, time.RFC822,
		time.RFC3339, time.RFC3339Nano, // Yahoo Finance: 2026-05-12T02:33:00Z
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
