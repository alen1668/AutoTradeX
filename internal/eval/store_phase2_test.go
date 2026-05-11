//go:build integration

package eval

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStore_ClaimNextPending_HappyPath(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	id, err := s.CreateRun(ctx, ReplayRun{
		SinceWindow: "7d", SinceCutoff: time.Now().Unix(),
		Model: "m", PromptText: "p", PromptSHA256: "h", Status: "pending",
	})
	require.NoError(t, err)

	run, ok, err := s.ClaimNextPending(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, id, run.ID)
	require.Equal(t, "running", run.Status)
	require.NotNil(t, run.StartedAt)

	// Empty queue: second call returns ok=false.
	_, ok, err = s.ClaimNextPending(ctx)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestStore_ClaimNextPending_ConcurrentSafe(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	// Single pending run; two goroutines race; exactly one wins.
	id, _ := s.CreateRun(ctx, ReplayRun{
		SinceWindow: "7d", SinceCutoff: time.Now().Unix(),
		Model: "m", PromptText: "p", PromptSHA256: "h", Status: "pending",
	})

	var wg sync.WaitGroup
	results := make([]bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			run, ok, err := s.ClaimNextPending(ctx)
			require.NoError(t, err)
			results[i] = ok
			if ok {
				require.Equal(t, id, run.ID)
			}
		}(i)
	}
	wg.Wait()

	wins := 0
	for _, r := range results {
		if r {
			wins++
		}
	}
	require.Equal(t, 1, wins, "exactly one goroutine should win")
}

func TestStore_AbortRunningRuns(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	// Seed: one running, one pending, one done.
	runIDs := make(map[string]int64)
	for _, status := range []string{"running", "pending", "done"} {
		id, err := s.CreateRun(ctx, ReplayRun{
			SinceWindow: "7d", SinceCutoff: time.Now().Unix(),
			Model: "m", PromptText: "p", PromptSHA256: "h", Status: status,
		})
		require.NoError(t, err)
		runIDs[status] = id
	}

	n, err := s.AbortRunningRuns(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	// running → aborted
	r, _ := s.GetRun(ctx, runIDs["running"])
	require.Equal(t, "aborted", r.Status)
	require.NotNil(t, r.FinishedAt)

	// pending and done untouched
	r, _ = s.GetRun(ctx, runIDs["pending"])
	require.Equal(t, "pending", r.Status)
	r, _ = s.GetRun(ctx, runIDs["done"])
	require.Equal(t, "done", r.Status)
}

func TestStore_UpdateProgress(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	id, _ := s.CreateRun(ctx, ReplayRun{
		SinceWindow: "7d", SinceCutoff: time.Now().Unix(),
		Model: "m", PromptText: "p", PromptSHA256: "h", Status: "running",
	})

	require.NoError(t, s.UpdateProgress(ctx, id, 5, 1))
	r, _ := s.GetRun(ctx, id)
	require.Equal(t, 5, r.SamplesDone)
	require.Equal(t, 1, r.SamplesFailed)

	// Subsequent update overwrites with later cumulative counts.
	require.NoError(t, s.UpdateProgress(ctx, id, 20, 3))
	r, _ = s.GetRun(ctx, id)
	require.Equal(t, 20, r.SamplesDone)
	require.Equal(t, 3, r.SamplesFailed)
}
