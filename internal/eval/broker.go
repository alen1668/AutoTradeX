package eval

import (
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
)

// EvalEvent is the payload pushed to SSE clients. Json fields are stable
// public contract for the front-end eval-stream.js.
type EvalEvent struct {
	Kind       string   `json:"kind"` // "agent_score" | "trade_closed"
	SignalID   int64    `json:"signal_id,omitempty"`
	Symbol     string   `json:"symbol,omitempty"`
	AgentScore *int     `json:"agent_score,omitempty"`
	Decision   string   `json:"decision,omitempty"` // approve | abandon | failed
	LatencyMs  int      `json:"latency_ms,omitempty"`
	PnLUSDC    *float64 `json:"pnl_usdc,omitempty"` // trade_closed only
	OccurredAt int64    `json:"occurred_at"`        // unix epoch seconds
}

// Publisher is the fire-and-forget interface ingest/trade/reconcile depend
// on. Concrete impl is *Broker. nil is safe — callers check.
type Publisher interface {
	Publish(evt EvalEvent)
}

// Broker fan-outs EvalEvents to all active SSE subscribers. Construction
// is cheap; one shared instance lives in cmd/tvbot/main.go for the process
// lifetime. Safe for concurrent Subscribe/Unsubscribe/Publish.
type Broker struct {
	log    zerolog.Logger
	mu     sync.RWMutex
	subs   map[int64]*subscriber
	nextID atomic.Int64
}

type subscriber struct {
	id    int64
	ch    chan EvalEvent
	drops atomic.Int32
}

// NewBroker constructs a Broker. log is used for slow-client warnings.
func NewBroker(log zerolog.Logger) *Broker {
	return &Broker{log: log, subs: map[int64]*subscriber{}}
}

// Subscribe registers a new client. Returned channel is buffered 10; the
// caller must keep draining and call Unsubscribe when done.
func (b *Broker) Subscribe() (int64, <-chan EvalEvent) {
	id := b.nextID.Add(1)
	s := &subscriber{id: id, ch: make(chan EvalEvent, 10)}
	b.mu.Lock()
	b.subs[id] = s
	b.mu.Unlock()
	return id, s.ch
}

// Unsubscribe removes the sub and closes its channel (if not already
// closed by slow-drain detection). Idempotent — safe to call twice.
func (b *Broker) Unsubscribe(id int64) {
	b.mu.Lock()
	s, ok := b.subs[id]
	if ok {
		delete(b.subs, id)
	}
	b.mu.Unlock()
	if ok {
		// Recover in case the chan was already closed by Publish's drop logic.
		defer func() { _ = recover() }()
		close(s.ch)
	}
}

// subCount is a test helper for asserting how many subs are currently
// registered. Not part of the public API.
func (b *Broker) subCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
