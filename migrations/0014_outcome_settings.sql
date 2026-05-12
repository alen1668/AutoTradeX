-- +goose Up
-- +goose StatementBegin

ALTER TABLE system_state
  ADD COLUMN outcome_horizon_min        INTEGER      NOT NULL DEFAULT 60,
  ADD COLUMN outcome_win_threshold_pct  NUMERIC(6,4) NOT NULL DEFAULT 0.003,
  ADD COLUMN outcome_loss_threshold_pct NUMERIC(6,4) NOT NULL DEFAULT -0.003,
  ADD COLUMN outcome_batch_size         INTEGER      NOT NULL DEFAULT 200,
  ADD COLUMN outcome_scan_interval_min  INTEGER      NOT NULL DEFAULT 5,
  ADD COLUMN outcome_stale_cutoff_h     INTEGER      NOT NULL DEFAULT 24;

COMMENT ON COLUMN system_state.outcome_horizon_min       IS 'abandon 反事实持有期 (分钟)';
COMMENT ON COLUMN system_state.outcome_win_threshold_pct IS '反事实 win 阈值, 0.003 = +0.3%';
COMMENT ON COLUMN system_state.outcome_loss_threshold_pct IS '反事实 loss 阈值, -0.003 = -0.3%';
COMMENT ON COLUMN system_state.outcome_batch_size        IS 'outcome worker 每批扫描行数';
COMMENT ON COLUMN system_state.outcome_scan_interval_min IS 'outcome worker 扫描周期 (分钟)';
COMMENT ON COLUMN system_state.outcome_stale_cutoff_h    IS '超过 N 小时仍无反事实价 → 标记 unavailable';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE system_state
  DROP COLUMN outcome_stale_cutoff_h,
  DROP COLUMN outcome_scan_interval_min,
  DROP COLUMN outcome_batch_size,
  DROP COLUMN outcome_loss_threshold_pct,
  DROP COLUMN outcome_win_threshold_pct,
  DROP COLUMN outcome_horizon_min;
-- +goose StatementEnd
