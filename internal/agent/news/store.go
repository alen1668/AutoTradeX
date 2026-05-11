package news

import (
	"context"
	"fmt"

	"github.com/lizhaojie/tvbot/internal/store"
)

// Repository is the minimum surface StoreAdapter needs.
type Repository interface {
	Insert(ctx context.Context, rec store.NewsSnapshotRecord) (int64, error)
}

type StoreAdapter struct{ repo Repository }

func NewStoreAdapter(r Repository) *StoreAdapter { return &StoreAdapter{repo: r} }

func intPtrOrNil(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (a *StoreAdapter) PersistSuccess(ctx context.Context, c Classification) (int64, error) {
	rec := store.NewsSnapshotRecord{
		MeasuredAt:   c.MeasuredAt,
		Impact:       c.Impact,
		Summary:      c.Summary,
		Reasoning:    c.Reasoning,
		PerHeadline:  c.PerHeadlineJSON,
		RawHeadlines: c.RawHeadlinesJSON,
		PromptHash:   c.PromptHash,
		PromptText:   c.PromptText,
		ResponseRaw:  strPtrOrNil(c.ResponseRaw),
		LLMModel:     c.LLMModel,
		LLMTokensIn:  intPtrOrNil(c.LLMTokensIn),
		LLMTokensOut: intPtrOrNil(c.LLMTokensOut),
		LLMLatencyMs: intPtrOrNil(c.LLMLatencyMs),
		ErrorMessage: nil,
	}
	return a.repo.Insert(ctx, rec)
}

func (a *StoreAdapter) PersistFailure(ctx context.Context, c Classification, cause error) (int64, error) {
	errMsg := cause.Error()
	reasoning := c.Reasoning
	if reasoning == "" {
		reasoning = fmt.Sprintf("LLM 调用失败: %s", errMsg)
	}
	rec := store.NewsSnapshotRecord{
		MeasuredAt:   c.MeasuredAt,
		Impact:       "none",
		Summary:      "",
		Reasoning:    reasoning,
		PerHeadline:  c.PerHeadlineJSON,
		RawHeadlines: c.RawHeadlinesJSON,
		PromptHash:   c.PromptHash,
		PromptText:   c.PromptText,
		ResponseRaw:  strPtrOrNil(c.ResponseRaw),
		LLMModel:     c.LLMModel,
		LLMTokensIn:  intPtrOrNil(c.LLMTokensIn),
		LLMTokensOut: intPtrOrNil(c.LLMTokensOut),
		LLMLatencyMs: intPtrOrNil(c.LLMLatencyMs),
		ErrorMessage: &errMsg,
	}
	return a.repo.Insert(ctx, rec)
}
