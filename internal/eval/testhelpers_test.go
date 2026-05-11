//go:build integration

package eval

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

// newTestPool 启动一个临时 postgres + 应用全部迁移,返回连接 pool。
// Mirrors internal/store/testhelpers_test.go testPool but lives here
// because that helper is package-private to internal/store.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skip integration test in -short mode")
	}
	dp, err := dockertest.NewPool("")
	require.NoError(t, err)
	dp.MaxWait = 60 * time.Second

	resource, err := dp.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres", Tag: "16-alpine",
		Env: []string{"POSTGRES_USER=test", "POSTGRES_PASSWORD=test", "POSTGRES_DB=test"},
	}, func(cfg *docker.HostConfig) {
		cfg.AutoRemove = true
		cfg.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = dp.Purge(resource) })

	dsn := fmt.Sprintf("postgres://test:test@localhost:%s/test?sslmode=disable",
		resource.GetPort("5432/tcp"))

	var pool *pgxpool.Pool
	err = dp.Retry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var e error
		pool, e = pgxpool.New(ctx, dsn)
		if e != nil {
			return e
		}
		return pool.Ping(ctx)
	})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	migrations := []string{
		"../../migrations/0001_init.sql",
		"../../migrations/0002_settings.sql",
		"../../migrations/0003_binance_settings.sql",
		"../../migrations/0004_more_settings.sql",
		"../../migrations/0005_strategies_archived.sql",
		"../../migrations/0006_signal_decision_abandoned.sql",
		"../../migrations/0007_agent_scoring.sql",
		"../../migrations/0008_agent_scorer_default_sonnet46.sql",
		"../../migrations/0009_replay_runs.sql",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	for _, rel := range migrations {
		abs, err := filepath.Abs(rel)
		require.NoError(t, err)
		body, err := os.ReadFile(abs)
		require.NoError(t, err)
		sql := extractGooseUp(string(body))
		_, err = conn.Exec(ctx, sql)
		require.NoError(t, err, "apply %s", rel)
	}
	return pool
}

// extractGooseUp pulls the body between -- +goose Up / StatementBegin /
// StatementEnd. Same approach as internal/store/testhelpers_test.go.
func extractGooseUp(s string) string {
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

func indexAfter(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i + len(sub)
		}
	}
	return -1
}
