package calendar

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

// StoreAdapter bridges store.EconomicEventsRepo into the calendar package
// API surface, hiding the pgxpool dependency from worker / lookup callers.
type StoreAdapter struct {
	repo *store.EconomicEventsRepo
	pool *pgxpool.Pool
}

func NewStoreAdapter(repo *store.EconomicEventsRepo, pool *pgxpool.Pool) *StoreAdapter {
	return &StoreAdapter{repo: repo, pool: pool}
}

func (s *StoreAdapter) SaveBatch(ctx context.Context, events []Event) error {
	for _, ev := range events {
		rec := store.EconomicEventRecord{
			SourceID: ev.SourceID,
			Name:     ev.Name,
			Currency: ev.Currency,
			Impact:   ev.Impact,
			StartsAt: ev.StartsAt,
		}
		if err := s.repo.Upsert(ctx, s.pool, rec); err != nil {
			return err
		}
	}
	return nil
}

func (s *StoreAdapter) ActiveBetween(ctx context.Context, from, to time.Time) ([]Event, error) {
	recs, err := s.repo.ActiveBetween(ctx, s.pool, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(recs))
	for _, r := range recs {
		out = append(out, Event{
			SourceID: r.SourceID, Name: r.Name, Currency: r.Currency,
			Impact: r.Impact, StartsAt: r.StartsAt,
		})
	}
	return out, nil
}
