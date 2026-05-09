-- +goose Up
-- +goose StatementBegin

-- 把 agent scorer 默认模型从 Haiku 4.5 升到 Sonnet 4.6:
-- 速度/智能更平衡, 适合 agent scorer 这种"低延迟 + 中等推理"的场景。
ALTER TABLE system_state
    ALTER COLUMN agent_scorer_model SET DEFAULT 'claude-sonnet-4-6';

-- 只迁移仍停留在旧默认值的行, 不覆盖用户手动选择的其他模型。
UPDATE system_state
   SET agent_scorer_model = 'claude-sonnet-4-6'
 WHERE agent_scorer_model = 'claude-haiku-4-5-20251001';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE system_state
    ALTER COLUMN agent_scorer_model SET DEFAULT 'claude-haiku-4-5-20251001';

UPDATE system_state
   SET agent_scorer_model = 'claude-haiku-4-5-20251001'
 WHERE agent_scorer_model = 'claude-sonnet-4-6';

-- +goose StatementEnd
