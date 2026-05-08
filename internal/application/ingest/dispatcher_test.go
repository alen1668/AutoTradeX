package ingest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/notify"
)

// fakeProcessor records call order with a simulated work delay.
type fakeProcessor struct {
	mu        sync.Mutex
	calls     []int64
	delay     time.Duration
	processed chan int64
}

func newFakeProcessor(delay time.Duration, capacity int) *fakeProcessor {
	return &fakeProcessor{
		delay:     delay,
		processed: make(chan int64, capacity),
	}
}

func (f *fakeProcessor) Process(_ context.Context, signalID int64) error {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	f.calls = append(f.calls, signalID)
	f.mu.Unlock()
	f.processed <- signalID
	return nil
}

func (f *fakeProcessor) seenInOrder() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int64, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestDispatcher_SameStrategyFIFO(t *testing.T) {
	fp := newFakeProcessor(50*time.Millisecond, 3)
	d := NewDispatcher(fp, notify.NoOp{}, zerolog.Nop())
	defer d.Shutdown(context.Background())

	d.Submit("ETH", 1)
	d.Submit("ETH", 2)
	d.Submit("ETH", 3)

	for i := 0; i < 3; i++ {
		select {
		case <-fp.processed:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for processing")
		}
	}
	assert.Equal(t, []int64{1, 2, 3}, fp.seenInOrder())
}

func TestDispatcher_DifferentStrategiesParallel(t *testing.T) {
	fp := newFakeProcessor(200*time.Millisecond, 4)
	d := NewDispatcher(fp, notify.NoOp{}, zerolog.Nop())
	defer d.Shutdown(context.Background())

	start := time.Now()
	d.Submit("ETH", 1)
	d.Submit("BTC", 2)
	d.Submit("SOL", 3)
	d.Submit("TRX", 4)

	for i := 0; i < 4; i++ {
		select {
		case <-fp.processed:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out")
		}
	}
	elapsed := time.Since(start)
	// 4 strategies × 200ms in parallel should land near 200ms, not 800ms.
	assert.Less(t, elapsed, 600*time.Millisecond,
		"expected parallel execution; got %v", elapsed)
}

func TestDispatcher_ShutdownDrainsInFlight(t *testing.T) {
	fp := newFakeProcessor(100*time.Millisecond, 4)
	d := NewDispatcher(fp, notify.NoOp{}, zerolog.Nop())

	d.Submit("ETH", 1)
	d.Submit("ETH", 2)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, d.Shutdown(ctx))

	assert.Equal(t, []int64{1, 2}, fp.seenInOrder(),
		"shutdown should drain the queue, not abort it")

	// Submit after shutdown runs synchronously and still completes.
	d.Submit("ETH", 3)
	select {
	case <-fp.processed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("post-shutdown submit should still process synchronously")
	}
}

// panicProcAdapter wraps a fakeProcessor and panics when signalID == 99.
type panicProcAdapter struct{ ok *fakeProcessor }

func (p panicProcAdapter) Process(ctx context.Context, id int64) error {
	if id == 99 {
		panic("boom")
	}
	return p.ok.Process(ctx, id)
}

func TestDispatcher_RecoversFromPanic(t *testing.T) {
	fp := newFakeProcessor(0, 2)
	d := NewDispatcher(panicProcAdapter{ok: fp}, notify.NoOp{}, zerolog.Nop())
	defer d.Shutdown(context.Background())

	d.Submit("ETH", 99) // panics
	d.Submit("ETH", 1)  // must still run after the panic
	select {
	case got := <-fp.processed:
		assert.Equal(t, int64(1), got)
	case <-time.After(2 * time.Second):
		t.Fatal("worker died after panic")
	}
}
