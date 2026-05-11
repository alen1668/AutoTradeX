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

	"github.com/lizhaojie/tvbot/internal/agent/history"
	"github.com/lizhaojie/tvbot/internal/agent/market"
	"github.com/lizhaojie/tvbot/internal/agent/portfolio"
	"github.com/lizhaojie/tvbot/internal/agent/scorer"
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

// portfolioRepo adapts the two store repos that portfolio.Provider needs
// (active virtual positions + daily realized PnL) into the single
// portfolio.Repo interface. Lives in main.go so the agent/portfolio
// package itself never imports two store types just to compose them.
type portfolioRepo struct {
	vp *store.VirtualPositionRepo
	ph *store.PositionHistoryRepo
}

func (r portfolioRepo) ListActive(ctx context.Context, q store.Querier) ([]*store.VirtualPositionRow, error) {
	return r.vp.ListActive(ctx, q)
}
func (r portfolioRepo) DailyRealizedPnL(ctx context.Context, q store.Querier, day time.Time) (decimal.Decimal, error) {
	return r.ph.DailyRealizedPnL(ctx, q, day)
}

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
	settingsRepo := store.NewSettingsRepo(pool)
	_ = userRepo // used by AuthHandler; surfaced here for completeness

	idem := idempotency.NewChecker(10000, signalRepo).WithPool(pool)

	// ── settings bootstrap (yaml → DB on first run only) ─────────────────────
	// On subsequent startups COALESCE ensures existing DB values are preserved,
	// so changes made via the admin UI survive restarts.
	if err := settingsRepo.Bootstrap(ctx, pool,
		decimal.NewFromFloat(cfg.Risk.MaxTotalLeverage),
		decimal.NewFromFloat(cfg.Risk.MaxDailyLossUSDC),
		cfg.Notifier.Feishu.WebhookURL, cfg.Notifier.Feishu.Enabled,
		cfg.Notifier.Telegram.BotToken, cfg.Notifier.Telegram.ChatID, cfg.Notifier.Telegram.Enabled,
		cfg.BinanceKey, cfg.BinanceSecret,
		cfg.WebhookSecret, cfg.IPWhitelist,
		cfg.Reconciler.IntervalSeconds, cfg.Binance.RecvWindowMs, cfg.Binance.OrderTimeoutMs,
	); err != nil {
		logger.Fatal().Err(err).Msg("settings bootstrap")
	}

	// ── risk pipeline ────────────────────────────────────────────────────────
	settingsProvider := risk.NewDBSettingsProvider(pool, settingsRepo)
	riskPipe := risk.NewPipeline(
		risk.MaxPositionRule{},
		risk.TotalLeverageRule{Settings: settingsProvider},
		risk.DailyLossBreakerRule{Settings: settingsProvider},
	)

	// ── notifier ─────────────────────────────────────────────────────────────
	// Read settings from DB (bootstrapped from yaml above on first run).
	// NOTE: notifier endpoints and Binance creds are resolved at startup.
	// To apply changes made via the admin UI, restart the bot.
	// Reconciler interval and Binance tuning are also read here (restart-required).
	dbSettings, err := settingsRepo.Get(ctx, pool)
	if err != nil {
		logger.Fatal().Err(err).Msg("settings get")
	}
	var notifiers []notify.Notifier
	if dbSettings.FeishuEnabled && dbSettings.FeishuURL != "" {
		notifiers = append(notifiers, notify.NewFeishu(dbSettings.FeishuURL))
	}
	if dbSettings.TelegramEnabled && dbSettings.TelegramBotToken != "" && dbSettings.TelegramChatID != "" {
		notifiers = append(notifiers, notify.NewTelegram("", dbSettings.TelegramBotToken, dbSettings.TelegramChatID))
	}
	var notifier notify.Notifier
	if len(notifiers) == 0 {
		notifier = notify.NoOp{}
	} else if len(notifiers) == 1 {
		notifier = notifiers[0]
	} else {
		notifier = notify.NewMulti(notifiers...)
	}

	// ── trader factory ────────────────────────────────────────────────────────
	// BOT_MODE is testnet|live; both routes go through Binance (testnet uses
	// the testnet endpoint with paper-trading USDT). API key + secret are
	// mandatory — there is no built-in offline mode.
	if dbSettings.BinanceAPIKey == "" || dbSettings.BinanceAPISecret == "" {
		logger.Fatal().Msgf("BOT_MODE=%s but Binance API key/secret not set; configure via /settings or env", cfg.BotMode)
	}
	binCfg := cfg.Binance
	if dbSettings.BinanceRecvWindowMs > 0 {
		binCfg.RecvWindowMs = dbSettings.BinanceRecvWindowMs
	}
	if dbSettings.BinanceOrderTimeoutMs > 0 {
		binCfg.OrderTimeoutMs = dbSettings.BinanceOrderTimeoutMs
	}
	var trader tradepkg.Trader = binanceinfra.New(binCfg, dbSettings.BinanceAPIKey, dbSettings.BinanceAPISecret, cfg.BotMode, logger)

	// ── application services ─────────────────────────────────────────────────
	tradeSvc := trade.NewService(pool, orderRepo, posRepo, historyRepo, trader).WithSystemRepo(systemRepo)

	// For testnet/live: inject BinanceTrader as StepSizer.
	if bt, ok := trader.(*binanceinfra.Trader); ok {
		tradeSvc.WithStepSizer(bt)
	}

	// ── ingest service ───────────────────────────────────────────────────────
	ingestCfg := ingest.Config{
		AccountEquityFallback: decimal.NewFromFloat(10000), // dry-run fallback
		SecretLoader: func(ctx context.Context) (string, error) {
			s, err := settingsRepo.Get(ctx, pool)
			if err != nil {
				return "", err
			}
			return s.WebhookSecret, nil
		},
	}
	// ── agent scoring layer ────────────────────────────────────────────
	// Constructed unconditionally; settings.agent_scorer_enabled is the
	// runtime gate. Empty LLMAPIKey here means scorer.Score will hit a 401
	// and fall through fail_mode — that's by design (the /system enable
	// path enforces the empty-key precheck so this only happens if
	// someone toggled the flag in DB by hand).
	agentEvalRepo := store.NewAgentEvalRepo(pool)
	var llmClient scorer.LLMClient
	switch dbSettings.LLMAPIProvider {
	case "anthropic", "":
		llmClient = scorer.NewAnthropicClient(dbSettings.LLMAPIKey, dbSettings.LLMAPIBaseURL)
	default:
		logger.Warn().Str("provider", dbSettings.LLMAPIProvider).
			Msg("unknown LLM provider; agent scorer will fail-mode")
		llmClient = scorer.NewAnthropicClient("", "")
	}
	scorerFactory := scorer.NewFactory(llmClient, agentEvalRepo, pool,
		logger.With().Str("c", "agent_scorer").Logger())
	historyProv := history.New(historyRepo, pool).WithLogger(
		logger.With().Str("c", "agent_history").Logger())
	portfolioProv := portfolio.New(portfolioRepo{vp: posRepo, ph: historyRepo}, pool).WithLogger(
		logger.With().Str("c", "agent_portfolio").Logger())
	var marketProv *market.Provider
	if bt, ok := trader.(*binanceinfra.Trader); ok {
		klineClient := market.NewBinanceKlineClient(bt.FuturesClient())
		marketProv = market.NewProvider(klineClient, 30*time.Second).WithLogger(
			logger.With().Str("c", "agent_market").Logger())
	}
	agentHook := ingest.NewAgentHook(scorerFactory, historyProv, portfolioProv, marketProv)

	ingestSvc := ingest.NewService(ingestCfg, pool,
		signalRepo, settingsRepo, strategyRepo, posRepo, systemRepo,
		idem, riskPipe, tradeSvc, notifier, agentHook, logger)

	// ── async dispatcher: per-strategy worker pool ─────────────────────────
	// TradingView times out webhooks at ~3s. The dispatcher lets the HTTP
	// handler return 200 in <100ms while trade execution runs on a worker
	// goroutine per strategy_id (FIFO within a strategy, parallel across).
	dispatcher := ingest.NewDispatcher(ingestSvc, notifier, logger)

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
	statusHandler := admin.NewStatusHandler(renderer, pool, systemRepo, settingsRepo, strategyRepo, posRepo, cfg.BotMode)
	authHandler := admin.NewAuthHandler(renderer, sess, userRepo, pool)
	strategiesHandler := admin.NewStrategiesHandler(renderer, strategyRepo, pool, statusHandler)
	positionsHandler := admin.NewPositionsHandler(renderer, pool, posRepo, strategyRepo, historyRepo, statusHandler)
	signalsHandler := admin.NewSignalsHandler(renderer, pool, signalRepo, agentEvalRepo, statusHandler)
	systemHandler := admin.NewSystemHandler(systemRepo, settingsRepo, pool, sess, renderer, statusHandler, cfg.BotMode)
	// IncomeFetcher comes from the Binance trader when present (testnet
	// and live both go through Binance, so the type-assert always succeeds
	// — kept here for safety in case a future test wires a fake trader).
	var incomeFetcher admin.IncomeFetcher
	if bt, ok := trader.(*binanceinfra.Trader); ok {
		incomeFetcher = bt
	}
	statsHandler := admin.NewStatsHandler(renderer, pool, statusHandler, incomeFetcher)
	evalHandler := admin.NewEvalHandler(renderer, pool).WithStatus(statusHandler)
	settingsHandler := admin.NewSettingsHandler(renderer, pool, settingsRepo, statusHandler)

	// ── webhook handler ──────────────────────────────────────────────────────
	webhookHandler := webhook.NewHandler(ingestSvc, dispatcher, logger)

	// ── HTTP server + routing ────────────────────────────────────────────────
	srv := web.New(cfg.HTTPListen, logger)
	r := srv.Router()

	// /webhook/tv — IP-whitelisted (DB-backed, live-effect); secret checked inside ingest service
	r.Route("/webhook", func(r chi.Router) {
		r.Use(webmw.IPWhitelist(func(ctx context.Context) ([]string, error) {
			s, err := settingsRepo.Get(ctx, pool)
			if err != nil {
				return nil, err
			}
			return s.IPWhitelist, nil
		}))
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
			r.Post("/strategies/{id}/archive", strategiesHandler.Archive)
			r.Post("/strategies/{id}/unarchive", strategiesHandler.Unarchive)
			r.Post("/strategies/{id}/delete", strategiesHandler.Delete)

			// Positions
			r.Get("/positions", positionsHandler.Index)

			// Signals
			r.Get("/signals", signalsHandler.Index)
			r.Get("/signals/{id}", signalsHandler.Detail)

			// System controls
			r.Get("/system", systemHandler.Index)
			r.Post("/system/arm", systemHandler.Arm)
			r.Post("/system/disarm", systemHandler.Disarm)
			r.Post("/system/breaker/reset", systemHandler.ResetBreaker)
			r.Post("/system/agent-enable", systemHandler.EnableAgentScorer)
			r.Post("/system/agent-disable", systemHandler.DisableAgentScorer)

			// Stats
			r.Get("/stats", statsHandler.Index)

			// Eval dashboard
			r.Get("/eval", evalHandler.Index)
			r.Get("/eval/replays", evalHandler.ReplayList)
			r.Get("/eval/replays/{id}", evalHandler.ReplayDetail)
			r.Get("/eval/replays/{id}/rows", evalHandler.ReplayRowsPartial)

			// Settings
			r.Get("/settings", settingsHandler.Index)
			r.Post("/settings/risk", settingsHandler.SaveRisk)
			r.Post("/settings/notifier", settingsHandler.SaveNotifier)
			r.Post("/settings/binance", settingsHandler.SaveBinance)
			r.Post("/settings/ip-whitelist", settingsHandler.SaveIPWhitelist)
			r.Post("/settings/advanced", settingsHandler.SaveAdvanced)
			r.Post("/settings/agent-scorer", settingsHandler.SaveAgentScorer)
			r.Post("/settings/llm-api", settingsHandler.SaveLLMAPI)

			// Status partial (HTMX hx-get for status bar auto-refresh)
			r.Get("/_partials/status", statusHandler.Partial)
		})
	})

	// ── graceful shutdown ────────────────────────────────────────────────────
	shutCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// ── startup recovery (BEFORE HTTP server) ────────────────────────────────
	recovery := reconcile.NewRecovery(pool, posRepo, orderRepo, historyRepo, systemRepo, trader, notifier, logger)
	if err := recovery.Run(context.Background()); err != nil {
		logger.Error().Err(err).Msg("startup recovery failed")
		os.Exit(1)
	}

	// ── signal recovery: re-enqueue pending webhook signals interrupted by
	// crash; flag stale ones (>10min) as abandoned with critical alert.
	sigRecovery := reconcile.NewSignalRecovery(pool, signalRepo, dispatcher,
		notifier, logger, 10*time.Minute)
	if err := sigRecovery.Run(context.Background()); err != nil {
		// soft-fail: bot can still process new webhooks; recovered signals
		// surface in /signals as 'pending' for the operator to examine.
		logger.Error().Err(err).Msg("signal recovery failed")
	}

	// ── order reconciler (background goroutine) ──────────────────────────────
	// Use DB value if set; fall back to yaml value.
	reconcilerInterval := cfg.Reconciler.IntervalSeconds
	if dbSettings.ReconcilerIntervalSeconds > 0 {
		reconcilerInterval = dbSettings.ReconcilerIntervalSeconds
	}
	reconciler := reconcile.New(pool, orderRepo, posRepo, historyRepo, trader, notifier, logger,
		time.Duration(reconcilerInterval)*time.Second)
	go func() {
		if err := reconciler.Run(shutCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("reconciler exited")
		}
	}()

	// Position heartbeat: re-runs the startup-recovery reconcile every 2
	// minutes so a position closed externally (server-side stop trigger,
	// manual exchange UI close) gets reflected in DB instead of staying
	// permanently 'open'. Independent of the per-30s order reconciler.
	go recovery.RunPeriodic(shutCtx, 2*time.Minute)

	if err := srv.Start(shutCtx); err != nil {
		logger.Error().Err(err).Msg("server error")
		os.Exit(1)
	}

	// HTTP listener has stopped accepting new requests; drain in-flight
	// dispatcher workers so already-received signals finish their trade
	// execution. Any signals still buffered at deadline stay 'pending' in
	// DB for the next startup recovery to pick up.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()
	if err := dispatcher.Shutdown(drainCtx); err != nil {
		logger.Warn().Err(err).Msg("dispatcher shutdown timed out — pending signals stay in DB for next recovery")
	}
}
