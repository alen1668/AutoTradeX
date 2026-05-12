-- +goose Up
-- +goose StatementBegin

ALTER TABLE system_state
  ADD COLUMN critique_auto_pin_confidence TEXT NOT NULL DEFAULT 'high';

COMMENT ON COLUMN system_state.critique_auto_pin_confidence
  IS 'critique LLM 写入后自动钉选哪些置信度: off | high | medium | low | all';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE system_state DROP COLUMN critique_auto_pin_confidence;
-- +goose StatementEnd
