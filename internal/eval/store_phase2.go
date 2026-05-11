package eval

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ClaimNextPending atomically picks the oldest pending run and flips it to
// running with started_at=now(). Uses FOR UPDATE SKIP LOCKED so future
// multi-instance worker pools never claim the same row twice.
//
// Returns (run, true, nil) when a row was claimed; (zero, false, nil) when
// the pending queue is empty; (zero, false, err) only on DB errors (caller
// logs and tries again next tick).
func (s *Store) ClaimNextPending(ctx context.Context) (ReplayRun, bool, error) {
	row := s.pool.QueryRow(ctx, `
UPDATE replay_runs
   SET status='running', started_at=now()
 WHERE id = (
     SELECT id FROM replay_runs
      WHERE status='pending'
      ORDER BY id ASC
      LIMIT 1
      FOR UPDATE SKIP LOCKED
 )
RETURNING id,
          extract(epoch from created_at)::bigint,
          since_window,
          extract(epoch from since_cutoff)::bigint,
          max_n, concurrency, model, prompt_text, prompt_name, prompt_sha256, status,
          extract(epoch from started_at)::bigint,
          extract(epoch from finished_at)::bigint,
          error_message,
          samples_total, samples_done, samples_failed,
          summary_json`)
	r, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReplayRun{}, false, nil
	}
	if err != nil {
		return ReplayRun{}, false, err
	}
	return r, true, nil
}

// AbortRunningRuns flips every status='running' row to 'aborted' with
// finished_at=now() and a synthetic error_message. Intended to be called
// once during server.Start to clear zombies from previous crashed runs.
// Returns the number of rows updated (0 when no zombies).
func (s *Store) AbortRunningRuns(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
UPDATE replay_runs
   SET status='aborted',
       finished_at=now(),
       error_message='process restart while running'
 WHERE status='running'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// UpdateProgress writes the latest cumulative samples_done / samples_failed
// onto a running run. Callers pass cumulative totals (not deltas) so a missed
// update during DB hiccup self-corrects on the next call.
func (s *Store) UpdateProgress(ctx context.Context, runID int64, done, failed int) error {
	_, err := s.pool.Exec(ctx, `
UPDATE replay_runs
   SET samples_done=$2, samples_failed=$3
 WHERE id=$1`, runID, done, failed)
	return err
}
