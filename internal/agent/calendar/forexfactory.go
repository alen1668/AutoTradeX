package calendar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultForexFactoryURL is the community-maintained mirror; the official
// weekly XML at forexfactory.com has the same schema.
const DefaultForexFactoryURL = "https://nfs.faireconomy.media/ff_calendar_thisweek.xml"

// defaultCurrencies: USD (most relevant for BTC) + ALL (global events).
var defaultCurrencies = map[string]struct{}{"USD": {}, "ALL": {}}

type ForexFactoryFetcher struct {
	url        string
	client     *http.Client
	currencies map[string]struct{}
}

func NewForexFactoryFetcher(url string) *ForexFactoryFetcher {
	return &ForexFactoryFetcher{
		url:        url,
		client:     &http.Client{Timeout: 30 * time.Second},
		currencies: defaultCurrencies,
	}
}

func (f *ForexFactoryFetcher) WithHTTPClient(c *http.Client) *ForexFactoryFetcher {
	f.client = c
	return f
}

type xmlEvents struct {
	XMLName xml.Name   `xml:"weeklyevents"`
	Events  []xmlEvent `xml:"event"`
}
type xmlEvent struct {
	Title   string `xml:"title"`
	Country string `xml:"country"`
	Date    string `xml:"date"`
	Time    string `xml:"time"`
	Impact  string `xml:"impact"`
}

func (f *ForexFactoryFetcher) Fetch(ctx context.Context) ([]Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ff GET: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ff GET: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var parsed xmlEvents
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("ff parse: %w", err)
	}
	et, err := time.LoadLocation("America/New_York")
	if err != nil {
		return nil, fmt.Errorf("load ET location: %w", err)
	}
	var out []Event
	for _, e := range parsed.Events {
		if !strings.EqualFold(e.Impact, "high") {
			continue
		}
		cur := strings.ToUpper(e.Country)
		if _, ok := f.currencies[cur]; !ok {
			continue
		}
		ts, err := parseFFDateTime(e.Date, e.Time, et)
		if err != nil {
			continue
		}
		out = append(out, Event{
			SourceID: makeSourceID(e.Title, ts),
			Name:     e.Title,
			Currency: cur,
			Impact:   "High",
			StartsAt: ts,
		})
	}
	return out, nil
}

// parseFFDateTime parses Forex Factory's MM-DD-YYYY + h:mmAM/PM in ET, returns UTC.
func parseFFDateTime(date, t string, loc *time.Location) (time.Time, error) {
	layout := "01-02-2006 3:04pm"
	composed := strings.TrimSpace(date) + " " + strings.ToLower(strings.TrimSpace(t))
	parsed, err := time.ParseInLocation(layout, composed, loc)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func makeSourceID(title string, ts time.Time) string {
	h := sha256.Sum256([]byte(title + "|" + ts.Format(time.RFC3339)))
	return "ff:" + hex.EncodeToString(h[:8])
}
