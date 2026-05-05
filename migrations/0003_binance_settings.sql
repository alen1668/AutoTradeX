-- +goose Up
-- +goose StatementBegin

ALTER TABLE system_state
  ADD COLUMN binance_api_key    TEXT,
  ADD COLUMN binance_api_secret TEXT;

COMMENT ON COLUMN system_state.binance_api_key    IS 'Binance API Key（live + testnet 共用，根据 BOT_MODE 选择 endpoint）';
COMMENT ON COLUMN system_state.binance_api_secret IS 'Binance API Secret';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE system_state
  DROP COLUMN IF EXISTS binance_api_key,
  DROP COLUMN IF EXISTS binance_api_secret;
-- +goose StatementEnd
