// Package eval contains the offline grayscale-period and replay-mode
// analytics shared between cmd/agent-eval (CLI) and the /eval/* web
// dashboard. Pure compute lives here; both consumers wire their own I/O.
package eval

import (
	"encoding/json"
	"math"

	"github.com/shopspring/decimal"
)

// ReplayRow is one signal worth of replay output: old prod score/decision +
// new replay score/decision + realized PnL (if any).
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
	Error       string // BadDecision / MissingFields / LLMError / history_json: ...
}

// Bucket is one score-bin's aggregate.
type Bucket struct {
	Label   string
	Signals int
	Trades  int
	Wins    int
	SumPnL  float64
	AvgPnL  float64
	WinRate float64
}

// MarshalJSON returns JSON null for NaN AvgPnL/WinRate (empty bucket).
func (b Bucket) MarshalJSON() ([]byte, error) {
	type alias Bucket
	return json.Marshal(struct {
		alias
		AvgPnL  any `json:"AvgPnL"`
		WinRate any `json:"WinRate"`
	}{
		alias:   alias(b),
		AvgPnL:  NilIfNaN(b.AvgPnL),
		WinRate: NilIfNaN(b.WinRate),
	})
}

// FlipMatrix is the kept-vs-flipped breakdown with PnL totals.
type FlipMatrix struct {
	Kept    int     // both versions same decision
	Flipped int     // versions disagree
	KeptPnL float64
	FlipPnL float64
}

func (m FlipMatrix) MarshalJSON() ([]byte, error) {
	type alias FlipMatrix
	return json.Marshal(struct {
		alias
		KeptPnL any `json:"KeptPnL"`
		FlipPnL any `json:"FlipPnL"`
	}{
		alias:   alias(m),
		KeptPnL: NilIfNaN(m.KeptPnL),
		FlipPnL: NilIfNaN(m.FlipPnL),
	})
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

func (r ReplayReport) MarshalJSON() ([]byte, error) {
	type alias ReplayReport
	return json.Marshal(struct {
		alias
		V1Spearman any `json:"v1_spearman"`
		V2Spearman any `json:"v2_spearman"`
	}{
		alias:      alias(r),
		V1Spearman: NilIfNaN(r.V1Spearman),
		V2Spearman: NilIfNaN(r.V2Spearman),
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

// EvalReport is the result of `cmd/agent-eval --since=3d` (no --replay).
// Used both by the cmd (stdout) and the /eval page.
type EvalReport struct {
	Since        string
	GeneratedAt  int64
	Buckets      []Bucket // 5 fixed score bins
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

type FailReason struct {
	Reason string
	Count  int
}
