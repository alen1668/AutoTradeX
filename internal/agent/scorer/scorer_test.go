package scorer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/store"
)

// fakeLLM implements LLMClient for unit tests.
type fakeLLM struct {
	text  string
	in    int
	out   int
	err   error
	calls int
}

func (f *fakeLLM) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	f.calls++
	if f.err != nil {
		return CompleteResponse{}, f.err
	}
	return CompleteResponse{Text: f.text, TokenIn: f.in, TokenOut: f.out}, nil
}

// fakeEvalRepo captures Insert calls.
type fakeEvalRepo struct {
	inserted []store.AgentEvaluation
}

func (r *fakeEvalRepo) Insert(_ context.Context, _ store.Querier, e store.AgentEvaluation) error {
	r.inserted = append(r.inserted, e)
	return nil
}

func discardLog() zerolog.Logger { return zerolog.New(io.Discard) }

func makeScorer(llm LLMClient, repo *fakeEvalRepo, signalID int64) *LLMScorer {
	return &LLMScorer{
		client: llm, repo: repo, pool: nil, log: discardLog(),
		health:    NewHealthTracker(10 * time.Minute),
		model:     DefaultModel,
		timeoutMs: 5000,
		signalID:  signalID,
	}
}

func TestLLMScorer_Approve(t *testing.T) {
	llm := &fakeLLM{
		text: `{"score":85,"decision":"approve","reasoning":"稳定"}`,
		in:   1000, out: 50,
	}
	repo := &fakeEvalRepo{}
	s := makeScorer(llm, repo, 42)
	res, err := s.Score(context.Background(), fixedInput())
	require.NoError(t, err)
	assert.Equal(t, 85, res.Score)
	assert.Equal(t, "approve", res.Decision)
	assert.Equal(t, "稳定", res.Reasoning)
	assert.Equal(t, 1000, res.TokenIn)
	assert.NotEmpty(t, res.PromptHash)

	require.Len(t, repo.inserted, 1)
	got := repo.inserted[0]
	assert.Equal(t, int64(42), got.SignalID)
	require.NotNil(t, got.Score)
	assert.Equal(t, 85, *got.Score)
	assert.Equal(t, "approve", got.Decision)
}

func TestLLMScorer_Abandon(t *testing.T) {
	llm := &fakeLLM{text: `{"score":30,"decision":"abandon","reasoning":"连亏"}`}
	repo := &fakeEvalRepo{}
	s := makeScorer(llm, repo, 1)
	res, _ := s.Score(context.Background(), fixedInput())
	assert.Equal(t, "abandon", res.Decision)
	assert.Equal(t, 30, res.Score)
}

func TestLLMScorer_NetworkError_ReturnsFailedAndPersists(t *testing.T) {
	llm := &fakeLLM{err: errors.New("connection refused")}
	repo := &fakeEvalRepo{}
	s := makeScorer(llm, repo, 1)
	res, err := s.Score(context.Background(), fixedInput())
	require.NoError(t, err, "Score must never bubble error to caller")
	assert.Equal(t, "failed", res.Decision)
	assert.Equal(t, -1, res.Score)
	assert.Contains(t, res.Reasoning, "connection refused")
	require.Len(t, repo.inserted, 1)
	assert.Equal(t, "failed", repo.inserted[0].Decision)
	assert.Nil(t, repo.inserted[0].Score)
}

func TestLLMScorer_NonJSONResponse_ReturnsFailedKeepsRaw(t *testing.T) {
	llm := &fakeLLM{text: "Sure, the score is around 80 I think"}
	repo := &fakeEvalRepo{}
	s := makeScorer(llm, repo, 1)
	res, _ := s.Score(context.Background(), fixedInput())
	assert.Equal(t, "failed", res.Decision)
	assert.Equal(t, -1, res.Score)
	require.Len(t, repo.inserted, 1)
	require.NotNil(t, repo.inserted[0].ResponseRaw)
	assert.Contains(t, *repo.inserted[0].ResponseRaw, "Sure, the score")
}

func TestLLMScorer_MissingFields_ReturnsFailed(t *testing.T) {
	llm := &fakeLLM{text: `{"score":75}`}
	repo := &fakeEvalRepo{}
	s := makeScorer(llm, repo, 1)
	res, _ := s.Score(context.Background(), fixedInput())
	assert.Equal(t, "failed", res.Decision)
	assert.Contains(t, res.Reasoning, "missing")
}

func TestLLMScorer_OutOfRangeScoreFails(t *testing.T) {
	llm := &fakeLLM{text: `{"score":150,"decision":"approve","reasoning":"x"}`}
	repo := &fakeEvalRepo{}
	s := makeScorer(llm, repo, 1)
	res, _ := s.Score(context.Background(), fixedInput())
	assert.Equal(t, "failed", res.Decision)
	assert.Contains(t, res.Reasoning, "out of")
}

