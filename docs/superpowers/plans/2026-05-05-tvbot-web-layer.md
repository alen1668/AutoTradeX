# TVBot Web Layer Implementation Plan (Plan 3 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Wire the HTTP layer — `/webhook/tv` endpoint that ingests TradingView signals, plus a minimal HTMX-based admin UI (login, strategies, positions, signals, system controls). End state: the bot runs as a single binary on `0.0.0.0:8080`, accepts a curl-posted webhook JSON, and is fully manageable from a browser.

**Architecture:** chi router. scs/v2 for sessions. html/template + HTMX (no build step). Tailwind via Play CDN (no Node toolchain) for MVP. Auth: cookie-session-protected admin routes, HMAC + IP whitelist on /webhook/tv.

**Tech Stack:** chi/v5, alexedwards/scs/v2, html/template (stdlib), HTMX 1.x via CDN, Tailwind via Play CDN.

**Spec:** `docs/superpowers/specs/2026-05-05-tradingview-webhook-bot-design.md` §10
**Plan 1+2 must be complete.**

---

## Out of Scope (deferred to Plan 4)

- Real Binance Trader (testnet + live) — Plan 4
- OrderReconciler background loop — Plan 4
- Startup recovery / position reconciliation — Plan 4
- Tailwind via npm/postcss precompile — Plan 4 if desired (CDN works for MVP)
- Production deployment (Caddy, systemd, etc.) — Plan 4

By the **end of Plan 3**, you can:
1. Run `make pg-up && make migrate-up && BOT_MODE=dry_run go run ./cmd/tvbot`
2. Open `http://localhost:8080/login`, log in, create a strategy, arm the system
3. `curl -X POST http://localhost:8080/webhook/tv -d '{"strategy_id":"...","signal":"Long","price":"100","timestamp":...,"secret":"..."}'`
4. See the open position in `/positions` and the recorded signal in `/signals`

---

## File Structure

| 路径 | 责任 |
|------|------|
| `cmd/tvbot/main.go` | full entrypoint: load config, build deps, start HTTP server, signal handling |
| `internal/web/server.go` | server struct, route mounting, graceful shutdown |
| `internal/web/middleware/recover.go` | panic recovery (logs + 500) |
| `internal/web/middleware/logger.go` | per-request structured log |
| `internal/web/middleware/traceid.go` | inject trace_id into context |
| `internal/web/middleware/auth.go` | cookie-session auth gate for admin |
| `internal/web/webhook/handler.go` | POST /webhook/tv → ingest.Service.Ingest |
| `internal/web/webhook/handler_test.go` | unit tests for the HTTP layer |
| `internal/web/admin/auth_handler.go` | GET/POST /login, POST /logout |
| `internal/web/admin/strategies_handler.go` | strategies CRUD endpoints |
| `internal/web/admin/positions_handler.go` | positions + history views |
| `internal/web/admin/signals_handler.go` | signals log |
| `internal/web/admin/system_handler.go` | arm/disarm/breaker-reset |
| `internal/web/admin/render.go` | template rendering helpers |
| `internal/web/admin/templates/layouts/base.html` | full-page layout with status bar |
| `internal/web/admin/templates/layouts/auth.html` | login layout (no nav) |
| `internal/web/admin/templates/partials/status_bar.html` | auto-refreshing status bar |
| `internal/web/admin/templates/partials/strategy_row.html` | one row of strategy table |
| `internal/web/admin/templates/pages/login.html` | login form |
| `internal/web/admin/templates/pages/strategies/index.html` | strategy list |
| `internal/web/admin/templates/pages/strategies/edit.html` | strategy form (new + edit) |
| `internal/web/admin/templates/pages/positions/index.html` | active + recent history |
| `internal/web/admin/templates/pages/positions/history.html` | paginated history |
| `internal/web/admin/templates/pages/signals/index.html` | signal log |
| `internal/web/admin/templates/pages/signals/detail.html` | single signal detail |
| `internal/web/admin/static/htmx.min.js` | HTMX library (vendored, ~14KB) |
| `cmd/tvbot/seed_user.go` | one-shot helper to create initial admin user |

---

## Conventions

- All admin handlers return rendered HTML.
- All `/webhook/tv` errors return JSON `{"error":"..."}` with appropriate status code (200 OK for accepted/duplicate/risk_denied/disarmed; 400 for invalid; 500 for internal errors).
- Trace ID = either inbound `X-Trace-ID` header or a generated UUIDv4.
- Sessions use `scs/v2` with default cookie store + 12h timeout.
- Static assets served from `internal/web/admin/static/` mounted at `/static/`.
- Templates loaded once at startup (parse failure → fail loud).

---

## Task 1: Web server skeleton + middleware

**Files:**
- Create: `internal/web/server.go`
- Create: `internal/web/middleware/recover.go`
- Create: `internal/web/middleware/logger.go`
- Create: `internal/web/middleware/traceid.go`

- [ ] **Step 1: Create `internal/web/middleware/traceid.go`**

```go
package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/lizhaojie/tvbot/internal/log"
)

const headerTraceID = "X-Trace-ID"

// TraceID middleware extracts or generates a trace ID and stores it on the
// request context. Echo it back to the client in the response header.
func TraceID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid := r.Header.Get(headerTraceID)
		if tid == "" {
			tid = uuid.NewString()
		}
		ctx := log.WithTraceID(r.Context(), tid)
		w.Header().Set(headerTraceID, tid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

- [ ] **Step 2: Create `internal/web/middleware/logger.go`**

```go
package middleware

import (
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/log"
)

