package eval

import (
	"sync"
	"testing"

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
