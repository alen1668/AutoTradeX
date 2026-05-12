package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CritiqueRow mirrors one agent_critiques row.
type CritiqueRow struct {
	ID           int64
	CreatedAt    time.Time
	WindowStart  time.Time
	WindowEnd    time.Time
	SampleSize   int
	Model        string
	PromptHash   string
	PatternsJSON []byte
	Summary      *string
	LatencyMs    *int
	TokenIn      *int
	TokenOut     *int
	Status       string
	ErrorMessage *string
}

// CritiquePatternRow mirrors one agent_critique_patterns row.
type CritiquePatternRow struct {
	ID           int64
	CritiqueID   int64
	PatternKey   string
	Title        string
	Suggestion   string
	Confidence   string
	EvidenceJSON []byte
	Pinned       bool
	PinnedAt     *time.Time
	PinnedBy     *string
}

// CritiqueRepo wraps agent_critiques and agent_critique_patterns. All
// methods operate on the pool directly (no Querier param needed — these
// don't compose with other repos in a transaction).
type CritiqueRepo struct{ pool *pgxpool.Pool }

func NewCritiqueRepo(pool *pgxpool.Pool) *CritiqueRepo { return &CritiqueRepo{pool: pool} }

// InsertWithPatterns persists a critique and all its child patterns
// atomically. Returns the new critique_id.
func (r *CritiqueRepo) InsertWithPatterns(ctx context.Context, c CritiqueRow, patterns []CritiquePatternRow) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	patternsJSON := c.PatternsJSON
	if len(patternsJSON) == 0 {
		patternsJSON = []byte("{}")
	}

	var id int64
	err = tx.QueryRow(ctx, `
INSERT INTO agent_critiques
  (window_start, window_end, sample_size, model, prompt_hash, patterns_json,
   summary, latency_ms, token_in, token_out, status, error_message)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
RETURNING id`,
		c.WindowStart, c.WindowEnd, c.SampleSize, c.Model, c.PromptHash, patternsJSON,
		c.Summary, c.LatencyMs, c.TokenIn, c.TokenOut, c.Status, c.ErrorMessage,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	for _, p := range patterns {
		evJSON := p.EvidenceJSON
		if len(evJSON) == 0 {
			evJSON = []byte("{}")
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO agent_critique_patterns
  (critique_id, pattern_key, title, suggestion, confidence, evidence_json)
VALUES ($1,$2,$3,$4,$5,$6)`,
			id, p.PatternKey, p.Title, p.Suggestion, p.Confidence, evJSON,
		); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return id, nil
}

// List returns up to `limit` recent critiques (no patterns).
func (r *CritiqueRepo) List(ctx context.Context, limit int) ([]CritiqueRow, error) {
	rows, err := r.pool.Query(ctx, `
SELECT id, created_at, window_start, window_end, sample_size, model, prompt_hash,
       patterns_json, summary, latency_ms, token_in, token_out, status, error_message
FROM agent_critiques
ORDER BY created_at DESC
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CritiqueRow
	for rows.Next() {
		var c CritiqueRow
		if err := rows.Scan(&c.ID, &c.CreatedAt, &c.WindowStart, &c.WindowEnd,
			&c.SampleSize, &c.Model, &c.PromptHash, &c.PatternsJSON,
			&c.Summary, &c.LatencyMs, &c.TokenIn, &c.TokenOut, &c.Status, &c.ErrorMessage); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PatternsByCritique returns all patterns belonging to one critique.
func (r *CritiqueRepo) PatternsByCritique(ctx context.Context, critiqueID int64) ([]CritiquePatternRow, error) {
	rows, err := r.pool.Query(ctx, `
SELECT id, critique_id, pattern_key, title, suggestion, confidence,
       evidence_json, pinned, pinned_at, pinned_by
FROM agent_critique_patterns
WHERE critique_id = $1
ORDER BY id ASC`, critiqueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPatternRows(rows)
}

// PinnedPatterns returns up to `limit` pinned patterns, most-recently-pinned first.
// Used by scorer prompt injection.
func (r *CritiqueRepo) PinnedPatterns(ctx context.Context, limit int) ([]CritiquePatternRow, error) {
	rows, err := r.pool.Query(ctx, `
SELECT id, critique_id, pattern_key, title, suggestion, confidence,
       evidence_json, pinned, pinned_at, pinned_by
FROM agent_critique_patterns
WHERE pinned = TRUE
ORDER BY pinned_at DESC NULLS LAST
LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPatternRows(rows)
}

func scanPatternRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]CritiquePatternRow, error) {
	var out []CritiquePatternRow
	for rows.Next() {
		var p CritiquePatternRow
		if err := rows.Scan(&p.ID, &p.CritiqueID, &p.PatternKey, &p.Title, &p.Suggestion,
			&p.Confidence, &p.EvidenceJSON, &p.Pinned, &p.PinnedAt, &p.PinnedBy); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PinByConfidence sets pinned=TRUE on every pattern of `critiqueID`
// whose confidence matches. `confidence` "all" matches any. Only rows
// currently pinned=FALSE are touched so re-runs are idempotent.
func (r *CritiqueRepo) PinByConfidence(ctx context.Context, critiqueID int64, confidence, pinnedBy string) error {
	if confidence == "all" {
		_, err := r.pool.Exec(ctx, `
UPDATE agent_critique_patterns
SET pinned = TRUE, pinned_at = now(), pinned_by = $2
WHERE critique_id = $1 AND pinned = FALSE`, critiqueID, pinnedBy)
		return err
	}
	_, err := r.pool.Exec(ctx, `
UPDATE agent_critique_patterns
SET pinned = TRUE, pinned_at = now(), pinned_by = $3
WHERE critique_id = $1 AND confidence = $2 AND pinned = FALSE`, critiqueID, confidence, pinnedBy)
	return err
}

// SetPinned toggles pinned flag on a single pattern row.
// `pinnedBy` is the actor tag (e.g. "manual" for ops, "auto" for the
// LLM-driven auto-pin path).
func (r *CritiqueRepo) SetPinned(ctx context.Context, patternID int64, pinned bool, pinnedBy string) error {
	if pinned {
		_, err := r.pool.Exec(ctx, `
UPDATE agent_critique_patterns
SET pinned = TRUE, pinned_at = now(), pinned_by = $2
WHERE id = $1`, patternID, pinnedBy)
		return err
	}
	_, err := r.pool.Exec(ctx, `
UPDATE agent_critique_patterns
SET pinned = FALSE, pinned_at = NULL, pinned_by = NULL
WHERE id = $1`, patternID)
	return err
}
