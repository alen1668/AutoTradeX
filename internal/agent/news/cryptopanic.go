package news

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const DefaultCryptoPanicURL = "https://cryptopanic.com/api/v1/posts/"

type CryptoPanicFetcher struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewCryptoPanicFetcher(baseURL, apiKey string) *CryptoPanicFetcher {
	return &CryptoPanicFetcher{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (f *CryptoPanicFetcher) WithHTTPClient(c *http.Client) *CryptoPanicFetcher {
	f.client = c
	return f
}

type cpResponse struct {
	Results []map[string]any `json:"results"`
}

func (f *CryptoPanicFetcher) Fetch(ctx context.Context, topN int) ([]Headline, error) {
	u, err := url.Parse(f.baseURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("auth_token", f.apiKey)
	q.Set("filter", "hot")
	q.Set("public", "true")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cryptopanic GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("cryptopanic GET: status %d body=%q", resp.StatusCode, string(body))
	}
	var parsed cpResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("cryptopanic decode: %w", err)
	}
	out := make([]Headline, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		h := Headline{Raw: r}
		if id, ok := r["id"].(float64); ok {
			h.ExternalID = int64(id)
		}
		if t, ok := r["title"].(string); ok {
			h.Title = t
		}
		if uu, ok := r["url"].(string); ok {
			h.URL = uu
		}
		if src, ok := r["source"].(map[string]any); ok {
			if title, ok := src["title"].(string); ok {
				h.Source = title
			}
		}
		if pa, ok := r["published_at"].(string); ok {
			if ts, err := time.Parse(time.RFC3339, pa); err == nil {
				h.PublishedAt = ts.UTC()
			}
		}
		out = append(out, h)
		if len(out) >= topN {
			break
		}
	}
	return out, nil
}
