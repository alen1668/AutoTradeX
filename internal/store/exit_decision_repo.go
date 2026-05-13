package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// ExitDecisionRow mirrors agent_exit_decisions row.
type ExitDecisionRow struct {
	ID        int64
	CreatedAt time.Time

	VirtualPositionID int64
	StrategyID        string
	Symbol            string
	Side              string

	EntryFillPrice   decimal.Decimal
	CurrentPrice     decimal.Decimal
	Qty              decimal.Decimal
	UnrealizedPnLUSD decimal.Decimal
	UnrealizedPnLPct decimal.Decimal
	PositionAgeSec   int
	CurrentSLPrice   *decimal.Decimal
	CurrentTPPrice   *decimal.Decimal

	Action          string
	Confidence      string
	Reasoning       string
	ProposedSLPrice *decimal.Decimal
	PartialPct      *decimal.Decimal

	Model      string
	PromptHash string
	LatencyMs  *int
	TokenIn    *int
	TokenOut   *int

	Mode            string
	ExecutedAt      *time.Time
	ExecutionStatus *string
	ExecutionError  *string

	OutcomeHorizonMin *int
	IfHoldPnLPct      *decimal.Decimal
	IfHoldLabel       *string
	OutcomeComputedAt *time.Time
}

// ExitDecisionListFilter narrows /eval/exit listing.
type ExitDecisionListFilter struct {
	Mode   string // empty = all
	Action string // empty = all
	After  time.Time
	Before time.Time
	Limit  int // default 50, max 500
}

type ExitDecisionRepo struct{ pool *pgxpool.Pool }

func NewExitDecisionRepo(pool *pgxpool.Pool) *ExitDecisionRepo {
	return &ExitDecisionRepo{pool: pool}
}

const exitColsRead = `
  id, created_at,
  virtual_position_id, strategy_id, symbol, side,
  entry_fill_price, current_price, qty, unrealized_pnl_usd, unrealized_pnl_pct,
  position_age_sec, current_sl_price, current_tp_price,
  action, confidence, reasoning, proposed_sl_price, partial_pct,
  model, prompt_hash, latency_ms, token_in, token_out,
  mode, executed_at, execution_status, execution_error,
  outcome_horizon_min, if_hold_pnl_pct, if_hold_label, outcome_computed_at`

func (r *ExitDecisionRepo) Insert(ctx context.Context, in ExitDecisionRow) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
INSERT INTO agent_exit_decisions
  (virtual_position_id, strategy_id, symbol, side,
   entry_fill_price, current_price, qty, unrealized_pnl_usd, unrealized_pnl_pct,
   position_age_sec, current_sl_price, current_tp_price,
   action, confidence, reasoning, proposed_sl_price, partial_pct,
   model, prompt_hash, latency_ms, token_in, token_out, mode)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
