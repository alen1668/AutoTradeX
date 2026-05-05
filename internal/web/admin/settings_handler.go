package admin

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/store"
)

// SettingsHandler renders the /settings page and handles form submissions.
type SettingsHandler struct {
	render  *Renderer
	pool    *pgxpool.Pool
	repo    *store.SettingsRepo
	statusH *StatusHandler
}

// NewSettingsHandler constructs a SettingsHandler.
func NewSettingsHandler(r *Renderer, pool *pgxpool.Pool, repo *store.SettingsRepo, statusH *StatusHandler) *SettingsHandler {
	return &SettingsHandler{render: r, pool: pool, repo: repo, statusH: statusH}
}

// Index handles GET /settings.
func (h *SettingsHandler) Index(w http.ResponseWriter, r *http.Request) {
	s, err := h.repo.Get(r.Context(), h.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := h.statusH.WithStatus(r, map[string]any{
		"Settings": s,
		"Saved":    r.URL.Query().Get("saved") == "1",
	})
	h.render.Render(w, http.StatusOK, "settings/index", data)
}

// SaveRisk handles POST /settings/risk — updates risk thresholds (live effect).
func (h *SettingsHandler) SaveRisk(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	leverage, err := decimal.NewFromString(r.FormValue("max_total_leverage"))
	if err != nil {
		http.Error(w, "invalid max_total_leverage: "+err.Error(), http.StatusBadRequest)
		return
	}
	loss, err := decimal.NewFromString(r.FormValue("max_daily_loss_usdc"))
	if err != nil {
		http.Error(w, "invalid max_daily_loss_usdc: "+err.Error(), http.StatusBadRequest)
		return
	}
	if !leverage.IsPositive() || !loss.IsPositive() {
		http.Error(w, "values must be positive", http.StatusBadRequest)
		return
	}
	if err := h.repo.UpdateRisk(r.Context(), h.pool, leverage, loss); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// SaveNotifier handles POST /settings/notifier — updates notifier config (requires restart).
func (h *SettingsHandler) SaveNotifier(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	feishuURL := r.FormValue("feishu_webhook_url")
	feishuEnabled := r.FormValue("feishu_enabled") == "on"
	tgToken := r.FormValue("telegram_bot_token")
	tgChat := r.FormValue("telegram_chat_id")
	tgEnabled := r.FormValue("telegram_enabled") == "on"
	if err := h.repo.UpdateNotifier(r.Context(), h.pool, feishuURL, feishuEnabled, tgToken, tgChat, tgEnabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}
