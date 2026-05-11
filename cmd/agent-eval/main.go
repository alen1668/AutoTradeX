// cmd/agent-eval is the operator entry point for the agent-eval flows.
// Heavy lifting lives in internal/eval; this binary is a thin shell:
//
//   - go run ./cmd/agent-eval --since=3d
//     print grayscale eval report to stdout
//
//   - go run ./cmd/agent-eval --since=24h --report=/tmp/eval.html
//     also write a legacy HTML mirror (kept for 1 month; the /eval
//     dashboard is the long-term home)
//
//   - go run ./cmd/agent-eval --replay --prompt-file=./prompts/v2.tmpl --since=7d
//     persist replay results to replay_runs / replay_run_rows tables
//     and print run_id=N to stdout so cron scripts can capture it
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/eval"
)

func main() {
	var (
		since         string
		reportPath    string
		replay        bool
		promptFile    string
		jsonPath      string
		maxN          int
		concurrency   int
		modelOverride string
	)
	flag.StringVar(&since, "since", "3d", "lookback window: 1h / 24h / 3d / 7d")
	flag.StringVar(&reportPath, "report", "", "if set, write an HTML copy of the report to this path (legacy; prefer /eval dashboard)")
	flag.BoolVar(&replay, "replay", false, "switch to replay mode (re-run external prompt over historical signals)")
	flag.StringVar(&promptFile, "prompt-file", "", "[replay] external prompt template file")
	flag.StringVar(&jsonPath, "json", "", "[replay] write machine-readable JSON to this path")
	flag.IntVar(&maxN, "max", 0, "[replay] cap on signals to replay (0 = unlimited)")
	flag.IntVar(&concurrency, "concurrency", 5, "[replay] concurrent LLM calls (clamped to [1,10])")
	flag.StringVar(&modelOverride, "model", "", "[replay] override LLM model (default: same as production)")
	flag.Parse()

	ctx := context.Background()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://tvbot:tvbot@localhost:5432/tvbot?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect: %v", err)
	}
	defer pool.Close()

	// Zombie warning: replay runs stuck in 'running' state for >1h are
	// suspicious (process probably crashed mid-run; Phase 1 has no
	// auto-recovery). Surface them so the operator notices.
	store := eval.NewStore(pool)
	if stale, err := store.ListStaleRunning(ctx, time.Hour); err == nil && len(stale) > 0 {
		fmt.Fprintf(os.Stderr, "warning: %d replay_run(s) stuck in 'running' >1h:\n", len(stale))
		for _, r := range stale {
			fmt.Fprintf(os.Stderr, "  #%d window=%s model=%s\n", r.ID, r.SinceWindow, r.Model)
		}
		fmt.Fprintln(os.Stderr, "  → consider: UPDATE replay_runs SET status='aborted' WHERE id IN (...)")
	}

	if replay {
		runReplayPersisted(ctx, pool, store, since, promptFile, maxN, concurrency,
			reportPath, jsonPath, modelOverride)
		return
	}
	runEvalStdout(ctx, pool, since, reportPath)
}

// runEvalStdout prints the grayscale-period report to stdout. When
// --report is set, also writes a legacy HTML mirror to that path.
func runEvalStdout(ctx context.Context, pool *pgxpool.Pool, since, reportPath string) {
	rep, err := eval.LoadEvalReport(ctx, pool, since)
	if err != nil {
		fail("load eval: %v", err)
	}
	fmt.Printf("eval since=%s   signals=%d trades=%d\n", rep.Since, rep.TotalSignals, rep.TotalTrades)
	for _, b := range rep.Buckets {
		fmt.Printf("  %-7s  n=%-4d trades=%-4d wins=%-4d sum_pnl=%.2f  avg_pnl=%s  win_rate=%s\n",
			b.Label, b.Signals, b.Trades, b.Wins, b.SumPnL,
			fmtFloat(b.AvgPnL, "%.2f"), fmtFloat(b.WinRate, "%.1f%%"))
	}
	fmt.Printf("spearman: %s\n", fmtFloat(rep.Spearman, "%.4f"))
	fmt.Printf("llm: total=%d failed=%d (%.1f%%) avg=%dms p95=%dms\n",
		rep.LLMHealth.TotalCalls, rep.LLMHealth.FailedCalls,
		rep.LLMHealth.FailureRate, rep.LLMHealth.AvgLatencyMs, rep.LLMHealth.P95LatencyMs)

	if reportPath != "" {
		f, err := os.Create(reportPath)
		if err != nil {
			fail("create report: %v", err)
		}
		defer f.Close()
		writeEvalHTMLFile(f, rep)
		fmt.Fprintf(os.Stderr, "(legacy) HTML report written to %s\n", reportPath)
	}
}

// fmtFloat returns "—" for NaN; otherwise formats with layout.
func fmtFloat(v float64, layout string) string {
	if v != v { // NaN
		return "—"
	}
	return fmt.Sprintf(layout, v)
}

