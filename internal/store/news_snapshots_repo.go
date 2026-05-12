package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewsSnapshotRecord mirrors one row of news_snapshots. per_headline and
// raw_headlines are stored as raw JSON bytes; callers (news.StoreAdapter)
// marshal/unmarshal at the boundary.
type NewsSnapshotRecord struct {
	ID           int64
	MeasuredAt   time.Time
	Impact       string
	Summary      string
	Reasoning    string
	PerHeadline  []byte
	RawHeadlines []byte
	PromptHash   string
	PromptText   string
	ResponseRaw  *string
	LLMModel     string
	LLMTokensIn  *int
	LLMTokensOut *int
	LLMLatencyMs *int
	ErrorMessage *string
}

// NewsSnapshotsRepo persists news_snapshots rows. Both success and failure
// runs are inserted (failure rows carry ErrorMessage != nil and Impact='none').
type NewsSnapshotsRepo struct {
	pool *pgxpool.Pool
}

func NewNewsSnapshotsRepo(pool *pgxpool.Pool) *NewsSnapshotsRepo {
	return &NewsSnapshotsRepo{pool: pool}
}

func (r *NewsSnapshotsRepo) Insert(ctx context.Context, q Querier, rec NewsSnapshotRecord) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
INSERT INTO news_snapshots
    (measured_at, impact, summary, reasoning, per_headline, raw_headlines,
     prompt_hash, prompt_text, response_raw,
     llm_model, llm_tokens_in, llm_tokens_out, llm_latency_ms, error_message)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
RETURNING id`,
		rec.MeasuredAt, rec.Impact, rec.Summary, rec.Reasoning,
		rec.PerHeadline, rec.RawHeadlines,
		rec.PromptHash, rec.PromptText, rec.ResponseRaw,
		rec.LLMModel, rec.LLMTokensIn, rec.LLMTokensOut, rec.LLMLatencyMs,
		rec.ErrorMessage,
	).Scan(&id)
	return id, err
}

// ListRecent returns the N most recent rows. If beforeID > 0 the cursor pages
// to rows with id < beforeID (used by the eval/news list page).
func (r *NewsSnapshotsRepo) ListRecent(ctx context.Context, q Querier, limit int, beforeID int64) ([]NewsSnapshotRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	// beforeID=0 ⇒ no cursor; the WHERE id < $1 OR $1 = 0 trick keeps the
	// query plan stable across both branches with a single statement.
	rows, err := q.Query(ctx, `
SELECT id, measured_at, impact, summary, reasoning, per_headline, raw_headlines,
       prompt_hash, prompt_text, response_raw,
       llm_model, llm_tokens_in, llm_tokens_out, llm_latency_ms, error_message
  FROM news_snapshots
 WHERE $1 = 0 OR id < $1
 ORDER BY id DESC
 LIMIT $2`, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NewsSnapshotRecord
	for rows.Next() {
		var rec NewsSnapshotRecord
		if err := rows.Scan(&rec.ID, &rec.MeasuredAt, &rec.Impact, &rec.Summary, &rec.Reasoning,
			&rec.PerHeadline, &rec.RawHeadlines,
			&rec.PromptHash, &rec.PromptText, &rec.ResponseRaw,
			&rec.LLMModel, &rec.LLMTokensIn, &rec.LLMTokensOut, &rec.LLMLatencyMs,
			&rec.ErrorMessage); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// CountAll returns the total number of rows.
func (r *NewsSnapshotsRepo) CountAll(ctx context.Context, q Querier) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT COUNT(*) FROM news_snapshots`).Scan(&n)
	return n, err
}

// ListPage returns one page of rows (OFFSET / LIMIT). Newest first.
func (r *NewsSnapshotsRepo) ListPage(ctx context.Context, q Querier, limit, offset int) ([]NewsSnapshotRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := q.Query(ctx, `
SELECT id, measured_at, impact, summary, reasoning, per_headline, raw_headlines,
       prompt_hash, prompt_text, response_raw,
       llm_model, llm_tokens_in, llm_tokens_out, llm_latency_ms, error_message
  FROM news_snapshots
 ORDER BY id DESC
 LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NewsSnapshotRecord
	for rows.Next() {
		var rec NewsSnapshotRecord
		if err := rows.Scan(&rec.ID, &rec.MeasuredAt, &rec.Impact, &rec.Summary, &rec.Reasoning,
			&rec.PerHeadline, &rec.RawHeadlines,
			&rec.PromptHash, &rec.PromptText, &rec.ResponseRaw,
			&rec.LLMModel, &rec.LLMTokensIn, &rec.LLMTokensOut, &rec.LLMLatencyMs,
			&rec.ErrorMessage); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Get returns the row matching id, or pgx.ErrNoRows when absent.
func (r *NewsSnapshotsRepo) Get(ctx context.Context, q Querier, id int64) (*NewsSnapshotRecord, error) {
	var rec NewsSnapshotRecord
	err := q.QueryRow(ctx, `
SELECT id, measured_at, impact, summary, reasoning, per_headline, raw_headlines,
       prompt_hash, prompt_text, response_raw,
       llm_model, llm_tokens_in, llm_tokens_out, llm_latency_ms, error_message
  FROM news_snapshots
 WHERE id = $1`, id,
	).Scan(&rec.ID, &rec.MeasuredAt, &rec.Impact, &rec.Summary, &rec.Reasoning,
		&rec.PerHeadline, &rec.RawHeadlines,
		&rec.PromptHash, &rec.PromptText, &rec.ResponseRaw,
		&rec.LLMModel, &rec.LLMTokensIn, &rec.LLMTokensOut, &rec.LLMLatencyMs,
		&rec.ErrorMessage)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

// Latest returns the newest snapshot or pgx.ErrNoRows when the table is empty.
func (r *NewsSnapshotsRepo) Latest(ctx context.Context, q Querier) (*NewsSnapshotRecord, error) {
	var rec NewsSnapshotRecord
	err := q.QueryRow(ctx, `
SELECT id, measured_at, impact, summary, reasoning, per_headline, raw_headlines,
       prompt_hash, prompt_text, response_raw,
       llm_model, llm_tokens_in, llm_tokens_out, llm_latency_ms, error_message
  FROM news_snapshots
 ORDER BY measured_at DESC
 LIMIT 1`,
	).Scan(&rec.ID, &rec.MeasuredAt, &rec.Impact, &rec.Summary, &rec.Reasoning,
		&rec.PerHeadline, &rec.RawHeadlines,
		&rec.PromptHash, &rec.PromptText, &rec.ResponseRaw,
		&rec.LLMModel, &rec.LLMTokensIn, &rec.LLMTokensOut, &rec.LLMLatencyMs,
		&rec.ErrorMessage)
	if err != nil {
		return nil, err
	}
	return &rec, nil
}
