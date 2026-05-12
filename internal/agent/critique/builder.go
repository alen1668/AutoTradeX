package critique

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DataReader fetches the inputs for one critique run from DB. Production
// impl is PGDataReader; tests use a fake.
type DataReader interface {
	Aggregates(ctx context.Context, since, until time.Time) ([]AggregateRow, error)
	Details(ctx context.Context, since, until time.Time, limit int) ([]DetailRow, error)
	PreviousSummary(ctx context.Context) (string, error)
}

// PGDataReader queries the live tables. Regime is intentionally rendered
// as empty for now (joining market_regime by signal time is non-trivial
// and not yet load-bearing for pattern discovery — we'll add it later
// once the LLM consistently exploits it).
type PGDataReader struct{ pool *pgxpool.Pool }

func NewPGDataReader(pool *pgxpool.Pool) *PGDataReader { return &PGDataReader{pool: pool} }

// Aggregates returns one row per (strategy_id × outcome) within window.
// Regime column is empty string in this version.
func (r *PGDataReader) Aggregates(ctx context.Context, since, until time.Time) ([]AggregateRow, error) {
	rows, err := r.pool.Query(ctx, `
SELECT COALESCE(s.strategy_id, '')                                          AS strategy_id,
       ''                                                                    AS regime,
       ae.outcome_label                                                      AS outcome,
       count(*)                                                              AS cnt,
       COALESCE(round(avg(ae.score)::numeric, 2)::text, '')                  AS avg_score,
       COALESCE(round(avg(ae.outcome_pnl_usd)::numeric, 4)::text, '')        AS avg_pnl_usd,
       COALESCE(
         round(
           sum(CASE WHEN ae.outcome_label='win' THEN 1 ELSE 0 END)::numeric
             / nullif(count(*), 0)::numeric,
           4
         )::text,
         '0'
       )                                                                     AS win_rate
FROM agent_evaluations ae
JOIN signals s ON s.id = ae.signal_id
WHERE ae.outcome_label IN ('win','loss','flat')
  AND ae.created_at >= $1 AND ae.created_at < $2
GROUP BY s.strategy_id, ae.outcome_label
ORDER BY s.strategy_id, ae.outcome_label`, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AggregateRow
	for rows.Next() {
		var a AggregateRow
		if err := rows.Scan(&a.StrategyID, &a.Regime, &a.Outcome,
			&a.Count, &a.AvgScore, &a.AvgPnLUSD, &a.WinRate); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Details returns up to `limit` evaluations within window, most-recent first.
// Reasoning is truncated to 60 chars to keep prompt size bounded.
func (r *PGDataReader) Details(ctx context.Context, since, until time.Time, limit int) ([]DetailRow, error) {
	rows, err := r.pool.Query(ctx, `
SELECT ae.signal_id,
       COALESCE(s.strategy_id, ''),
       s.symbol,
       s.kind::text,
       COALESCE(ae.score, 0),
       ae.decision,
       COALESCE(ae.outcome_label, ''),
       COALESCE(ae.outcome_pnl_pct::text, ''),
       COALESCE(LEFT(ae.reasoning, 60), '')
FROM agent_evaluations ae
JOIN signals s ON s.id = ae.signal_id
WHERE ae.outcome_label IN ('win','loss','flat')
  AND ae.created_at >= $1 AND ae.created_at < $2
ORDER BY ae.created_at DESC
LIMIT $3`, since, until, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DetailRow
	for rows.Next() {
		var d DetailRow
		if err := rows.Scan(&d.SignalID, &d.StrategyID, &d.Symbol, &d.Kind,
			&d.Score, &d.Decision, &d.Outcome, &d.PnLPct, &d.ReasoningShort); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// PreviousSummary returns the summary text of the most recent completed
// critique, or empty string if none. Used to seed the prompt with
// "上次复盘摘要" so the LLM doesn't repeat itself across runs.
func (r *PGDataReader) PreviousSummary(ctx context.Context) (string, error) {
	var s *string
	err := r.pool.QueryRow(ctx, `
SELECT summary FROM agent_critiques
WHERE status = 'done'
ORDER BY created_at DESC
LIMIT 1`).Scan(&s)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if s == nil {
		return "", nil
	}
	return *s, nil
}
