package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type Settings struct {
	MaxTotalLeverage decimal.Decimal // zero => not set
	MaxDailyLossUSDC decimal.Decimal
	FeishuURL        string
	FeishuEnabled    bool
	TelegramBotToken string
	TelegramChatID   string
	TelegramEnabled  bool
}

type SettingsRepo struct {
	pool *pgxpool.Pool
}

func NewSettingsRepo(pool *pgxpool.Pool) *SettingsRepo { return &SettingsRepo{pool: pool} }

func (r *SettingsRepo) Get(ctx context.Context, q Querier) (*Settings, error) {
	var s Settings
	var maxLev, maxLoss *decimal.Decimal
	var feishuURL, tgToken, tgChat *string
	err := q.QueryRow(ctx, `
SELECT max_total_leverage, max_daily_loss_usdc,
       feishu_webhook_url, feishu_enabled,
       telegram_bot_token, telegram_chat_id, telegram_enabled
  FROM system_state WHERE id=1`,
	).Scan(&maxLev, &maxLoss, &feishuURL, &s.FeishuEnabled, &tgToken, &tgChat, &s.TelegramEnabled)
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

// Bootstrap copies values from cfg into the DB iff the settings have never
// been initialised (detected by max_total_leverage IS NULL). Idempotent —
// safe to call on every startup; once the numeric thresholds are set the
// entire block becomes a no-op, preserving any changes made via the admin UI.
func (r *SettingsRepo) Bootstrap(ctx context.Context, q Querier,
	maxLeverage, maxDailyLoss decimal.Decimal,
	feishuURL string, feishuEnabled bool,
	tgToken, tgChatID string, tgEnabled bool) error {
	_, err := q.Exec(ctx, `
UPDATE system_state SET
  max_total_leverage   = COALESCE(max_total_leverage,   $1),
  max_daily_loss_usdc  = COALESCE(max_daily_loss_usdc,  $2),
  feishu_webhook_url   = COALESCE(feishu_webhook_url,   NULLIF($3,'')),
  feishu_enabled       = CASE WHEN max_total_leverage IS NULL THEN $4 ELSE feishu_enabled END,
  telegram_bot_token   = COALESCE(telegram_bot_token,   NULLIF($5,'')),
  telegram_chat_id     = COALESCE(telegram_chat_id,     NULLIF($6,'')),
  telegram_enabled     = CASE WHEN max_total_leverage IS NULL THEN $7 ELSE telegram_enabled END
WHERE id=1`,
		maxLeverage, maxDailyLoss, feishuURL, feishuEnabled, tgToken, tgChatID, tgEnabled)
	return err
}
