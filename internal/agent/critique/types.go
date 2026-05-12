// Package critique implements the self-critique LLM agent that
// periodically reflects on agent_evaluations + their outcome labels and
// proposes structured "误判模式" patterns. Patterns can be pinned by
// operators, in which case the scorer prompt template injects them.
package critique

import "time"

// Status mirrors agent_critiques.status.
type Status string

const (
	StatusDone               Status = "done"
	StatusFailed             Status = "failed"
	StatusInsufficientSample Status = "insufficient_sample"
)

// PatternSet is the parsed shape of agent_critiques.patterns_json — the
// LLM must return this exact JSON structure. See prompt template.
type PatternSet struct {
	Summary  string    `json:"summary"`
	Patterns []Pattern `json:"patterns"`
}

// Pattern is one entry in PatternSet.Patterns.
type Pattern struct {
	ID                  string         `json:"id"`
	Title               string         `json:"title"`
	EvidenceSignalIDs   []int64        `json:"evidence_signal_ids"`
	Stats               map[string]any `json:"stats"`
	SuggestionForPrompt string         `json:"suggestion_for_prompt"`
	Confidence          string         `json:"confidence"`
}

// Critique is the in-memory representation of one agent_critiques row,
// minus the children patterns (those live in PatternSet / DB child rows).
type Critique struct {
	ID           int64
	CreatedAt    time.Time
	WindowStart  time.Time
	WindowEnd    time.Time
	SampleSize   int
	Model        string
	PromptHash   string
	PatternsJSON []byte
	Summary      *string
	LatencyMs    *int
	TokenIn      *int
	TokenOut     *int
	Status       Status
	ErrorMessage *string
}
