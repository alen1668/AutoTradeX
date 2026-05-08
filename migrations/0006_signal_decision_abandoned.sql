-- +goose Up
-- +goose StatementBegin

COMMENT ON COLUMN signals.decision IS
  'pending|accepted|duplicate|risk_denied|disarmed|invalid|abandoned. '
  'abandoned = signal stayed pending more than 10 minutes (likely bot crash); '
  'operator should review.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
COMMENT ON COLUMN signals.decision IS NULL;
-- +goose StatementEnd
