//go:build integration

package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/stretchr/testify/require"
)

// setupDB spins up a postgres container, applies all migrations, and
// returns a pool. Mirrors the helper used in internal/application/ingest.
func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip()
	}
	dpool, err := dockertest.NewPool("")
	require.NoError(t, err)
	dpool.MaxWait = 60 * time.Second

	res, err := dpool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres", Tag: "16-alpine",
		Env: []string{"POSTGRES_USER=test", "POSTGRES_PASSWORD=test", "POSTGRES_DB=test"},
	}, func(c *docker.HostConfig) { c.AutoRemove = true })
	require.NoError(t, err)
	t.Cleanup(func() { _ = dpool.Purge(res) })

	dsn := "postgres://test:test@" + res.GetHostPort("5432/tcp") + "/test?sslmode=disable"
	var p *pgxpool.Pool
	require.NoError(t, dpool.Retry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		var err error
		p, err = pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		return p.Ping(ctx)
	}))
	t.Cleanup(p.Close)

	applyAllMigrations(t, p)
	return p
}

func applyAllMigrations(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	require.NoError(t, err)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	var files []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".sql" {
			files = append(files, filepath.Join(root, e.Name()))
		}
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		_, err = p.Exec(context.Background(), extractGooseUpStmt(string(b)))
		require.NoError(t, err, "applying %s", f)
	}
}

// extractGooseUpStmt returns whatever sits between the first
// "-- +goose StatementBegin" and "-- +goose StatementEnd" in the up
// section. Mirrors the helper used by other test packages.
func extractGooseUpStmt(s string) string {
	const begin = "-- +goose StatementBegin"
	const end = "-- +goose StatementEnd"
	i := indexAfter(s, begin)
	if i < 0 {
		return s
	}
	body := s[i:]
	j := indexAfter(body, end)
	if j < 0 {
		return body
	}
	return body[:j-len(end)]
}

func indexAfter(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i + len(sub)
		}
	}
	return -1
}
