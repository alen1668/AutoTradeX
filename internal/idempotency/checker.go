package idempotency

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

// SignalLookup is the subset of SignalRepo we need.
type SignalLookup interface {
	ExistsByKey(ctx context.Context, q store.Querier, strategyID string, tvTimestampMs int64) (bool, error)
}

// Checker is a 2-layer idempotency check: LRU first (fast), DB second (truth).
//
// Pass repo=nil to use LRU-only mode (e.g., in unit tests or dry_run scenarios
// where no DB is wired). In production, always pass a real SignalRepo wired
// to a *pgxpool.Pool.
type Checker struct {
	lru  *lruCache
	repo SignalLookup
	pool *pgxpool.Pool
}

func NewChecker(lruSize int, repo SignalLookup) *Checker {
	return &Checker{lru: newLRU(lruSize), repo: repo}
}

// WithPool sets the pool used for repo lookups. Optional for LRU-only mode.
func (c *Checker) WithPool(p *pgxpool.Pool) *Checker {
	c.pool = p
	return c
}

// Check returns true if (strategyID, tvTimestampMs) has been seen before.
// Always adds to the LRU on the first sighting.
func (c *Checker) Check(ctx context.Context, strategyID string, tvTimestampMs int64) (bool, error) {
	if c.lru.SeenOrAdd(strategyID, tvTimestampMs) {
		return true, nil
	}
	if c.repo == nil || c.pool == nil {
		return false, nil
	}
	exists, err := c.repo.ExistsByKey(ctx, c.pool, strategyID, tvTimestampMs)
	if err != nil {
		return false, err
	}
	return exists, nil
}
