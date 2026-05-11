package eval

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"text/template"
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

// execute runs the full replay pipeline for one claimed run. Recovers from
// panics, parses the prompt template, loads cases, dispatches ReplayOne
// concurrently with progress writeback, aggregates, and marks done/failed.
func (w *Worker) execute(ctx context.Context, run ReplayRun) {
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("worker panic: %v", r)
			w.log.Error().Str("err", msg).Int64("run_id", run.ID).Msg("worker: panic")
			_ = w.store.MarkRunFailed(ctx, run.ID, msg)
			w.sendCritical(ctx, run.ID, msg)
		}
	}()

	tmpl, err := template.New("p").Parse(run.PromptText)
	if err != nil {
		msg := "parse prompt: " + err.Error()
		_ = w.store.MarkRunFailed(ctx, run.ID, msg)
		w.sendCritical(ctx, run.ID, msg)
		return
	}
	w.executeWithTemplate(ctx, run, tmpl)
}

// executeWithTemplate is the testable core of execute: prompt template is
// already parsed. Production caller is execute(); tests call this directly
// with a hand-crafted template.
func (w *Worker) executeWithTemplate(ctx context.Context, run ReplayRun, tmpl *template.Template) {
	cutoff := time.Unix(run.SinceCutoff, 0)
	cases, err := LoadReplayCases(ctx, w.pool, cutoff, run.MaxN)
	if err != nil {
		msg := "load cases: " + err.Error()
		_ = w.store.MarkRunFailed(ctx, run.ID, msg)
		w.sendCritical(ctx, run.ID, msg)
		return
	}
	if err := w.store.MarkRunRunning(ctx, run.ID, len(cases)); err != nil {
		w.log.Warn().Err(err).Msg("worker: mark running")
	}

	concurrency := run.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 10 {
		concurrency = 10
	}

	rows := make([]ReplayRow, len(cases))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var done, failed atomic.Int32

	for i, c := range cases {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c ReplayCase) {
			defer wg.Done()
			defer func() { <-sem }()
			row := ReplayOne(ctx, c, tmpl, w.llm, run.Model, w.timeoutMs)
			rows[i] = row
			if err := w.store.InsertRow(ctx, run.ID, row); err != nil {
				w.log.Warn().Err(err).Int64("signal_id", c.SignalID).Msg("worker: insert row")
			}
			if row.Error != "" {
				failed.Add(1)
			}
			d := done.Add(1)
			if err := w.store.UpdateProgress(ctx, run.ID, int(d), int(failed.Load())); err != nil {
				w.log.Warn().Err(err).Msg("worker: update progress")
			}
		}(i, c)
	}
	wg.Wait()

	// Aggregate report exactly like RunReplay does (kept in lockstep so the
	// summary JSON shape matches what cmd/agent-eval --replay writes).
	withPnL := 0
	for _, r := range rows {
		if r.Error == "" && r.HasPnL {
			withPnL++
		}
	}
	v1s, v1p := ExtractScoresAndPnLs(rows, func(r ReplayRow) int { return r.OldScore })
	v2s, v2p := ExtractScoresAndPnLs(rows, func(r ReplayRow) int { return r.NewScore })
	rep := ReplayReport{
		Since:      run.SinceWindow,
		PromptFile: derefOr(run.PromptName, ""),
		SampleSize: len(rows),
		WithPnL:    withPnL,
		V1Spearman: Spearman(v1s, v1p),
		V2Spearman: Spearman(v2s, v2p),
		V1Buckets:  Bucketize(rows, func(r ReplayRow) int { return r.OldScore }),
		V2Buckets:  Bucketize(rows, func(r ReplayRow) int { return r.NewScore }),
		Flips:      FlipMatrixOf(rows),
		Rows:       append([]ReplayRow{}, rows...),
	}
	SortByDeltaScoreDesc(rep.Rows)

	totalFailed := int(failed.Load())
	totalDone := int(done.Load()) - totalFailed
	if err := w.store.MarkRunDone(ctx, run.ID, &rep, totalDone, totalFailed); err != nil {
		w.log.Error().Err(err).Msg("worker: mark done")
		_ = w.store.MarkRunFailed(ctx, run.ID, "mark done: "+err.Error())
		w.sendCritical(ctx, run.ID, "mark done: "+err.Error())
		return
	}

	// Critical alert if 100% failed on a non-empty run.
	if len(cases) > 0 && totalFailed == len(cases) {
		w.sendCritical(ctx, run.ID,
			fmt.Sprintf("all %d samples failed (model=%s)", len(cases), run.Model))
	}
}

// derefOr returns *p or `def` if p is nil. Small helper to avoid a nil
// dereference panic when run.PromptName is unset.
func derefOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

// sendCritical wraps notif.Send with a 10-minute throttle: at most one
// "replay critical" message is dispatched in any rolling 10-minute window.
// Matches spec 1's alert-throttling pattern (warn-frequent vs critical-rare).
func (w *Worker) sendCritical(ctx context.Context, runID int64, msg string) {
	if w.notif == nil {
		return
	}
	if !w.lastCriticalAt.IsZero() && time.Since(w.lastCriticalAt) < 10*time.Minute {
		w.log.Debug().Int64("run_id", runID).Msg("worker: critical alert throttled")
		return
	}
	w.lastCriticalAt = time.Now()
	if err := w.notif.Send(ctx, notify.Message{
		Title:    fmt.Sprintf("Replay run #%d failed", runID),
		Body:     msg,
		Severity: notify.SeverityCritical,
		Fields:   map[string]any{"run_id": runID},
	}); err != nil {
		w.log.Warn().Err(err).Msg("worker: feishu send failed")
	}
}
