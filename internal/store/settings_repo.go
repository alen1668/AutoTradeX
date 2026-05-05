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
       binance_order_timeout_ms
  FROM system_state WHERE id=1`,
	).Scan(&maxLev, &maxLoss, &feishuURL, &s.FeishuEnabled, &tgToken, &tgChat, &s.TelegramEnabled,
		&bnKey, &bnSecret,
		&webhookSecret,
		&s.IPWhitelist,
		&reconcilerInterval,
		&recvWindow,
		&orderTimeout)
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
