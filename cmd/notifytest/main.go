// notifytest is a one-shot tool that pushes one example of every Build*
// notification message to the configured Lark/Feishu webhook so a human can
// eyeball the rendering. It does NOT touch any trading state.
//
// Usage: DATABASE_URL=... go run ./cmd/notifytest
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/notify"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL not set")
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("connect db: %v", err)
	}
	defer pool.Close()

	var feishuURL string
	var feishuEnabled bool
	err = pool.QueryRow(ctx,
		`SELECT COALESCE(feishu_webhook_url,''), feishu_enabled FROM system_state WHERE id=1`).
		Scan(&feishuURL, &feishuEnabled)
	if err != nil {
		log.Fatalf("read settings: %v", err)
	}

	// Notifier targets: feishu (from DB) and/or telegram (from env vars).
	// If both are configured, every sample is pushed to both.
	var notifiers []notify.Notifier
	if feishuEnabled && feishuURL != "" && os.Getenv("SKIP_FEISHU") == "" {
		notifiers = append(notifiers, notify.NewFeishu(feishuURL))
		fmt.Println("target: feishu (from DB settings)")
	}
	if tok, chat := os.Getenv("TELEGRAM_BOT_TOKEN"), os.Getenv("TELEGRAM_CHAT_ID"); tok != "" && chat != "" {
		notifiers = append(notifiers, notify.NewTelegram("", tok, chat))
		fmt.Println("target: telegram (from env)")
	}
	if len(notifiers) == 0 {
		log.Fatal("no notifier configured: enable feishu in DB, or set TELEGRAM_BOT_TOKEN+TELEGRAM_CHAT_ID env")
	}

	dec := func(s string) decimal.Decimal {
		d, _ := decimal.NewFromString(s)
		return d
	}

	type sample struct {
		label string
		msg   notify.Message
	}
	samples := []sample{
		{"01 开仓成功(开多)", notify.BuildOpenMessage(
			"20260506", "ETHUSDT", 10, "long",
			dec("2350.00"), dec("2344.13"), dec("0.426"))},
		{"02 平仓 盈利(信号平仓)", notify.BuildCloseMessage(
			"20260506", "ETHUSDT", "long", notify.CloseReasonSignal,
			dec("2300"), dec("2400"), dec("1"), dec("100"))},
		{"03 平仓 亏损(信号平仓)", notify.BuildCloseMessage(
			"20260506", "ETHUSDT", "long", notify.CloseReasonSignal,
			dec("2400"), dec("2300"), dec("1"), dec("-100"))},
		{"04 止损触发", notify.BuildCloseMessage(
			"20260506", "ETHUSDT", "short", notify.CloseReasonStopLoss,
			dec("2300"), dec("2350"), dec("0.5"), dec("-25"))},
		{"05 止盈触发", notify.BuildCloseMessage(
			"20260506", "ETHUSDT", "long", notify.CloseReasonTakeProfit,
			dec("2300"), dec("2400"), dec("0.5"), dec("50"))},
		{"06 离线平仓 (恢复)", notify.BuildCloseMessage(
			"20260506", "ETHUSDT", "short", notify.CloseReasonRecoveryOffline,
			dec("2340.66"), dec("2382.10"), dec("0.426"), dec("-17.65"))},
		{"07 信号被风控拒绝", notify.BuildDeniedMessage(
			"20260506", "ETHUSDT", "long", "max_total_leverage",
			"leverage 3.50 > max_total_leverage 3.00")},
		{"08 开仓失败", notify.BuildOpenFailedMessage(
			"20260506", "ETHUSDT", "long",
			"place stop: binance: -4015 stop trigger price not valid")},
		{"09 平仓失败", notify.BuildCloseFailedMessage(
			"20260506", "ETHUSDT", "binance: -1021 timestamp out of recv window")},
		{"10 保护单被取消", notify.BuildProtectiveCanceledMessage(
			"20260506", "ETHUSDT", "stop", 39, "open")},
		{"11 启动恢复 持仓不一致", notify.BuildRecoveryMismatchMessage(
			"20260506", "ETHUSDT", "short", 39,
			dec("0.426"), dec("0.500"))},
		{"12 启动恢复 异常", notify.BuildRecoveryAnomalyMessage(
			39, "get position risk: binance: 5xx")},
		{"13 离线平仓 出场价未知", notify.BuildRecoveryAutoClosedNoExitPriceMessage(
			"20260506", "ETHUSDT", "short", 39,
			dec("2340.66"), dec("0.426"))},
	}

	fmt.Printf("Pushing %d sample messages to %d target(s)…\n", len(samples), len(notifiers))
	for i, s := range samples {
		fmt.Printf("[%d/%d] %s\n", i+1, len(samples), s.label)
		for _, n := range notifiers {
			label := fmt.Sprintf("%T", n)
			fmt.Printf("        → %s … ", label)
			if err := n.Send(ctx, s.msg); err != nil {
				fmt.Printf("FAIL: %v\n", err)
				continue
			}
			fmt.Println("ok")
		}
		// Both Lark and Telegram have ~5 msg/sec rate limits; pace at 1s per
		// sample (across all notifiers).
		time.Sleep(1100 * time.Millisecond)
	}
	fmt.Println("done")
}
