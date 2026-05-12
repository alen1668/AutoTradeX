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
	// 登录后默认进收益首页 — 用户最关心盈亏，其次再看策略/持仓
	http.Redirect(w, r, "/stats", http.StatusSeeOther)
}

func (h *AuthHandler) PostLogout(w http.ResponseWriter, r *http.Request) {
	_ = h.sess.Destroy(r.Context())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// silence unused
var _ = context.Background
