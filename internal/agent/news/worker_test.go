package news

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/eval"
)

type stubNewsFetcher struct {
	mu    sync.Mutex
	calls int
	out   []Headline
	err   error
}

func (s *stubNewsFetcher) Fetch(ctx context.Context, topN int) ([]Headline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.out, s.err
}

type stubNewsClassifier struct {
	out Classification
	err error
}

func (s *stubNewsClassifier) Classify(ctx context.Context, hs []Headline) (Classification, error) {
	return s.out, s.err
}

type stubNewsPersistor struct {
	mu         sync.Mutex
	successCnt int
	failureCnt int
}

func (s *stubNewsPersistor) PersistSuccess(ctx context.Context, c Classification) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.successCnt++
	return int64(s.successCnt + s.failureCnt), nil
}

func (s *stubNewsPersistor) PersistFailure(ctx context.Context, c Classification, err error) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failureCnt++
	return int64(s.successCnt + s.failureCnt), nil
}

type capturedPublisher struct {
	mu     sync.Mutex
	events []eval.EvalEvent
}

func (p *capturedPublisher) Publish(e eval.EvalEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
}

type stubNewsSettings struct {
	enabled  bool
	interval int
	topN     int
}

func (s *stubNewsSettings) Read(ctx context.Context) (WorkerSettings, error) {
	return WorkerSettings{Enabled: s.enabled, IntervalMin: s.interval, TopN: s.topN}, nil
}

func TestNewsWorker_HappyPath(t *testing.T) {
	fetcher := &stubNewsFetcher{out: []Headline{{Title: "A"}}}
	classifier := &stubNewsClassifier{out: Classification{Impact: "high", MeasuredAt: time.Now()}}
	persistor := &stubNewsPersistor{}
	publisher := &capturedPublisher{}
	w := NewWorker(fetcher, classifier, persistor, publisher,
		&stubNewsSettings{enabled: true, interval: 15, topN: 5}, zerolog.Nop())
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if persistor.successCnt != 1 || persistor.failureCnt != 0 {
		t.Errorf("persist counts: success=%d failure=%d", persistor.successCnt, persistor.failureCnt)
	}
	if len(publisher.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(publisher.events))
	}
	ev := publisher.events[0]
	if ev.Kind != "news_alert" || ev.Impact != "high" {
		t.Errorf("event: %+v", ev)
	}
	if ev.SnapshotID == 0 {
		t.Errorf("SnapshotID should be set")
	}
}

func TestNewsWorker_DisabledSkipsEverything(t *testing.T) {
	fetcher := &stubNewsFetcher{}
	classifier := &stubNewsClassifier{}
	persistor := &stubNewsPersistor{}
	publisher := &capturedPublisher{}
	w := NewWorker(fetcher, classifier, persistor, publisher,
		&stubNewsSettings{enabled: false, interval: 15, topN: 5}, zerolog.Nop())
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if fetcher.calls != 0 || persistor.successCnt != 0 || persistor.failureCnt != 0 || len(publisher.events) != 0 {
		t.Errorf("disabled but state mutated: fetcher.calls=%d success=%d failure=%d events=%d",
			fetcher.calls, persistor.successCnt, persistor.failureCnt, len(publisher.events))
	}
}

func TestNewsWorker_FetchFailure_WritesFailureRow(t *testing.T) {
	fetcher := &stubNewsFetcher{err: errors.New("rate limit")}
	classifier := &stubNewsClassifier{}
	persistor := &stubNewsPersistor{}
	publisher := &capturedPublisher{}
	w := NewWorker(fetcher, classifier, persistor, publisher,
		&stubNewsSettings{enabled: true, interval: 15, topN: 5}, zerolog.Nop())
	if err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("expected error returned to caller")
	}
	if persistor.failureCnt != 1 || persistor.successCnt != 0 {
		t.Errorf("failure not persisted: success=%d failure=%d", persistor.successCnt, persistor.failureCnt)
	}
	if len(publisher.events) != 1 || publisher.events[0].Impact != "none" {
		t.Errorf("expected 1 news_alert impact=none, got %+v", publisher.events)
	}
}

func TestNewsWorker_ClassifierFailure_WritesFailureRow(t *testing.T) {
	fetcher := &stubNewsFetcher{out: []Headline{{Title: "X"}}}
	classifier := &stubNewsClassifier{err: errors.New("llm timeout"), out: Classification{MeasuredAt: time.Now()}}
	persistor := &stubNewsPersistor{}
	publisher := &capturedPublisher{}
	w := NewWorker(fetcher, classifier, persistor, publisher,
		&stubNewsSettings{enabled: true, interval: 15, topN: 5}, zerolog.Nop())
	if err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if persistor.failureCnt != 1 || persistor.successCnt != 0 {
		t.Errorf("counts: %+v", persistor)
	}
	if len(publisher.events) != 1 || publisher.events[0].Impact != "none" {
		t.Errorf("event: %+v", publisher.events)
	}
}

func TestNewsWorker_StartCancellable(t *testing.T) {
	w := NewWorker(&stubNewsFetcher{},
		&stubNewsClassifier{},
		&stubNewsPersistor{},
		&capturedPublisher{},
		&stubNewsSettings{enabled: false, interval: 15, topN: 5},
		zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Start(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Start did not return")
	}
}
