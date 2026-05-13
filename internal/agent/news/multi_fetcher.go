package news

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
)

// MultiFetcher 并发调多个子 Fetcher,合并 + 去重(URL) + 按 PublishedAt 倒序 + 截 topN。
// 单个子源失败仅 log warn 不中断;所有子源都失败才返回 error。
type MultiFetcher struct {
	fetchers []Fetcher
	log      zerolog.Logger
}

func NewMultiFetcher(fetchers []Fetcher, log zerolog.Logger) *MultiFetcher {
	return &MultiFetcher{fetchers: fetchers, log: log}
}

func (m *MultiFetcher) Fetch(ctx context.Context, topN int) ([]Headline, error) {
	if topN <= 0 {
		topN = 12
	}
	perSource := topN*2/len(m.fetchers) + 1
	if perSource < 5 {
		perSource = 5
	}

	type result struct {
		headlines []Headline
		err       error
	}
	results := make([]result, len(m.fetchers))

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for i, f := range m.fetchers {
		i, f := i, f
		g.Go(func() error {
			hs, err := f.Fetch(gctx, perSource)
			mu.Lock()
			results[i] = result{headlines: hs, err: err}
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	merged := make([]Headline, 0)
	failed := 0
	for _, r := range results {
		if r.err != nil {
			m.log.Warn().Err(r.err).Msg("news sub-source failed")
			failed++
			continue
		}
		merged = append(merged, r.headlines...)
	}
	if failed == len(m.fetchers) {
		return nil, fmt.Errorf("all %d news sources failed", failed)
	}

	seen := make(map[string]struct{}, len(merged))
	dedup := make([]Headline, 0, len(merged))
	for _, h := range merged {
		key := h.URL
		if key == "" {
			key = h.Title
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dedup = append(dedup, h)
	}

	sort.SliceStable(dedup, func(i, j int) bool {
		return dedup[i].PublishedAt.After(dedup[j].PublishedAt)
	})

	if len(dedup) > topN {
		dedup = dedup[:topN]
	}
	return dedup, nil
}
