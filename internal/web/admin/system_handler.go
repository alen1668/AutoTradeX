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
	dest := r.Referer()
	if dest == "" {
		dest = "/strategies"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func (h *SystemHandler) Disarm(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.Disarm(r.Context(), h.pool); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dest := r.Referer()
	if dest == "" {
		dest = "/strategies"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func (h *SystemHandler) ResetBreaker(w http.ResponseWriter, r *http.Request) {
	if err := h.repo.ResetBreaker(r.Context(), h.pool); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dest := r.Referer()
	if dest == "" {
		dest = "/strategies"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
