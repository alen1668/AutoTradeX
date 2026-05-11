package eval

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
	"github.com/lizhaojie/tvbot/internal/notify"
)

// Worker is the singleton goroutine that drives web-submitted replay runs.
// It polls replay_runs for pending rows every `poll` interval, claims one
// atomically, and dispatches ReplayOne over the loaded cases. Cmd-mode
// (cmd/agent-eval --replay) creates runs with status='running' directly so
// it never competes with this worker.
type Worker struct {
	pool      *pgxpool.Pool
	store     *Store
	llm       scorer.LLMClient
	model     string
	timeoutMs int
	notif     notify.Notifier
	log       zerolog.Logger
	poll      time.Duration

	// lastCriticalAt throttles "replay critical" feishu pings to one per 10m.
	lastCriticalAt time.Time
}

// NewWorker constructs a Worker. Caller wires in the production LLM client
// (eval.MakeLLMClient) and notify.Notifier from cmd/tvbot/main.go.
func NewWorker(pool *pgxpool.Pool, llm scorer.LLMClient, model string, timeoutMs int, notif notify.Notifier, log zerolog.Logger) *Worker {
	return &Worker{
		pool:      pool,
		store:     NewStore(pool),
		llm:       llm,
		model:     model,
		timeoutMs: timeoutMs,
		notif:     notif,
		log:       log,
		poll:      time.Second,
	}
}

// Run blocks until ctx is canceled. Performs a one-time AbortRunningRuns
// sweep before entering the poll loop.
func (w *Worker) Run(ctx context.Context) {
	if n, err := w.store.AbortRunningRuns(ctx); err != nil {
		w.log.Warn().Err(err).Msg("worker: startup abort sweep failed")
	} else if n > 0 {
		w.log.Info().Int64("aborted", n).Msg("worker: swept running zombies on start")
	}

	t := time.NewTicker(w.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *Worker) tick(ctx context.Context) {
	run, ok, err := w.store.ClaimNextPending(ctx)
	if err != nil {
		w.log.Warn().Err(err).Msg("worker: claim pending failed")
		return
	}
	if !ok {
		return
	}
	w.execute(ctx, run)
}

// execute is a stub at this stage — Task 4 fills it in. Returning early
// here lets Task 3's tests pass without needing the full pipeline.
func (w *Worker) execute(_ context.Context, _ ReplayRun) {
	// Filled in by Task 4
}
