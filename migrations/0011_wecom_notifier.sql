-- +goose Up
-- +goose StatementBegin

-- WeCom (企业微信) 群机器人 webhook + news notify threshold.
ALTER TABLE system_state
    ADD COLUMN wecom_enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN wecom_webhook_url      TEXT    NOT NULL DEFAULT '',
    ADD COLUMN news_notify_min_impact TEXT    NOT NULL DEFAULT 'medium';

COMMENT ON COLUMN system_state.wecom_enabled IS '企业微信群机器人 webhook 开关';
COMMENT ON COLUMN system_state.wecom_webhook_url IS 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=XXX';
COMMENT ON COLUMN system_state.news_notify_min_impact IS
    'news worker 仅在 impact >= 此阈值时推送通知 (none|low|medium|high; 默认 medium)';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE system_state
    DROP COLUMN IF EXISTS wecom_enabled,
    DROP COLUMN IF EXISTS wecom_webhook_url,
    DROP COLUMN IF EXISTS news_notify_min_impact;
-- +goose StatementEnd
