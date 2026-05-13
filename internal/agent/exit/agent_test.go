package exit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
)

type fakeLLM struct {
	resp scorer.CompleteResponse
	err  error
	got  scorer.CompleteRequest
}

func (f *fakeLLM) Complete(_ context.Context, r scorer.CompleteRequest) (scorer.CompleteResponse, error) {
	f.got = r
	return f.resp, f.err
}

func TestAgent_Decide_HoldHappyPath(t *testing.T) {
	llm := &fakeLLM{resp: scorer.CompleteResponse{
		Text:     `{"action":"hold","confidence":"medium","reasoning":"持仓走势仍符合开仓逻辑"}`,
		TokenIn:  120,
		TokenOut: 32,
	}}
	a := NewAgent(llm, "claude-sonnet-4-6")
	d, meta, err := a.Decide(context.Background(), sampleInput(t))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if d.Action != ActionHold {
		t.Errorf("action: %v", d.Action)
	}
	if meta.TokenIn != 120 || meta.TokenOut != 32 {
		t.Errorf("token meta: %+v", meta)
	}
	if meta.PromptHash == "" {
		t.Error("missing prompt hash")
	}
	if !strings.Contains(llm.got.Prompt, "ETHUSDC") {
		t.Error("prompt not rendered with input")
	}
}

func TestAgent_Decide_LLMErrorBubbles(t *testing.T) {
	llm := &fakeLLM{err: errors.New("upstream 503")}
	a := NewAgent(llm, "claude-sonnet-4-6")
	_, _, err := a.Decide(context.Background(), sampleInput(t))
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("want LLM error, got %v", err)
	}
}

func TestAgent_Decide_ParseErrorBubbles(t *testing.T) {
	llm := &fakeLLM{resp: scorer.CompleteResponse{Text: "not json at all"}}
	a := NewAgent(llm, "claude-sonnet-4-6")
	_, _, err := a.Decide(context.Background(), sampleInput(t))
	if err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("want parse error, got %v", err)
	}
}

func TestAgent_Decide_ModelDefaultsToConfigured(t *testing.T) {
	llm := &fakeLLM{resp: scorer.CompleteResponse{
		Text: `{"action":"hold","confidence":"low","reasoning":"r"}`,
	}}
	a := NewAgent(llm, "claude-haiku-4-5-20251001")
	if _, _, err := a.Decide(context.Background(), sampleInput(t)); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if llm.got.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("model: %q", llm.got.Model)
	}
}
