-- +goose Up
-- +goose StatementBegin

-- 1. signals 表加冗余字段, 方便列表查询和 UI 渲染
ALTER TABLE signals
    ADD COLUMN agent_score    INTEGER,
    ADD COLUMN agent_decision TEXT,
    ADD COLUMN agent_dry_run  BOOLEAN;

COMMENT ON COLUMN signals.agent_score IS
    '0~100, agent 给出的执行可信度评分; NULL = 未经 agent';
COMMENT ON COLUMN signals.agent_decision IS
    'approve|abandon|failed|NULL; failed = LLM 调用失败, 实际行为受 fail_mode 控制';
COMMENT ON COLUMN signals.agent_dry_run IS
    '决策时刻的 dry_run 状态; agent_decision=abandon 且 dry_run=true 表示灰度期"想拒但仍下"的样本';

-- 2. agent_evaluations 详细日志表
CREATE TABLE agent_evaluations (
    id            BIGSERIAL PRIMARY KEY,
    signal_id     BIGINT NOT NULL REFERENCES signals(id) ON DELETE CASCADE,
    model         TEXT NOT NULL,
    prompt_hash   TEXT NOT NULL,
    score         INTEGER,
    decision      TEXT NOT NULL,
    reasoning     TEXT NOT NULL,
    history_json  JSONB NOT NULL,
    prompt_text   TEXT NOT NULL,
    response_raw  TEXT,
    latency_ms    INTEGER NOT NULL,
    token_in      INTEGER,
    token_out     INTEGER,
    cost_cents    NUMERIC(10,4),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_agent_eval_signal     ON agent_evaluations(signal_id);
CREATE INDEX idx_agent_eval_created    ON agent_evaluations(created_at DESC);
CREATE INDEX idx_agent_eval_model_hash ON agent_evaluations(model, prompt_hash);

-- 3. system_state 表加 agent scorer 配置 (项目用 system_state 当 settings 表)
ALTER TABLE system_state
    ADD COLUMN agent_scorer_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN agent_scorer_model         TEXT    NOT NULL DEFAULT 'claude-haiku-4-5-20251001',
    ADD COLUMN agent_scorer_threshold     INTEGER NOT NULL DEFAULT 60,
    ADD COLUMN agent_scorer_timeout_ms    INTEGER NOT NULL DEFAULT 5000,
    ADD COLUMN agent_scorer_history_limit INTEGER NOT NULL DEFAULT 20,
    ADD COLUMN agent_scorer_fail_mode     TEXT    NOT NULL DEFAULT 'open',
    ADD COLUMN agent_scorer_dry_run       BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN llm_api_provider           TEXT    NOT NULL DEFAULT 'anthropic',
    ADD COLUMN llm_api_key                TEXT    NOT NULL DEFAULT '',
    ADD COLUMN llm_api_base_url           TEXT    NOT NULL DEFAULT '';

COMMENT ON COLUMN system_state.agent_scorer_enabled       IS 'AI 评分总开关, 默认关闭, 即使配 LLM API key 也不工作';
COMMENT ON COLUMN system_state.agent_scorer_dry_run       IS 'dry_run 观察模式: agent 仍打分但不影响下单决策';
COMMENT ON COLUMN system_state.agent_scorer_fail_mode     IS 'open|closed; LLM 调用失败时的兜底行为, 默认 open (放行)';
COMMENT ON COLUMN system_state.llm_api_key                IS 'LLM provider API key; 为空时启用 scorer 会被前置校验拦下';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS agent_evaluations;

ALTER TABLE signals
    DROP COLUMN IF EXISTS agent_score,
    DROP COLUMN IF EXISTS agent_decision,
    DROP COLUMN IF EXISTS agent_dry_run;

ALTER TABLE system_state
    DROP COLUMN IF EXISTS agent_scorer_enabled,
    DROP COLUMN IF EXISTS agent_scorer_model,
    DROP COLUMN IF EXISTS agent_scorer_threshold,
    DROP COLUMN IF EXISTS agent_scorer_timeout_ms,
    DROP COLUMN IF EXISTS agent_scorer_history_limit,
    DROP COLUMN IF EXISTS agent_scorer_fail_mode,
    DROP COLUMN IF EXISTS agent_scorer_dry_run,
    DROP COLUMN IF EXISTS llm_api_provider,
    DROP COLUMN IF EXISTS llm_api_key,
    DROP COLUMN IF EXISTS llm_api_base_url;
-- +goose StatementEnd
