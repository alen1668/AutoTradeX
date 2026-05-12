package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type Settings struct {
	// existing
	MaxTotalLeverage decimal.Decimal // zero => not set
	MaxDailyLossUSDC decimal.Decimal
	FeishuURL        string
	FeishuEnabled    bool
	TelegramBotToken string
	TelegramChatID   string
	TelegramEnabled  bool
	BinanceAPIKey    string
	BinanceAPISecret string
	// new
	WebhookSecret             string
	IPWhitelist               []string
	ReconcilerIntervalSeconds int
	BinanceRecvWindowMs       int
	BinanceOrderTimeoutMs     int
	// Agent scorer
	AgentScorerEnabled      bool
	AgentScorerModel        string
	AgentScorerThreshold    int
	AgentScorerTimeoutMs    int
	AgentScorerHistoryLimit int
	AgentScorerFailMode     string
	AgentScorerDryRun       bool
	LLMAPIProvider          string
	LLMAPIKey               string
	LLMAPIBaseURL           string
	// Macro context (regime / calendar / news workers)
	RegimeEnabled     bool
	RegimeIntervalMin int
	CalendarEnabled   bool
	NewsEnabled       bool
	NewsIntervalMin   int
	NewsAPIKey        string
	NewsLLMModel      string
	// WeCom (企业微信) group bot + news notify threshold.
	WecomEnabled        bool
	WecomWebhookURL     string
	NewsNotifyMinImpact string // none|low|medium|high
	// Perp metrics worker (binance funding / OI / top L/S ratio per symbol).
	PerpMetricsEnabled         bool
	PerpMetricsLookbackMinutes int
	// Outcome backfiller worker (eval/outcome package).
	OutcomeHorizonMin       int
	OutcomeWinThresholdPct  decimal.Decimal
	OutcomeLossThresholdPct decimal.Decimal
	OutcomeBatchSize        int
	OutcomeScanIntervalMin  int
	OutcomeStaleCutoffH     int
	// Critique self-reflection (agent/critique package).
	CritiqueEnabled    bool
	CritiqueModel      string
	CritiqueWindowDays int
	CritiqueMinSample  int
	CritiqueMaxPinned  int
	CritiqueCronUTC    string
}

type SettingsRepo struct {
	pool *pgxpool.Pool
}

func NewSettingsRepo(pool *pgxpool.Pool) *SettingsRepo { return &SettingsRepo{pool: pool} }

func (r *SettingsRepo) Get(ctx context.Context, q Querier) (*Settings, error) {
	var s Settings
	var maxLev, maxLoss *decimal.Decimal
	var feishuURL, tgToken, tgChat *string
	var bnKey, bnSecret *string
	var webhookSecret *string
	var reconcilerInterval, recvWindow, orderTimeout *int
	err := q.QueryRow(ctx, `
SELECT max_total_leverage, max_daily_loss_usdc,
       feishu_webhook_url, feishu_enabled,
       telegram_bot_token, telegram_chat_id, telegram_enabled,
       binance_api_key, binance_api_secret,
       webhook_secret,
       COALESCE(ip_whitelist, '{}'::TEXT[]),
       reconciler_interval_seconds,
       binance_recv_window_ms,
       binance_order_timeout_ms,
       agent_scorer_enabled, agent_scorer_model, agent_scorer_threshold,
       agent_scorer_timeout_ms, agent_scorer_history_limit,
       agent_scorer_fail_mode, agent_scorer_dry_run,
       llm_api_provider, llm_api_key, llm_api_base_url,
       regime_enabled, regime_interval_min,
       calendar_enabled,
       news_enabled, news_interval_min, news_api_key, news_llm_model,
       wecom_enabled, wecom_webhook_url, news_notify_min_impact,
       perp_metrics_enabled, perp_metrics_lookback_minutes,
       outcome_horizon_min, outcome_win_threshold_pct, outcome_loss_threshold_pct,
       outcome_batch_size, outcome_scan_interval_min, outcome_stale_cutoff_h,
       critique_enabled, critique_model, critique_window_days,
       critique_min_sample, critique_max_pinned, critique_cron_utc
  FROM system_state WHERE id=1`,
	).Scan(&maxLev, &maxLoss, &feishuURL, &s.FeishuEnabled, &tgToken, &tgChat, &s.TelegramEnabled,
		&bnKey, &bnSecret,
		&webhookSecret,
		&s.IPWhitelist,
		&reconcilerInterval,
		&recvWindow,
		&orderTimeout,
		&s.AgentScorerEnabled, &s.AgentScorerModel, &s.AgentScorerThreshold,
		&s.AgentScorerTimeoutMs, &s.AgentScorerHistoryLimit,
		&s.AgentScorerFailMode, &s.AgentScorerDryRun,
		&s.LLMAPIProvider, &s.LLMAPIKey, &s.LLMAPIBaseURL,
		&s.RegimeEnabled, &s.RegimeIntervalMin,
		&s.CalendarEnabled,
		&s.NewsEnabled, &s.NewsIntervalMin, &s.NewsAPIKey, &s.NewsLLMModel,
		&s.WecomEnabled, &s.WecomWebhookURL, &s.NewsNotifyMinImpact,
		&s.PerpMetricsEnabled, &s.PerpMetricsLookbackMinutes,
		&s.OutcomeHorizonMin, &s.OutcomeWinThresholdPct, &s.OutcomeLossThresholdPct,
		&s.OutcomeBatchSize, &s.OutcomeScanIntervalMin, &s.OutcomeStaleCutoffH,
		&s.CritiqueEnabled, &s.CritiqueModel, &s.CritiqueWindowDays,
		&s.CritiqueMinSample, &s.CritiqueMaxPinned, &s.CritiqueCronUTC,
	)
	if err != nil {
		return nil, err
	}
	if maxLev != nil {
		s.MaxTotalLeverage = *maxLev
	}
	if maxLoss != nil {
		s.MaxDailyLossUSDC = *maxLoss
	}
	if feishuURL != nil {
		s.FeishuURL = *feishuURL
	}
	if tgToken != nil {
		s.TelegramBotToken = *tgToken
	}
	if tgChat != nil {
		s.TelegramChatID = *tgChat
	}
	if bnKey != nil {
		s.BinanceAPIKey = *bnKey
	}
	if bnSecret != nil {
		s.BinanceAPISecret = *bnSecret
	}
	if webhookSecret != nil {
		s.WebhookSecret = *webhookSecret
	}
	if reconcilerInterval != nil {
		s.ReconcilerIntervalSeconds = *reconcilerInterval
	}
	if recvWindow != nil {
		s.BinanceRecvWindowMs = *recvWindow
	}
	if orderTimeout != nil {
		s.BinanceOrderTimeoutMs = *orderTimeout
	}
	return &s, nil
}

