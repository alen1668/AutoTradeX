package admin

import (
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/config"
	"github.com/lizhaojie/tvbot/internal/store"
)

type SystemHandler struct {
	repo         *store.SystemStateRepo
	settingsRepo *store.SettingsRepo
	pool         *pgxpool.Pool
	sess         *scs.SessionManager
	render       *Renderer
	statusH      *StatusHandler
	mode         config.BotMode
}

func NewSystemHandler(
	repo *store.SystemStateRepo,
	settingsRepo *store.SettingsRepo,
	pool *pgxpool.Pool,
	sess *scs.SessionManager,
	render *Renderer,
	statusH *StatusHandler,
	mode config.BotMode,
) *SystemHandler {
	return &SystemHandler{
		repo:         repo,
		settingsRepo: settingsRepo,
		pool:         pool,
		sess:         sess,
		render:       render,
		statusH:      statusH,
		mode:         mode,
	}
}

func (h *SystemHandler) Index(w http.ResponseWriter, r *http.Request) {
	state, err := h.repo.Get(r.Context(), h.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	settings, err := h.settingsRepo.Get(r.Context(), h.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	flashErr := h.sess.PopString(r.Context(), "flash_error")
	data := h.statusH.WithStatus(r, map[string]any{
		"State":      state,
		"Mode":       string(h.mode),
		"Settings":   settings,
		"FlashError": flashErr,
	})
	h.render.Render(w, http.StatusOK, "system/index", data)
}

// EnableAgentScorer turns AI scoring on. Refuses if LLM API key is empty
// — the empty-key precheck is the second of the spec's three safety
// gates (key precheck + explicit-enable + one-click-disable).
func (h *SystemHandler) EnableAgentScorer(w http.ResponseWriter, r *http.Request) {
	s, err := h.settingsRepo.Get(r.Context(), h.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(s.LLMAPIKey) == "" {
		h.sess.Put(r.Context(), "flash_error", "请先在 /settings 配置 LLM API key 后再启用 AI 评分")
		http.Redirect(w, r, "/system", http.StatusSeeOther)
		return
	}
	if err := h.settingsRepo.SetAgentScorerEnabled(r.Context(), h.pool, true); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/system", http.StatusSeeOther)
}

// DisableAgentScorer turns AI scoring off. Always succeeds — the
// one-click off path has no precheck (the third safety gate).
func (h *SystemHandler) DisableAgentScorer(w http.ResponseWriter, r *http.Request) {
	if err := h.settingsRepo.SetAgentScorerEnabled(r.Context(), h.pool, false); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/system", http.StatusSeeOther)
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
