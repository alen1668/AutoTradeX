package eval

import (
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

func TestBroker_SubscribeReturnsDistinctIDs(t *testing.T) {
	b := NewBroker(zerolog.Nop())
	id1, ch1 := b.Subscribe()
	id2, ch2 := b.Subscribe()
	require.NotEqual(t, id1, id2)
	require.NotNil(t, ch1)
	require.NotNil(t, ch2)
}

func TestBroker_UnsubscribeRemovesSub(t *testing.T) {
	b := NewBroker(zerolog.Nop())
	id, _ := b.Subscribe()
	b.Unsubscribe(id)
	require.Equal(t, 0, b.subCount())
}

func TestBroker_UnsubscribeNonExistentIsNoOp(t *testing.T) {
	b := NewBroker(zerolog.Nop())
	b.Unsubscribe(9999) // must not panic
}

func TestBroker_ConcurrentSubscribeSafe(t *testing.T) {
	b := NewBroker(zerolog.Nop())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = b.Subscribe()
		}()
	}
	wg.Wait()
	require.Equal(t, 20, b.subCount())
}

func TestBroker_PublishFanOut(t *testing.T) {
	b := NewBroker(zerolog.Nop())
	_, ch1 := b.Subscribe()
	_, ch2 := b.Subscribe()

	evt := EvalEvent{Kind: "agent_score", OccurredAt: 1}
	b.Publish(evt)

	got1 := <-ch1
	got2 := <-ch2
	require.Equal(t, evt, got1)
	require.Equal(t, evt, got2)
}

func TestBroker_PublishToNoSubscribersNoOp(t *testing.T) {
	b := NewBroker(zerolog.Nop())
	b.Publish(EvalEvent{Kind: "agent_score"}) // must not panic / hang
}

func TestBroker_PublishNonBlockingOnFullChan(t *testing.T) {
	b := NewBroker(zerolog.Nop())
	_, _ = b.Subscribe() // capture chan but never drain

	// 100 publishes into a 10-buffer chan must NOT block; overflow is
	// dropped and counted toward the slow-client threshold.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			b.Publish(EvalEvent{Kind: "agent_score", OccurredAt: int64(i)})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked")
	}
}

func TestBroker_SlowClientDroppedAfter3ConsecutiveFails(t *testing.T) {
	b := NewBroker(zerolog.Nop())
	id, ch := b.Subscribe()

	// Fill buffer (10), then 3 more publishes → drops=3 → close + delete.
	for i := 0; i < 10; i++ {
		b.Publish(EvalEvent{OccurredAt: int64(i)})
	}
	// First overflow → drops=1 (NOT yet dropped)
	b.Publish(EvalEvent{OccurredAt: 100})
	require.Equal(t, 1, b.subCount())
	// Second overflow → drops=2
	b.Publish(EvalEvent{OccurredAt: 101})
	require.Equal(t, 1, b.subCount())
	// Third overflow → drops=3 → sub removed + chan closed
	b.Publish(EvalEvent{OccurredAt: 102})
	require.Equal(t, 0, b.subCount())

	// Drain remaining buffered + assert chan is closed.
	for range ch {
	}
	// Unsubscribe after-the-fact must be no-op.
	b.Unsubscribe(id)
}

func TestBroker_SuccessfulSendResetsDrops(t *testing.T) {
	b := NewBroker(zerolog.Nop())
	_, ch := b.Subscribe()

	// Fill buffer (10), then 2 overflows → drops=2
	for i := 0; i < 10; i++ {
		b.Publish(EvalEvent{OccurredAt: int64(i)})
	}
	b.Publish(EvalEvent{OccurredAt: 100})
	b.Publish(EvalEvent{OccurredAt: 101})

	// Drain everything → next send must succeed and reset drops to 0.
	for i := 0; i < 10; i++ {
		<-ch
	}
	b.Publish(EvalEvent{OccurredAt: 200}) // drops reset to 0
	require.Equal(t, 1, b.subCount())

	// Now we should be able to take 3 overflows again before getting dropped.
	// Drain the lone reset event so buffer is empty.
	<-ch
	for i := 0; i < 10; i++ {
		b.Publish(EvalEvent{OccurredAt: int64(300 + i)})
	}
	b.Publish(EvalEvent{OccurredAt: 400}) // overflow 1
	require.Equal(t, 1, b.subCount(), "drops reset means we should survive overflow 1 again")
}