func (r *SettingsRepo) UpdateRisk(ctx context.Context, q Querier, maxLeverage, maxDailyLossUSDC decimal.Decimal) error {
	_, err := q.Exec(ctx, `
UPDATE system_state
   SET max_total_leverage=$1, max_daily_loss_usdc=$2, updated_at=now()
 WHERE id=1`, maxLeverage, maxDailyLossUSDC)
	return err
}

func (r *SettingsRepo) UpdateNotifier(ctx context.Context, q Querier,
	feishuURL string, feishuEnabled bool,
	tgToken, tgChatID string, tgEnabled bool) error {
	_, err := q.Exec(ctx, `
UPDATE system_state
   SET feishu_webhook_url=NULLIF($1,''),
       feishu_enabled=$2,
       telegram_bot_token=NULLIF($3,''),
       telegram_chat_id=NULLIF($4,''),
       telegram_enabled=$5,
       updated_at=now()
 WHERE id=1`, feishuURL, feishuEnabled, tgToken, tgChatID, tgEnabled)
	return err
}

// UpdateIPWhitelist stores the given IP/CIDR entries into the DB.
// An empty slice clears the whitelist (no restriction / dev mode).
func (r *SettingsRepo) UpdateIPWhitelist(ctx context.Context, q Querier, entries []string) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET ip_whitelist=$1, updated_at=now() WHERE id=1`,
		entries)
	return err
}

// UpdateWebhookSecret stores the webhook secret. Empty string is stored as NULL.
func (r *SettingsRepo) UpdateWebhookSecret(ctx context.Context, q Querier, secret string) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET webhook_secret=NULLIF($1,''), updated_at=now() WHERE id=1`,
		secret)
	return err
}

// UpdateReconciler stores the reconciler interval in seconds (requires restart).
func (r *SettingsRepo) UpdateReconciler(ctx context.Context, q Querier, intervalSeconds int) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET reconciler_interval_seconds=$1, updated_at=now() WHERE id=1`,
		intervalSeconds)
	return err
}

// UpdateBinanceTuning stores recv_window and order_timeout (requires restart).
func (r *SettingsRepo) UpdateBinanceTuning(ctx context.Context, q Querier, recvWindowMs, orderTimeoutMs int) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET binance_recv_window_ms=$1, binance_order_timeout_ms=$2, updated_at=now() WHERE id=1`,
		recvWindowMs, orderTimeoutMs)
	return err
}

