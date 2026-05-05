package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/risk"
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

// SaveBinance handles POST /settings/binance — updates Binance API credentials (requires restart).
func (h *SettingsHandler) SaveBinance(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	apiKey := r.FormValue("binance_api_key")
	apiSecret := r.FormValue("binance_api_secret")
	if err := h.repo.UpdateBinance(r.Context(), h.pool, apiKey, apiSecret); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// SaveIPWhitelist handles POST /settings/ip-whitelist — updates the IP whitelist (live effect).
func (h *SettingsHandler) SaveIPWhitelist(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	text := r.FormValue("entries")
	var entries []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			entries = append(entries, line)
		}
	}
	// Validate entries before storing
	if len(entries) > 0 {
		if _, err := risk.NewIPWhitelistRule(entries); err != nil {
			http.Error(w, "invalid whitelist entry: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := h.repo.UpdateIPWhitelist(r.Context(), h.pool, entries); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// SaveAdvanced handles POST /settings/advanced — updates webhook secret, reconciler interval,
// and Binance tuning parameters.
// Webhook secret takes live effect; reconciler interval and binance tuning require restart.
func (h *SettingsHandler) SaveAdvanced(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	// Webhook secret — only update if non-empty (empty means "keep existing")
	secret := r.FormValue("webhook_secret")
	if secret != "" {
		if err := h.repo.UpdateWebhookSecret(ctx, h.pool, secret); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Reconciler interval
	reconcilerStr := r.FormValue("reconciler_interval_seconds")
	if reconcilerStr != "" {
		reconcilerSecs, err := strconv.Atoi(reconcilerStr)
		if err != nil || reconcilerSecs < 5 || reconcilerSecs > 3600 {
			http.Error(w, "reconciler_interval_seconds must be between 5 and 3600", http.StatusBadRequest)
			return
		}
		if err := h.repo.UpdateReconciler(ctx, h.pool, reconcilerSecs); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Binance recv_window
	recvWindowStr := r.FormValue("binance_recv_window_ms")
	orderTimeoutStr := r.FormValue("binance_order_timeout_ms")
	if recvWindowStr != "" || orderTimeoutStr != "" {
		recvWindow, err := strconv.Atoi(recvWindowStr)
		if err != nil || recvWindow < 1000 || recvWindow > 60000 {
			http.Error(w, "binance_recv_window_ms must be between 1000 and 60000", http.StatusBadRequest)
			return
		}
		orderTimeout, err := strconv.Atoi(orderTimeoutStr)
		if err != nil || orderTimeout < 500 || orderTimeout > 30000 {
			http.Error(w, "binance_order_timeout_ms must be between 500 and 30000", http.StatusBadRequest)
			return
		}
		if err := h.repo.UpdateBinanceTuning(ctx, h.pool, recvWindow, orderTimeout); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}
