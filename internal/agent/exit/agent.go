package exit

import (
	"context"
	"fmt"
	"time"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
)

// DecisionMeta captures everything needed to persist a decision row
// alongside the parsed Decision: model identity, latency, token usage.
type DecisionMeta struct {
	Model      string
	PromptHash string
	LatencyMs  int
	TokenIn    int
	TokenOut   int
}

// Agent wraps one LLM round-trip: render prompt → call LLM → parse JSON.
// It is single-purpose and stateless; Worker is the orchestrator above it.
type Agent struct {
	llm   scorer.LLMClient
	model string
}

// NewAgent — model may be the empty string; in that case the caller must
// override per-call via cfg-derived value (Worker resolves cfg.Model →
// scorer_model fallback before calling Decide).
func NewAgent(llm scorer.LLMClient, model string) *Agent {
	return &Agent{llm: llm, model: model}
}

// Decide renders the prompt, calls the LLM, and parses the response.
// Returns (Decision, meta, nil) on success; bubbles LLM and parse errors.
// Caller is responsible for persisting and (in active mode) executing.
func (a *Agent) Decide(ctx context.Context, in Input) (Decision, DecisionMeta, error) {
	prompt, err := RenderPrompt(in)
	if err != nil {
		return Decision{}, DecisionMeta{}, fmt.Errorf("render: %w", err)
	}

	t0 := time.Now()
	resp, err := a.llm.Complete(ctx, scorer.CompleteRequest{
		Model:     a.model,
		Prompt:    prompt,
		MaxTokens: 1024,
	})
	if err != nil {
		return Decision{}, DecisionMeta{}, fmt.Errorf("llm: %w", err)
	}

	d, err := Parse(resp.Text)
	if err != nil {
		return Decision{}, DecisionMeta{}, fmt.Errorf("parse: %w", err)
	}

	return d, DecisionMeta{
		Model:      a.model,
		PromptHash: PromptHash(),
		LatencyMs:  int(time.Since(t0) / time.Millisecond),
		TokenIn:    resp.TokenIn,
		TokenOut:   resp.TokenOut,
	}, nil
}
