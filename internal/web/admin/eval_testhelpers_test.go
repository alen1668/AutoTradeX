//go:build integration

package admin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/stretchr/testify/require"
)

// newEvalTestPool 启 临时 postgres + apply 所有 9 个迁移,返回 pool。
// 比 setupAuthDB 多 apply 0002~0009(eval handler 需要 signals.agent_score
// 列 + replay_runs 两张表)。
func newEvalTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dp, err := dockertest.NewPool("")
	require.NoError(t, err)
	dp.MaxWait = 60 * time.Second
	res, err := dp.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres", Tag: "16-alpine",
		Env: []string{"POSTGRES_USER=test", "POSTGRES_PASSWORD=test", "POSTGRES_DB=test"},
	}, func(c *docker.HostConfig) {
		c.AutoRemove = true
		c.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = dp.Purge(res) })

	dsn := fmt.Sprintf("postgres://test:test@%s/test?sslmode=disable", res.GetHostPort("5432/tcp"))
	var pool *pgxpool.Pool
	require.NoError(t, dp.Retry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var e error
		pool, e = pgxpool.New(ctx, dsn)
		if e != nil {
			return e
		}
		return pool.Ping(ctx)
	}))
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, rel := range []string{
		"../../../migrations/0001_init.sql",
		"../../../migrations/0002_settings.sql",
		"../../../migrations/0003_binance_settings.sql",
		"../../../migrations/0004_more_settings.sql",
		"../../../migrations/0005_strategies_archived.sql",
		"../../../migrations/0006_signal_decision_abandoned.sql",
		"../../../migrations/0007_agent_scoring.sql",
		"../../../migrations/0008_agent_scorer_default_sonnet46.sql",
		"../../../migrations/0009_replay_runs.sql",
	} {
		abs, err := filepath.Abs(rel)
		require.NoError(t, err)
		body, err := os.ReadFile(abs)
		require.NoError(t, err, "read %s", abs)
		sql := extractGooseUpFull(string(body))
		_, err = pool.Exec(ctx, sql)
		require.NoError(t, err, "apply %s", rel)
	}
	return pool
}

// extractGooseUpFull is similar to admin/extractGooseUp but anchored at
// "-- +goose Up" so files containing both Up and Down sections only emit
// the Up body.
func extractGooseUpFull(s string) string {
	start := "-- +goose Up"
	begin := "-- +goose StatementBegin"
	end := "-- +goose StatementEnd"
	i := indexAfter(s, start)
	if i < 0 {
		return ""
	}
	j := indexAfter(s[i:], begin)
	if j < 0 {
		return ""
	}
	body := s[i+j:]
	k := indexAfter(body, end)
	if k < 0 {
		return body
	}
	return body[:k-len(end)]
}