// Bootstrap copies values from cfg into the DB iff the settings have never
// been initialised (detected by max_total_leverage IS NULL). Idempotent —
// safe to call on every startup; once the numeric thresholds are set the
// entire block becomes a no-op, preserving any changes made via the admin UI.
func (r *SettingsRepo) Bootstrap(ctx context.Context, q Querier,
	maxLeverage, maxDailyLoss decimal.Decimal,
	feishuURL string, feishuEnabled bool,
	tgToken, tgChatID string, tgEnabled bool,
	bnKey, bnSecret string,
	webhookSecret string, ipWhitelist []string,
	reconcilerInterval, recvWindowMs, orderTimeoutMs int,
) error {
	_, err := q.Exec(ctx, `
UPDATE system_state SET
  max_total_leverage          = COALESCE(max_total_leverage,          $1),
  max_daily_loss_usdc         = COALESCE(max_daily_loss_usdc,         $2),
  feishu_webhook_url          = COALESCE(feishu_webhook_url,          NULLIF($3,'')),
  feishu_enabled              = CASE WHEN max_total_leverage IS NULL THEN $4 ELSE feishu_enabled END,
  telegram_bot_token          = COALESCE(telegram_bot_token,          NULLIF($5,'')),
  telegram_chat_id            = COALESCE(telegram_chat_id,            NULLIF($6,'')),
  telegram_enabled            = CASE WHEN max_total_leverage IS NULL THEN $7 ELSE telegram_enabled END,
  binance_api_key             = COALESCE(binance_api_key,             NULLIF($8,'')),
  binance_api_secret          = COALESCE(binance_api_secret,          NULLIF($9,'')),
  webhook_secret              = COALESCE(webhook_secret,              NULLIF($10,'')),
  ip_whitelist                = COALESCE(ip_whitelist,                $11),
  reconciler_interval_seconds = COALESCE(reconciler_interval_seconds, NULLIF($12,0)),
  binance_recv_window_ms      = COALESCE(binance_recv_window_ms,      NULLIF($13,0)),
  binance_order_timeout_ms    = COALESCE(binance_order_timeout_ms,    NULLIF($14,0))
WHERE id=1`,
		maxLeverage, maxDailyLoss,
		feishuURL, feishuEnabled,
		tgToken, tgChatID, tgEnabled,
		bnKey, bnSecret,
		webhookSecret, ipWhitelist,
		reconcilerInterval, recvWindowMs, orderTimeoutMs)
	return err
}

// UpdateBinance updates the Binance API key and secret in the DB.
// Empty strings are stored as NULL.
func (r *SettingsRepo) UpdateBinance(ctx context.Context, q Querier, apiKey, apiSecret string) error {
	_, err := q.Exec(ctx, `
UPDATE system_state
   SET binance_api_key=NULLIF($1,''),
       binance_api_secret=NULLIF($2,''),
       updated_at=now()
 WHERE id=1`, apiKey, apiSecret)
	return err
}

// UpdateAgentScorer updates the full agent scorer parameter block. The
// `enabled` flag is the *current* desired state; callers that just want to
// toggle on/off should use SetAgentScorerEnabled instead so the /system
// page's empty-key precheck stays the only path that flips it on.
func (r *SettingsRepo) UpdateAgentScorer(ctx context.Context, q Querier,
	enabled bool, model string, threshold, timeoutMs, historyLimit int,
	failMode string, dryRun bool,
) error {
	_, err := q.Exec(ctx, `
UPDATE system_state SET
    agent_scorer_enabled       = $1,
    agent_scorer_model         = $2,
    agent_scorer_threshold     = $3,
    agent_scorer_timeout_ms    = $4,
    agent_scorer_history_limit = $5,
    agent_scorer_fail_mode     = $6,
    agent_scorer_dry_run       = $7,
    updated_at=now()
 WHERE id=1`,
		enabled, model, threshold, timeoutMs, historyLimit, failMode, dryRun,
	)
	return err
}

// SetAgentScorerEnabled flips just the enabled flag. The /system page calls
// this after the empty-key precheck.
func (r *SettingsRepo) SetAgentScorerEnabled(ctx context.Context, q Querier, enabled bool) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET agent_scorer_enabled=$1, updated_at=now() WHERE id=1`, enabled)
	return err
}

// UpdateLLMAPI updates the LLM provider, API key, and base URL. Empty
// api_key preserves the existing value (matches Binance API key UI behavior:
// the form's password field can be left blank to keep the previous secret).
func (r *SettingsRepo) UpdateLLMAPI(ctx context.Context, q Querier, provider, apiKey, baseURL string) error {
	_, err := q.Exec(ctx, `
