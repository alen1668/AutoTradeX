-- +goose Up
-- +goose StatementBegin

ALTER TABLE agent_evaluations
  ADD COLUMN outcome_horizon_min  INT,
  ADD COLUMN outcome_pnl_usd      NUMERIC(20,8),
  ADD COLUMN outcome_pnl_pct      NUMERIC(10,6),
  ADD COLUMN outcome_label        TEXT,
  ADD COLUMN outcome_computed_at  TIMESTAMPTZ;

CREATE INDEX ix_agent_eval_outcome_pending
  ON agent_evaluations(created_at)
  WHERE outcome_label IS NULL;

CREATE INDEX ix_agent_eval_outcome_label
  ON agent_evaluations(outcome_label, created_at DESC)
  WHERE outcome_label IS NOT NULL;

COMMENT ON COLUMN agent_evaluations.outcome_label IS 'win | loss | flat | unavailable | NULL(=pending)';
COMMENT ON COLUMN agent_evaluations.outcome_pnl_usd IS 'approve 路径真实成交 PnL (USD)';
COMMENT ON COLUMN agent_evaluations.outcome_pnl_pct IS 'abandon 路径反事实 PnL 百分比 (signed by direction)';
COMMENT ON COLUMN agent_evaluations.outcome_horizon_min IS '反事实持有期 (分钟)，写入时配置值';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS ix_agent_eval_outcome_label;
DROP INDEX IF EXISTS ix_agent_eval_outcome_pending;
ALTER TABLE agent_evaluations
  DROP COLUMN IF EXISTS outcome_computed_at,
  DROP COLUMN IF EXISTS outcome_label,
  DROP COLUMN IF EXISTS outcome_pnl_pct,
  DROP COLUMN IF EXISTS outcome_pnl_usd,
  DROP COLUMN IF EXISTS outcome_horizon_min;
-- +goose StatementEnd
