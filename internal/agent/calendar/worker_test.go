package calendar

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

type stubFetcher struct {
	mu     sync.Mutex
	calls  int
	events []Event
	err    error
}

func (s *stubFetcher) Fetch(ctx context.Context) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.events, s.err
}

type stubSink struct {
	mu    sync.Mutex
	saved [][]Event
}

func (s *stubSink) SaveBatch(ctx context.Context, evs []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = append(s.saved, evs)
	return nil
}

type stubCalSettings struct{ enabled bool }

func (s *stubCalSettings) Read(ctx context.Context) (WorkerSettings, error) {
	return WorkerSettings{Enabled: s.enabled}, nil
}

func TestCalendarWorker_RunOnceFetchesAndSavesWhenEnabled(t *testing.T) {
	fetcher := &stubFetcher{events: []Event{{Name: "CPI"}, {Name: "FOMC"}}}
	sink := &stubSink{}
	w := NewWorker(fetcher, sink, &stubCalSettings{enabled: true}, zerolog.Nop())
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if fetcher.calls != 1 {
		t.Errorf("fetch calls: %d", fetcher.calls)
	}
	if len(sink.saved) != 1 || len(sink.saved[0]) != 2 {
		t.Errorf("saved: %+v", sink.saved)
	}
}

func TestCalendarWorker_RunOnceSkipsWhenDisabled(t *testing.T) {
	fetcher := &stubFetcher{}
	sink := &stubSink{}
	w := NewWorker(fetcher, sink, &stubCalSettings{enabled: false}, zerolog.Nop())
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if fetcher.calls != 0 || len(sink.saved) != 0 {
		t.Errorf("disabled but fetcher.calls=%d saves=%d", fetcher.calls, len(sink.saved))
	}
}

func TestCalendarWorker_FetchErrorDoesNotSave(t *testing.T) {
	fetcher := &stubFetcher{err: errors.New("ff 503")}
	sink := &stubSink{}
	w := NewWorker(fetcher, sink, &stubCalSettings{enabled: true}, zerolog.Nop())
	if err := w.RunOnce(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if len(sink.saved) != 0 {
		t.Errorf("should not save on fetch error")
	}
}

func TestCalendarWorker_StartCancellable(t *testing.T) {
	fetcher := &stubFetcher{}
	sink := &stubSink{}
	w := NewWorker(fetcher, sink, &stubCalSettings{enabled: true}, zerolog.Nop()).WithInterval(time.Hour)
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