// writeEvalHTMLFile is a minimal self-contained HTML mirror for the legacy
// --report=*.html path. The /eval dashboard is the long-term replacement.
func writeEvalHTMLFile(w io.Writer, rep eval.EvalReport) {
	fmt.Fprintf(w, `<!doctype html><html><body><h1>Eval since=%s</h1>`, rep.Since)
	fmt.Fprintf(w, `<p>signals=%d trades=%d spearman=%s</p><table border=1>`,
		rep.TotalSignals, rep.TotalTrades, fmtFloat(rep.Spearman, "%.4f"))
	fmt.Fprintf(w, `<tr><th>Bucket</th><th>Signals</th><th>Trades</th><th>Wins</th><th>SumPnL</th><th>AvgPnL</th><th>WinRate</th></tr>`)
	for _, b := range rep.Buckets {
		fmt.Fprintf(w, `<tr><td>%s</td><td>%d</td><td>%d</td><td>%d</td><td>%.2f</td><td>%s</td><td>%s</td></tr>`,
			b.Label, b.Signals, b.Trades, b.Wins, b.SumPnL,
			fmtFloat(b.AvgPnL, "%.2f"), fmtFloat(b.WinRate, "%.1f%%"))
	}
	fmt.Fprintf(w, `</table></body></html>`)
}

// runReplayPersisted runs the full Phase 1 replay flow:
// 1) create pending run row; print run_id=N to stdout
// 2) load cases; mark running with samples_total
// 3) dispatch RunReplay; insert rows; mark done with summary_json
// Any failure short-circuits with MarkRunFailed + os.Exit(1).
func runReplayPersisted(
	ctx context.Context,
	pool *pgxpool.Pool,
	store *eval.Store,
	since, promptFile string,
	maxN, concurrency int,
	reportPath, jsonPath, modelOverride string,
) {
	if promptFile == "" {
		fail("replay: --prompt-file is required")
	}
	cutoff, ok := eval.ParseSince(since)
	if !ok {
		fail("replay: --since=%q not in %v", since, eval.AllowedSinces)
	}

	tmplBytes, err := os.ReadFile(promptFile)
	if err != nil {
		fail("read prompt: %v", err)
	}
	tmpl, err := template.New("p").Parse(string(tmplBytes))
	if err != nil {
		fail("parse prompt: %v", err)
	}

	apiKey, model, baseURL, timeoutMs := eval.LoadLLMConfig(ctx, pool)
	if env := os.Getenv("LLM_API_KEY"); env != "" {
		apiKey = env
	}
	if modelOverride != "" {
		model = modelOverride
	}
	if apiKey == "" {
		fail("no LLM API key (set LLM_API_KEY env or system_state.llm_api_key)")
	}

	sha := sha256.Sum256(tmplBytes)
	promptSHA := hex.EncodeToString(sha[:])
	promptName := filepath.Base(promptFile)

	runID, err := store.CreateRun(ctx, eval.ReplayRun{
		SinceWindow:  since,
		SinceCutoff:  cutoff.Unix(),
		MaxN:         maxN,
		Concurrency:  concurrency,
		Model:        model,
		PromptText:   string(tmplBytes),
		PromptName:   &promptName,
		PromptSHA256: promptSHA,
		Status:       "running", // never 'pending' — Phase 2 worker polls pending; cmd must not race
	})
	if err != nil {
		fail("create run: %v", err)
	}
	fmt.Printf("run_id=%d\n", runID) // captured by cron / shells

	cases, err := eval.LoadReplayCases(ctx, pool, cutoff, maxN)
	if err != nil {
		_ = store.MarkRunFailed(ctx, runID, "load cases: "+err.Error())
		fail("load cases: %v", err)
	}
	if err := store.MarkRunRunning(ctx, runID, len(cases)); err != nil {
		fail("mark running: %v", err)
	}

	llm := eval.MakeLLMClient(apiKey, baseURL)
	rep := eval.RunReplay(ctx, cases, tmpl, llm, model, timeoutMs, concurrency)
	rep.Since = since
	rep.PromptFile = promptName

	var failed int
	for _, row := range rep.Rows {
		if row.Error != "" {
			failed++
		}
		if err := store.InsertRow(ctx, runID, row); err != nil {
			_ = store.MarkRunFailed(ctx, runID, "insert row: "+err.Error())
			fail("insert row sig=%d: %v", row.SignalID, err)
		}
	}
	if err := store.MarkRunDone(ctx, runID, &rep, len(rep.Rows)-failed, failed); err != nil {
		fail("mark done: %v", err)
	}
	fmt.Printf("done: %d samples, %d failed\n", len(rep.Rows), failed)

	// Legacy --report / --json outputs (kept for 1 month after /eval ships).
	if reportPath != "" {
		f, err := os.Create(reportPath)
		if err != nil {
			fail("create report: %v", err)
		}
		defer f.Close()
		if err := eval.RenderHTML(f, rep); err != nil {
			fail("render html: %v", err)
		}
	}
	if jsonPath != "" {
		f, err := os.Create(jsonPath)
		if err != nil {
			fail("create json: %v", err)
		}
		defer f.Close()
		if err := eval.RenderJSON(f, rep); err != nil {
			fail("render json: %v", err)
		}
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "agent-eval: "+format+"\n", args...)
	os.Exit(1)
}
