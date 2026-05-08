package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/store"
)

// Submitter is the dispatcher contract — kept tiny so this file doesn't
// depend on the ingest package (avoids import cycles).
type Submitter interface {
	Submit(strategyID string, signalID int64)
}

// SignalRecovery handles restart-time recovery of the signals queue.
// Pending signals younger than `freshness` are re-enqueued onto the
// dispatcher in FIFO order; older pending signals are flipped to
// 'abandoned' with a critical Lark/Telegram alert so the operator
// knows to manually evaluate them.
type SignalRecovery struct {
	pool       *pgxpool.Pool
	signalRepo *store.SignalRepo
	submitter  Submitter
	notifier   notify.Notifier
	log        zerolog.Logger
	freshness  time.Duration
}

func NewSignalRecovery(pool *pgxpool.Pool, signalRepo *store.SignalRepo,
	submitter Submitter, notifier notify.Notifier, log zerolog.Logger,
	freshness time.Duration) *SignalRecovery {
	return &SignalRecovery{
		pool:       pool,
		signalRepo: signalRepo,
		submitter:  submitter,
		notifier:   notifier,
		log:        log,
		freshness:  freshness,
	}
}

// Run executes the recovery once. Idempotent.
func (r *SignalRecovery) Run(ctx context.Context) error {
	r.log.Info().Msg("signal recovery: starting")
	cutoff := time.Now().UTC().Add(-r.freshness)

	young, err := r.signalRepo.ListPendingForRecovery(ctx, r.pool, cutoff)
	if err != nil {
		return fmt.Errorf("list pending young: %w", err)
	}
	for _, s := range young {
		r.log.Info().Int64("id", s.ID).Str("strategy", s.StrategyID).
			Msg("signal recovery: re-enqueuing")
		r.submitter.Submit(s.StrategyID, s.ID)
	}

	// Now find anything still pending older than cutoff. We pass time.Time{}
	// (zero) so the query returns ALL pending, then filter in-process by
	// received_at < cutoff.
	all, err := r.signalRepo.ListPendingForRecovery(ctx, r.pool, time.Time{})
	if err != nil {
		return fmt.Errorf("list pending all: %w", err)
	}
	var oldIDs []int64
	for _, s := range all {
		if s.ReceivedAt.Before(cutoff) {
			oldIDs = append(oldIDs, s.ID)
		}
	}
	if len(oldIDs) > 0 {
		reason := fmt.Sprintf(
			"startup recovery: signal pending more than %s, too old to safely process",
			r.freshness)
		if err := r.signalRepo.MarkAbandoned(ctx, r.pool, oldIDs, reason); err != nil {
			return fmt.Errorf("mark abandoned: %w", err)
		}
		_ = r.notifier.Send(ctx, notify.Message{
			Title: "⚠️ 启动恢复 信号被放弃",
			Body: fmt.Sprintf(
				"有 %d 条信号 pending 超过 %s 已标记 abandoned,请人工核对 /signals 页面",
				len(oldIDs), r.freshness),
			Severity: notify.SeverityCritical,
		})
	}

	r.log.Info().
		Int("enqueued", len(young)).Int("abandoned", len(oldIDs)).
		Msg("signal recovery: done")
	return nil
}
