package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/stretchr/testify/require"
)

// testPool creates an ephemeral postgres container, applies migrations, and
// returns a connected pool. Cleans up automatically via t.Cleanup.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skip integration test in -short mode")
	}

	pool, err := dockertest.NewPool("")
	require.NoError(t, err, "dockertest.NewPool")
	pool.MaxWait = 60 * time.Second

	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "16-alpine",
		Env: []string{
			"POSTGRES_USER=test",
			"POSTGRES_PASSWORD=test",
			"POSTGRES_DB=test",
		},
	}, func(cfg *docker.HostConfig) {
		cfg.AutoRemove = true
		cfg.RestartPolicy = docker.RestartPolicy{Name: "no"}
	})
	require.NoError(t, err, "dockertest run postgres")
	t.Cleanup(func() { _ = pool.Purge(resource) })

	hostPort := resource.GetHostPort("5432/tcp")
	dsn := fmt.Sprintf("postgres://test:test@%s/test?sslmode=disable", hostPort)

	var pgPool *pgxpool.Pool
	require.NoError(t, pool.Retry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		p, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		if err := p.Ping(ctx); err != nil {
			p.Close()
			return err
		}
		pgPool = p
		return nil
	}))
	t.Cleanup(pgPool.Close)

	applyMigrations(t, pgPool)
	return pgPool
}

func applyMigrations(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
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
		"../../migrations/0010_market_regime.sql",
		"../../migrations/0011_wecom_notifier.sql",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := p.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	for _, rel := range migrations {
		migPath, err := filepath.Abs(rel)
		require.NoError(t, err)
		data, err := os.ReadFile(migPath)
		require.NoError(t, err)
		// Crude split on -- +goose markers and execute the Up StatementBegin block.
		sql := extractGooseUp(string(data))
		_, err = conn.Exec(ctx, sql)
		require.NoError(t, err, "apply migration %s", rel)
	}
}

// extractGooseUp pulls the body between `-- +goose Up\n-- +goose StatementBegin`
// and `-- +goose StatementEnd` (only the Up block).
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

// helper used by repos that mutate timestamps
var _ = pgx.ErrNoRows
