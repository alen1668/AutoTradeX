package admin

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/config"
	"github.com/lizhaojie/tvbot/internal/store"
)

type SystemHandler struct {
	repo    *store.SystemStateRepo
	pool    *pgxpool.Pool
	sess    *scs.SessionManager
	render  *Renderer
	statusH *StatusHandler
	mode    config.BotMode
}

func NewSystemHandler(
	repo *store.SystemStateRepo,
	pool *pgxpool.Pool,
	sess *scs.SessionManager,
	render *Renderer,
	statusH *StatusHandler,
	mode config.BotMode,
) *SystemHandler {
	return &SystemHandler{
		repo:    repo,
		pool:    pool,
		sess:    sess,
		render:  render,
		statusH: statusH,
		mode:    mode,
	}
}

func (h *SystemHandler) Index(w http.ResponseWriter, r *http.Request) {
	state, err := h.repo.Get(r.Context(), h.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := h.statusH.WithStatus(r, map[string]any{
		"State": state,
		"Mode":  string(h.mode),
	})
	h.render.Render(w, http.StatusOK, "system/index", data)
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
