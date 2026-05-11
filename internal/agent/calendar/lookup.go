package calendar

import (
	"context"
	"time"
)

// LookupSource is the minimum surface ActiveEventsAt needs. StoreAdapter
// implements it.
type LookupSource interface {
	ActiveBetween(ctx context.Context, from, to time.Time) ([]Event, error)
}

// ActiveEventsAt returns events whose starts_at is within ±1h of t.
func ActiveEventsAt(ctx context.Context, src LookupSource, t time.Time) ([]Event, error) {
	return src.ActiveBetween(ctx, t.Add(-time.Hour), t.Add(time.Hour))
}
