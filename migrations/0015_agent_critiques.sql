-- +goose Up
-- +goose StatementBegin

CREATE TABLE agent_critiques (
  id            BIGSERIAL PRIMARY KEY,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  window_start  TIMESTAMPTZ NOT NULL,
  window_end    TIMESTAMPTZ NOT NULL,
  sample_size   INT NOT NULL,
  model         TEXT NOT NULL,
  prompt_hash   TEXT NOT NULL,
  patterns_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  summary       TEXT,
  latency_ms    INT,
  token_in      INT,
  token_out     INT,
  status        TEXT NOT NULL DEFAULT 'done',
  error_message TEXT
);
COMMENT ON TABLE  agent_critiques IS 'critique LLM 周期反思的产出（patterns_json 是原始 LLM 输出）';
COMMENT ON COLUMN agent_critiques.status IS 'done | failed | insufficient_sample';

CREATE INDEX ix_critique_created ON agent_critiques(created_at DESC);

CREATE TABLE agent_critique_patterns (
  id            BIGSERIAL PRIMARY KEY,
  critique_id   BIGINT NOT NULL REFERENCES agent_critiques(id) ON DELETE CASCADE,
  pattern_key   TEXT NOT NULL,
  title         TEXT NOT NULL,
  suggestion    TEXT NOT NULL,
  confidence    TEXT NOT NULL,
  evidence_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  pinned        BOOLEAN NOT NULL DEFAULT FALSE,
  pinned_at     TIMESTAMPTZ,
  pinned_by     TEXT,
  UNIQUE (critique_id, pattern_key)
);
COMMENT ON TABLE  agent_critique_patterns IS 'critique 内的具体误判模式（人工可单条钉选注入 scorer prompt）';
COMMENT ON COLUMN agent_critique_patterns.confidence IS 'high | medium | low';
COMMENT ON COLUMN agent_critique_patterns.pinned_by IS 'manual (后期扩展 auto)';

CREATE INDEX ix_pattern_pinned ON agent_critique_patterns(pinned_at DESC) WHERE pinned;

ALTER TABLE system_state
  ADD COLUMN critique_enabled        BOOLEAN     NOT NULL DEFAULT TRUE,
  ADD COLUMN critique_model          TEXT        NOT NULL DEFAULT '',
  ADD COLUMN critique_window_days    INTEGER     NOT NULL DEFAULT 7,
  ADD COLUMN critique_min_sample     INTEGER     NOT NULL DEFAULT 20,
  ADD COLUMN critique_max_pinned     INTEGER     NOT NULL DEFAULT 5,
  ADD COLUMN critique_cron_utc       TEXT        NOT NULL DEFAULT '0 4 * * *';

COMMENT ON COLUMN system_state.critique_enabled      IS '主开关，false 时 worker 不跑、scorer 不注入';
COMMENT ON COLUMN system_state.critique_model        IS '空字符串 = 回退到 agent_scorer_model';
COMMENT ON COLUMN system_state.critique_window_days  IS '反思窗口（天），默认 7';
COMMENT ON COLUMN system_state.critique_min_sample   IS '最小样本量，<该值时跳过 LLM 调用';
COMMENT ON COLUMN system_state.critique_max_pinned   IS 'scorer 注入的 pinned pattern 上限';
COMMENT ON COLUMN system_state.critique_cron_utc     IS '反思 cron 表达式（UTC），默认每日 04:00';

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
ALTER TABLE system_state
  DROP COLUMN critique_cron_utc,
  DROP COLUMN critique_max_pinned,
  DROP COLUMN critique_min_sample,
  DROP COLUMN critique_window_days,
  DROP COLUMN critique_model,
  DROP COLUMN critique_enabled;
DROP TABLE IF EXISTS agent_critique_patterns;
DROP TABLE IF EXISTS agent_critiques;
-- +goose StatementEnd
