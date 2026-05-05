-- +goose Up
-- +goose StatementBegin

ALTER TABLE system_state
  ADD COLUMN webhook_secret              TEXT,
  ADD COLUMN ip_whitelist                TEXT[],
  ADD COLUMN reconciler_interval_seconds INT,
  ADD COLUMN binance_recv_window_ms      INT,
  ADD COLUMN binance_order_timeout_ms    INT;

COMMENT ON COLUMN system_state.webhook_secret              IS 'TradingView webhook 共享密钥';
COMMENT ON COLUMN system_state.ip_whitelist                IS 'webhook 接收的 IP/CIDR 白名单（NULL=未 bootstrap）';
COMMENT ON COLUMN system_state.reconciler_interval_seconds IS '订单对账周期（秒），需重启';
COMMENT ON COLUMN system_state.binance_recv_window_ms      IS 'Binance API recv_window 毫秒，需重启';
COMMENT ON COLUMN system_state.binance_order_timeout_ms    IS 'Binance 下单超时毫秒，需重启';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE system_state
  DROP COLUMN IF EXISTS webhook_secret,
  DROP COLUMN IF EXISTS ip_whitelist,
  DROP COLUMN IF EXISTS reconciler_interval_seconds,
  DROP COLUMN IF EXISTS binance_recv_window_ms,
  DROP COLUMN IF EXISTS binance_order_timeout_ms;
-- +goose StatementEnd
