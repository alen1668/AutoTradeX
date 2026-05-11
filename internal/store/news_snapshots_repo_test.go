//go:build integration

package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func TestNewsSnapshotsRepo_InsertAndLatest(t *testing.T) {
	pool := testPool(t)
	repo := NewNewsSnapshotsRepo(pool)
	ctx := context.Background()

	_, err := repo.Latest(ctx, pool)
	require.Error(t, err, "Latest on empty table should error")

	perHeadline, _ := json.Marshal([]map[string]string{
		{"title": "h1", "url": "u1", "impact": "high", "reason": "r1"},
	})
	rawHeadlines, _ := json.Marshal([]map[string]any{
		{"title": "h1", "url": "u1", "source": "X", "votes": 5},
	})

	rec := NewsSnapshotRecord{
		MeasuredAt:   time.Now().UTC().Truncate(time.Second),
		Impact:       "high",
		Summary:      "整体偏空",
		Reasoning:    "标题 1 属于 SEC 起诉, 高影响",
		PerHeadline:  perHeadline,
		RawHeadlines: rawHeadlines,
		PromptHash:   "abc12345",
		PromptText:   "完整 prompt",
		ResponseRaw:  strPtr(`{"impact":"high"}`),
		LLMModel:     "claude-haiku-4-5",
		LLMTokensIn:  intPtr(123),
		LLMTokensOut: intPtr(45),
		LLMLatencyMs: intPtr(1200),
		ErrorMessage: nil,
	}
	id, err := repo.Insert(ctx, pool, rec)
	require.NoError(t, err)
	assert.NotZero(t, id)

	got, err := repo.Latest(ctx, pool)
	require.NoError(t, err)
	assert.Equal(t, "high", got.Impact)
	assert.Equal(t, "整体偏空", got.Summary)
	assert.Equal(t, "abc12345", got.PromptHash)
	require.NotNil(t, got.ResponseRaw)
	assert.Equal(t, `{"impact":"high"}`, *got.ResponseRaw)
}

func TestNewsSnapshotsRepo_InsertFailureRecord(t *testing.T) {
	pool := testPool(t)
	repo := NewNewsSnapshotsRepo(pool)
	ctx := context.Background()

	rec := NewsSnapshotRecord{
		MeasuredAt:   time.Now().UTC().Truncate(time.Second),
		Impact:       "none",
		Summary:      "",
		Reasoning:    "LLM 调用失败: timeout",
		PerHeadline:  []byte(`[]`),
		RawHeadlines: []byte(`[]`),
		PromptHash:   "abc12345",
		PromptText:   "...",
		LLMModel:     "claude-haiku-4-5",
		ErrorMessage: strPtr("context deadline exceeded"),
	}
	_, err := repo.Insert(ctx, pool, rec)
	require.NoError(t, err)
	got, _ := repo.Latest(ctx, pool)
	require.NotNil(t, got.ErrorMessage)
	assert.Equal(t, "context deadline exceeded", *got.ErrorMessage)
	assert.Nil(t, got.LLMTokensIn, "tokens nil on failure")
	assert.Nil(t, got.LLMTokensOut, "tokens nil on failure")
}