UPDATE system_state SET
    llm_api_provider = $1,
    llm_api_key      = COALESCE(NULLIF($2,''), llm_api_key),
    llm_api_base_url = $3,
    updated_at=now()
 WHERE id=1`, provider, apiKey, baseURL)
	return err
}

// UpdateMacro updates the regime/calendar/news worker settings in one call.
func (r *SettingsRepo) UpdateMacro(ctx context.Context, q Querier,
	regimeEnabled bool, regimeIntervalMin int,
	calendarEnabled bool,
	newsEnabled bool, newsIntervalMin int, newsAPIKey, newsLLMModel string,
) error {
	_, err := q.Exec(ctx, `
UPDATE system_state SET
    regime_enabled      = $1,
    regime_interval_min = $2,
    calendar_enabled    = $3,
    news_enabled        = $4,
    news_interval_min   = $5,
    news_api_key        = $6,
    news_llm_model      = $7,
    updated_at=now()
 WHERE id=1`,
		regimeEnabled, regimeIntervalMin,
		calendarEnabled,
		newsEnabled, newsIntervalMin, newsAPIKey, newsLLMModel,
	)
	return err
}

// SetRegimeEnabled flips just the regime flag.
func (r *SettingsRepo) SetRegimeEnabled(ctx context.Context, q Querier, enabled bool) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET regime_enabled=$1, updated_at=now() WHERE id=1`, enabled)
	return err
}

// SetCalendarEnabled flips just the calendar flag.
func (r *SettingsRepo) SetCalendarEnabled(ctx context.Context, q Querier, enabled bool) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET calendar_enabled=$1, updated_at=now() WHERE id=1`, enabled)
	return err
}

// SetNewsEnabled flips just the news flag.
func (r *SettingsRepo) SetNewsEnabled(ctx context.Context, q Querier, enabled bool) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET news_enabled=$1, updated_at=now() WHERE id=1`, enabled)
	return err
}

// UpdateWecom stores the WeCom group bot webhook + enabled flag + news
// notify threshold ('none'|'low'|'medium'|'high').
func (r *SettingsRepo) UpdateWecom(ctx context.Context, q Querier,
	enabled bool, webhookURL, newsMinImpact string,
) error {
	_, err := q.Exec(ctx, `
UPDATE system_state SET
    wecom_enabled          = $1,
    wecom_webhook_url      = $2,
    news_notify_min_impact = $3,
    updated_at=now()
 WHERE id=1`, enabled, webhookURL, newsMinImpact)
	return err
}

// UpdatePerpMetrics updates the perp-metrics worker flags.
func (r *SettingsRepo) UpdatePerpMetrics(ctx context.Context, q Querier,
	enabled bool, lookbackMinutes int,
) error {
	_, err := q.Exec(ctx, `
UPDATE system_state SET
    perp_metrics_enabled          = $1,
    perp_metrics_lookback_minutes = $2,
    updated_at=now()
 WHERE id=1`, enabled, lookbackMinutes)
	return err
}

// SetPerpMetricsEnabled flips just the perp metrics flag.
func (r *SettingsRepo) SetPerpMetricsEnabled(ctx context.Context, q Querier, enabled bool) error {
	_, err := q.Exec(ctx,
		`UPDATE system_state SET perp_metrics_enabled=$1, updated_at=now() WHERE id=1`, enabled)
	return err
}

// UpdateOutcome stores the 6 outcome-backfiller knobs in one shot.
// All fields validated by the caller (handler).
func (r *SettingsRepo) UpdateOutcome(ctx context.Context, q Querier,
	horizonMin int, winThresh, lossThresh decimal.Decimal,
	batchSize, scanIntervalMin, staleCutoffH int,
) error {
	_, err := q.Exec(ctx, `
UPDATE system_state
SET outcome_horizon_min        = $1,
    outcome_win_threshold_pct  = $2,
    outcome_loss_threshold_pct = $3,
    outcome_batch_size         = $4,
    outcome_scan_interval_min  = $5,
    outcome_stale_cutoff_h     = $6,
    updated_at                 = now()
WHERE id=1`, horizonMin, winThresh, lossThresh, batchSize, scanIntervalMin, staleCutoffH)
	return err
}

// UpdateCritique stores the 6 critique-agent knobs in one shot.
func (r *SettingsRepo) UpdateCritique(ctx context.Context, q Querier,
	enabled bool, model string,
	windowDays, minSample, maxPinned int,
	cronUTC string,
) error {
	_, err := q.Exec(ctx, `
UPDATE system_state
SET critique_enabled     = $1,
    critique_model       = $2,
    critique_window_days = $3,
    critique_min_sample  = $4,
    critique_max_pinned  = $5,
    critique_cron_utc    = $6,
    updated_at           = now()
WHERE id=1`, enabled, model, windowDays, minSample, maxPinned, cronUTC)
	return err
}
