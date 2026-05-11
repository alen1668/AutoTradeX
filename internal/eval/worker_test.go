//go:build integration

package eval

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"text/template"
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

	w := newTestWorker(t, pool, &scriptedLLM{respond: func(string) (scorer.CompleteResponse, error) {
		return scorer.CompleteResponse{}, nil
	}}, &captureNotifier{})
	w.tick(ctx)

	r, _ := s.GetRun(ctx, id)
	// Worker has dispatched + completed (empty case set → status=done).
	require.Contains(t, []string{"running", "done"}, r.Status)
}

// seedReplayCase inserts a signals row + agent_evaluations row so
// LoadReplayCases returns one entry. Returns the new signal id.
func seedReplayCase(t *testing.T, ctx context.Context, pool *pgxpool.Pool, score int) int64 {
	t.Helper()
	var sigID int64
	require.NoError(t, pool.QueryRow(ctx, `
INSERT INTO signals (strategy_id, symbol, kind, signal_price, tv_timestamp_ms,
                     raw_payload, decision, trace_id, agent_score, received_at)
VALUES ('s1', 'BTCUSDT', 'long', 50000, $1, '{}'::jsonb, 'accepted', $2, $3, now())
RETURNING id`,
		time.Now().UnixMilli(),
		"tx"+time.Now().Format("150405.000000000"),
		score,
	).Scan(&sigID))

	// history_json must be a valid historySnapshot or ReplayOne fills row.Error.
	const histJSON = `{"symbol_history":[],"strategy_history":[],"portfolio":null,"market":null,"high_vol_windows":[]}`
	_, err := pool.Exec(ctx, `
INSERT INTO agent_evaluations (signal_id, model, prompt_hash, score, decision,
                                reasoning, history_json, prompt_text,
                                latency_ms, token_in, token_out)
VALUES ($1, 'm', 'h', $2, 'approve', 'r', $3::jsonb, 'p', 100, 100, 100)`,
		sigID, score, histJSON)
	require.NoError(t, err)
	return sigID
}

func TestWorker_Execute_DispatchesAndAggregates(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	// Seed 3 replay cases.
	for i := 0; i < 3; i++ {
		seedReplayCase(t, ctx, pool, 50+i)
	}

	// Prompt template renders to "ok" — content irrelevant; we mock the LLM.
	tmpl := template.Must(template.New("p").Parse("ok"))

	var llmCalls atomic.Int32
	respond := func(string) (scorer.CompleteResponse, error) {
		llmCalls.Add(1)
		return scorer.CompleteResponse{
			Text:    `{"score": 70, "decision": "approve", "reasoning": "fine"}`,
			TokenIn: 100, TokenOut: 20,
		}, nil
	}
	w := newTestWorker(t, pool, &scriptedLLM{respond: respond}, &captureNotifier{})

	// Hand-craft a run as if it had been claimed.
	cutoff, _ := ParseSince("1h")
	id, _ := s.CreateRun(ctx, ReplayRun{
		SinceWindow: "1h", SinceCutoff: cutoff.Unix(),
		MaxN: 10, Concurrency: 2, Model: "claude-sonnet-4-6",
		PromptText: "ok", PromptSHA256: "h", Status: "pending",
	})

	// Call ClaimNextPending so status flips + StartedAt set.
	run, ok, err := s.ClaimNextPending(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, id, run.ID)

	w.executeWithTemplate(ctx, run, tmpl)

	require.Equal(t, int32(3), llmCalls.Load(), "expected one LLM call per case")
	r, _ := s.GetRun(ctx, id)
	require.Equal(t, "done", r.Status)
	require.Equal(t, 3, r.SamplesTotal)
	require.Equal(t, 3, r.SamplesDone)
	require.Equal(t, 0, r.SamplesFailed)
	require.NotNil(t, r.Summary)
	require.Equal(t, 3, r.Summary.SampleSize)
}

func TestWorker_Execute_BadTemplateMarksFailed(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	notif := &captureNotifier{}
	w := newTestWorker(t, pool, &scriptedLLM{respond: func(string) (scorer.CompleteResponse, error) {
		t.Fatal("LLM must not be called on a bad-template run")
		return scorer.CompleteResponse{}, nil
	}}, notif)

	cutoff, _ := ParseSince("1h")
	id, _ := s.CreateRun(ctx, ReplayRun{
		SinceWindow: "1h", SinceCutoff: cutoff.Unix(),
		MaxN: 10, Concurrency: 1, Model: "claude-sonnet-4-6",
		PromptText: "{{ .Bogus ", PromptSHA256: "h", Status: "pending",
	})
	run, _, _ := s.ClaimNextPending(ctx)
	require.Equal(t, id, run.ID)

	w.execute(ctx, run)

	r, _ := s.GetRun(ctx, id)
	require.Equal(t, "failed", r.Status)
	require.NotNil(t, r.ErrorMessage)
	require.Contains(t, *r.ErrorMessage, "parse prompt")
	require.Equal(t, 1, len(notif.take()))
}

func TestWorker_Execute_AllFailedSendsCritical(t *testing.T) {
	pool := newTestPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	seedReplayCase(t, ctx, pool, 50)

	notif := &captureNotifier{}
	// LLM always returns invalid JSON → every ReplayOne sets row.Error.
	w := newTestWorker(t, pool, &scriptedLLM{respond: func(string) (scorer.CompleteResponse, error) {
		return scorer.CompleteResponse{Text: "not json"}, nil
	}}, notif)

	cutoff, _ := ParseSince("1h")
	id, _ := s.CreateRun(ctx, ReplayRun{
		SinceWindow: "1h", SinceCutoff: cutoff.Unix(),
		MaxN: 10, Concurrency: 1, Model: "claude-sonnet-4-6",
		PromptText: "ok", PromptSHA256: "h", Status: "pending",
	})
	run, _, _ := s.ClaimNextPending(ctx)
	require.Equal(t, id, run.ID)

	tmpl := template.Must(template.New("p").Parse("ok"))
	w.executeWithTemplate(ctx, run, tmpl)

	r, _ := s.GetRun(ctx, id)
	require.Equal(t, "done", r.Status)
	require.Equal(t, 1, r.SamplesFailed)
	require.Equal(t, 1, r.SamplesTotal)

	msgs := notif.take()
	require.Len(t, msgs, 1)
	require.Equal(t, notify.SeverityCritical, msgs[0].Severity)
	require.Contains(t, msgs[0].Body, "all 1 samples failed")
}
