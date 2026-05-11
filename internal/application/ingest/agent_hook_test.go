package ingest

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/eval"
)

// recordingPublisher counts Publish calls + saves the last event for assertion.
type recordingPublisher struct {
	mu     sync.Mutex
	events []eval.EvalEvent
}

func (p *recordingPublisher) Publish(evt eval.EvalEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, evt)
}

func (p *recordingPublisher) Calls() []eval.EvalEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]eval.EvalEvent{}, p.events...)
}

func TestAgentHook_PublisherInjectionNilSafe(t *testing.T) {
	// Hook constructed without publisher (production default before Phase 3
	// wires it). publishScoreEvent must not panic.
	h := &AgentHook{}
	score := 75
	h.publishScoreEvent(123, "BTCUSDT", &score, "approve", 234)
	// No assertion needed: test passes if no panic.
}

func TestAgentHook_PublishCalledWithCorrectFields(t *testing.T) {
	p := &recordingPublisher{}
	h := &AgentHook{publisher: p}
	score := 42
	h.publishScoreEvent(7, "ETHUSDT", &score, "abandon", 1234)
	calls := p.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "agent_score", calls[0].Kind)
	require.Equal(t, int64(7), calls[0].SignalID)
	require.Equal(t, "ETHUSDT", calls[0].Symbol)
	require.NotNil(t, calls[0].AgentScore)
	require.Equal(t, 42, *calls[0].AgentScore)
	require.Equal(t, "abandon", calls[0].Decision)
	require.Equal(t, 1234, calls[0].LatencyMs)
	require.Greater(t, calls[0].OccurredAt, int64(0))
}
