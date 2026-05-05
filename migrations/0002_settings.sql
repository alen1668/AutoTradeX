-- +goose Up
-- +goose StatementBegin

ALTER TABLE system_state
  ADD COLUMN max_total_leverage NUMERIC(10,4),
  ADD COLUMN max_daily_loss_usdc NUMERIC(20,8),
  ADD COLUMN feishu_webhook_url TEXT,
  ADD COLUMN feishu_enabled BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN telegram_bot_token TEXT,
  ADD COLUMN telegram_chat_id TEXT,
  ADD COLUMN telegram_enabled BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN system_state.max_total_leverage  IS '全局总杠杆上限（NULL 表示未设置，将由启动时从 yaml bootstrap）';
COMMENT ON COLUMN system_state.max_daily_loss_usdc IS '日亏熔断阈值（USDC，NULL 表示未设置）';
COMMENT ON COLUMN system_state.feishu_webhook_url  IS '飞书 webhook URL';
COMMENT ON COLUMN system_state.feishu_enabled      IS '是否启用飞书通知';
COMMENT ON COLUMN system_state.telegram_bot_token  IS 'Telegram Bot Token';
COMMENT ON COLUMN system_state.telegram_chat_id    IS 'Telegram Chat ID';
COMMENT ON COLUMN system_state.telegram_enabled    IS '是否启用 Telegram 通知';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE system_state
  DROP COLUMN IF EXISTS max_total_leverage,
  DROP COLUMN IF EXISTS max_daily_loss_usdc,
  DROP COLUMN IF EXISTS feishu_webhook_url,
  DROP COLUMN IF EXISTS feishu_enabled,
  DROP COLUMN IF EXISTS telegram_bot_token,
  DROP COLUMN IF EXISTS telegram_chat_id,
  DROP COLUMN IF EXISTS telegram_enabled;
-- +goose StatementEnd
