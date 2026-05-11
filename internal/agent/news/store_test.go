package news

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lizhaojie/tvbot/internal/store"
)

type fakeRepo struct {
	saved store.NewsSnapshotRecord
	err   error
}

func (f *fakeRepo) Insert(ctx context.Context, rec store.NewsSnapshotRecord) (int64, error) {
	f.saved = rec
	return 1, f.err
}

func TestStoreAdapter_PersistSuccess(t *testing.T) {
	r := &fakeRepo{}
	a := NewStoreAdapter(r)
	c := Classification{
		Impact:           "high",
		Summary:          "x",
		Reasoning:        "y",
		PerHeadline:      []HeadlineJudgment{{Title: "A", Impact: "high"}},
		PerHeadlineJSON:  []byte(`[{"title":"A","impact":"high"}]`),
		RawHeadlinesJSON: []byte(`[{}]`),
		PromptHash:       "abcd1234",
		PromptText:       "prompt",
		ResponseRaw:      `{"impact":"high"}`,
		LLMModel:         "haiku",
		LLMTokensIn:      100,
		LLMTokensOut:     20,
		LLMLatencyMs:     800,
		MeasuredAt:       time.Now().UTC(),
	}
	if _, err := a.PersistSuccess(context.Background(), c); err != nil {
		t.Fatalf("PersistSuccess: %v", err)
	}
	if r.saved.Impact != "high" || r.saved.PromptHash != "abcd1234" {
		t.Errorf("saved: %+v", r.saved)
	}
	if r.saved.ErrorMessage != nil {
		t.Errorf("ErrorMessage should be nil on success: %v", *r.saved.ErrorMessage)
	}
	if r.saved.LLMTokensIn == nil || *r.saved.LLMTokensIn != 100 {
		t.Errorf("LLMTokensIn: %v", r.saved.LLMTokensIn)
	}
}

func TestStoreAdapter_PersistFailure(t *testing.T) {
	r := &fakeRepo{}
	a := NewStoreAdapter(r)
	c := Classification{
		PerHeadlineJSON:  []byte("[]"),
		RawHeadlinesJSON: []byte("[]"),
		PromptHash:       "abcd1234",
		PromptText:       "prompt",
		LLMModel:         "haiku",
		MeasuredAt:       time.Now().UTC(),
	}
	if _, err := a.PersistFailure(context.Background(), c, errors.New("LLM timeout")); err != nil {
		t.Fatalf("PersistFailure: %v", err)
	}
	if r.saved.Impact != "none" {
		t.Errorf("PersistFailure should force impact=none, got %q", r.saved.Impact)
	}
	if r.saved.ErrorMessage == nil || *r.saved.ErrorMessage != "LLM timeout" {
		t.Errorf("ErrorMessage: %v", r.saved.ErrorMessage)
	}
	if r.saved.Reasoning == "" {
		t.Errorf("Reasoning should be set on failure for traceability")
	}
}
