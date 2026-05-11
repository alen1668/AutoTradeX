package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store wraps the replay_runs / replay_run_rows CRUD.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by the given pool. The pool must connect
// to a database where migrations through 0009_replay_runs.sql have been
// applied.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// CreateRun inserts a new replay_runs row and returns the assigned ID.
// Status defaults to 'pending' if empty.
func (s *Store) CreateRun(ctx context.Context, r ReplayRun) (int64, error) {
	if r.Status == "" {
		r.Status = "pending"
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO replay_runs (
  since_window, since_cutoff, max_n, concurrency, model,
  prompt_text, prompt_name, prompt_sha256, status
) VALUES ($1, to_timestamp($2), $3, $4, $5, $6, $7, $8, $9)
RETURNING id`,
		r.SinceWindow, r.SinceCutoff, r.MaxN, r.Concurrency, r.Model,
		r.PromptText, r.PromptName, r.PromptSHA256, r.Status,
	).Scan(&id)
	return id, err
}

// GetRun loads one run by id. Returns (nil, nil) when no row matches.
// Summary is decoded from summary_json (nil when column is NULL).
func (s *Store) GetRun(ctx context.Context, id int64) (*ReplayRun, error) {
	row := s.pool.QueryRow(ctx, getRunSelect+`WHERE id = $1`, id)
	r, err := scanRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRuns returns up to limit rows where id < cursor (or all when cursor=0),
// ordered newest first. Second return is the next cursor (0 if exhausted).
func (s *Store) ListRuns(ctx context.Context, cursor int64, limit int) ([]ReplayRun, int64, error) {
	q := getRunSelect
	args := []any{limit}
	if cursor > 0 {
		q += `WHERE id < $2 `
		args = append(args, cursor)
	}
	q += `ORDER BY id DESC LIMIT $1`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []ReplayRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var next int64
	if len(out) == limit && len(out) > 0 {
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

// MarkRunRunning sets status=running, started_at=now(), samples_total=N.
func (s *Store) MarkRunRunning(ctx context.Context, runID int64, samplesTotal int) error {
	_, err := s.pool.Exec(ctx, `
UPDATE replay_runs
   SET status='running', started_at=now(), samples_total=$2
 WHERE id=$1`, runID, samplesTotal)
	return err
}

// MarkRunDone sets status=done, finished_at=now(), writes summary_json,
// and updates samples_done / samples_failed.
func (s *Store) MarkRunDone(ctx context.Context, runID int64, summary *ReplayReport, done, failed int) error {
	raw, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
UPDATE replay_runs
   SET status='done', finished_at=now(),
       samples_done=$2, samples_failed=$3, summary_json=$4
 WHERE id=$1`, runID, done, failed, raw)
	return err
}

// MarkRunFailed sets status=failed and error_message.
func (s *Store) MarkRunFailed(ctx context.Context, runID int64, msg string) error {
	_, err := s.pool.Exec(ctx, `
UPDATE replay_runs
   SET status='failed', finished_at=now(), error_message=$2
 WHERE id=$1`, runID, msg)
	return err
}

// InsertRow appends one replay_run_rows entry. Subject to UNIQUE(run_id, signal_id).
// NULL columns (replay_score / replay_decision / replay_reason / error_kind)
// are set based on whether row.Error is non-empty.
func (s *Store) InsertRow(ctx context.Context, runID int64, row ReplayRow) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO replay_run_rows (
  run_id, signal_id, replay_score, replay_decision, replay_reason,
  prod_score, prod_decision, pnl_usdc, error_kind
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		runID, row.SignalID,
		nullableInt(row.NewScore, row.Error),
		nullableStr(row.NewDecision, row.Error),
		nullableStr(row.NewReason, row.Error),
		row.OldScore, nullableStr(row.OldDecision, ""),
		row.PnLUSDC,
		errorKindOf(row.Error),
	)
	return err
}

