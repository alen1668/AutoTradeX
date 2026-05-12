package critique

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
)

type fakeLLM struct {
	text string
	err  error
}

func (f *fakeLLM) Complete(_ context.Context, _ scorer.CompleteRequest) (scorer.CompleteResponse, error) {
	if f.err != nil {
		return scorer.CompleteResponse{}, f.err
	}
	return scorer.CompleteResponse{Text: f.text, TokenIn: 100, TokenOut: 200}, nil
}

type fakeData struct {
	aggs []AggregateRow
	dets []DetailRow
	prev string
}

func (f *fakeData) Aggregates(context.Context, time.Time, time.Time) ([]AggregateRow, error) {
	return f.aggs, nil
}
func (f *fakeData) Details(context.Context, time.Time, time.Time, int) ([]DetailRow, error) {
	return f.dets, nil
}
func (f *fakeData) PreviousSummary(context.Context) (string, error) { return f.prev, nil }

type fakeStore struct {
	inserted *Critique
	patterns []Pattern
}

func (f *fakeStore) Insert(_ context.Context, c Critique, ps []Pattern) (int64, error) {
	f.inserted = &c
	f.patterns = ps
	return 7, nil
}

func TestAgent_Run_HappyPath(t *testing.T) {
	llm := &fakeLLM{text: `{"summary":"近期模型在 trend 下高估做多","patterns":[
		{"id":"p1","title":"trend 高估做多","evidence_signal_ids":[1,2,3],
		 "stats":{"approve_count":5,"win_rate":0.2},
		 "suggestion_for_prompt":"trend + funding>0.05 扣 15 分","confidence":"high"}
	]}`}
	data := &fakeData{dets: make([]DetailRow, 30)} // 30 samples ≥ min_sample(20)
	st := &fakeStore{}
	a := NewAgent(llm, data, st, Config{
		Model: "claude-haiku-4-5", WindowDays: 7, MinSample: 20, TimeoutMs: 30000,
	}, nil)

	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.inserted == nil {
		t.Fatal("expected insert")
	}
	if st.inserted.Status != StatusDone {
		t.Fatalf("want done, got %s", st.inserted.Status)
	}
	if len(st.patterns) != 1 || st.patterns[0].ID != "p1" {
		t.Fatalf("pattern parse failed: %+v", st.patterns)
	}
	if st.inserted.Summary == nil || *st.inserted.Summary == "" {
		t.Fatalf("summary not propagated: %+v", st.inserted.Summary)
	}
}

func TestAgent_Run_InsufficientSample(t *testing.T) {
	data := &fakeData{dets: make([]DetailRow, 5)} // < min_sample
	st := &fakeStore{}
	a := NewAgent(&fakeLLM{}, data, st, Config{
		MinSample: 20, WindowDays: 7,
	}, nil)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.inserted == nil || st.inserted.Status != StatusInsufficientSample {
		t.Fatalf("want insufficient_sample, got %v", st.inserted)
	}
}

func TestAgent_Run_LLMError(t *testing.T) {
	llm := &fakeLLM{err: errors.New("timeout")}
	data := &fakeData{dets: make([]DetailRow, 30)}
	st := &fakeStore{}
	a := NewAgent(llm, data, st, Config{
		MinSample: 20, WindowDays: 7, TimeoutMs: 1000,
	}, nil)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.inserted == nil || st.inserted.Status != StatusFailed {
		t.Fatalf("want failed, got %v", st.inserted)
	}
	if st.inserted.ErrorMessage == nil {
		t.Fatal("error_message not set")
	}
}

func TestAgent_Run_BadJSON(t *testing.T) {
	llm := &fakeLLM{text: "not json"}
	data := &fakeData{dets: make([]DetailRow, 30)}
	st := &fakeStore{}
	a := NewAgent(llm, data, st, Config{
		MinSample: 20, WindowDays: 7, TimeoutMs: 1000,
	}, nil)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.inserted == nil || st.inserted.Status != StatusFailed {
		t.Fatalf("want failed, got %v", st.inserted)
	}
}

func TestAgent_Run_BadConfidence(t *testing.T) {
	llm := &fakeLLM{text: `{"summary":"x","patterns":[{"id":"p1","title":"t","evidence_signal_ids":[1,2,3],"stats":{},"suggestion_for_prompt":"s","confidence":"bogus"}]}`}
	data := &fakeData{dets: make([]DetailRow, 30)}
	st := &fakeStore{}
	a := NewAgent(llm, data, st, Config{MinSample: 20, WindowDays: 7, TimeoutMs: 1000}, nil)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.inserted == nil || st.inserted.Status != StatusFailed {
		t.Fatalf("invalid confidence should mark failed, got %v", st.inserted)
	}
}

func TestAgent_Run_DuplicatePatternID(t *testing.T) {
	llm := &fakeLLM{text: `{"summary":"x","patterns":[
		{"id":"p1","title":"t1","evidence_signal_ids":[1,2,3],"stats":{},"suggestion_for_prompt":"s","confidence":"high"},
		{"id":"p1","title":"t2","evidence_signal_ids":[4,5,6],"stats":{},"suggestion_for_prompt":"s","confidence":"low"}
	]}`}
	data := &fakeData{dets: make([]DetailRow, 30)}
	st := &fakeStore{}
	a := NewAgent(llm, data, st, Config{MinSample: 20, WindowDays: 7, TimeoutMs: 1000}, nil)
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.inserted == nil || st.inserted.Status != StatusFailed {
		t.Fatalf("dup id should mark failed, got %v", st.inserted)
	}
}