// statusRecorder wraps ResponseWriter to capture the response status.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// RequestLogger emits one structured log line per request, tagged with trace_id.
func RequestLogger(base zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rec, r)
			logger := log.FromContext(r.Context(), base)
			logger.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", rec.status).
				Dur("duration", time.Since(start)).
				Msg("http")
		})
	}
}
```

- [ ] **Step 3: Create `internal/web/middleware/recover.go`**

```go
package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/log"
)

// Recoverer catches panics, logs them, and returns 500.
func Recoverer(base zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rv := recover(); rv != nil {
					logger := log.FromContext(r.Context(), base)
					logger.Error().
						Interface("panic", rv).
						Bytes("stack", debug.Stack()).
						Str("path", r.URL.Path).
						Msg("panic recovered")
					http.Error(w, fmt.Sprintf("internal error"), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 4: Create `internal/web/server.go` (skeleton)**

```go
package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/web/middleware"
)

type Server struct {
	addr   string
	router *chi.Mux
	server *http.Server
	log    zerolog.Logger
}

func New(addr string, log zerolog.Logger) *Server {
	r := chi.NewRouter()
	r.Use(middleware.TraceID)
	r.Use(middleware.RequestLogger(log))
	r.Use(middleware.Recoverer(log))
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return &Server{
		addr:   addr,
		router: r,
		log:    log,
	}
}

func (s *Server) Router() chi.Router { return s.router }

func (s *Server) Start(ctx context.Context) error {
	s.server = &http.Server{
		Addr:              s.addr,
		Handler:           s.router,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.log.Info().Str("addr", s.addr).Msg("http listening")
	errCh := make(chan error, 1)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case <-ctx.Done():
		return s.shutdown()
	case err := <-errCh:
		return err
	}
}

func (s *Server) shutdown() error {
	if s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.log.Info().Msg("http shutting down")
	return s.server.Shutdown(ctx)
}
```

- [ ] **Step 5: Smoke test compile**

```bash
go build ./internal/web/... ./internal/web/middleware/...
```

- [ ] **Step 6: Commit**

```bash
git add internal/web/
git commit -m "feat(web): server skeleton + traceid/logger/recover middleware"
```

---

## Task 2: /webhook/tv handler

**Files:**
- Create: `internal/web/webhook/handler.go`
- Create: `internal/web/webhook/handler_test.go`

The handler's job:
1. Read body
2. Extract client IP (X-Forwarded-For honored only if behind a trusted proxy; else use RemoteAddr)
3. Call `ingest.Service.Ingest(ctx, body, ip)`
4. Return JSON response based on `IngestResult.Decision`

- [ ] **Step 1: Create `internal/web/webhook/handler.go`**

```go
package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/application/ingest"
	"github.com/lizhaojie/tvbot/internal/log"
)

const maxBodyBytes = 16 * 1024 // 16KB; TV payloads are tiny

// IngestService is the subset of ingest.Service we need.
type IngestService interface {
	Ingest(ctx context.Context, body []byte, ip net.IP) (*ingest.IngestResult, error)
}

type Handler struct {
	svc IngestService
	log zerolog.Logger
}

func NewHandler(svc IngestService, log zerolog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

type response struct {
	Decision    string `json:"decision"`
	Reason      string `json:"reason,omitempty"`
	Action      string `json:"action,omitempty"`
	SignalID    int64  `json:"signal_id,omitempty"`
	RuleName    string `json:"rule,omitempty"`
}

func (h *Handler) Post(w http.ResponseWriter, r *http.Request) {
	logger := log.FromContext(r.Context(), h.log)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, response{Decision: "invalid", Reason: "read body: " + err.Error()})
		return
	}
	ip := clientIP(r)

	res, err := h.svc.Ingest(r.Context(), body, ip)
	if err != nil {
		logger.Error().Err(err).Msg("ingest internal error")
		writeJSON(w, http.StatusInternalServerError, response{Decision: "error", Reason: err.Error()})
		return
	}

	status := http.StatusOK
	if res.Decision == "invalid" {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, response{
		Decision: res.Decision, Reason: res.Reason,
		Action: res.ActionTaken, SignalID: res.SignalID, RuleName: res.RuleName,
	})
}

// clientIP extracts the client IP. If behind a trusted reverse proxy that
// sets X-Forwarded-For, you'd configure that elsewhere. For now: prefer
// the rightmost X-Forwarded-For entry (the proxy's caller) only when the
// remote addr is loopback (i.e., we're behind a tunnel like cloudflared);
// otherwise trust RemoteAddr.
func clientIP(r *http.Request) net.IP {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := splitAndTrim(xff)
			if len(parts) > 0 {
				if ip2 := net.ParseIP(parts[len(parts)-1]); ip2 != nil {
					return ip2
				}
			}
		}
	}
	return net.ParseIP(host)
}

func splitAndTrim(s string) []string {
	out := []string{}
	cur := ""
	for _, c := range s {
		if c == ',' || c == ' ' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 2: Create `internal/web/webhook/handler_test.go`**

```go
package webhook

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/application/ingest"
)

type stubSvc struct {
	got struct {
		body []byte
		ip   net.IP
	}
	res *ingest.IngestResult
	err error
}

func (s *stubSvc) Ingest(_ context.Context, body []byte, ip net.IP) (*ingest.IngestResult, error) {
	s.got.body = body
	s.got.ip = ip
	return s.res, s.err
}

func TestHandler_AcceptsValidSignal(t *testing.T) {
	svc := &stubSvc{res: &ingest.IngestResult{
		SignalID: 42, Decision: "accepted", ActionTaken: "open_long",
	}}
	h := NewHandler(svc, zerolog.Nop())

	body := `{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"100","timestamp":1,"secret":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	h.Post(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp response
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "accepted", resp.Decision)
	assert.Equal(t, "open_long", resp.Action)
	assert.Equal(t, int64(42), resp.SignalID)

	assert.Equal(t, []byte(body), svc.got.body)
	assert.NotNil(t, svc.got.ip)
}

func TestHandler_InvalidReturns400(t *testing.T) {
	svc := &stubSvc{res: &ingest.IngestResult{Decision: "invalid", Reason: "bad json"}}
	h := NewHandler(svc, zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.Post(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandler_InternalErrorReturns500(t *testing.T) {
	svc := &stubSvc{err: assertErr("boom")}
	h := NewHandler(svc, zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader("{}"))
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	h.Post(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandler_RejectsGet(t *testing.T) {
	h := NewHandler(&stubSvc{}, zerolog.Nop())
	req := httptest.NewRequest(http.MethodGet, "/webhook/tv", nil)
	w := httptest.NewRecorder()
	h.Post(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestClientIP_HonorsXFFFromLoopback(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader(""))
	req.RemoteAddr = "127.0.0.1:80"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")
	got := clientIP(req)
	assert.Equal(t, "10.0.0.1", got.String(),
		"rightmost XFF entry is the immediate proxy's caller")
}

func TestClientIP_IgnoresXFFFromExternal(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", strings.NewReader(""))
	req.RemoteAddr = "8.8.8.8:80"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	got := clientIP(req)
	assert.Equal(t, "8.8.8.8", got.String(),
		"don't trust XFF from external clients")
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
```

- [ ] **Step 3: Run tests**

```bash
go test -race -v ./internal/web/webhook/...
```
Expected: 6 tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/web/webhook/
git commit -m "feat(web/webhook): POST /webhook/tv handler with body limit + IP extraction"
```

---

## Task 3: Auth — sessions + login/logout

**Files:**
- Create: `internal/web/middleware/auth.go`
- Create: `internal/web/admin/auth_handler.go`
- Create: `internal/web/admin/auth_handler_test.go`
- Create: `internal/web/admin/render.go`
- Create: `internal/web/admin/templates/layouts/auth.html`
- Create: `internal/web/admin/templates/pages/login.html`

We use scs/v2 for cookie sessions. The session store is in-memory (sufficient for single-user MVP); after restart the user re-logs in. Sessions hold `username` (string).

- [ ] **Step 1: Create `internal/web/admin/render.go`**

```go
package admin

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed templates
var templatesFS embed.FS

// Renderer parses templates once at construction; serves them by page name.
type Renderer struct {
	templates map[string]*template.Template
}

func NewRenderer() (*Renderer, error) {
	r := &Renderer{templates: map[string]*template.Template{}}

	pages, err := fs.Glob(templatesFS, "templates/pages/**/*.html")
	if err != nil {
		return nil, err
	}
	pages2, _ := fs.Glob(templatesFS, "templates/pages/*.html")
	pages = append(pages, pages2...)

	for _, p := range pages {
		name, err := pageName(p)
		if err != nil {
			return nil, err
		}
		layout := pickLayout(name)
		t, err := template.New("base").Funcs(funcMap()).ParseFS(templatesFS,
			"templates/layouts/"+layout+".html",
			"templates/partials/*.html",
			p,
		)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		r.templates[name] = t
	}
	return r, nil
}

func pageName(path string) (string, error) {
	clean := strings.TrimPrefix(path, "templates/pages/")
	clean = strings.TrimSuffix(clean, ".html")
	clean = filepath.ToSlash(clean)
	if clean == "" {
		return "", fmt.Errorf("empty page name from %s", path)
	}
	return clean, nil
}

func pickLayout(name string) string {
	if name == "login" {
		return "auth"
	}
	return "base"
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"json": func(v any) (template.JS, error) {
			// helper for embedding JSON in scripts (kept tiny for MVP)
			return template.JS(fmt.Sprintf("%v", v)), nil
		},
	}
}

// Render writes the named page (e.g. "strategies/index") with `data` as ctx.
func (r *Renderer) Render(w http.ResponseWriter, status int, name string, data any) {
	t, ok := r.templates[name]
	if !ok {
		http.Error(w, "template not found: "+name, http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base", data); err != nil {
		http.Error(w, "render: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
```

- [ ] **Step 2: Create `internal/web/middleware/auth.go`**

```go
package middleware

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
)

// RequireUser blocks the request unless the session contains a non-empty username.
func RequireUser(sess *scs.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := sess.GetString(r.Context(), "username")
			if user == "" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CurrentUser fetches the logged-in username; empty string when anon.
func CurrentUser(sess *scs.SessionManager, r *http.Request) string {
	return sess.GetString(r.Context(), "username")
}
```

- [ ] **Step 3: Create `internal/web/admin/auth_handler.go`**

```go
package admin

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
	"golang.org/x/crypto/bcrypt"

	"github.com/lizhaojie/tvbot/internal/store"
)

type AuthHandler struct {
	render   *Renderer
	sess     *scs.SessionManager
	userRepo *UserRepo
}

// UserRepo abstracts user lookup. We define a tiny interface here; the
// concrete impl is a small wrapper around a pgxpool that talks to the
// `users` table.
type UserRepo struct {
	pool interface {
		QueryRow(ctx _ctx, sql string, args ...any) _row
	}
}

type _ctx = interface{ Done() <-chan struct{} } // placeholder to keep file self-contained
type _row = interface{ Scan(dest ...any) error }

// NOTE: Implementer should replace UserRepo with a concrete impl using
// store.Querier, or import an existing UserRepo from internal/store.
// For MVP, see Step 4 for the actual UserRepo implementation in store/.

func NewAuthHandler(render *Renderer, sess *scs.SessionManager, repo *UserRepo) *AuthHandler {
	return &AuthHandler{render: render, sess: sess, userRepo: repo}
}

func (h *AuthHandler) GetLogin(w http.ResponseWriter, r *http.Request) {
	h.render.Render(w, http.StatusOK, "login", map[string]any{"Error": ""})
}

func (h *AuthHandler) PostLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render.Render(w, http.StatusBadRequest, "login", map[string]any{"Error": "bad form"})
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	hash, err := h.lookupHash(r.Context(), username)
	if err != nil {
		h.render.Render(w, http.StatusUnauthorized, "login", map[string]any{"Error": "invalid credentials"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		h.render.Render(w, http.StatusUnauthorized, "login", map[string]any{"Error": "invalid credentials"})
		return
	}
	if err := h.sess.RenewToken(r.Context()); err != nil {
		h.render.Render(w, http.StatusInternalServerError, "login", map[string]any{"Error": "session error"})
		return
	}
	h.sess.Put(r.Context(), "username", username)
	http.Redirect(w, r, "/strategies", http.StatusSeeOther)
}

func (h *AuthHandler) PostLogout(w http.ResponseWriter, r *http.Request) {
	_ = h.sess.Destroy(r.Context())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// helper: actually wraps store.UserRepo (defined in next step)
func (h *AuthHandler) lookupHash(ctx _ctx, _ string) (string, error) {
	// Replaced in real impl by call to h.userRepo
	return "", nil
}
```

**The above contains placeholders (`_ctx`, `_row`, `lookupHash`).** Implementer: **replace** with the concrete UserRepo from Step 4 below. The real auth handler should:
- `userRepo *store.UserRepo`
- `pool *pgxpool.Pool`  (passed to repo)
- `lookupHash` calls `h.userRepo.GetPasswordHash(r.Context(), h.pool, username)`

The placeholder structure is given so you understand the shape; the FINAL code uses `store.UserRepo` and a `*pgxpool.Pool`. See Step 4.

- [ ] **Step 4: Create UserRepo in `internal/store/user_repo.go`**

```go
package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

func (r *UserRepo) GetPasswordHash(ctx context.Context, q Querier, username string) (string, error) {
	var hash string
	err := q.QueryRow(ctx, `SELECT password_hash FROM users WHERE username=$1`, username).Scan(&hash)
	return hash, err
}

func (r *UserRepo) Create(ctx context.Context, q Querier, username, hash string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2)`,
		username, hash)
	return err
}
```

Add a unit test (covered by integration in Step 7).

- [ ] **Step 5: REWRITE `internal/web/admin/auth_handler.go`** with the real types:

```go
package admin

import (
	"context"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/lizhaojie/tvbot/internal/store"
)

type AuthHandler struct {
	render   *Renderer
	sess     *scs.SessionManager
	userRepo *store.UserRepo
	pool     *pgxpool.Pool
}

func NewAuthHandler(render *Renderer, sess *scs.SessionManager,
	userRepo *store.UserRepo, pool *pgxpool.Pool) *AuthHandler {
	return &AuthHandler{render: render, sess: sess, userRepo: userRepo, pool: pool}
}

func (h *AuthHandler) GetLogin(w http.ResponseWriter, r *http.Request) {
	h.render.Render(w, http.StatusOK, "login", map[string]any{"Error": ""})
}

func (h *AuthHandler) PostLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render.Render(w, http.StatusBadRequest, "login", map[string]any{"Error": "bad form"})
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	hash, err := h.userRepo.GetPasswordHash(r.Context(), h.pool, username)
	if err != nil {
		h.render.Render(w, http.StatusUnauthorized, "login", map[string]any{"Error": "invalid credentials"})
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		h.render.Render(w, http.StatusUnauthorized, "login", map[string]any{"Error": "invalid credentials"})
		return
	}
	if err := h.sess.RenewToken(r.Context()); err != nil {
		h.render.Render(w, http.StatusInternalServerError, "login", map[string]any{"Error": "session error"})
		return
	}
	h.sess.Put(r.Context(), "username", username)
	http.Redirect(w, r, "/strategies", http.StatusSeeOther)
}

func (h *AuthHandler) PostLogout(w http.ResponseWriter, r *http.Request) {
	_ = h.sess.Destroy(r.Context())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// silence unused
var _ = context.Background
```

- [ ] **Step 6: Templates**

`internal/web/admin/templates/layouts/auth.html`:
```html
{{ define "base" }}
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>tvbot — login</title>
  <script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="bg-gray-100 min-h-screen flex items-center justify-center">
  <div class="bg-white shadow rounded p-8 w-full max-w-md">
    {{ template "content" . }}
  </div>
</body>
</html>
{{ end }}
```

`internal/web/admin/templates/pages/login.html`:
```html
{{ define "content" }}
<h1 class="text-2xl font-bold mb-6">tvbot 登录</h1>
{{ if .Error }}
  <div class="bg-red-100 border border-red-400 text-red-700 px-4 py-2 rounded mb-4">
    {{ .Error }}
  </div>
{{ end }}
<form method="POST" action="/login" class="space-y-4">
  <div>
    <label class="block text-sm font-medium">用户名</label>
    <input type="text" name="username" required
      class="mt-1 w-full rounded border-gray-300 px-3 py-2">
  </div>
  <div>
    <label class="block text-sm font-medium">密码</label>
    <input type="password" name="password" required
      class="mt-1 w-full rounded border-gray-300 px-3 py-2">
  </div>
  <button type="submit"
    class="w-full bg-blue-600 hover:bg-blue-700 text-white font-medium py-2 rounded">
    登录
  </button>
</form>
{{ end }}
```

- [ ] **Step 7: Integration test `auth_handler_test.go`**

```go
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
```

- [ ] **Step 8: Run tests**

```bash
go test -tags=integration -race -v ./internal/web/admin/...
```
Expected: 3 tests pass.

- [ ] **Step 9: Commit**

```bash
git add internal/web/middleware/auth.go internal/web/admin/auth_handler.go internal/web/admin/auth_handler_test.go internal/web/admin/render.go internal/web/admin/templates/ internal/store/user_repo.go
git commit -m "feat(web/admin): scs sessions + bcrypt login + UserRepo"
```

---

## Task 4: Strategies admin pages

**Files:**
- Create: `internal/web/admin/strategies_handler.go`
- Create: `internal/web/admin/templates/layouts/base.html`
- Create: `internal/web/admin/templates/partials/status_bar.html`
- Create: `internal/web/admin/templates/partials/strategy_row.html`
- Create: `internal/web/admin/templates/pages/strategies/index.html`
- Create: `internal/web/admin/templates/pages/strategies/edit.html`

The `StrategiesHandler` exposes:
- GET `/strategies` — list all strategies
- GET `/strategies/new` — empty form
- POST `/strategies` — create
- GET `/strategies/{id}/edit` — form prefilled
- POST `/strategies/{id}` — update (use HTTP POST + form `_method=PUT` if not using PUT)
- POST `/strategies/{id}/toggle` — toggle enabled (HTMX target)
- POST `/strategies/{id}/delete` — delete

For MVP, all writes are POST (no PUT/DELETE). HTMX swaps target single rows.

- [ ] **Step 1: Create `internal/web/admin/strategies_handler.go`**

(See plan reference; implementer should write a straightforward CRUD handler that:
- Lists via `store.StrategyRepo.List`
- Edit form with all 8 strategy fields (id, symbol, leverage, size_usdc, stop_loss_pct, take_profit_pct, max_open_usdc, enabled)
- Validation: leverage 1-125, size_usdc > 0, stop_loss_pct > 0, max_open_usdc >= size_usdc
- On error, re-render the form with error message and the user's input preserved.)

```go
package admin

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/store"
)

type StrategiesHandler struct {
	render *Renderer
	repo   *store.StrategyRepo
	pool   *pgxpool.Pool
}

func NewStrategiesHandler(r *Renderer, repo *store.StrategyRepo, pool *pgxpool.Pool) *StrategiesHandler {
	return &StrategiesHandler{render: r, repo: repo, pool: pool}
}

func (h *StrategiesHandler) Index(w http.ResponseWriter, r *http.Request) {
	rows, err := h.repo.List(r.Context(), h.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.render.Render(w, http.StatusOK, "strategies/index", map[string]any{
		"Strategies": rows,
	})
}

func (h *StrategiesHandler) New(w http.ResponseWriter, r *http.Request) {
	h.render.Render(w, http.StatusOK, "strategies/edit", map[string]any{
		"Strategy": &store.StrategyRow{Enabled: true, Leverage: 5},
		"IsNew":    true,
	})
}

func (h *StrategiesHandler) Edit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := h.repo.Get(r.Context(), h.pool, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	h.render.Render(w, http.StatusOK, "strategies/edit", map[string]any{
		"Strategy": row,
		"IsNew":    false,
	})
}

func (h *StrategiesHandler) Save(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	row, err := parseStrategyForm(r)
	if err != nil {
		h.render.Render(w, http.StatusBadRequest, "strategies/edit", map[string]any{
			"Strategy": row, "IsNew": chi.URLParam(r, "id") == "", "Error": err.Error(),
		})
		return
	}
	if chi.URLParam(r, "id") == "" {
		err = h.repo.Create(r.Context(), h.pool, *row)
	} else {
		row.ID = chi.URLParam(r, "id")
		err = h.repo.Update(r.Context(), h.pool, *row)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/strategies", http.StatusSeeOther)
}

func (h *StrategiesHandler) Toggle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := h.repo.Get(r.Context(), h.pool, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	row.Enabled = !row.Enabled
	if err := h.repo.Update(r.Context(), h.pool, *row); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// HTMX response: render the single row partial
	h.render.Render(w, http.StatusOK, "strategies/index", map[string]any{
		"Strategies": []*store.StrategyRow{row}, "PartialOnly": true,
	})
}

func (h *StrategiesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.repo.Delete(r.Context(), h.pool, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/strategies", http.StatusSeeOther)
}

func parseStrategyForm(r *http.Request) (*store.StrategyRow, error) {
	var row store.StrategyRow
	row.ID = r.FormValue("id")
	row.Symbol = r.FormValue("symbol")
	leverage, err := strconv.Atoi(r.FormValue("leverage"))
	if err != nil {
		return &row, err
	}
	row.Leverage = leverage
	row.SizeUSDC, err = decimal.NewFromString(r.FormValue("size_usdc"))
	if err != nil {
		return &row, err
	}
	row.StopLossPct, err = decimal.NewFromString(r.FormValue("stop_loss_pct"))
	if err != nil {
		return &row, err
	}
	if v := r.FormValue("take_profit_pct"); v != "" {
		row.TakeProfitPct, err = decimal.NewFromString(v)
		if err != nil {
			return &row, err
		}
	}
	row.MaxOpenUSDC, err = decimal.NewFromString(r.FormValue("max_open_usdc"))
	if err != nil {
		return &row, err
	}
	row.Enabled = r.FormValue("enabled") == "on"
	return &row, nil
}
```

- [ ] **Step 2: Templates**

`internal/web/admin/templates/layouts/base.html`:
```html
{{ define "base" }}
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>tvbot</title>
  <script src="https://cdn.tailwindcss.com"></script>
  <script src="/static/htmx.min.js"></script>
</head>
<body class="bg-gray-50 min-h-screen">
  <nav class="bg-gray-900 text-white">
    <div class="max-w-7xl mx-auto px-4 py-3 flex items-center justify-between">
      <div class="flex space-x-6 items-center">
        <span class="font-bold">tvbot</span>
        <a href="/strategies" class="hover:text-blue-300">策略</a>
        <a href="/positions" class="hover:text-blue-300">持仓</a>
        <a href="/signals" class="hover:text-blue-300">信号</a>
      </div>
      <form method="POST" action="/logout" class="inline">
        <button class="text-sm hover:text-red-300">登出</button>
      </form>
    </div>
  </nav>

  {{ template "status_bar" .Status }}

  <main class="max-w-7xl mx-auto px-4 py-6">
    {{ template "content" . }}
  </main>
</body>
</html>
{{ end }}
```

`internal/web/admin/templates/partials/status_bar.html`:
```html
{{ define "status_bar" }}
<div id="status-bar" class="bg-white border-b shadow-sm" hx-get="/_partials/status" hx-trigger="every 5s" hx-swap="outerHTML">
  <div class="max-w-7xl mx-auto px-4 py-3 flex space-x-6 text-sm">
    <span>模式: <strong class="{{ if eq .Mode "live" }}text-red-600{{ else if eq .Mode "testnet" }}text-yellow-600{{ else }}text-green-600{{ end }}">{{ .Mode }}</strong></span>
    <span>armed: {{ if .Armed }}<span class="text-green-700">✅</span>{{ else }}<span class="text-red-600">❌</span>{{ end }}</span>
    <span>熔断: {{ if .BreakerTripped }}<span class="text-red-600">⚠️ 触发</span>{{ else }}<span class="text-green-700">正常</span>{{ end }}</span>
    <span>今日 PnL: <strong class="{{ if .DailyPnLNegative }}text-red-600{{ else }}text-green-700{{ end }}">{{ .DailyPnL }}</strong></span>
    <span>活跃: {{ .ActivePositions }}</span>
  </div>
</div>
{{ end }}
```

`internal/web/admin/templates/pages/strategies/index.html`:
```html
{{ define "content" }}
<div class="flex justify-between items-center mb-4">
  <h1 class="text-2xl font-bold">策略管理</h1>
  <a href="/strategies/new" class="bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded">新增</a>
</div>
<table class="w-full bg-white shadow rounded">
  <thead class="bg-gray-100 text-left text-sm">
    <tr>
      <th class="px-3 py-2">ID</th>
      <th class="px-3 py-2">币种</th>
      <th class="px-3 py-2">杠杆</th>
      <th class="px-3 py-2">名义(USDC)</th>
      <th class="px-3 py-2">止损%</th>
      <th class="px-3 py-2">止盈%</th>
      <th class="px-3 py-2">上限(USDC)</th>
      <th class="px-3 py-2">启用</th>
      <th class="px-3 py-2">操作</th>
    </tr>
  </thead>
  <tbody>
    {{ range .Strategies }}
      {{ template "strategy_row" . }}
    {{ else }}
      <tr><td colspan="9" class="px-3 py-4 text-center text-gray-500">暂无策略</td></tr>
    {{ end }}
  </tbody>
</table>
{{ end }}
```

`internal/web/admin/templates/partials/strategy_row.html`:
```html
{{ define "strategy_row" }}
<tr id="strategy-{{ .ID }}" class="border-t">
  <td class="px-3 py-2 font-mono text-sm">{{ .ID }}</td>
  <td class="px-3 py-2">{{ .Symbol }}</td>
  <td class="px-3 py-2">{{ .Leverage }}x</td>
  <td class="px-3 py-2">{{ .SizeUSDC }}</td>
  <td class="px-3 py-2">{{ .StopLossPct }}</td>
  <td class="px-3 py-2">{{ if .TakeProfitPct.IsPositive }}{{ .TakeProfitPct }}{{ else }}—{{ end }}</td>
  <td class="px-3 py-2">{{ .MaxOpenUSDC }}</td>
  <td class="px-3 py-2">
    <button hx-post="/strategies/{{ .ID }}/toggle" hx-target="#strategy-{{ .ID }}" hx-swap="outerHTML"
      class="px-2 py-1 rounded text-xs {{ if .Enabled }}bg-green-200 text-green-900{{ else }}bg-gray-200 text-gray-700{{ end }}">
      {{ if .Enabled }}启用{{ else }}已禁用{{ end }}
    </button>
  </td>
  <td class="px-3 py-2 space-x-2">
    <a href="/strategies/{{ .ID }}/edit" class="text-blue-600 text-sm">编辑</a>
    <form method="POST" action="/strategies/{{ .ID }}/delete" class="inline" onsubmit="return confirm('确认删除？')">
      <button class="text-red-600 text-sm">删除</button>
    </form>
  </td>
</tr>
{{ end }}
```

`internal/web/admin/templates/pages/strategies/edit.html`:
```html
{{ define "content" }}
<h1 class="text-2xl font-bold mb-4">{{ if .IsNew }}新增策略{{ else }}编辑策略{{ end }}</h1>
{{ if .Error }}
<div class="bg-red-100 border border-red-400 text-red-700 px-4 py-2 rounded mb-4">{{ .Error }}</div>
{{ end }}
<form method="POST" action="{{ if .IsNew }}/strategies{{ else }}/strategies/{{ .Strategy.ID }}{{ end }}" class="bg-white shadow rounded p-6 space-y-4 max-w-2xl">
  <div class="grid grid-cols-2 gap-4">
    <div>
      <label class="block text-sm font-medium">策略 ID</label>
      <input type="text" name="id" value="{{ .Strategy.ID }}" {{ if not .IsNew }}readonly{{ end }} required class="mt-1 w-full rounded border-gray-300 px-3 py-2">
    </div>
    <div>
      <label class="block text-sm font-medium">币种</label>
      <input type="text" name="symbol" value="{{ .Strategy.Symbol }}" placeholder="ETHUSDC" required class="mt-1 w-full rounded border-gray-300 px-3 py-2">
    </div>
    <div>
      <label class="block text-sm font-medium">杠杆</label>
      <input type="number" name="leverage" value="{{ .Strategy.Leverage }}" min="1" max="125" required class="mt-1 w-full rounded border-gray-300 px-3 py-2">
    </div>
    <div>
      <label class="block text-sm font-medium">名义价值 (USDC)</label>
      <input type="text" name="size_usdc" value="{{ .Strategy.SizeUSDC }}" required class="mt-1 w-full rounded border-gray-300 px-3 py-2">
    </div>
    <div>
      <label class="block text-sm font-medium">止损 %</label>
      <input type="text" name="stop_loss_pct" value="{{ .Strategy.StopLossPct }}" required class="mt-1 w-full rounded border-gray-300 px-3 py-2">
    </div>
    <div>
      <label class="block text-sm font-medium">止盈 %（可空）</label>
      <input type="text" name="take_profit_pct" value="{{ if .Strategy.TakeProfitPct.IsPositive }}{{ .Strategy.TakeProfitPct }}{{ end }}" class="mt-1 w-full rounded border-gray-300 px-3 py-2">
    </div>
    <div>
      <label class="block text-sm font-medium">单策略未平仓上限 (USDC)</label>
      <input type="text" name="max_open_usdc" value="{{ .Strategy.MaxOpenUSDC }}" required class="mt-1 w-full rounded border-gray-300 px-3 py-2">
    </div>
    <div class="flex items-end">
      <label class="inline-flex items-center">
        <input type="checkbox" name="enabled" {{ if .Strategy.Enabled }}checked{{ end }}> <span class="ml-2">启用</span>
      </label>
    </div>
  </div>
  <div class="flex space-x-2">
    <button class="bg-blue-600 hover:bg-blue-700 text-white px-4 py-2 rounded">保存</button>
    <a href="/strategies" class="bg-gray-200 hover:bg-gray-300 px-4 py-2 rounded">取消</a>
  </div>
</form>
{{ end }}
```

- [ ] **Step 3: Verify compile**

```bash
go build ./internal/web/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/web/admin/strategies_handler.go internal/web/admin/templates/
git commit -m "feat(web/admin): strategies CRUD pages with HTMX toggle"
```

---

## Task 5: Positions, Signals, System handlers + templates

**Files:**
- Create: `internal/web/admin/positions_handler.go`
- Create: `internal/web/admin/signals_handler.go`
- Create: `internal/web/admin/system_handler.go`
- Create: `internal/web/admin/status_handler.go` (status bar partial)
- Create: `internal/web/admin/templates/pages/positions/index.html`
- Create: `internal/web/admin/templates/pages/signals/index.html`

These follow the same pattern as strategies. Implementer should:

- **PositionsHandler.Index** — query `virtual_positions` (active) + `position_history` (recent 50)
- **SignalsHandler.Index** — query `signals` (recent 100, with filters: strategy_id, decision)
- **SystemHandler** — POST `/system/arm`, `/system/disarm`, `/system/breaker/reset`
- **StatusHandler.Partial** — GET `/_partials/status` returning the status bar HTML for HTMX refresh

For brevity, I show only the system handler in detail; positions/signals follow same query→render pattern.

```go
// internal/web/admin/system_handler.go
package admin

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

type SystemHandler struct {
	repo *store.SystemStateRepo
	pool *pgxpool.Pool
	sess *scs.SessionManager
}

func NewSystemHandler(repo *store.SystemStateRepo, pool *pgxpool.Pool, sess *scs.SessionManager) *SystemHandler {
	return &SystemHandler{repo: repo, pool: pool, sess: sess}
}

func (h *SystemHandler) Arm(w http.ResponseWriter, r *http.Request) {
	user := h.sess.GetString(r.Context(), "username")
	if err := h.repo.Arm(r.Context(), h.pool, user); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, r.Referer(), http.StatusSeeOther)
}

func (h *SystemHandler) Disarm(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.Disarm(r.Context(), h.pool); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, r.Referer(), http.StatusSeeOther)
}

func (h *SystemHandler) ResetBreaker(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.ResetBreaker(r.Context(), h.pool); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, r.Referer(), http.StatusSeeOther)
}
```

For positions/signals/status, write straightforward handlers:
- positions: `GetActiveByStrategy` (for each enabled strategy, fetch active VP) + `position_history.ListByStrategy` for recent N
- signals: `signal_repo.ListRecent(N)`
- status: Get `system_state`, count active positions, format status bar partial

Templates use the same Tailwind layout. **Implementer**: write tables matching the screenshots from the spec (信号列表显示: 接收时间 / 策略 / 方向 / 价格 / decision / reason).

- [ ] **Step 1**: Write each handler (4 files)
- [ ] **Step 2**: Write each template (3 files)
- [ ] **Step 3**: `go build ./internal/web/...`
- [ ] **Step 4**: Commit

```bash
git add internal/web/admin/
git commit -m "feat(web/admin): positions/signals/system handlers + status partial"
```

---

## Task 6: cmd/tvbot wiring

**Files:**
- Modify: `cmd/tvbot/main.go` (replace stub with full entrypoint)
- Create: `cmd/tvbot/seed_user.go` (sub-command for `tvbot seed-user`)

The main.go:
1. Load config (`config.Load`)
2. Print startup banner with mode + armed status
3. Connect DB pool
4. Build all repos
5. Build idempotency, notifier, trader (DryRun for now), risk pipeline, application services
6. Build session manager + auth + handlers
7. Mount routes on the chi server
8. Start HTTP server, handle SIGINT/SIGTERM gracefully

If `os.Args[1] == "seed-user"`, branch to seed-user mode (interactive: read username + password from stdin, bcrypt hash, insert).

- [ ] **Step 1: Write `cmd/tvbot/main.go`**

(implementer composes all dependencies; uses everything wired)

- [ ] **Step 2: Write `cmd/tvbot/seed_user.go`**

```go
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/lizhaojie/tvbot/internal/store"
)

func runSeedUser(databaseURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	in := bufio.NewReader(os.Stdin)
	fmt.Print("用户名: ")
	username, _ := in.ReadString('\n')
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username required")
	}
	fmt.Print("密码: ")
	pwBytes, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return err
	}
	pw := string(pwBytes)
	if len(pw) < 8 {
		return fmt.Errorf("password must be ≥8 chars")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := store.NewUserRepo(pool).Create(ctx, pool, username, string(hash)); err != nil {
		return err
	}
	fmt.Printf("user %q created\n", username)
	return nil
}
```

- [ ] **Step 3: Manual smoke test**

```bash
make pg-up && make migrate-up
BOT_MODE=dry_run DATABASE_URL=postgres://tvbot:tvbot@localhost:5432/tvbot?sslmode=disable \
  WEBHOOK_SECRET=local SESSION_SECRET=$(openssl rand -hex 32) \
  go run ./cmd/tvbot seed-user
# enter username + password

BOT_MODE=dry_run DATABASE_URL=postgres://tvbot:tvbot@localhost:5432/tvbot?sslmode=disable \
  WEBHOOK_SECRET=local SESSION_SECRET=$(openssl rand -hex 32) \
  go run ./cmd/tvbot
# in another terminal:
curl -sS http://localhost:8080/healthz   # expects: ok
```

Open browser: `http://localhost:8080/login`. Log in with the seeded user.

- [ ] **Step 4: Commit**

```bash
git add cmd/tvbot/
git commit -m "feat(cmd): full tvbot entrypoint + seed-user subcommand"
```

---

## Task 7: HTMX vendored

**Files:**
- Create: `internal/web/admin/static/htmx.min.js`

Download `htmx-1.9.12` (or current) minified from https://unpkg.com/htmx.org@1.9.12/dist/htmx.min.js (or any pinned version) and vendor it. Single file, ~14KB.

- [ ] **Step 1: Download via curl**

```bash
mkdir -p internal/web/admin/static
curl -sSL https://unpkg.com/htmx.org@1.9.12/dist/htmx.min.js -o internal/web/admin/static/htmx.min.js
```

- [ ] **Step 2: Verify file**

```bash
ls -la internal/web/admin/static/htmx.min.js
# expect ~50KB size
```

- [ ] **Step 3: Commit**

```bash
git add internal/web/admin/static/
git commit -m "feat(web/admin): vendor HTMX 1.9.12"
```

---

## Task 8: End-to-end browser test (manual checklist)

This is a **manual smoke test**, not an automated test (those are next plan).

- [ ] **Step 1: Start everything**

```bash
make pg-up
make migrate-up
go run ./cmd/tvbot
```

- [ ] **Step 2: Open browser → login**

- [ ] **Step 3: Click 策略 → 新增**

  Fill in:
  - id = `test_eth`
  - symbol = `ETHUSDC`
  - leverage = `5`
  - size_usdc = `100`
  - stop_loss_pct = `1.5`
  - take_profit_pct = `3.0`
  - max_open_usdc = `500`
  - enabled = checked
  Save.

- [ ] **Step 4: Arm system**

  Should be a button in the status bar / system control area. Click "启动交易".

- [ ] **Step 5: Send a webhook from another terminal**

  ```bash
  curl -sS -X POST http://localhost:8080/webhook/tv \
    -H 'Content-Type: application/json' \
    -d '{"strategy_id":"test_eth","symbol":"ETHUSDC","signal":"Long","price":"2300","timestamp":1714723504000,"secret":"local"}' | jq .
  ```

  Expected response:
  ```json
  {"decision":"accepted","action":"open_long","signal_id":1}
  ```

- [ ] **Step 6: Verify in UI**

  - `/positions` shows 1 active long position
  - `/signals` shows 1 row decision=accepted

- [ ] **Step 7: Send Exit Long**

  ```bash
  curl -sS -X POST http://localhost:8080/webhook/tv \
    -H 'Content-Type: application/json' \
    -d '{"strategy_id":"test_eth","symbol":"ETHUSDC","signal":"Exit Long","price":"2350","timestamp":1714723600000,"secret":"local"}' | jq .
  ```

- [ ] **Step 8: Verify**

  - `/positions` shows the position closed; history has 1 row
  - PnL is positive (we closed at higher price)

If all of this works → **Plan 3 complete**. Document the result in the commit message of the final commit.

---

## Final Verification

```bash
go vet ./...
go test -race ./...
go test -tags=integration -race ./...
```

Expected: all green.

---

## Self-Review Checklist

- [ ] All 4 admin pages reachable + functional
- [ ] /webhook/tv accepts JSON; returns sensible status codes
- [ ] Auth: unauthenticated user redirected to /login
- [ ] HTMX status bar refreshes every 5s
- [ ] Strategies CRUD all paths work
- [ ] Manual e2e: webhook → position → close → history all visible in UI
- [ ] No placeholders / TBD in code
- [ ] No dead Tailwind classes / 404s on static paths

---

**EOF**