// ListStaleRunning returns runs with status='running' AND started_at older
// than `olderThan`. Used by `cmd/agent-eval` startup to surface zombies.
func (s *Store) ListStaleRunning(ctx context.Context, olderThan time.Duration) ([]ReplayRun, error) {
	rows, err := s.pool.Query(ctx, getRunSelect+`
WHERE status='running' AND started_at < now() - make_interval(secs => $1)
ORDER BY started_at`, int(olderThan.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReplayRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRows returns up to `limit` rows from a run, ordered by
// |replay_score - prod_score| DESC. Errors keep error_kind set; replay
// score columns are nil for those.
func (s *Store) ListRows(ctx context.Context, runID int64, limit int) ([]ReplayRow, error) {
	rows, err := s.pool.Query(ctx, `
SELECT signal_id, replay_score, replay_decision, replay_reason,
       prod_score, prod_decision, pnl_usdc, error_kind
FROM replay_run_rows
WHERE run_id = $1
ORDER BY ABS(COALESCE(replay_score, 0) - COALESCE(prod_score, 0)) DESC
LIMIT $2`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReplayRow
	for rows.Next() {
		var r ReplayRow
		var replayScore, prodScore *int
		var replayDec, replayReason, prodDec, errKind *string
		if err := rows.Scan(&r.SignalID, &replayScore, &replayDec, &replayReason,
			&prodScore, &prodDec, &r.PnLUSDC, &errKind); err != nil {
			return nil, err
		}
		if replayScore != nil {
			r.NewScore = *replayScore
		}
		if prodScore != nil {
			r.OldScore = *prodScore
		}
		if replayDec != nil {
			r.NewDecision = *replayDec
		}
		if replayReason != nil {
			r.NewReason = *replayReason
		}
		if prodDec != nil {
			r.OldDecision = *prodDec
		}
		if errKind != nil {
			r.Error = *errKind
		}
		r.HasPnL = r.PnLUSDC != nil
		out = append(out, r)
	}
	return out, rows.Err()
}

// getRunSelect is the SELECT clause shared by GetRun / ListRuns / ListStaleRunning.
// Note the trailing space: callers can append further clauses without worrying
// about token boundaries.
const getRunSelect = `
SELECT id,
       extract(epoch from created_at)::bigint,
       since_window,
       extract(epoch from since_cutoff)::bigint,
       max_n, concurrency, model, prompt_text, prompt_name, prompt_sha256, status,
       extract(epoch from started_at)::bigint,
       extract(epoch from finished_at)::bigint,
       error_message,
       samples_total, samples_done, samples_failed,
       summary_json
FROM replay_runs `

// rowScanner is satisfied by both pgx.Row (single) and pgx.Rows (iter).
type rowScanner interface {
	Scan(...any) error
}

func scanRun(rs rowScanner) (ReplayRun, error) {
	var r ReplayRun
	var started, finished *float64
	var summary []byte
	if err := rs.Scan(
		&r.ID, &r.CreatedAt, &r.SinceWindow, &r.SinceCutoff,
		&r.MaxN, &r.Concurrency, &r.Model, &r.PromptText, &r.PromptName,
		&r.PromptSHA256, &r.Status, &started, &finished, &r.ErrorMessage,
		&r.SamplesTotal, &r.SamplesDone, &r.SamplesFailed, &summary,
	); err != nil {
		return r, err
	}
	if started != nil {
		v := int64(*started)
		r.StartedAt = &v
	}
	if finished != nil {
		v := int64(*finished)
		r.FinishedAt = &v
	}
	if len(summary) > 0 {
		var rep ReplayReport
		if err := json.Unmarshal(summary, &rep); err == nil {
			r.Summary = &rep
		}
	}
	return r, nil
}

func nullableInt(v int, errStr string) any {
	if errStr != "" {
		return nil
	}
	return v
}

func nullableStr(v string, errStr string) any {
	if errStr != "" || v == "" {
		return nil
	}
	return v
}

// errorKindOf extracts a stable token from row.Error: the prefix before
// the first colon if present, else the full string. ReplayOne fills Error
// with strings like "history_json: ...", "llm: ...", "parse: missing fields",
// "score 150 out of [0,100]", etc.
func errorKindOf(err string) any {
	if err == "" {
		return nil
	}
	for i := 0; i < len(err); i++ {
		if err[i] == ':' {
			return err[:i]
		}
	}
	return err
}
