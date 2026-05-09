package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"text/template"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
)

// fakeLLM is a stub LLMClient driven by injected outputs.
type fakeLLM struct {
	resp scorer.CompleteResponse
	err  error
}

func (f *fakeLLM) Complete(_ context.Context, _ scorer.CompleteRequest) (scorer.CompleteResponse, error) {
	return f.resp, f.err
}

func sampleCase(t *testing.T) ReplayCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/history_sample.json")
	require.NoError(t, err)
	pnl := -8.5
	return ReplayCase{
		SignalID:      1247,
		StrategyID:    "supertrend-eth",
		Symbol:        "ETHUSDC",
		Kind:          "long",
		SignalPrice:   decimal.RequireFromString("2300.50"),
		TVTimestampMs: 1714723504000,
		OldScore:      72,
		OldDecision:   "approve",
		OldReason:     "old reason text",
		HistoryJSON:   raw,
		PnLUSDC:       &pnl,
		HasPnL:        true,
	}
}

func TestReplayOne_Success(t *testing.T) {
	c := sampleCase(t)
	tmpl := template.Must(template.New("v2").Parse(
		"S={{.StrategyID}} P={{.Signal.Price}}"))
	llm := &fakeLLM{resp: scorer.CompleteResponse{
		Text:    `{"score":38,"decision":"abandon","reasoning":"new reason"}`,
		TokenIn: 100, TokenOut: 20,
	}}
	row := replayOne(context.Background(), c, tmpl, llm, scorer.DefaultModel, 5000)
	assert.Empty(t, row.Error)
	assert.Equal(t, int64(1247), row.SignalID)
	assert.Equal(t, 72, row.OldScore)
	assert.Equal(t, 38, row.NewScore)
	assert.Equal(t, "abandon", row.NewDecision)
	assert.Equal(t, "new reason", row.NewReason)
	assert.Equal(t, true, row.HasPnL)
	assert.NotNil(t, row.PnLUSDC)
	assert.InDelta(t, -8.5, *row.PnLUSDC, 1e-9)
}

func TestReplayOne_LLMError(t *testing.T) {
	c := sampleCase(t)
	tmpl := template.Must(template.New("v2").Parse("noop"))
	llm := &fakeLLM{err: errors.New("connection refused")}
	row := replayOne(context.Background(), c, tmpl, llm, scorer.DefaultModel, 5000)
	assert.NotEmpty(t, row.Error)
	assert.Contains(t, row.Error, "connection refused")
}

func TestReplayOne_BadJSON(t *testing.T) {
	c := sampleCase(t)
	tmpl := template.Must(template.New("v2").Parse("noop"))
	llm := &fakeLLM{resp: scorer.CompleteResponse{Text: "not json at all"}}
	row := replayOne(context.Background(), c, tmpl, llm, scorer.DefaultModel, 5000)
	assert.NotEmpty(t, row.Error)
}

func TestReplayOne_ScoreOutOfRange(t *testing.T) {
	c := sampleCase(t)
	tmpl := template.Must(template.New("v2").Parse("noop"))
	llm := &fakeLLM{resp: scorer.CompleteResponse{
		Text: `{"score":150,"decision":"approve","reasoning":"x"}`,
	}}
	row := replayOne(context.Background(), c, tmpl, llm, scorer.DefaultModel, 5000)
	assert.NotEmpty(t, row.Error)
	assert.Contains(t, row.Error, "score")
}

func TestReplayOne_BadHistoryJSON(t *testing.T) {
	c := sampleCase(t)
	c.HistoryJSON = []byte("{not valid json}")
	tmpl := template.Must(template.New("v2").Parse("noop"))
	llm := &fakeLLM{}
	row := replayOne(context.Background(), c, tmpl, llm, scorer.DefaultModel, 5000)
	assert.NotEmpty(t, row.Error)
	assert.Contains(t, row.Error, "history_json")
}

func TestReplayOne_TemplateExecError(t *testing.T) {
	c := sampleCase(t)
	tmpl := template.Must(template.New("v2").Parse("{{.Nonexistent}}"))
	tmpl.Option("missingkey=error")
	llm := &fakeLLM{}
	row := replayOne(context.Background(), c, tmpl, llm, scorer.DefaultModel, 5000)
	assert.NotEmpty(t, row.Error)
}

func TestReplayOne_BadDecision(t *testing.T) {
	c := sampleCase(t)
	tmpl := template.Must(template.New("v2").Parse("noop"))
	llm := &fakeLLM{resp: scorer.CompleteResponse{
		Text: `{"score":50,"decision":"maybe","reasoning":"x"}`,
	}}
	row := replayOne(context.Background(), c, tmpl, llm, scorer.DefaultModel, 5000)
	assert.NotEmpty(t, row.Error)
	assert.Contains(t, row.Error, "decision")
}

func TestReplayOne_MissingFields(t *testing.T) {
	c := sampleCase(t)
	tmpl := template.Must(template.New("v2").Parse("noop"))
	// Valid JSON but missing required fields (no "score" / "decision" / "reasoning").
	llm := &fakeLLM{resp: scorer.CompleteResponse{
		Text: `{"foo":"bar"}`,
	}}
	row := replayOne(context.Background(), c, tmpl, llm, scorer.DefaultModel, 5000)
	assert.NotEmpty(t, row.Error)
	assert.Contains(t, row.Error, "missing fields")
}
