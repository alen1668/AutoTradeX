//go:build integration

package eval

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
	"github.com/lizhaojie/tvbot/internal/notify"
)

// scriptedLLM is a controllable scorer.LLMClient for worker tests.
type scriptedLLM struct {
	mu      sync.Mutex
	respond func(prompt string) (scorer.CompleteResponse, error)
	calls   int
}

func (f *scriptedLLM) Complete(_ context.Context, req scorer.CompleteRequest) (scorer.CompleteResponse, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.respond(req.Prompt)
}

// captureNotifier records every notify.Message sent.
type captureNotifier struct {
	mu       sync.Mutex
	messages []notify.Message
}

func (c *captureNotifier) Send(_ context.Context, m notify.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, m)
	return nil
}

func (c *captureNotifier) take() []notify.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]notify.Message{}, c.messages...)
	return out
}

func newTestWorker(t *testing.T, pool *pgxpool.Pool, llm scorer.LLMClient, notif notify.Notifier) *Worker {
	t.Helper()
	return &Worker{
		pool:      pool,
		store:     NewStore(pool),
		llm:       llm,
		model:     "claude-sonnet-4-6",
		timeoutMs: 5000,
		notif:     notif,
		log:       zerolog.Nop(),
		poll:      50 * time.Millisecond,
	}
}

func TestWorker_AbortSweepOnStart(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	// Two zombies pre-existing.
	for i := 0; i < 2; i++ {
		_, err := s.CreateRun(ctx, ReplayRun{
			SinceWindow: "7d", SinceCutoff: time.Now().Unix(),
			Model: "m", PromptText: "p", PromptSHA256: "h", Status: "running",
		})
		require.NoError(t, err)
	}

	w := newTestWorker(t, pool, &scriptedLLM{respond: func(string) (scorer.CompleteResponse, error) {
		return scorer.CompleteResponse{}, errors.New("should not be called")
	}}, &captureNotifier{})

	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		w.Run(wctx)
		close(done)
	}()
	// Give the sweep a moment then stop.
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	// Both runs should now be aborted.
	rows, err := pool.Query(ctx, `SELECT status FROM replay_runs`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var status string
		require.NoError(t, rows.Scan(&status))
		require.Equal(t, "aborted", status)
	}
}

func TestWorker_TickClaimsPending(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	id, _ := s.CreateRun(ctx, ReplayRun{
		SinceWindow: "7d", SinceCutoff: time.Now().Unix(),
		Model: "m", PromptText: "p", PromptSHA256: "h", Status: "pending",
	})

	w := newTestWorker(t, pool, &scriptedLLM{}, &captureNotifier{})
	w.tick(ctx)

	r, _ := s.GetRun(ctx, id)
	require.Equal(t, "running", r.Status)
}
