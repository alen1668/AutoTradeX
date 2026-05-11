// Package calendar fetches the economic calendar (Forex Factory weekly XML)
// and exposes a query API for the macrocontext reader.
package calendar

import "time"

// Event is one calendar entry, normalized to UTC.
type Event struct {
	SourceID string
	Name     string
	Currency string
	Impact   string
	StartsAt time.Time
}
