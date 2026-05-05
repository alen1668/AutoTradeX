package admin

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

type SignalsHandler struct {
	render        *Renderer
	pool          *pgxpool.Pool
	repo          *store.SignalRepo
	statusHandler *StatusHandler
}

func NewSignalsHandler(r *Renderer, pool *pgxpool.Pool, repo *store.SignalRepo, sh *StatusHandler) *SignalsHandler {
	return &SignalsHandler{render: r, pool: pool, repo: repo, statusHandler: sh}
}

func (h *SignalsHandler) Index(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	rows, err := h.repo.ListRecent(r.Context(), h.pool, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := h.statusHandler.WithStatus(r, map[string]any{
		"Signals": rows,
	})
	h.render.Render(w, http.StatusOK, "signals/index", data)
}