func TestLLMScorer_BadDecisionFails(t *testing.T) {
	llm := &fakeLLM{text: `{"score":50,"decision":"maybe","reasoning":"x"}`}
	repo := &fakeEvalRepo{}
	s := makeScorer(llm, repo, 1)
	res, _ := s.Score(context.Background(), fixedInput())
	assert.Equal(t, "failed", res.Decision)
	assert.Contains(t, res.Reasoning, "approve|abandon")
}

func TestLLMScorer_HealthRecordsSuccess(t *testing.T) {
	llm := &fakeLLM{text: `{"score":50,"decision":"approve","reasoning":"x"}`}
	repo := &fakeEvalRepo{}
	s := makeScorer(llm, repo, 1)
	_, _ = s.Score(context.Background(), fixedInput())
	bad, fails, total := s.health.IsUnhealthy()
	assert.False(t, bad)
	assert.Equal(t, 0, fails)
	assert.Equal(t, 1, total)
}

func TestLLMScorer_HealthRecordsFailure(t *testing.T) {
	repo := &fakeEvalRepo{}
	s := makeScorer(&fakeLLM{err: errors.New("net err")}, repo, 1)
	for i := 0; i < 5; i++ {
		_, _ = s.Score(context.Background(), fixedInput())
	}
	_, fails, total := s.health.IsUnhealthy()
	assert.Equal(t, 5, fails)
	assert.Equal(t, 5, total)
}

func TestLLMScorer_HistoryJSONInEval(t *testing.T) {
	llm := &fakeLLM{text: `{"score":50,"decision":"approve","reasoning":"x"}`}
	repo := &fakeEvalRepo{}
	s := makeScorer(llm, repo, 1)
	_, _ = s.Score(context.Background(), fixedInput())
	require.Len(t, repo.inserted, 1)
	var snap struct {
		Sym  []map[string]any `json:"symbol_history"`
		Stra []map[string]any `json:"strategy_history"`
	}
	require.NoError(t, json.Unmarshal(repo.inserted[0].HistoryJSON, &snap))
	assert.NotNil(t, snap.Sym)
}

func TestFactory_WithSignalBindsParams(t *testing.T) {
	f := NewFactory(&fakeLLM{}, &fakeEvalRepo{}, nil, discardLog())
	sc := f.WithSignal(99, "claude-test", 1234)
	assert.Equal(t, int64(99), sc.signalID)
	assert.Equal(t, "claude-test", sc.model)
	assert.Equal(t, 1234, sc.timeoutMs)
	assert.NotNil(t, sc.health, "health tracker must be the factory's shared instance")
	assert.Same(t, f.health, sc.health)
}

func TestLLMScorer_AcceptsMarkdownFencedJSON(t *testing.T) {
	// Real-world Anthropic responses commonly wrap "raw" JSON in ```json ... ```
	// fences even when the prompt explicitly forbids it. The scorer must extract
	// the inner object and parse cleanly, NOT report failure.
	llm := &fakeLLM{
		text: "```json\n{\"score\":75,\"decision\":\"approve\",\"reasoning\":\"x\"}\n```",
	}
	repo := &fakeEvalRepo{}
	s := makeScorer(llm, repo, 1)
	res, err := s.Score(context.Background(), fixedInput())
	require.NoError(t, err)
	assert.Equal(t, 75, res.Score, "must parse through markdown fence")
	assert.Equal(t, "approve", res.Decision)
}

func TestLLMScorer_AcceptsJSONWithPreamble(t *testing.T) {
	// Some replies have a sentence of preamble before the JSON.
	llm := &fakeLLM{
		text: `Sure, here is my analysis: {"score":42,"decision":"abandon","reasoning":"weak"}`,
	}
	repo := &fakeEvalRepo{}
	s := makeScorer(llm, repo, 1)
	res, _ := s.Score(context.Background(), fixedInput())
	assert.Equal(t, 42, res.Score)
	assert.Equal(t, "abandon", res.Decision)
}

func TestExtractJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"score":1}`, `{"score":1}`},
		{"```json\n{\"score\":1}\n```", `{"score":1}`},
		{"some preface text {\"a\":1} trailing", `{"a":1}`},
		{`no json here`, `no json here`}, // unchanged → caller's json.Unmarshal will fail and trigger 'failed' decision
	}
	for _, c := range cases {
		assert.Equal(t, c.want, ExtractJSON(c.in), "input: %q", c.in)
	}
}
