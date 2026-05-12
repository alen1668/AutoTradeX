package outcome

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// PGRepo implements PendingReader and Writer against the live postgres
// schema. It joins agent_evaluations to signals to surface the bits Compute
// needs, and joins position_history (via virtual_positions) to retrieve
// realized PnL for the approve path.
type PGRepo struct{ pool *pgxpool.Pool }

func NewPGRepo(pool *pgxpool.Pool) *PGRepo { return &PGRepo{pool: pool} }

// PendingEvaluations returns up to limit pending rows older than minAgeMin
// minutes. Only signals with kind IN ('long','short') are returned —
// exit_* signals have no meaningful win/loss outcome and are filtered out.
func (r *PGRepo) PendingEvaluations(ctx context.Context, limit, minAgeMin int) ([]EvalRow, error) {
	rows, err := r.pool.Query(ctx, `
SELECT ae.signal_id,
       s.symbol,
       s.kind::text,
       s.signal_price,
       s.tv_timestamp_ms
FROM agent_evaluations ae
JOIN signals s ON s.id = ae.signal_id
WHERE ae.outcome_label IS NULL
  AND ae.decision IN ('approve','abandon')
  AND s.kind IN ('long','short')
  AND ae.created_at < now() - make_interval(mins => $1::int)
ORDER BY ae.created_at ASC
LIMIT $2`, minAgeMin, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EvalRow
	for rows.Next() {
		var (
			er   EvalRow
			kind string
			tvMs int64
		)
		if err := rows.Scan(&er.SignalID, &er.Symbol, &kind, &er.SignalPrice, &tvMs); err != nil {
			return nil, err
		}
		// long → buy, short → sell (Input.Direction convention)
		switch kind {
		case "long":
			er.Direction = "buy"
		case "short":
			er.Direction = "sell"
		default:
			// already filtered in SQL; defensive skip
			continue
		}
		er.SignalTime = time.UnixMilli(tvMs).UTC()
		out = append(out, er)
	}
	return out, rows.Err()
}

// PositionPnL returns the realized pnl_usdc of the position opened by
// this signal, or (nil, nil) if no closed position exists. The join goes
// through virtual_positions (entry_signal_id) then to position_history via
// strategy_id + symbol + opened_at — mirroring how the rest of the codebase
// links signals to their trades.
func (r *PGRepo) PositionPnL(ctx context.Context, signalID int64) (*decimal.Decimal, error) {
	var pnl decimal.Decimal
	err := r.pool.QueryRow(ctx, `
SELECT ph.pnl_usdc
FROM virtual_positions vp
JOIN position_history ph
  ON ph.strategy_id = vp.strategy_id
 AND ph.symbol      = vp.symbol
 AND ph.opened_at   = vp.opened_at
WHERE vp.entry_signal_id = $1
ORDER BY ph.closed_at ASC
LIMIT 1`, signalID).Scan(&pnl)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &pnl, nil
}

// WriteOutcome updates the agent_evaluations row in place. Only writes
// when outcome_label IS NULL — this is the idempotency guarantee for
// catch-up runs.
func (r *PGRepo) WriteOutcome(ctx context.Context, signalID int64, res Result) error {
	_, err := r.pool.Exec(ctx, `
UPDATE agent_evaluations
SET outcome_horizon_min  = $2,
    outcome_pnl_usd      = $3,
    outcome_pnl_pct      = $4,
    outcome_label        = $5,
    outcome_computed_at  = $6
WHERE signal_id = $1 AND outcome_label IS NULL`,
		signalID, res.HorizonMin, res.PnLUSD, res.PnLPct, string(res.Label), res.ComputedAt)
	return err
}
