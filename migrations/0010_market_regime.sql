-- +goose Up
-- +goose StatementBegin

-- 1. market_regime: 每 30min 一行
CREATE TABLE market_regime (
    id              BIGSERIAL PRIMARY KEY,
    measured_at     TIMESTAMPTZ NOT NULL,
    label           TEXT NOT NULL,
    trend_strength  NUMERIC(5,4) NOT NULL,
    volatility_24h  NUMERIC(10,6) NOT NULL,
    vol_percentile  NUMERIC(5,4) NOT NULL,
    change_24h_pct  NUMERIC(8,4) NOT NULL,
    price_range_pos NUMERIC(5,4) NOT NULL,
    kline_count     INTEGER NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_regime_measured_at ON market_regime(measured_at DESC);

COMMENT ON COLUMN market_regime.label IS 'trend_up|trend_down|range|crash|spike';
COMMENT ON COLUMN market_regime.vol_percentile IS '0~1, 相对最近 30 天的波动率分位';

-- 2. economic_events: 每天 UPSERT
CREATE TABLE economic_events (
    id          BIGSERIAL PRIMARY KEY,
    source_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    currency    TEXT NOT NULL,
    impact      TEXT NOT NULL,
    starts_at   TIMESTAMPTZ NOT NULL,
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source_id)
);
CREATE INDEX idx_events_starts_at ON economic_events(starts_at);

COMMENT ON COLUMN economic_events.source_id IS '"ff:" + sha256(title+starts_at)[:16], 用于幂等 UPSERT';
COMMENT ON COLUMN economic_events.impact IS 'High|Medium|Low (本 spec 只抓 High)';

-- 3. news_snapshots: 每 15min 一行, 成功失败都写
CREATE TABLE news_snapshots (
    id              BIGSERIAL PRIMARY KEY,
    measured_at     TIMESTAMPTZ NOT NULL,
    impact          TEXT NOT NULL,
    summary         TEXT NOT NULL,
    reasoning       TEXT NOT NULL,
    per_headline    JSONB NOT NULL,
    raw_headlines   JSONB NOT NULL,
    prompt_hash     TEXT NOT NULL,
    prompt_text     TEXT NOT NULL,
    response_raw    TEXT,
    llm_model       TEXT NOT NULL,
    llm_tokens_in   INTEGER,
    llm_tokens_out  INTEGER,
    llm_latency_ms  INTEGER,
    error_message   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_news_measured_at  ON news_snapshots(measured_at DESC);
CREATE INDEX idx_news_impact       ON news_snapshots(impact, measured_at DESC);
CREATE INDEX idx_news_prompt_hash  ON news_snapshots(prompt_hash);

COMMENT ON COLUMN news_snapshots.per_headline IS
    '[{title, url, impact, reason}, ...]; 对齐 macrocontext.HeadlineJudgment';
COMMENT ON COLUMN news_snapshots.reasoning IS
    'LLM 输出的整体定级依据, ≤ 500 字';
COMMENT ON COLUMN news_snapshots.raw_headlines IS
    'cryptopanic 返回的原始 post 数据 (含 votes / source / currencies 等)';
COMMENT ON COLUMN news_snapshots.error_message IS
    '非空表示这一次跑失败 (HTTP/JSON/LLM 任一), impact 强制 "none"';

-- 4. system_state 加宏观上下文相关字段
ALTER TABLE system_state
    ADD COLUMN regime_enabled        BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN regime_interval_min   INTEGER NOT NULL DEFAULT 30,
    ADD COLUMN calendar_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN news_enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN news_interval_min     INTEGER NOT NULL DEFAULT 15,
    ADD COLUMN news_api_key          TEXT    NOT NULL DEFAULT '',
    ADD COLUMN news_llm_model        TEXT    NOT NULL DEFAULT 'claude-haiku-4-5-20251001';

COMMENT ON COLUMN system_state.regime_enabled   IS '关 = regime worker 跳过; scorer prompt 中 regime 段写"不可用"';
COMMENT ON COLUMN system_state.calendar_enabled IS '关 = calendar worker 跳过; event_window 永不命中';
COMMENT ON COLUMN system_state.news_enabled     IS '关 = news worker 跳过; news_alert 段写"不可用"';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS news_snapshots;
DROP TABLE IF EXISTS economic_events;
DROP TABLE IF EXISTS market_regime;
ALTER TABLE system_state
    DROP COLUMN IF EXISTS regime_enabled,
    DROP COLUMN IF EXISTS regime_interval_min,
    DROP COLUMN IF EXISTS calendar_enabled,
    DROP COLUMN IF EXISTS news_enabled,
    DROP COLUMN IF EXISTS news_interval_min,
    DROP COLUMN IF EXISTS news_api_key,
    DROP COLUMN IF EXISTS news_llm_model;
-- +goose StatementEnd
