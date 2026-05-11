-- +goose Up
-- +goose StatementBegin

CREATE TABLE replay_runs (
  id              BIGSERIAL PRIMARY KEY,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  since_window    TEXT NOT NULL,
  since_cutoff    TIMESTAMPTZ NOT NULL,
  max_n           INT NOT NULL DEFAULT 0,
  concurrency     INT NOT NULL DEFAULT 5,
  model           TEXT NOT NULL,
  prompt_text     TEXT NOT NULL,
  prompt_name     TEXT,
  prompt_sha256   TEXT NOT NULL,
  status          TEXT NOT NULL
                    CHECK (status IN ('pending','running','done','failed','aborted')),
  started_at      TIMESTAMPTZ,
  finished_at     TIMESTAMPTZ,
  error_message   TEXT,
  samples_total   INT NOT NULL DEFAULT 0,
  samples_done    INT NOT NULL DEFAULT 0,
  samples_failed  INT NOT NULL DEFAULT 0,
  summary_json    JSONB
);

COMMENT ON COLUMN replay_runs.since_cutoff IS
  '解析后的绝对时间, 防 ''7d'' 含义随时间漂移';
COMMENT ON COLUMN replay_runs.prompt_text IS
  '完整 prompt 快照, 本地文件丢了也能复现';
COMMENT ON COLUMN replay_runs.summary_json IS
  'Bucket / FlipMatrix / Spearman / ProdSpearman 等聚合快照, 详情页直读免重算';

CREATE INDEX replay_runs_status_created_idx ON replay_runs (status, created_at DESC);
CREATE INDEX replay_runs_prompt_window_idx  ON replay_runs (prompt_sha256, since_window);

CREATE TABLE replay_run_rows (
  id              BIGSERIAL PRIMARY KEY,
  run_id          BIGINT NOT NULL REFERENCES replay_runs(id) ON DELETE CASCADE,
  signal_id       BIGINT NOT NULL REFERENCES signals(id),
  replay_score    INT,
  replay_decision TEXT,
  replay_reason   TEXT,
  prod_score      INT,
  prod_decision   TEXT,
  pnl_usdc        DOUBLE PRECISION,
  error_kind      TEXT,
  replayed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (run_id, signal_id)
);

CREATE INDEX replay_run_rows_run_idx ON replay_run_rows (run_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS replay_run_rows;
DROP TABLE IF EXISTS replay_runs;
-- +goose StatementEnd
