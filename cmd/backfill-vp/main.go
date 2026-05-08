// backfill-vp queries Binance for the actual exit-fill of a closed VP whose
// position_history row was never written (e.g. closed by startup recovery
// before the recovery code learned to record history). It reports findings
// and, with -apply, inserts a position_history row.
//
// Usage:
//   DATABASE_URL=... go run ./cmd/backfill-vp -vp 39           # dry-run
//   DATABASE_URL=... go run ./cmd/backfill-vp -vp 39 -apply    # write history
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/config"
	binanceinfra "github.com/lizhaojie/tvbot/internal/infrastructure/binance"
	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/store"
)

func main() {
	vpID := flag.Int64("vp", 0, "VP id to backfill")
	apply := flag.Bool("apply", false, "actually insert into position_history (default: dry-run)")
	manualExitStr := flag.String("manual-exit-price", "", "if no protective order filled (e.g. manual close), use this exit price")
	manualFeesStr := flag.String("manual-fees", "0", "fees in USDC for manual close path")
	manualReason := flag.String("manual-reason", "manual", "close_reason for manual path (manual / recovery_offline)")
	flag.Parse()
	if *vpID == 0 {
		log.Fatal("-vp is required")
	}
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

	// Read API creds + bot mode + binance tuning from DB / yaml.
	cfg, err := config.Load("./config/config.yaml")
	if err != nil {
		log.Fatalf("load yaml: %v", err)
	}
	mode := config.BotMode(getEnv("BOT_MODE", string(cfg.BotMode)))
	if mode == "" {
		mode = config.ModeTestnet
	}

	var apiKey, apiSecret string
	var recvWindow, orderTimeout *int
	err = pool.QueryRow(ctx, `
SELECT COALESCE(binance_api_key,''), COALESCE(binance_api_secret,''),
       binance_recv_window_ms, binance_order_timeout_ms
  FROM system_state WHERE id=1`).Scan(&apiKey, &apiSecret, &recvWindow, &orderTimeout)
	if err != nil {
		log.Fatalf("read settings: %v", err)
	}
	if apiKey == "" || apiSecret == "" {
		log.Fatal("binance api key/secret not set in DB")
	}
	binCfg := cfg.Binance
	if recvWindow != nil && *recvWindow > 0 {
		binCfg.RecvWindowMs = *recvWindow
	}
	if orderTimeout != nil && *orderTimeout > 0 {
		binCfg.OrderTimeoutMs = *orderTimeout
	}

	logger := zerolog.New(os.Stderr).Level(zerolog.WarnLevel)
	trader := binanceinfra.New(binCfg, apiKey, apiSecret, mode, logger)

	// Load VP + linked orders.
	posRepo := store.NewVirtualPositionRepo(pool)
	orderRepo := store.NewOrderRepo(pool)

	vp, err := posRepo.GetByID(ctx, pool, *vpID)
	if err != nil {
		log.Fatalf("load VP %d: %v", *vpID, err)
	}
	fmt.Printf("VP %d: %s %s side=%s qty=%s entry_fill=%s opened=%s closed=%s status=%s\n",
		vp.ID, vp.StrategyID, vp.Symbol, vp.Side, vp.Qty.String(),
		vp.EntryFillPrice.StringFixed(2),
		vp.OpenedAt.Format(time.RFC3339), vp.ClosedAt.Format(time.RFC3339), vp.Status)

	// Iterate the protective orders, asking testnet for their current state.
	type fill struct {
		Purpose  string
		Price    decimal.Decimal
		Qty      decimal.Decimal
		Fees     decimal.Decimal
		ClosedAt time.Time // overrides vp.ClosedAt when known (e.g. from income API)
	}
	var found *fill
	for _, oid := range []int64{vp.StopOrderID, vp.BackupStopOrderID, vp.TakeProfitOrderID} {
		if oid == 0 {
			continue
		}
		clientID, err := orderRepo.GetClientOrderIDByID(ctx, pool, oid)
		if err != nil {
			fmt.Printf("  order_id=%d: get client_id failed: %v\n", oid, err)
			continue
		}
		res, err := trader.GetOrder(ctx, vp.Symbol, clientID)
		if err != nil {
			fmt.Printf("  %s: GetOrder failed: %v\n", clientID, err)
			continue
		}
		fmt.Printf("  %s: status=%s filled_qty=%s avg_price=%s fees=%s\n",
			clientID, res.Status, res.FilledQty.String(),
			res.AvgFillPrice.StringFixed(2), res.FeesUSDC.StringFixed(4))
		if string(res.Status) == "filled" && found == nil {
			purpose := ""
			switch oid {
			case vp.StopOrderID:
				purpose = "stop"
			case vp.BackupStopOrderID:
				purpose = "backup_stop"
			case vp.TakeProfitOrderID:
				purpose = "take_profit"
			}
			found = &fill{Purpose: purpose, Price: res.AvgFillPrice, Qty: res.FilledQty, Fees: res.FeesUSDC}
		}
	}

	if found == nil {
		// Fall back to Income API: query realized P&L + commission rows for
		// this symbol in the VP's lifetime window. This catches manual
		// closes (no protective order filled) by looking at what Binance
		// actually credited/debited the account.
		fmt.Println("\nNo protective order filled — trying Income API…")
		// Window: skip the entry instant (commission for entry posts at
		// opened_at) by starting 2s later; pad the end by 60s for safety.
		since := vp.OpenedAt.Add(2 * time.Second)
		until := vp.ClosedAt.Add(60 * time.Second)
		records, err := trader.Income(ctx, since, until)
		if err != nil {
			log.Fatalf("income api: %v", err)
		}
		var pnlSum, commSum decimal.Decimal
		var actualClose time.Time
		matched := 0
		for _, r := range records {
			if r.Symbol != vp.Symbol {
				continue
			}
			matched++
			fmt.Printf("  income: %s %s %s @ %s\n",
				r.Type, r.Income.StringFixed(4), r.Symbol, r.Time.Format(time.RFC3339))
			switch r.Type {
			case "REALIZED_PNL":
				pnlSum = pnlSum.Add(r.Income)
				if r.Time.After(actualClose) {
					actualClose = r.Time
				}
			case "COMMISSION":
				commSum = commSum.Add(r.Income.Abs())
				if r.Time.After(actualClose) {
					actualClose = r.Time
				}
			}
		}
		if matched == 0 || pnlSum.IsZero() {
			if *manualExitStr == "" {
				fmt.Println("\nIncome API returned nothing useful for this window.")
				fmt.Println("If you closed manually, supply the exit price by hand:")
				fmt.Println("  go run ./cmd/backfill-vp -vp X -manual-exit-price 2382.10 -manual-fees 0.5 -apply")
				os.Exit(2)
			}
			exitPrice, err := decimal.NewFromString(*manualExitStr)
			if err != nil {
				log.Fatalf("invalid -manual-exit-price: %v", err)
			}
			fees, err := decimal.NewFromString(*manualFeesStr)
			if err != nil {
				log.Fatalf("invalid -manual-fees: %v", err)
			}
			found = &fill{Purpose: "manual", Price: exitPrice, Qty: vp.Qty, Fees: fees}
			fmt.Printf("\nUsing manual exit price: %s (fees %s)\n", exitPrice.StringFixed(2), fees.StringFixed(4))
		} else {
			// Back-compute exit price from realized PnL:
			//   long:  pnl = (exit-entry)*qty → exit = entry + pnl/qty
			//   short: pnl = (entry-exit)*qty → exit = entry - pnl/qty
			var exitPrice decimal.Decimal
			if vp.Side == "long" {
				exitPrice = vp.EntryFillPrice.Add(pnlSum.Div(vp.Qty))
			} else {
				exitPrice = vp.EntryFillPrice.Sub(pnlSum.Div(vp.Qty))
			}
			fmt.Printf("\nDerived from Income API: pnl=%s, exit=%s, fees=%s, actual_close=%s\n",
				pnlSum.StringFixed(4), exitPrice.StringFixed(2), commSum.StringFixed(4),
				actualClose.Format(time.RFC3339))
			found = &fill{Purpose: "manual", Price: exitPrice, Qty: vp.Qty, Fees: commSum, ClosedAt: actualClose}
		}
	}

	pnl := notify.ComputePnL(vp.Side, vp.EntryFillPrice, found.Price, found.Qty)
	pnlPct := decimal.Zero
	if !vp.EntryFillPrice.IsZero() && !found.Qty.IsZero() {
		pnlPct = pnl.Div(vp.EntryFillPrice.Mul(found.Qty)).Mul(decimal.NewFromInt(100))
	}
	closedAt := vp.ClosedAt
	if !found.ClosedAt.IsZero() {
		closedAt = found.ClosedAt
	}
	duration := int(closedAt.Sub(vp.OpenedAt).Seconds())
	closeReason := mapPurposeToCloseReason(found.Purpose, *manualReason)

	fmt.Println("\nProposed position_history row:")
	fmt.Printf("  strategy=%s symbol=%s side=%s qty=%s\n", vp.StrategyID, vp.Symbol, vp.Side, found.Qty)
	fmt.Printf("  entry_signal=%s entry_fill=%s\n",
		vp.EntrySignalPrice.StringFixed(2), vp.EntryFillPrice.StringFixed(2))
	fmt.Printf("  exit_signal=%s exit_fill=%s (from %s)\n",
		found.Price.StringFixed(2), found.Price.StringFixed(2), found.Purpose)
	fmt.Printf("  pnl=%s USDC (%s%%)  fees=%s\n",
		pnl.StringFixed(4), pnlPct.StringFixed(4), found.Fees.StringFixed(4))
	fmt.Printf("  close_reason=%s  duration=%ds\n", closeReason, duration)
	fmt.Printf("  opened=%s closed=%s\n",
		vp.OpenedAt.Format(time.RFC3339), closedAt.Format(time.RFC3339))

	if !*apply {
		fmt.Println("\n(dry-run) re-run with -apply to insert.")
		return
	}

	row := store.PositionHistoryRow{
		StrategyID: vp.StrategyID, Symbol: vp.Symbol, Side: vp.Side, Qty: found.Qty,
		EntrySignalPrice: vp.EntrySignalPrice, EntryFillPrice: vp.EntryFillPrice,
		ExitSignalPrice:  found.Price, ExitFillPrice: found.Price,
		PnLUSDC:          pnl, PnLPct: pnlPct, FeesUSDC: found.Fees,
		CloseReason:      closeReason,
		DurationSeconds:  duration,
		OpenedAt:         vp.OpenedAt, ClosedAt: closedAt,
	}
	histRepo := store.NewPositionHistoryRepo(pool)
	if err := histRepo.Insert(ctx, pool, row); err != nil {
		log.Fatalf("insert history: %v", err)
	}
	fmt.Println("\ninserted position_history row OK")
}

func mapPurposeToCloseReason(p, manualReason string) string {
	switch p {
	case "stop", "backup_stop":
		return "stop_loss"
	case "take_profit":
		return "take_profit"
	case "manual":
		return manualReason
	}
	return "recovery_offline"
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