RETURNING id`,
		in.VirtualPositionID, in.StrategyID, in.Symbol, in.Side,
		in.EntryFillPrice, in.CurrentPrice, in.Qty, in.UnrealizedPnLUSD, in.UnrealizedPnLPct,
		in.PositionAgeSec, in.CurrentSLPrice, in.CurrentTPPrice,
		in.Action, in.Confidence, in.Reasoning, in.ProposedSLPrice, in.PartialPct,
		in.Model, in.PromptHash, in.LatencyMs, in.TokenIn, in.TokenOut, in.Mode,
	).Scan(&id)
	return id, err
}

func (r *ExitDecisionRepo) GetByID(ctx context.Context, id int64) (*ExitDecisionRow, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+exitColsRead+` FROM agent_exit_decisions WHERE id=$1`, id)
	out := &ExitDecisionRow{}
	if err := scanExitDecision(row, out); err != nil {
		return nil, err
	}
	return out, nil
}

// LastForPosition returns the most recent decision for the position, or
// (nil, nil) when none exist. Used for cooldown checks.
func (r *ExitDecisionRepo) LastForPosition(ctx context.Context, positionID int64) (*ExitDecisionRow, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+exitColsRead+`
                                  FROM agent_exit_decisions
                                 WHERE virtual_position_id=$1
                                 ORDER BY created_at DESC LIMIT 1`, positionID)
	out := &ExitDecisionRow{}
	if err := scanExitDecision(row, out); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return out, nil
}

func (r *ExitDecisionRepo) List(ctx context.Context, f ExitDecisionListFilter) ([]ExitDecisionRow, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	q := `SELECT ` + exitColsRead + ` FROM agent_exit_decisions WHERE 1=1`
	args := []any{}
	push := func(s string, v any) { args = append(args, v); q += " AND " + s + "$" + strconv.Itoa(len(args)) }
	if f.Mode != "" {
		push("mode=", f.Mode)
	}
	if f.Action != "" {
		push("action=", f.Action)
	}
	if !f.After.IsZero() {
		push("created_at>=", f.After)
	}
	if !f.Before.IsZero() {
		push("created_at<", f.Before)
	}
	q += " ORDER BY created_at DESC LIMIT " + strconv.Itoa(limit)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExitDecisionRow{}
	for rows.Next() {
		v := ExitDecisionRow{}
		if err := scanExitDecision(rows, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PendingOutcome returns rows whose if-hold counterfact has not yet been
// backfilled and whose decision time is at or before olderThan.
func (r *ExitDecisionRepo) PendingOutcome(ctx context.Context, olderThan time.Time, limit int) ([]ExitDecisionRow, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
SELECT `+exitColsRead+`
  FROM agent_exit_decisions
 WHERE outcome_computed_at IS NULL AND created_at <= $1
 ORDER BY created_at ASC LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExitDecisionRow{}
	for rows.Next() {
		v := ExitDecisionRow{}
		if err := scanExitDecision(rows, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *ExitDecisionRepo) SetExecution(ctx context.Context, id int64, executedAt *time.Time, status string, errMsg string) error {
	var ep *string
	if errMsg != "" {
		ep = &errMsg
	}
	_, err := r.pool.Exec(ctx, `
UPDATE agent_exit_decisions
   SET executed_at=$2, execution_status=$3, execution_error=$4
 WHERE id=$1`, id, executedAt, status, ep)
	return err
}

func (r *ExitDecisionRepo) SetIfHoldOutcome(ctx context.Context, id int64, horizonMin int, pct *decimal.Decimal, label *string) error {
	_, err := r.pool.Exec(ctx, `
UPDATE agent_exit_decisions
   SET outcome_horizon_min=$2, if_hold_pnl_pct=$3, if_hold_label=$4, outcome_computed_at=now()
 WHERE id=$1`, id, horizonMin, pct, label)
	return err
}

// scanExitDecision works on both pgx.Row (from QueryRow) and pgx.Rows
// (single row in a Next() loop). Both expose the Scan(dest...) method.
func scanExitDecision(r interface{ Scan(dest ...any) error }, out *ExitDecisionRow) error {
	return r.Scan(
		&out.ID, &out.CreatedAt,
		&out.VirtualPositionID, &out.StrategyID, &out.Symbol, &out.Side,
		&out.EntryFillPrice, &out.CurrentPrice, &out.Qty, &out.UnrealizedPnLUSD, &out.UnrealizedPnLPct,
		&out.PositionAgeSec, &out.CurrentSLPrice, &out.CurrentTPPrice,
		&out.Action, &out.Confidence, &out.Reasoning, &out.ProposedSLPrice, &out.PartialPct,
		&out.Model, &out.PromptHash, &out.LatencyMs, &out.TokenIn, &out.TokenOut,
		&out.Mode, &out.ExecutedAt, &out.ExecutionStatus, &out.ExecutionError,
		&out.OutcomeHorizonMin, &out.IfHoldPnLPct, &out.IfHoldLabel, &out.OutcomeComputedAt,
	)
}
