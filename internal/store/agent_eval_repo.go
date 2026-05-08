package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// AgentEvaluation is one full record of an agent scorer run, persisted to
// agent_evaluations. Insert is best-effort: failure must NOT fail the
// trade — the caller logs warn and moves on.
type AgentEvaluation struct {
	ID          int64
	SignalID    int64
	Model       string
	PromptHash  string
	Score       *int            // NULL when decision='failed'
	Decision    string          // approve | abandon | failed
	Reasoning   string
	HistoryJSON json.RawMessage // ScoreInput snapshot (history + portfolio + market + windows)
	PromptText  string
	ResponseRaw *string // LLM raw response; nil when LLM call itself failed
	LatencyMs   int
	TokenIn     *int
	TokenOut    *int
	CostCents   *decimal.Decimal
	CreatedAt   time.Time
}

type AgentEvalRepo struct {
	pool *pgxpool.Pool
}

func NewAgentEvalRepo(pool *pgxpool.Pool) *AgentEvalRepo {
	return &AgentEvalRepo{pool: pool}
}

// Insert writes one evaluation row.
func (r *AgentEvalRepo) Insert(ctx context.Context, q Querier, e AgentEvaluation) error {
	_, err := q.Exec(ctx, `
INSERT INTO agent_evaluations
  (signal_id, model, prompt_hash, score, decision, reasoning,
   history_json, prompt_text, response_raw, latency_ms,
   token_in, token_out, cost_cents)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		e.SignalID, e.Model, e.PromptHash, e.Score, e.Decision, e.Reasoning,
		e.HistoryJSON, e.PromptText, e.ResponseRaw, e.LatencyMs,
		e.TokenIn, e.TokenOut, e.CostCents,
	)
	return err
}

// LatestForSignal returns the most recent evaluation for a signal, or
// (nil, nil) if no evaluation exists. Used by the signal detail page.
func (r *AgentEvalRepo) LatestForSignal(ctx context.Context, q Querier, signalID int64) (*AgentEvaluation, error) {
	var e AgentEvaluation
	err := q.QueryRow(ctx, `
SELECT id, signal_id, model, prompt_hash, score, decision, reasoning,
       history_json, prompt_text, response_raw, latency_ms,
       token_in, token_out, cost_cents, created_at
  FROM agent_evaluations WHERE signal_id=$1 ORDER BY created_at DESC LIMIT 1`, signalID,
	).Scan(&e.ID, &e.SignalID, &e.Model, &e.PromptHash,
		&e.Score, &e.Decision, &e.Reasoning,
		&e.HistoryJSON, &e.PromptText, &e.ResponseRaw, &e.LatencyMs,
		&e.TokenIn, &e.TokenOut, &e.CostCents, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ListSince returns all evaluations created after `since`, oldest first.
// Used by cmd/agent-eval to build offline grayscale reports.
func (r *AgentEvalRepo) ListSince(ctx context.Context, q Querier, since time.Time) ([]AgentEvaluation, error) {
	rows, err := q.Query(ctx, `
SELECT id, signal_id, model, prompt_hash, score, decision, reasoning,
       history_json, prompt_text, response_raw, latency_ms,
       token_in, token_out, cost_cents, created_at
  FROM agent_evaluations WHERE created_at >= $1
  ORDER BY created_at ASC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentEvaluation
	for rows.Next() {
		var e AgentEvaluation
		if err := rows.Scan(&e.ID, &e.SignalID, &e.Model, &e.PromptHash,
			&e.Score, &e.Decision, &e.Reasoning,
			&e.HistoryJSON, &e.PromptText, &e.ResponseRaw, &e.LatencyMs,
			&e.TokenIn, &e.TokenOut, &e.CostCents, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
