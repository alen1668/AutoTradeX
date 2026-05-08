-- +goose Up
-- +goose StatementBegin

ALTER TABLE strategies
  ADD COLUMN archived BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN strategies.archived IS '已归档（操作员主动收纳，不在主列表显示，但保留历史与外键关联）';

CREATE INDEX idx_strategies_archived ON strategies(archived);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_strategies_archived;
ALTER TABLE strategies DROP COLUMN IF EXISTS archived;
-- +goose StatementEnd
