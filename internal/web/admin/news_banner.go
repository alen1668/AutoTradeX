package admin

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

// NewsBannerData is the shape consumed by the news_banner partial.
// Empty / nil values render no banner.
type NewsBannerData struct {
	ID            int64
	Impact        string // high | medium | low | none (raw enum from DB)
	ImpactLabel   string // 中文 label for the badge
	Summary       string
	MeasuredAtUnix int64
}

// NewsBannerProvider reads the latest news_snapshots row with a 30s
// in-memory cache so every admin page render doesn't hit the DB. News
// itself updates every 15 minutes — 30s staleness is fine.
type NewsBannerProvider struct {
	repo *store.NewsSnapshotsRepo
	pool *pgxpool.Pool

	mu        sync.Mutex
	cached    *NewsBannerData
	expiresAt time.Time
}

func NewNewsBannerProvider(repo *store.NewsSnapshotsRepo, pool *pgxpool.Pool) *NewsBannerProvider {
	return &NewsBannerProvider{repo: repo, pool: pool}
}

const newsBannerCacheTTL = 30 * time.Second

// Latest returns the latest news banner. Returns nil when:
//   - news_snapshots table is empty
//   - the latest row has status='failed' / impact='none' (we don't surface
//     placeholder failure rows to operators)
//   - any DB error (logged at caller; banner just doesn't show)
func (p *NewsBannerProvider) Latest(ctx context.Context) *NewsBannerData {
	p.mu.Lock()
	defer p.mu.Unlock()

	if time.Now().Before(p.expiresAt) {
		return p.cached
	}

	rec, err := p.repo.Latest(ctx, p.pool)
	p.expiresAt = time.Now().Add(newsBannerCacheTTL)

	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			// Real error: clear cache and let next call retry.
			p.cached = nil
		} else {
			p.cached = nil
		}
		return p.cached
	}
	if rec == nil || rec.Impact == "" || rec.Impact == "none" || rec.ErrorMessage != nil {
		// Hide failed / placeholder rows so we don't show "无重要新闻" loudly.
		p.cached = nil
		return nil
	}

	p.cached = &NewsBannerData{
		ID:             rec.ID,
		Impact:         rec.Impact,
		ImpactLabel:    impactLabelCN(rec.Impact),
		Summary:        rec.Summary,
		MeasuredAtUnix: rec.MeasuredAt.Unix(),
	}
	return p.cached
}

func impactLabelCN(impact string) string {
	switch impact {
	case "high":
		return "高影响"
	case "medium":
		return "中影响"
	case "low":
		return "低影响"
	}
	return impact
}
