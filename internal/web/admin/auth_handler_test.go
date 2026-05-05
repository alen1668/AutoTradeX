//go:build integration

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/lizhaojie/tvbot/internal/store"
)

func setupAuthDB(t *testing.T) (*pgxpool.Pool, *store.UserRepo) {
	t.Helper()
	pool, err := dockertest.NewPool("")
	require.NoError(t, err)
	pool.MaxWait = 60 * time.Second
	res, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres", Tag: "16-alpine",
		Env: []string{"POSTGRES_USER=test", "POSTGRES_PASSWORD=test", "POSTGRES_DB=test"},
	}, func(c *docker.HostConfig) { c.AutoRemove = true })
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Purge(res) })

	dsn := "postgres://test:test@" + res.GetHostPort("5432/tcp") + "/test?sslmode=disable"
	var p *pgxpool.Pool
	require.NoError(t, pool.Retry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		pp, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		if err := pp.Ping(ctx); err != nil {
			pp.Close()
			return err
		}
		p = pp
		return nil
	}))
	t.Cleanup(p.Close)

	mig, err := filepath.Abs("../../../migrations/0001_init.sql")
	require.NoError(t, err)
	data, err := os.ReadFile(mig)
	require.NoError(t, err)
	body := extractGooseUp(string(data))
	_, err = p.Exec(context.Background(), body)
	require.NoError(t, err)

	repo := store.NewUserRepo(p)
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.DefaultCost)
	require.NoError(t, repo.Create(context.Background(), p, "alice", string(hash)))
	return p, repo
}

func extractGooseUp(s string) string {
	begin := "-- +goose StatementBegin"
	end := "-- +goose StatementEnd"
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

func TestPostLogin_GoodCredentialsSetsSession(t *testing.T) {
	p, repo := setupAuthDB(t)
	r, err := NewRenderer()
	require.NoError(t, err)
	sess := scs.New()
	h := NewAuthHandler(r, sess, repo, p)

	form := url.Values{"username": {"alice"}, "password": {"secret"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	wrapper := sess.LoadAndSave(http.HandlerFunc(h.PostLogin))
	wrapper.ServeHTTP(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/strategies", w.Header().Get("Location"))
	assert.NotEmpty(t, w.Header().Get("Set-Cookie"))
}

func TestPostLogin_BadPasswordReturns401(t *testing.T) {
	p, repo := setupAuthDB(t)
	r, _ := NewRenderer()
	sess := scs.New()
	h := NewAuthHandler(r, sess, repo, p)

	form := url.Values{"username": {"alice"}, "password": {"WRONG"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	sess.LoadAndSave(http.HandlerFunc(h.PostLogin)).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPostLogin_UnknownUserReturns401(t *testing.T) {
	p, repo := setupAuthDB(t)
	r, _ := NewRenderer()
	sess := scs.New()
	h := NewAuthHandler(r, sess, repo, p)
	form := url.Values{"username": {"bob"}, "password": {"x"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	sess.LoadAndSave(http.HandlerFunc(h.PostLogin)).ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

var _ = scs.Status(0)
