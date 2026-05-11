package news

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
)

type fakeLLM struct {
	resp   scorer.CompleteResponse
	err    error
	gotReq scorer.CompleteRequest
}

func (f *fakeLLM) Complete(ctx context.Context, req scorer.CompleteRequest) (scorer.CompleteResponse, error) {
	f.gotReq = req
	return f.resp, f.err
}

func TestClassifier_HappyPath(t *testing.T) {
	raw := `{"impact":"high","summary":"整体偏空","reasoning":"标题 0 属于 SEC 起诉","per_headline":[{"index":0,"impact":"high","reason":"SEC"},{"index":1,"impact":"low","reason":"普通"}]}`
	llm := &fakeLLM{resp: scorer.CompleteResponse{Text: raw, TokenIn: 100, TokenOut: 50}}
	c := NewClassifier(llm, "test-model", zerolog.Nop())

	hs := []Headline{
		{Title: "SEC sues X", URL: "u1", Source: "CoinDesk", PublishedAt: time.Now()},
		{Title: "Daily price analysis", URL: "u2", Source: "Blog"},
	}
	got, err := c.Classify(context.Background(), hs)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got.Impact != "high" || got.Summary != "整体偏空" {
		t.Errorf("got %+v", got)
	}
	if len(got.PerHeadline) != 2 {
		t.Errorf("per_headline len: %d", len(got.PerHeadline))
	}
	if got.PerHeadline[0].Impact != "high" || got.PerHeadline[0].Title != "SEC sues X" {
		t.Errorf("per_headline[0]: %+v", got.PerHeadline[0])
	}
	if got.PromptHash == "" || got.PromptText == "" || got.ResponseRaw != raw {
		t.Errorf("missing audit fields: hash=%q text-len=%d raw-len=%d", got.PromptHash, len(got.PromptText), len(got.ResponseRaw))
	}
}

func TestClassifier_PerHeadlineCountMismatch_IsParseError(t *testing.T) {
	raw := `{"impact":"medium","summary":"x","reasoning":"y","per_headline":[{"index":0,"impact":"medium","reason":"r"}]}`
	llm := &fakeLLM{resp: scorer.CompleteResponse{Text: raw}}
	c := NewClassifier(llm, "haiku", zerolog.Nop())
	hs := []Headline{{Title: "A"}, {Title: "B"}}
	_, err := c.Classify(context.Background(), hs)
	if err == nil {
		t.Fatal("expected error on per_headline coverage mismatch")
	}
}

func TestClassifier_InvalidJSON_IsError(t *testing.T) {
	llm := &fakeLLM{resp: scorer.CompleteResponse{Text: "not json at all"}}
	c := NewClassifier(llm, "haiku", zerolog.Nop())
	_, err := c.Classify(context.Background(), []Headline{{Title: "A"}})
	if err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

func TestClassifier_LLMError(t *testing.T) {
	llm := &fakeLLM{err: errors.New("LLM timeout")}
	c := NewClassifier(llm, "haiku", zerolog.Nop())
	_, err := c.Classify(context.Background(), []Headline{{Title: "A"}})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestClassifier_EmptyHeadlines_ShortCircuits(t *testing.T) {
	llm := &fakeLLM{}
	c := NewClassifier(llm, "haiku", zerolog.Nop())
	got, err := c.Classify(context.Background(), nil)
	if err != nil {
		t.Fatalf("Classify(nil): %v", err)
	}
	if got.Impact != "none" {
		t.Errorf("empty headlines -> Impact %q, want none", got.Impact)
	}
	if len(got.PerHeadline) != 0 {
		t.Errorf("per_headline should be empty")
	}
	if string(got.PerHeadlineJSON) != "[]" {
		t.Errorf("PerHeadlineJSON: %q", string(got.PerHeadlineJSON))
	}
	if llm.gotReq.Model != "" {
		t.Errorf("LLM should not be called for empty input")
	}
}

func TestClassifier_AcceptsFencedCodeBlock(t *testing.T) {
	raw := "```json\n{\"impact\":\"low\",\"summary\":\"s\",\"reasoning\":\"r\",\"per_headline\":[{\"index\":0,\"impact\":\"low\",\"reason\":\"r\"}]}\n```"
	llm := &fakeLLM{resp: scorer.CompleteResponse{Text: raw}}
	c := NewClassifier(llm, "haiku", zerolog.Nop())
	got, err := c.Classify(context.Background(), []Headline{{Title: "A"}})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got.Impact != "low" {
		t.Errorf("impact: %q", got.Impact)
	}
}
