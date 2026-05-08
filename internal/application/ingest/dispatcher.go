package ingest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/notify"
)

// Processor is the slow-path interface the dispatcher schedules onto.
// Service implements this via Process().
type Processor interface {
	Process(ctx context.Context, signalID int64) error
}

// queueBufferSize bounds how many signals can pile up per strategy.
// Sized for paranoia (1024 ≈ 17 minutes of one-signal-per-second), not
// throughput — TradingView strategies emit at the bar boundary so any
// realistic burst is far smaller.
const queueBufferSize = 1024

// processTimeout is the per-signal worker deadline. Big enough for a
// 4-leg Binance call sequence (entry market + stop + backup_stop +
// take_profit) under network jitter.
const processTimeout = 30 * time.Second

// Dispatcher fans signals out to per-strategy worker goroutines so
// different strategies process in parallel while signals sharing a
// strategy_id stay strictly FIFO. Submit is non-blocking; if the
// queue is full or the dispatcher is shutting down, the signal is
// processed synchronously as a degraded fallback (still logged + alerted).
type Dispatcher struct {
	proc     Processor
	notifier notify.Notifier
	log      zerolog.Logger

	queues sync.Map // strategyID(string) → chan int64

	wg     sync.WaitGroup
	closed atomic.Bool
}

// NewDispatcher builds a dispatcher backed by the given processor.
func NewDispatcher(proc Processor, n notify.Notifier, log zerolog.Logger) *Dispatcher {
	return &Dispatcher{proc: proc, notifier: n, log: log}
}

// Submit hands signalID to the worker for strategyID. Non-blocking: if
// the queue is full or the dispatcher is shutting down it falls back
// to running Process synchronously and logs a critical alert. Never
// drops a signal.
func (d *Dispatcher) Submit(strategyID string, signalID int64) {
	if d.closed.Load() {
		d.runSync(strategyID, signalID, "dispatcher closed")
		return
	}
	q := d.queueFor(strategyID)
	select {
	case q <- signalID:
		// enqueued
	default:
		d.runSync(strategyID, signalID, "queue full")
	}
}

// queueFor returns (and lazily creates) the per-strategy channel + worker.
func (d *Dispatcher) queueFor(strategyID string) chan int64 {
	if existing, ok := d.queues.Load(strategyID); ok {
		return existing.(chan int64)
	}
	ch := make(chan int64, queueBufferSize)
	actual, loaded := d.queues.LoadOrStore(strategyID, ch)
	if loaded {
		return actual.(chan int64)
	}
	d.wg.Add(1)
	go d.run(strategyID, ch)
	return ch
}

func (d *Dispatcher) run(strategyID string, ch chan int64) {
	defer d.wg.Done()
	for signalID := range ch {
		d.processOne(strategyID, signalID)
	}
}

func (d *Dispatcher) processOne(strategyID string, signalID int64) {
	defer func() {
		if r := recover(); r != nil {
			d.log.Error().Interface("panic", r).
				Str("strategy_id", strategyID).Int64("signal_id", signalID).
				Msg("dispatcher: panic in Process")
			_ = d.notifier.Send(context.Background(), notify.Message{
				Title:    "❌ 信号处理 panic",
				Body:     fmt.Sprintf("strategy=%s signal_id=%d panic=%v", strategyID, signalID, r),
				Severity: notify.SeverityCritical,
			})
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()
	if err := d.proc.Process(ctx, signalID); err != nil {
		d.log.Warn().Err(err).Str("strategy_id", strategyID).Int64("signal_id", signalID).
			Msg("dispatcher: Process returned error")
	}
}

// runSync is the degraded path for full queues / closed dispatcher. We
// still process the signal so it isn't lost.
func (d *Dispatcher) runSync(strategyID string, signalID int64, why string) {
	d.log.Warn().Str("strategy_id", strategyID).Int64("signal_id", signalID).
		Str("reason", why).Msg("dispatcher: synchronous fallback")
	_ = d.notifier.Send(context.Background(), notify.Message{
		Title: "⚠️ 信号同步降级",
		Body: fmt.Sprintf("strategy=%s signal_id=%d reason=%s — 调用方等待中,影响响应延迟",
			strategyID, signalID, why),
		Severity: notify.SeverityWarn,
	})
	d.processOne(strategyID, signalID)
}

// Shutdown stops accepting new signals and waits up to ctx deadline for
// in-flight workers to finish. Signals still buffered in channels at
// deadline stay 'pending' in DB; startup recovery on next boot picks
// them up.
func (d *Dispatcher) Shutdown(ctx context.Context) error {
	if !d.closed.CompareAndSwap(false, true) {
		return nil
	}
	d.queues.Range(func(_, v any) bool {
		close(v.(chan int64))
		return true
	})
	done := make(chan struct{})
	go func() { d.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("dispatcher shutdown: %w", ctx.Err())
	}
}
