package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/application/ingest"
	"github.com/lizhaojie/tvbot/internal/application/reconcile"
	"github.com/lizhaojie/tvbot/internal/application/trade"
	"github.com/lizhaojie/tvbot/internal/config"
	"github.com/lizhaojie/tvbot/internal/idempotency"
	binanceinfra "github.com/lizhaojie/tvbot/internal/infrastructure/binance"
	ilog "github.com/lizhaojie/tvbot/internal/log"
	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/risk"
	"github.com/lizhaojie/tvbot/internal/store"
	tradepkg "github.com/lizhaojie/tvbot/internal/trade"
	"github.com/lizhaojie/tvbot/internal/web/admin"
	webmw "github.com/lizhaojie/tvbot/internal/web/middleware"
	"github.com/lizhaojie/tvbot/internal/web/webhook"

	web "github.com/lizhaojie/tvbot/internal/web"
)

func main() {
	// ── seed-user sub-command ────────────────────────────────────────────────
	if len(os.Args) > 1 && os.Args[1] == "seed-user" {
		cfg, err := config.Load("config/config.yaml")
		if err != nil {
			fmt.Fprintf(os.Stderr, "config: %v\n", err)
			os.Exit(1)
		}
		runSeedUser(cfg)
		return
	}

	// ── config ───────────────────────────────────────────────────────────────
	cfg, err := config.Load("config/config.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	// ── banner ───────────────────────────────────────────────────────────────
	fmt.Println("================================")
	fmt.Println("        tvbot starting          ")
	fmt.Printf("  mode: %s\n", cfg.BotMode)
	if cfg.BotMode == config.ModeLive {
		fmt.Println("  ⚠️  LIVE MODE — real money at stake")
	}
	fmt.Println("  armed: false (run /system/arm to enable)")
	fmt.Println("================================")

	// ── logger ───────────────────────────────────────────────────────────────
	logger := ilog.New(cfg.LogLevel)

	// ── DB pool ──────────────────────────────────────────────────────────────
	ctx := context.Background()
	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("db connect")
	}
	defer pool.Close()

	// ── repos ────────────────────────────────────────────────────────────────
	signalRepo := store.NewSignalRepo(pool)
	strategyRepo := store.NewStrategyRepo(pool)
	systemRepo := store.NewSystemStateRepo(pool)
	posRepo := store.NewVirtualPositionRepo(pool)
	historyRepo := store.NewPositionHistoryRepo(pool)
	orderRepo := store.NewOrderRepo(pool)
	userRepo := store.NewUserRepo(pool)
	_ = userRepo // used by AuthHandler; surfaced here for completeness

	// ── trader factory (mode-based) ──────────────────────────────────────────
	var trader tradepkg.Trader
	switch cfg.BotMode {
	case config.ModeTestnet, config.ModeLive:
		bt := binanceinfra.New(cfg.Binance, cfg.BinanceKey, cfg.BinanceSecret, cfg.BotMode, logger)
		trader = bt
		// Application service will pick up bt as StepSizer automatically via
		// the interface check in NewService; explicit set for clarity.
		_ = bt // assigned below after tradeSvc is created
	default: // dry_run
		trader = tradepkg.NewDryRunTrader()
	}

	// ── application services ─────────────────────────────────────────────────
	tradeSvc := trade.NewService(pool, orderRepo, posRepo, historyRepo, trader)

	// For testnet/live: inject BinanceTrader as StepSizer (already done by
	// NewService's interface check, but be explicit for clarity).
	if bt, ok := trader.(*binanceinfra.Trader); ok {
		tradeSvc.WithStepSizer(bt)
	}

	idem := idempotency.NewChecker(10000, signalRepo).WithPool(pool)

	// ── risk pipeline ────────────────────────────────────────────────────────
	riskPipe := risk.NewPipeline(
		risk.MaxPositionRule{},
		risk.TotalLeverageRule{MaxLeverage: decimal.NewFromFloat(cfg.Risk.MaxTotalLeverage)},
		risk.DailyLossBreakerRule{MaxDailyLossUSDC: decimal.NewFromFloat(cfg.Risk.MaxDailyLossUSDC)},
	)

	// IP whitelist rule — enforced as HTTP middleware on /webhook/tv.
	// Empty list means all IPs allowed (dev/no-whitelist mode).
	ipRule, err := risk.NewIPWhitelistRule(cfg.IPWhitelist)
	if err != nil {
		logger.Fatal().Err(err).Msg("ip_whitelist config")
	}

	// ── notifier ─────────────────────────────────────────────────────────────
	var notifiers []notify.Notifier
	if cfg.Notifier.Feishu.Enabled && cfg.Notifier.Feishu.WebhookURL != "" {
		notifiers = append(notifiers, notify.NewFeishu(cfg.Notifier.Feishu.WebhookURL))
	}
	if cfg.Notifier.Telegram.Enabled && cfg.Notifier.Telegram.BotToken != "" {
		notifiers = append(notifiers, notify.NewTelegram("", cfg.Notifier.Telegram.BotToken, cfg.Notifier.Telegram.ChatID))
	}
	var notifier notify.Notifier
	if len(notifiers) == 0 {
		notifier = notify.NoOp{}
	} else {
		notifier = notify.NewMulti(notifiers...)
	}

	// ── ingest service ───────────────────────────────────────────────────────
	ingestCfg := ingest.Config{
		WebhookSecret:         cfg.WebhookSecret,
		AccountEquityFallback: decimal.NewFromFloat(10000), // dry-run fallback
	}
	ingestSvc := ingest.NewService(ingestCfg, pool,
		signalRepo, strategyRepo, posRepo, systemRepo,
		idem, riskPipe, tradeSvc, notifier, logger)

	// ── sessions ─────────────────────────────────────────────────────────────
	sess := scs.New()
	sess.Lifetime = 12 * time.Hour
	sess.Cookie.HttpOnly = true
	sess.Cookie.Secure = false // dev; set true behind TLS in prod

	// ── template renderer ────────────────────────────────────────────────────
	renderer, err := admin.NewRenderer()
	if err != nil {
		logger.Fatal().Err(err).Msg("template parse")
	}

	// ── admin handlers ───────────────────────────────────────────────────────
	statusHandler := admin.NewStatusHandler(renderer, pool, systemRepo, strategyRepo, posRepo, cfg.BotMode)
	authHandler := admin.NewAuthHandler(renderer, sess, userRepo, pool)
	strategiesHandler := admin.NewStrategiesHandler(renderer, strategyRepo, pool, statusHandler)
	positionsHandler := admin.NewPositionsHandler(renderer, pool, posRepo, strategyRepo, historyRepo, statusHandler)
	signalsHandler := admin.NewSignalsHandler(renderer, pool, signalRepo, statusHandler)
	systemHandler := admin.NewSystemHandler(systemRepo, pool, sess)

	// ── webhook handler ──────────────────────────────────────────────────────
	webhookHandler := webhook.NewHandler(ingestSvc, logger)

	// ── HTTP server + routing ────────────────────────────────────────────────
	srv := web.New(cfg.HTTPListen, logger)
	r := srv.Router()

	// /webhook/tv — IP-whitelisted; HMAC checked inside ingest service
	r.Route("/webhook", func(r chi.Router) {
		r.Use(webmw.IPWhitelist(ipRule))
		r.Post("/tv", webhookHandler.Post)
	})

	// /static/* — vendored HTMX and other static assets
	r.Handle("/static/*", http.StripPrefix("/static/",
		http.FileServer(http.Dir("internal/web/admin/static"))))

	// admin routes — all wrapped in session middleware
	r.Group(func(r chi.Router) {
		r.Use(sess.LoadAndSave)

		// Auth (no RequireUser gate — login page must be reachable unauthenticated)
		r.Get("/login", authHandler.GetLogin)
		r.Post("/login", authHandler.PostLogin)
		r.Post("/logout", authHandler.PostLogout)

		// Protected admin area
		r.Group(func(r chi.Router) {
			r.Use(webmw.RequireUser(sess))

			// Root → redirect to /strategies
			r.Get("/", func(w http.ResponseWriter, req *http.Request) {
				http.Redirect(w, req, "/strategies", http.StatusSeeOther)
			})

			// Strategies
			r.Get("/strategies", strategiesHandler.Index)
			r.Get("/strategies/new", strategiesHandler.New)
			r.Post("/strategies", strategiesHandler.Save)
			r.Get("/strategies/{id}/edit", strategiesHandler.Edit)
			r.Post("/strategies/{id}", strategiesHandler.Save)
			r.Post("/strategies/{id}/toggle", strategiesHandler.Toggle)
			r.Post("/strategies/{id}/delete", strategiesHandler.Delete)

			// Positions
			r.Get("/positions", positionsHandler.Index)

			// Signals
			r.Get("/signals", signalsHandler.Index)

			// System controls
			r.Post("/system/arm", systemHandler.Arm)
			r.Post("/system/disarm", systemHandler.Disarm)
			r.Post("/system/breaker/reset", systemHandler.ResetBreaker)

			// Status partial (HTMX hx-get for status bar auto-refresh)
			r.Get("/_partials/status", statusHandler.Partial)
		})
	})

	// ── graceful shutdown ────────────────────────────────────────────────────
	shutCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// ── startup recovery (BEFORE HTTP server) ────────────────────────────────
	recovery := reconcile.NewRecovery(pool, posRepo, systemRepo, trader, notifier, logger)
	if err := recovery.Run(context.Background()); err != nil {
		logger.Error().Err(err).Msg("startup recovery failed")
		os.Exit(1)
	}

	// ── order reconciler (background goroutine) ──────────────────────────────
	reconciler := reconcile.New(pool, orderRepo, posRepo, trader, notifier, logger,
		time.Duration(cfg.Reconciler.IntervalSeconds)*time.Second)
	go func() {
		if err := reconciler.Run(shutCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("reconciler exited")
		}
	}()

	if err := srv.Start(shutCtx); err != nil {
		logger.Error().Err(err).Msg("server error")
		os.Exit(1)
	}
}
