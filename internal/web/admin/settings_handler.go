package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
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
		"Models":   scorer.SupportedModels,
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

// SaveAgentScorer handles POST /settings/agent-scorer — updates agent scorer
// parameters (model/threshold/timeout/history_limit/fail_mode/dry_run).
// Does NOT touch the enabled flag — that path is /system/agent-{en,dis}able
// so the empty-key precheck stays the only way to flip the flag on.
func (h *SettingsHandler) SaveAgentScorer(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	threshold, err := strconv.Atoi(r.FormValue("threshold"))
	if err != nil || threshold < 0 || threshold > 100 {
		http.Error(w, "threshold must be 0..100", http.StatusBadRequest)
		return
	}
	timeoutMs, err := strconv.Atoi(r.FormValue("timeout_ms"))
	if err != nil || timeoutMs < 500 || timeoutMs > 60000 {
		http.Error(w, "timeout_ms must be 500..60000", http.StatusBadRequest)
		return
	}
	historyLimit, err := strconv.Atoi(r.FormValue("history_limit"))
	if err != nil || historyLimit < 1 || historyLimit > 100 {
		http.Error(w, "history_limit must be 1..100", http.StatusBadRequest)
		return
	}
	failMode := r.FormValue("fail_mode")
	if failMode != "open" && failMode != "closed" {
		http.Error(w, "fail_mode must be open|closed", http.StatusBadRequest)
		return
	}
	dryRun := r.FormValue("dry_run") == "on"
	model := strings.TrimSpace(r.FormValue("model"))
	if model == "" {
		http.Error(w, "model required", http.StatusBadRequest)
		return
	}
	if !scorer.IsSupportedModel(model) {
		http.Error(w, "unsupported model", http.StatusBadRequest)
		return
	}

	cur, err := h.repo.Get(r.Context(), h.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.repo.UpdateAgentScorer(r.Context(), h.pool,
		cur.AgentScorerEnabled, // preserve — the enable toggle lives on /system
		model, threshold, timeoutMs, historyLimit, failMode, dryRun,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// SaveLLMAPI handles POST /settings/llm-api — updates LLM provider, base URL,
// and api_key. An empty api_key preserves the existing value (so the form
// password field can be left blank to keep the previous secret).
func (h *SettingsHandler) SaveLLMAPI(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	provider := r.FormValue("provider")
	if provider != "anthropic" {
		http.Error(w, "unsupported provider (only anthropic in v1)", http.StatusBadRequest)
		return
	}
	apiKey := r.FormValue("api_key")
	baseURL := strings.TrimSpace(r.FormValue("base_url"))
	if err := h.repo.UpdateLLMAPI(r.Context(), h.pool, provider, apiKey, baseURL); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// SaveWecom handles POST /settings/wecom — updates WeCom group bot webhook
// + the news notify min-impact threshold.
func (h *SettingsHandler) SaveWecom(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	enabled := r.FormValue("wecom_enabled") == "on"
	webhookURL := strings.TrimSpace(r.FormValue("wecom_webhook_url"))
	minImpact := strings.TrimSpace(r.FormValue("news_notify_min_impact"))

	if enabled && webhookURL == "" {
		http.Error(w, "启用企业微信需要先填 webhook URL", http.StatusBadRequest)
		return
	}
	if enabled && !strings.HasPrefix(webhookURL, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send") {
		http.Error(w, "webhook URL 必须是 https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=... 格式", http.StatusBadRequest)
		return
	}
	switch minImpact {
	case "", "none", "low", "medium", "high":
		// ok
	default:
		http.Error(w, "news_notify_min_impact 必须是 none|low|medium|high", http.StatusBadRequest)
		return
	}
	if minImpact == "" {
		minImpact = "medium"
	}
	if err := h.repo.UpdateWecom(r.Context(), h.pool, enabled, webhookURL, minImpact); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// SaveMacro handles POST /settings/macro — updates regime/calendar/news
// worker flags + intervals + news API key/model. Enabling News without an
// LLM API key is rejected (precheck) so we never get into a state where
// the worker would spam fail-mode rows.
func (h *SettingsHandler) SaveMacro(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	regimeEnabled := r.FormValue("regime_enabled") == "on"
	calendarEnabled := r.FormValue("calendar_enabled") == "on"
	newsEnabled := r.FormValue("news_enabled") == "on"
	regimeInterval, err := strconv.Atoi(strings.TrimSpace(r.FormValue("regime_interval_min")))
	if err != nil || regimeInterval < 1 || regimeInterval > 240 {
		http.Error(w, "regime_interval_min must be 1..240", http.StatusBadRequest)
		return
	}
	newsInterval, err := strconv.Atoi(strings.TrimSpace(r.FormValue("news_interval_min")))
	if err != nil || newsInterval < 1 || newsInterval > 60 {
		http.Error(w, "news_interval_min must be 1..60", http.StatusBadRequest)
		return
	}
	newsAPIKey := strings.TrimSpace(r.FormValue("news_api_key"))
	newsModel := strings.TrimSpace(r.FormValue("news_llm_model"))
	if newsModel == "" {
		http.Error(w, "news_llm_model required", http.StatusBadRequest)
		return
	}
	if newsEnabled {
		cur, err := h.repo.Get(r.Context(), h.pool)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if cur.LLMAPIKey == "" {
			http.Error(w, "启用 News 需要先在 /settings 配置 LLM API Key", http.StatusBadRequest)
			return
		}
		// CryptoPanic API key is no longer required (default source is CoinDesk
		// RSS which is free + keyless). The field is kept in settings so users
		// who switch to a paid CryptoPanic plan in the future can still store it.
	}
	if err := h.repo.UpdateMacro(r.Context(), h.pool,
		regimeEnabled, regimeInterval,
		calendarEnabled,
		newsEnabled, newsInterval, newsAPIKey, newsModel,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Perp metrics worker (separate UPDATE so older deployments without the
	// 0012 migration can still post the macro form successfully — but with
	// 0012 applied via the test fixture migrations list, this UPDATE always
	// runs and validates).
	perpEnabled := r.FormValue("perp_metrics_enabled") == "on"
	perpLookback, err := strconv.Atoi(strings.TrimSpace(r.FormValue("perp_metrics_lookback_minutes")))
	if err != nil || perpLookback < 5 || perpLookback > 120 {
		http.Error(w, "perp_metrics_lookback_minutes must be 5..120", http.StatusBadRequest)
		return
	}
	if err := h.repo.UpdatePerpMetrics(r.Context(), h.pool, perpEnabled, perpLookback); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}
