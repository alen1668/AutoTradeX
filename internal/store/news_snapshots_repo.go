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
