// agent-eval is the offline grayscale-period report tool described in
// the spec ("灰度上线流程" §). It joins agent_evaluations to actual
// realized PnL so operators can answer the central question: does the
// agent's score correlate with profit?
//
// Usage:
//
//	go run ./cmd/agent-eval --since=3d
//	go run ./cmd/agent-eval --since=24h --report=/tmp/eval.html
//	DATABASE_URL=postgres://... go run ./cmd/agent-eval --since=7d
package main

import (
	"context"
	"flag"
	"fmt"
	"html"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type bucket struct {
	Label    string
	Signals  int
	Trades   int
	SumPnL   float64
	Wins     int
}

func main() {
	var since string
	var reportPath string
	flag.StringVar(&since, "since", "3d", "lookback window: 3d / 24h / 7d / 1h ...")
	flag.StringVar(&reportPath, "report", "", "if set, write an HTML copy of the report to this path")
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

	cutoff, err := parseSince(since)
	if err != nil {
		fail("parse since: %v", err)
	}

	// Score-bucket × realized PnL. Match agent score → matched virtual
	// position → close history (same join as the /stats page).
	rows, err := pool.Query(ctx, `
SELECT
  CASE
    WHEN s.agent_score < 20 THEN '0-20'
    WHEN s.agent_score < 40 THEN '20-40'
    WHEN s.agent_score < 60 THEN '40-60'
    WHEN s.agent_score < 80 THEN '60-80'
    ELSE '80-100'
  END AS bucket,
  ph.pnl_usdc IS NOT NULL AS has_trade,
  COALESCE(ph.pnl_usdc, 0)::float8 AS pnl
FROM signals s
LEFT JOIN virtual_positions vp ON vp.entry_signal_id = s.id
LEFT JOIN position_history ph
       ON ph.strategy_id = vp.strategy_id
      AND ph.symbol = vp.symbol
      AND ph.opened_at = vp.opened_at
WHERE s.agent_score IS NOT NULL AND s.received_at >= $1`, cutoff)
	if err != nil {
		fail("query buckets: %v", err)
	}
	defer rows.Close()

	buckets := map[string]*bucket{
		"0-20":   {Label: "0-20"},
		"20-40":  {Label: "20-40"},
		"40-60":  {Label: "40-60"},
		"60-80":  {Label: "60-80"},
		"80-100": {Label: "80-100"},
	}
	var totalSignals int
	for rows.Next() {
		var label string
		var hasTrade bool
		var pnl float64
		if err := rows.Scan(&label, &hasTrade, &pnl); err != nil {
			fail("scan: %v", err)
		}
		b := buckets[label]
		b.Signals++
		totalSignals++
		if hasTrade {
			b.Trades++
			b.SumPnL += pnl
			if pnl > 0 {
				b.Wins++
			}
		}
	}

	// LLM call health for the same window.
	var llmTotal, llmFailed int
	var sumLatency int
	var sumCost float64
	if err := pool.QueryRow(ctx, `
SELECT COUNT(*), COUNT(*) FILTER (WHERE decision='failed'),
       COALESCE(SUM(latency_ms),0),
       COALESCE(SUM(cost_cents),0)::float8
FROM agent_evaluations WHERE created_at >= $1`, cutoff).Scan(
		&llmTotal, &llmFailed, &sumLatency, &sumCost,
	); err != nil {
		fail("query health: %v", err)
	}

	out := &strings.Builder{}
	fmt.Fprintf(out, "Agent 评估报告 since %s (%d 条已评估信号)\n\n", since, totalSignals)
	fmt.Fprintf(out, "分数分桶 vs 实际盈亏\n")
	fmt.Fprintf(out, "  %-8s %8s %8s %12s %8s\n", "bucket", "signals", "trades", "avg PnL$", "win%")
	for _, k := range []string{"0-20", "20-40", "40-60", "60-80", "80-100"} {
		b := buckets[k]
		avg := 0.0
		win := 0.0
		if b.Trades > 0 {
			avg = b.SumPnL / float64(b.Trades)
			win = float64(b.Wins) / float64(b.Trades) * 100
		}
		fmt.Fprintf(out, "  %-8s %8d %8d %12.2f %7.1f%%\n", b.Label, b.Signals, b.Trades, avg, win)
	}

	fmt.Fprintf(out, "\nLLM 调用健康\n")
	fmt.Fprintf(out, "  total           %d\n", llmTotal)
	if llmTotal > 0 {
		fmt.Fprintf(out, "  failed          %d (%.1f%%)\n", llmFailed,
			100*float64(llmFailed)/float64(llmTotal))
		fmt.Fprintf(out, "  avg latency     %.0f ms\n", float64(sumLatency)/float64(llmTotal))
	} else {
		fmt.Fprintf(out, "  failed          0 (0.0%%)\n")
		fmt.Fprintf(out, "  avg latency     n/a\n")
	}
	fmt.Fprintf(out, "  cumulative cost ¢%.2f (≈ $%.4f)\n", sumCost, sumCost/100)

	text := out.String()
	fmt.Print(text)

	if reportPath != "" {
		body := "<html><body><pre style=\"font-family:monospace; padding:1em\">" +
			html.EscapeString(text) + "</pre></body></html>"
		if err := os.WriteFile(reportPath, []byte(body), 0644); err != nil {
			fail("write report: %v", err)
		}
		fmt.Fprintf(os.Stderr, "report written to %s\n", reportPath)
	}
}

func parseSince(s string) (time.Time, error) {
	now := time.Now().UTC()
	if strings.HasSuffix(s, "d") {
		var n int
		if _, err := fmt.Sscanf(s, "%dd", &n); err != nil {
			return time.Time{}, err
		}
		return now.Add(-time.Duration(n) * 24 * time.Hour), nil
	}
	if strings.HasSuffix(s, "h") {
		var n int
		if _, err := fmt.Sscanf(s, "%dh", &n); err != nil {
			return time.Time{}, err
		}
		return now.Add(-time.Duration(n) * time.Hour), nil
	}
	return time.Time{}, fmt.Errorf("unsupported format %q (use Xd or Xh)", s)
}

func fail(f string, args ...any) {
	fmt.Fprintf(os.Stderr, "agent-eval: "+f+"\n", args...)
	os.Exit(1)
}
