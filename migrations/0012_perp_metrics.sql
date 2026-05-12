-- +goose Up
-- +goose StatementBegin

CREATE TABLE perp_metrics (
    id                       BIGSERIAL PRIMARY KEY,
    symbol                   TEXT        NOT NULL,
    observed_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    funding_rate             NUMERIC(12,10) NOT NULL,
    next_funding_time        TIMESTAMPTZ,
    mark_price               NUMERIC(20,8),
    open_interest            NUMERIC(28,8) NOT NULL,
    open_interest_24h_pct    NUMERIC(10,4),
    price_24h_pct            NUMERIC(10,4),
    top_ls_ratio             NUMERIC(10,4),

    funding_label            TEXT NOT NULL,
    oi_signal                TEXT NOT NULL,
    ls_label                 TEXT NOT NULL,

    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (symbol, observed_at)
);
CREATE INDEX idx_perp_metrics_symbol_time ON perp_metrics(symbol, observed_at DESC);

COMMENT ON COLUMN perp_metrics.funding_rate IS '当前 funding rate, 例如 0.00025 = 0.025%';
COMMENT ON COLUMN perp_metrics.open_interest IS '标的币数量 (例如 BTCUSDT 给出的是 BTC 数量)';
COMMENT ON COLUMN perp_metrics.open_interest_24h_pct IS '% 变化 vs 24h 前 (无历史时为 NULL)';
COMMENT ON COLUMN perp_metrics.price_24h_pct IS '% 24h 价格变化';
COMMENT ON COLUMN perp_metrics.top_ls_ratio IS '币安 top-trader long/short position ratio (5m 周期)';
COMMENT ON COLUMN perp_metrics.funding_label IS 'extreme_long|mild_long|neutral|mild_short|extreme_short';
COMMENT ON COLUMN perp_metrics.oi_signal IS 'new_longs|new_shorts|short_squeeze|capitulation|neutral';
COMMENT ON COLUMN perp_metrics.ls_label IS 'strongly_bullish|bullish|balanced|bearish|strongly_bearish';

ALTER TABLE system_state
    ADD COLUMN perp_metrics_enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN perp_metrics_lookback_minutes INTEGER NOT NULL DEFAULT 30;

COMMENT ON COLUMN system_state.perp_metrics_enabled IS '关 = worker 跳过 + scorer prompt 写 "永续指标暂不可用"';
COMMENT ON COLUMN system_state.perp_metrics_lookback_minutes IS 'scorer 接受的最大数据陈旧度 (分钟)';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE system_state
    DROP COLUMN IF EXISTS perp_metrics_enabled,
    DROP COLUMN IF EXISTS perp_metrics_lookback_minutes;
DROP TABLE IF EXISTS perp_metrics;
-- +goose StatementEnd
