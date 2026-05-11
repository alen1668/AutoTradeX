package admin

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/eval"
)

// EvalHandler renders /eval/* pages. All pages are read-only in Phase 1.
type EvalHandler struct {
	render  *Renderer
	pool    *pgxpool.Pool
	store   *eval.Store
	statusH *StatusHandler // injected later via WithStatus; nil-safe for tests
}

func NewEvalHandler(r *Renderer, pool *pgxpool.Pool) *EvalHandler {
	var store *eval.Store
	if pool != nil {
		store = eval.NewStore(pool)
	}
	return &EvalHandler{render: r, pool: pool, store: store}
}

// WithStatus injects the global status handler so the layout can render the
// status bar. Called by cmd/tvbot/main.go after both are constructed.
func (h *EvalHandler) WithStatus(s *StatusHandler) *EvalHandler {
	h.statusH = s
	return h
}

// Index handles GET /eval. Implemented in Task 10.
func (h *EvalHandler) Index(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// ReplayList handles GET /eval/replays. Implemented in Task 11.
func (h *EvalHandler) ReplayList(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// ReplayDetail handles GET /eval/replays/{id}. Implemented in Task 12.
func (h *EvalHandler) ReplayDetail(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id")
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// ReplayRowsPartial handles GET /eval/replays/{id}/rows (HTMX lazy fragment).
// Implemented in Task 13.
func (h *EvalHandler) ReplayRowsPartial(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id")
	http.Error(w, "not implemented", http.StatusNotImplemented)
}

// withTimeout shortens r.Context() to 5s; used by all eval queries.
func withTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 5*time.Second)
}

// parseInt64ID parses chi URL param "id" as int64. Returns 0 on failure.
func parseInt64ID(r *http.Request) int64 {
	var id int64
	_, _ = fmt.Sscanf(chi.URLParam(r, "id"), "%d", &id)
	return id
}
