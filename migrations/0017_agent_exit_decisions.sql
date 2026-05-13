-- +goose Up
-- +goose StatementBegin

CREATE TABLE agent_exit_decisions (
  id                  BIGSERIAL PRIMARY KEY,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

  virtual_position_id BIGINT NOT NULL REFERENCES virtual_positions(id) ON DELETE CASCADE,
  strategy_id         TEXT NOT NULL,
  symbol              TEXT NOT NULL,
  side                TEXT NOT NULL,

  entry_fill_price    NUMERIC NOT NULL,
  current_price       NUMERIC NOT NULL,
  qty                 NUMERIC NOT NULL,
  unrealized_pnl_usd  NUMERIC NOT NULL,
  unrealized_pnl_pct  NUMERIC NOT NULL,
  position_age_sec    INT NOT NULL,
  current_sl_price    NUMERIC,
  current_tp_price    NUMERIC,

  action              TEXT NOT NULL,
  confidence          TEXT NOT NULL,
  reasoning           TEXT NOT NULL,
  proposed_sl_price   NUMERIC,
  partial_pct         NUMERIC,

  model               TEXT NOT NULL,
  prompt_hash         TEXT NOT NULL,
  latency_ms          INT,
  token_in            INT,
  token_out           INT,

  mode                TEXT NOT NULL,
  executed_at         TIMESTAMPTZ,
  execution_status    TEXT,
  execution_error     TEXT,

  outcome_horizon_min INT,
  if_hold_pnl_pct     NUMERIC,
  if_hold_label       TEXT,
  outcome_computed_at TIMESTAMPTZ
);
COMMENT ON TABLE  agent_exit_decisions IS 'Exit Agent 每次对一个 open 持仓的决策（含 shadow / active 两种 mode）';
COMMENT ON COLUMN agent_exit_decisions.action            IS 'hold | tighten_sl | take_partial | exit_now';
COMMENT ON COLUMN agent_exit_decisions.confidence        IS 'high | medium | low';
COMMENT ON COLUMN agent_exit_decisions.mode              IS 'shadow | active';
COMMENT ON COLUMN agent_exit_decisions.execution_status  IS 'success | skipped_constraint | failed (NULL = shadow)';
COMMENT ON COLUMN agent_exit_decisions.if_hold_label     IS 'improved | worsened | unchanged | unavailable (仅 active 非 hold 决策填非空)';

CREATE INDEX ix_exit_dec_position_recent ON agent_exit_decisions(virtual_position_id, created_at DESC);
CREATE INDEX ix_exit_dec_pending_outcome ON agent_exit_decisions(created_at) WHERE outcome_computed_at IS NULL;
CREATE INDEX ix_exit_dec_action          ON agent_exit_decisions(action, created_at DESC);

ALTER TABLE system_state
  ADD COLUMN exit_agent_enabled                       BOOLEAN  NOT NULL DEFAULT FALSE,
  ADD COLUMN exit_agent_mode                          TEXT     NOT NULL DEFAULT 'shadow',
  ADD COLUMN exit_agent_model                         TEXT     NOT NULL DEFAULT '',
  ADD COLUMN exit_agent_scan_interval_min             INTEGER  NOT NULL DEFAULT 5,
  ADD COLUMN exit_agent_min_position_age_sec          INTEGER  NOT NULL DEFAULT 60,
  ADD COLUMN exit_agent_decision_cooldown_min         INTEGER  NOT NULL DEFAULT 15,
  ADD COLUMN exit_agent_require_confidence_for_exit   TEXT     NOT NULL DEFAULT 'high',
  ADD COLUMN exit_agent_horizon_min                   INTEGER  NOT NULL DEFAULT 60,
  ADD COLUMN exit_agent_max_concurrent                INTEGER  NOT NULL DEFAULT 4;

COMMENT ON COLUMN system_state.exit_agent_enabled                     IS 'Exit Agent 主开关 (默认 false 安全启停)';
COMMENT ON COLUMN system_state.exit_agent_mode                        IS 'shadow (写表不动单) | active (真实执行)';
COMMENT ON COLUMN system_state.exit_agent_model                       IS '空字符串 = 回退到 agent_scorer_model';
COMMENT ON COLUMN system_state.exit_agent_scan_interval_min           IS '扫描周期 (分钟)';
COMMENT ON COLUMN system_state.exit_agent_min_position_age_sec        IS '持仓年龄低于此阈值跳过, 防开仓即被 LLM 折腾';
COMMENT ON COLUMN system_state.exit_agent_decision_cooldown_min       IS '同仓决策最小间隔 (分钟)';
COMMENT ON COLUMN system_state.exit_agent_require_confidence_for_exit IS 'exit_now 至少需要的 confidence (high|medium|low)';
COMMENT ON COLUMN system_state.exit_agent_horizon_min                 IS '反事实窗口 (分钟)';
COMMENT ON COLUMN system_state.exit_agent_max_concurrent              IS '单轮最多并发处理几个仓';

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE system_state
  DROP COLUMN exit_agent_max_concurrent,
  DROP COLUMN exit_agent_horizon_min,
  DROP COLUMN exit_agent_require_confidence_for_exit,
  DROP COLUMN exit_agent_decision_cooldown_min,
  DROP COLUMN exit_agent_min_position_age_sec,
  DROP COLUMN exit_agent_scan_interval_min,
  DROP COLUMN exit_agent_model,
  DROP COLUMN exit_agent_mode,
  DROP COLUMN exit_agent_enabled;
DROP TABLE IF EXISTS agent_exit_decisions;
-- +goose StatementEnd
