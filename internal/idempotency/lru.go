package idempotency

import (
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
)

type key struct {
	strategyID    string
	tvTimestampMs int64
}

type lruCache struct {
	mu    sync.Mutex
	cache *lru.Cache[key, struct{}]
}

func newLRU(size int) *lruCache {
	c, _ := lru.New[key, struct{}](size)
	return &lruCache{cache: c}
}

// SeenOrAdd returns true if the key was already present; false (and adds) otherwise.
func (l *lruCache) SeenOrAdd(strategyID string, tvTimestampMs int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := key{strategyID, tvTimestampMs}
	if _, ok := l.cache.Get(k); ok {
		return true
	}
	l.cache.Add(k, struct{}{})
	return false
}
