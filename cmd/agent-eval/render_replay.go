package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
)

// ReplayReport bundles all replay output. Tagged for stable JSON output —
// the JSON contract is consumed by future automation (meta-prompt loops).
type ReplayReport struct {
	Since      string      `json:"since"`
	PromptFile string      `json:"prompt_file"`
	SampleSize int         `json:"sample_size"`
	WithPnL    int         `json:"with_pnl"`
	V1Spearman float64     `json:"v1_spearman"`
	V2Spearman float64     `json:"v2_spearman"`
	V1Buckets  []Bucket    `json:"v1_buckets"`
	V2Buckets  []Bucket    `json:"v2_buckets"`
	Flips      FlipMatrix  `json:"flips"`
	Rows       []ReplayRow `json:"rows"`
}

// fmtNaN formats v with layout, returning "—" when v is NaN.
func fmtNaN(v float64, layout string) string {
	if math.IsNaN(v) {
		return "—"
	}
	return fmt.Sprintf(layout, v)
}

// renderReplayText writes the human-readable terminal report.
func renderReplayText(w io.Writer, r ReplayReport) error {
	fmt.Fprintf(w, "Replay 报告: %s vs 生产 prompt(v1)\n", r.PromptFile)
	fmt.Fprintf(w, "样本: since=%s, 共 %d 条评估过的信号 (%d 条有 PnL)\n\n",
		r.Since, r.SampleSize, r.WithPnL)

	fmt.Fprintln(w, "== 概要指标 ==")
	fmt.Fprintf(w, "                       v1 (生产)   v2 (新)    Δ\n")
	fmt.Fprintf(w, "  Spearman(score,PnL)   %s   %s   %s\n",
		fmtNaN(r.V1Spearman, "%6.2f"),
		fmtNaN(r.V2Spearman, "%6.2f"),
		fmtNaN(r.V2Spearman-r.V1Spearman, "%+6.2f"))

	fmt.Fprintln(w, "\n== 5 桶 avg PnL ($) ==")
	fmt.Fprintf(w, "  bucket    v1 signals  v1 avg     v2 signals  v2 avg\n")
	for i := 0; i < 5; i++ {
		fmt.Fprintf(w, "  %-8s  %8d   %s    %8d   %s\n",
			r.V1Buckets[i].Label,
			r.V1Buckets[i].Signals,
			fmtNaN(r.V1Buckets[i].AvgPnL, "%8.2f"),
			r.V2Buckets[i].Signals,
			fmtNaN(r.V2Buckets[i].AvgPnL, "%8.2f"))
	}

	fmt.Fprintln(w, "\n== Decision 翻转矩阵 ==")
	fmt.Fprintf(w, "  v1\\v2     approve  abandon\n")
	fmt.Fprintf(w, "  approve     %3d      %3d\n", r.Flips.ApproveToApprove, r.Flips.ApproveToAbandon)
	fmt.Fprintf(w, "  abandon     %3d      %3d\n", r.Flips.AbandonToApprove, r.Flips.AbandonToAbandon)
	fmt.Fprintln(w, "  翻转质量 (仅 has-PnL):")
	fmt.Fprintf(w, "    approve→abandon (%d 条): 平均 PnL = %s $\n",
		r.Flips.ApproveToAbandon, fmtNaN(r.Flips.ApproveToAbandonAvgPnL, "%.2f"))
	fmt.Fprintf(w, "    abandon→approve (%d 条): 平均 PnL = %s $\n",
		r.Flips.AbandonToApprove, fmtNaN(r.Flips.AbandonToApproveAvgPnL, "%.2f"))

	fmt.Fprintln(w, "\n== 逐信号明细 (按 |Δscore| 排序) ==")
	fmt.Fprintf(w, "  %-9s  %-18s  %-8s  %-5s  %3s   %3s   %4s   %8s   %s\n",
		"signal_id", "strategy", "symbol", "side", "v1", "v2", "Δ", "pnl", "flip")
	for _, row := range r.Rows {
		flip := flipLabel(row.OldDecision, row.NewDecision)
		pnlStr := "—"
		if row.HasPnL && row.PnLUSDC != nil {
			pnlStr = fmt.Sprintf("%8.2f", *row.PnLUSDC)
		}
		v2Str := fmt.Sprintf("%3d", row.NewScore)
		if row.Error != "" {
			v2Str = "ERROR"
		}
		fmt.Fprintf(w, "  %-9d  %-18s  %-8s  %-5s  %3d   %s   %+4d   %8s   %s\n",
			row.SignalID, row.StrategyID, row.Symbol, row.Kind,
			row.OldScore, v2Str, row.NewScore-row.OldScore, pnlStr, flip)
	}
	return nil
}

// flipLabel returns a short label for an old→new decision pair: "—" when unchanged, "A→B" / "B→A" for the two flip directions.
func flipLabel(old, new string) string {
	if old == new {
		return "—"
	}
	if old == "approve" && new == "abandon" {
		return "A→B"
	}
	if old == "abandon" && new == "approve" {
		return "B→A"
	}
	return "?"
}

// renderReplayJSON writes the machine-readable JSON report.
func renderReplayJSON(w io.Writer, r ReplayReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// renderReplayHTML writes a self-contained HTML page with summary + per-signal collapsible reasoning.
func renderReplayHTML(w io.Writer, r ReplayReport) error {
	fmt.Fprintln(w, `<!doctype html><html><head><meta charset="utf-8"><title>Replay 报告</title>`)
	fmt.Fprintln(w, `<style>body{font-family:system-ui;margin:2em;max-width:1100px}`)
	fmt.Fprintln(w, `pre{background:#f4f4f4;padding:8px;overflow:auto}`)
	fmt.Fprintln(w, `table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:4px 8px}</style></head><body>`)
	fmt.Fprintf(w, `<h1>Replay 报告: %s vs v1</h1>`, html.EscapeString(r.PromptFile))
	fmt.Fprintf(w, `<p>样本: since=%s, %d 条 (%d 条有 PnL)</p>`,
		html.EscapeString(r.Since), r.SampleSize, r.WithPnL)

	fmt.Fprintln(w, `<h2>概要</h2><pre>`)
	var textBuf bytes.Buffer
	if err := renderReplayText(&textBuf, r); err != nil {
		return err
	}
	fmt.Fprint(w, html.EscapeString(textBuf.String()))
	fmt.Fprintln(w, `</pre>`)

	fmt.Fprintln(w, `<h2>详细 reasoning (折叠)</h2>`)
	for _, row := range r.Rows {
		if row.Error != "" {
			fmt.Fprintf(w, `<details><summary>signal %d — ERROR: %s</summary></details>`,
				row.SignalID, html.EscapeString(row.Error))
			continue
		}
		fmt.Fprintf(w, `<details><summary>signal %d  (%s %s %s) v1=%d v2=%d</summary>`,
			row.SignalID, html.EscapeString(row.StrategyID),
			html.EscapeString(row.Symbol), html.EscapeString(row.Kind),
			row.OldScore, row.NewScore)
		fmt.Fprintf(w, `<h4>v1 reasoning</h4><pre>%s</pre>`, html.EscapeString(row.OldReason))
		fmt.Fprintf(w, `<h4>v2 reasoning</h4><pre>%s</pre>`, html.EscapeString(row.NewReason))
		fmt.Fprintln(w, `</details>`)
	}
	fmt.Fprintln(w, `</body></html>`)
	return nil
}
