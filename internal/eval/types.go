// Package eval contains the offline grayscale-period and replay-mode
// analytics shared between cmd/agent-eval (CLI) and the /eval/* web
// dashboard. Pure compute lives here; both consumers wire their own I/O.
package eval

import (
	"encoding/json"
	"math"

	"github.com/shopspring/decimal"
)

// ReplayRow is the per-signal record produced by ReplayOne. Aggregation
// functions operate on slices of these. PnLUSDC is nil when the signal had
// no closed trade; HasPnL distinguishes nil from "0.0 PnL".
type ReplayRow struct {
	SignalID    int64
	StrategyID  string
	Symbol      string
	Kind        string
	OldScore    int
	OldDecision string
	OldReason   string
	NewScore    int
	NewDecision string
	NewReason   string
	PnLUSDC     *float64
	HasPnL      bool
	Error       string // non-empty when LLM call / parse failed for new prompt
}

// Bucket is one row of the 5-tier score-vs-PnL summary produced by replay.
// JSON shape is consumed by future automation; field names are stable.
type Bucket struct {
	Label   string
	Signals int     // count of signals in bucket
	Trades  int     // count of has-PnL signals in bucket
	AvgPnL  float64 // mean of PnLs over Trades; NaN if Trades==0
	WinPct  float64 // % of Trades with PnL>0; NaN if Trades==0
}

// MarshalJSON for Bucket — turns NaN AvgPnL/WinPct into JSON null.
func (b Bucket) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Label   string `json:"label"`
		Signals int    `json:"signals"`
		Trades  int    `json:"trades"`
		AvgPnL  any    `json:"avg_pnl"`
		WinPct  any    `json:"win_pct"`
	}{b.Label, b.Signals, b.Trades, NilIfNaN(b.AvgPnL), NilIfNaN(b.WinPct)})
}

// FlipMatrix counts the four old×new decision combinations and the avg PnL
// for the two true-flip cells.
type FlipMatrix struct {
	ApproveToApprove       int
	ApproveToAbandon       int
	AbandonToApprove       int
	AbandonToAbandon       int
	ApproveToAbandonAvgPnL float64 // NaN if no has-PnL flips of this kind
	AbandonToApproveAvgPnL float64
}

// MarshalJSON for FlipMatrix — turns NaN flip-quality avg PnL into JSON null.
func (m FlipMatrix) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ApproveToApprove       int `json:"approve_to_approve"`
		ApproveToAbandon       int `json:"approve_to_abandon"`
		AbandonToApprove       int `json:"abandon_to_approve"`
		AbandonToAbandon       int `json:"abandon_to_abandon"`
		ApproveToAbandonAvgPnL any `json:"approve_to_abandon_avg_pnl"`
		AbandonToApproveAvgPnL any `json:"abandon_to_approve_avg_pnl"`
	}{m.ApproveToApprove, m.ApproveToAbandon, m.AbandonToApprove, m.AbandonToAbandon,
		NilIfNaN(m.ApproveToAbandonAvgPnL), NilIfNaN(m.AbandonToApproveAvgPnL)})
}

// NilIfNaN returns nil for NaN so JSON marshals to null instead of "NaN".
func NilIfNaN(v float64) any {
	if math.IsNaN(v) {
		return nil
	}
	return v
}

// ReplayCase is the input record loaded from DB for one signal to be
// replayed. Consumed by ReplayOne, which reconstructs ScoreInput from it.
type ReplayCase struct {
	SignalID      int64
	StrategyID    string
	Symbol        string
	Kind          string
	SignalPrice   decimal.Decimal
	TVTimestampMs int64
	OldScore      int
	OldDecision   string
	OldReason     string
	HistoryJSON   []byte
	PnLUSDC       *float64
	HasPnL        bool
}

// ReplayReport bundles all replay output (old + new score buckets, flips,
// rows). Returned by RunReplay and serialized to summary_json on persistence.
// JSON contract is consumed by future automation; field names are stable.
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

// MarshalJSON for ReplayReport — turns NaN Spearman values into JSON null
// (they're NaN when sample size < 2).
func (r ReplayReport) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Since      string      `json:"since"`
		PromptFile string      `json:"prompt_file"`
		SampleSize int         `json:"sample_size"`
		WithPnL    int         `json:"with_pnl"`
		V1Spearman any         `json:"v1_spearman"`
		V2Spearman any         `json:"v2_spearman"`
		V1Buckets  []Bucket    `json:"v1_buckets"`
		V2Buckets  []Bucket    `json:"v2_buckets"`
		Flips      FlipMatrix  `json:"flips"`
		Rows       []ReplayRow `json:"rows"`
	}{
		r.Since, r.PromptFile, r.SampleSize, r.WithPnL,
		NilIfNaN(r.V1Spearman), NilIfNaN(r.V2Spearman),
		r.V1Buckets, r.V2Buckets, r.Flips, r.Rows,
	})
}

// ReplayRun is the persistent record of one replay invocation
// (= one row in replay_runs).
type ReplayRun struct {
	ID            int64
	CreatedAt     int64 // unix epoch seconds; templates format as needed
	SinceWindow   string
	SinceCutoff   int64 // unix epoch seconds
	MaxN          int
	Concurrency   int
	Model         string
	PromptText    string
	PromptName    *string
	PromptSHA256  string
	Status        string // pending|running|done|failed|aborted
	StartedAt     *int64
	FinishedAt    *int64
	ErrorMessage  *string
	SamplesTotal  int
	SamplesDone   int
	SamplesFailed int
	Summary       *ReplayReport // decoded from summary_json, nil until status=done
}

// EvalBucket is the per-bucket aggregate used by the grayscale dashboard.
// Distinct from Bucket (replay-mode) because the dashboard surfaces more
// columns: Wins + SumPnL + WinRate. Kept separate so adding fields here
// doesn't churn ReplayReport JSON consumers.
type EvalBucket struct {
	Label   string
	Signals int
	Trades  int
	Wins    int
	SumPnL  float64
	AvgPnL  float64 // NaN if Trades==0
	WinRate float64 // 0..100; NaN if Trades==0
}

// EvalReport is the result of `cmd/agent-eval --since=3d` (no --replay).
// Used both by the cmd (stdout) and the /eval page.
type EvalReport struct {
	Since        string
	GeneratedAt  int64
	Buckets      []EvalBucket // 5 fixed score bins
	TotalSignals int
	TotalTrades  int
	Spearman     float64 // NaN if insufficient samples
	LLMHealth    LLMHealth
}

// LLMHealth aggregates last-N-days agent_evaluations table.
type LLMHealth struct {
	TotalCalls     int
	FailedCalls    int
	FailureRate    float64 // 0..100
	AvgLatencyMs   int
	P95LatencyMs   int
	TopFailReasons []FailReason // top 3
}

// FailReason is one bucket of the top-failure-reasons summary.
type FailReason struct {
	Reason string
	Count  int
}
