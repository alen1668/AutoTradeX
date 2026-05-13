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
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/agent/calendar"
	"github.com/lizhaojie/tvbot/internal/agent/critique"
	"github.com/lizhaojie/tvbot/internal/agent/exit"
	"github.com/lizhaojie/tvbot/internal/agent/history"
	"github.com/lizhaojie/tvbot/internal/agent/macrocontext"
	"github.com/lizhaojie/tvbot/internal/agent/market"
	"github.com/lizhaojie/tvbot/internal/agent/news"
	"github.com/lizhaojie/tvbot/internal/agent/perpmetrics"
	"github.com/lizhaojie/tvbot/internal/agent/portfolio"
	"github.com/lizhaojie/tvbot/internal/agent/regime"
	"github.com/lizhaojie/tvbot/internal/agent/scorer"
	"github.com/lizhaojie/tvbot/internal/application/ingest"
	"github.com/lizhaojie/tvbot/internal/application/reconcile"
	"github.com/lizhaojie/tvbot/internal/application/trade"
	"github.com/lizhaojie/tvbot/internal/config"
	evalpkg "github.com/lizhaojie/tvbot/internal/eval"
	"github.com/lizhaojie/tvbot/internal/eval/outcome"
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
	if dbSettings.WecomEnabled && dbSettings.WecomWebhookURL != "" {
		notifiers = append(notifiers, notify.NewWecom(dbSettings.WecomWebhookURL))
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

	// ── Phase 3 SSE broker ───────────────────────────────────────────────────
	// Fan-out hub for agent_score / trade_closed events. Hot path stays
	// non-blocking; SSE clients connect via /eval/stream.
	broker := evalpkg.NewBroker(logger)

	// ── application services ─────────────────────────────────────────────────
	tradeSvc := trade.NewService(pool, orderRepo, posRepo, historyRepo, trader).WithSystemRepo(systemRepo).WithPublisher(broker)

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
	critiqueRepo := store.NewCritiqueRepo(pool)
	scorerFactory := scorer.NewFactory(
		llmClient, agentEvalRepo, pool,
		logger.With().Str("c", "agent_scorer").Logger(),
		critiqueRepo,                 // PinnedPatternsProvider — store.CritiqueRepo has the right method
		dbSettings.CritiqueMaxPinned, // empty/zero → fallback to 5 inside NewFactory
	)
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
	// ── macro context layer (regime / calendar / news) ─────────────────────
	// Three repos + a Reader for AgentHook. Workers themselves are started
	// later, after shutCtx exists; here we only wire what AgentHook needs
	// at construction time.
	regimeRepo := store.NewMarketRegimeRepo(pool)
	eventsRepo := store.NewEconomicEventsRepo(pool)
	newsRepo := store.NewNewsSnapshotsRepo(pool)
	perpRepo := store.NewPerpMetricsRepo(pool)
	calendarStore := calendar.NewStoreAdapter(eventsRepo, pool)
	macroReader := macrocontext.NewReader(
		macrocontext.WrapRegimeRepo(regimeRepo, pool),
		calendarStore,
		macrocontext.WrapNewsRepo(newsRepo, pool),
	).WithPerp(
		macrocontext.WrapPerpRepo(perpRepo, pool),
		macrocontext.WrapSettingsForPerp(settingsRepo, pool),
	)

	agentHook := ingest.NewAgentHook(scorerFactory, historyProv, portfolioProv, marketProv).
		WithPublisher(broker).
		WithMacroReader(macroReader)

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
	renderer = renderer.WithNewsBanner(admin.NewNewsBannerProvider(newsRepo, pool))

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
	evalHandler := admin.NewEvalHandler(renderer, pool).WithStatus(statusHandler).WithBroker(broker)
	evalNewHandler := admin.NewEvalNewHandler(renderer, pool).WithStatus(statusHandler)
	settingsHandler := admin.NewSettingsHandler(renderer, pool, settingsRepo, statusHandler)
	// critiqueRepo is declared above (near scorerFactory) so the scorer can
	// inject pinned critique patterns. critiqueManualCh triggers manual runs.
	critiqueManualCh := make(chan struct{}, 4)
	critiqueHandler := admin.NewCritiqueHandler(renderer, critiqueRepo, critiqueManualCh).WithStatus(statusHandler)
	exitHandler := admin.NewExitHandler(renderer, store.NewExitDecisionRepo(pool)).WithStatus(statusHandler)
	postmortemHandler := admin.NewPostmortemHandler(renderer, pool).WithStatus(statusHandler)

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

			// Root → 收益统计是默认首页（用户最关心盈亏）
			r.Get("/", func(w http.ResponseWriter, req *http.Request) {
				http.Redirect(w, req, "/stats", http.StatusSeeOther)
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
			r.Get("/eval/stream", evalHandler.Stream)
			r.Get("/eval/replays", evalHandler.ReplayList)
			r.Get("/eval/replays/new", evalNewHandler.GetNew)
			r.Post("/eval/replays/preview", evalNewHandler.PostPreview)
			r.Post("/eval/replays", evalNewHandler.PostCreate)
			r.Get("/eval/replays/{id}", evalHandler.ReplayDetail)
			r.Get("/eval/replays/{id}/rows", evalHandler.ReplayRowsPartial)
			r.Get("/eval/ab/{id}", evalHandler.ABCompare)
			r.Get("/eval/news", evalHandler.NewsList)
			r.Get("/eval/news/{id}", evalHandler.NewsDetail)
			r.Get("/eval/perp", evalHandler.PerpList)
			r.Get("/eval/critique", critiqueHandler.List)
			r.Get("/eval/critique/{id}", critiqueHandler.Detail)
			r.Post("/eval/critique/run", critiqueHandler.Run)
			r.Post("/eval/critique/patterns/{id}/pin", critiqueHandler.SetPin)
			r.Post("/eval/critique/{id}/bulk-pin", critiqueHandler.BulkPin)
			r.Get("/eval/exit", exitHandler.List)
			r.Get("/eval/exit/{id}", exitHandler.Detail)
			r.Get("/eval/postmortem", postmortemHandler.View)
			r.Get("/eval/postmortem/details", postmortemHandler.Details)

			// Settings
			r.Get("/settings", settingsHandler.Index)
			r.Post("/settings/risk", settingsHandler.SaveRisk)
			r.Post("/settings/notifier", settingsHandler.SaveNotifier)
			r.Post("/settings/binance", settingsHandler.SaveBinance)
			r.Post("/settings/ip-whitelist", settingsHandler.SaveIPWhitelist)
			r.Post("/settings/advanced", settingsHandler.SaveAdvanced)
			r.Post("/settings/agent-scorer", settingsHandler.SaveAgentScorer)
			r.Post("/settings/llm-api", settingsHandler.SaveLLMAPI)
			r.Post("/settings/macro", settingsHandler.SaveMacro)
			r.Post("/settings/wecom", settingsHandler.SaveWecom)

			// Status partial (HTMX hx-get for status bar auto-refresh)
			r.Get("/_partials/status", statusHandler.Partial)
		})
	})

	// ── graceful shutdown ────────────────────────────────────────────────────
	shutCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// ── macro context workers (regime / calendar / news) ───────────────────
	// Each gated by its own settings flag (default-off). Workers run on
	// independent goroutines and share the shutdown context. All three
	// failure-mode behaviors keep scorer working even if every source is
	// empty; see internal/agent/macrocontext for the read path.
	if bt, ok := trader.(*binanceinfra.Trader); ok {
		klineClient := market.NewBinanceKlineClient(bt.FuturesClient())
		regimeWorker := regime.NewWorker(
			klineClient,
			regimeRepoAdapter{repo: regimeRepo, pool: pool},
			regime.NewSettingsAdapter(settingsRepo, pool),
			logger.With().Str("c", "regime").Logger(),
		)
		go regimeWorker.Start(shutCtx)
	} else {
		logger.Warn().Msg("regime worker not started: trader is not BinanceTrader")
	}
	{
		calendarFetcher := calendar.NewForexFactoryFetcher(calendar.DefaultForexFactoryURL)
		calendarWorker := calendar.NewWorker(
			calendarFetcher,
			calendarStore,
			calendar.NewSettingsAdapter(settingsRepo, pool),
			logger.With().Str("c", "calendar").Logger(),
		)
		go calendarWorker.Start(shutCtx)
	}
	if dbSettings.NewsLLMModel != "" {
		// Multi-source RSS aggregation: 加密原生 (CoinDesk) + 宏观财经
		// (MarketWatch / CNBC / Yahoo Finance)。任一源失败不阻塞其它源,
		// 让 LLM 看到跨市场信号 (e.g. ETF 撤资、CPI、央行行动)。
		// CryptoPanic 已停止免费版 (2026-04-01),保留 cryptopanic.go 给付费用户。
		newsFetcher := news.NewMultiFetcher(
			[]news.Fetcher{
				news.NewRSSFetcher("coindesk", news.DefaultCoinDeskRSSURL, "CoinDesk"),
				news.NewRSSFetcher("marketwatch", news.DefaultMarketWatchRSSURL, "MarketWatch"),
				news.NewRSSFetcher("cnbc", news.DefaultCNBCMarketsRSSURL, "CNBC"),
				news.NewRSSFetcher("yahoo", news.DefaultYahooFinanceRSSURL, "Yahoo Finance"),
			},
			logger.With().Str("c", "news_multi").Logger(),
		)
		newsClassifier := news.NewClassifier(llmClient, dbSettings.NewsLLMModel,
			logger.With().Str("c", "news_llm").Logger())
		newsPersistor := news.NewStoreAdapter(newsRepoAdapter{repo: newsRepo, pool: pool})
		newsWorker := news.NewWorker(
			newsFetcher,
			newsClassifier,
			newsPersistor,
			broker,
			news.NewSettingsAdapter(settingsRepo, pool),
			logger.With().Str("c", "news").Logger(),
		).WithNotifier(notifier)
		go newsWorker.Start(shutCtx)
	}

	// ── perp metrics worker ────────────────────────────────────────────────
	// Gated by settings.perp_metrics_enabled (default false). Pulls funding /
	// OI / top-LS ratio per (active strategy symbol ∪ BTCUSDT).
	//
	// Perp metrics are a market-wide public dataset (no auth required) — we
	// always use the LIVE binance URL so testnet deployments still see real
	// market sentiment data. Trades still go through the testnet trader.
	if _, ok := trader.(*binanceinfra.Trader); ok {
		perpFetcher := perpmetrics.NewBinanceFetcher(newLivePerpClient())
		perpWorker := perpmetrics.NewWorker(
			perpFetcher,
			perpKlineAdapter{provider: marketProv},
			perpStoreAdapter{repo: perpRepo, pool: pool},
			perpSymbolsAdapter{strategyRepo: strategyRepo, pool: pool},
			perpmetrics.NewSettingsAdapter(settingsRepo, pool),
			logger.With().Str("c", "perp").Logger(),
		)
		go perpWorker.Start(shutCtx)
	} else {
		logger.Warn().Msg("perp metrics worker not started: trader is not BinanceTrader")
	}

	// ── outcome backfiller worker ──────────────────────────────────────────
	// Backfills agent_evaluations.outcome_* columns for every scored signal:
	// approve path → trades.pnl_usdc via position_history; abandon path →
	// fixed-horizon counterfactual close from live binance. Pure data layer,
	// no LLM. Safe to run continuously.
	{
		outcomeSettings := outcome.NewSettingsAdapter(settingsRepo, pool)
		outcomeCfg, err := outcomeSettings.Read(shutCtx)
		if err != nil {
			logger.Warn().Err(err).Msg("outcome: settings load failed, using defaults")
			outcomeCfg = outcome.Config{
				HorizonMin:   60,
				BatchSize:    200,
				ScanInterval: 5 * time.Minute,
				StaleCutoffH: 24,
			}
		}
		outcomeRepo := outcome.NewPGRepo(pool)
		outcomeWorker := outcome.NewWorker(
			outcomeRepo,                                       // PendingReader
			outcomeKlineAdapter{client: newLivePerpClient()}, // KlineFetcher
			outcomeRepo,                                       // Writer
			outcomeCfg,
			ptrLogger(logger.With().Str("c", "outcome").Logger()),
		)
		go outcomeWorker.Start(shutCtx)
		logger.Info().Msg("outcome worker started")
	}

	// ── critique self-reflection worker ────────────────────────────────────
	// Cron-driven LLM agent that reflects on agent_evaluations + their
	// outcome labels and proposes structured "误判模式" patterns. Patterns
	// can be pinned by operators (web /eval/critique) to inject into
	// scorer prompt. critiqueRepo and critiqueManualCh are declared above
	// near the handler construction block so they can be passed to the
	// web handler before the router is built.
	{
		critiqueSettings := critique.NewSettingsAdapter(settingsRepo, pool)
		s, err := critiqueSettings.Read(shutCtx)
		if err != nil {
			logger.Warn().Err(err).Msg("critique: settings read failed, using defaults")
		}
		// Resolve model: empty critique_model falls back to scorer model.
		model := s.Model
		if model == "" {
			model = dbSettings.AgentScorerModel
		}
		critiqueAgent := critique.NewAgent(
			llmClient,
			critique.NewPGDataReader(pool),
			critique.NewPGStore(critiqueRepo),
			critique.Config{
				Model:             model,
				WindowDays:        s.WindowDays,
				MinSample:         s.MinSample,
				MaxPinned:         s.MaxPinned,
				TimeoutMs:         60_000,
				DetailLimit:       200,
				AutoPinConfidence: s.AutoPinConfidence,
			},
			ptrLogger(logger.With().Str("c", "critique").Logger()),
		)
		critiqueWorker := critique.NewWorker(
			critiqueAgent,
			critiqueSettings,
			logger.With().Str("c", "critique-worker").Logger(),
		)
		go critiqueWorker.Start(shutCtx, critiqueManualCh)
		logger.Info().Bool("enabled", s.Enabled).Msg("critique worker started")
	}

	// ── Exit Agent (持仓生命周期决策, shadow mode by default) ──────────────
	// Periodic LLM-driven decisions over open virtual_positions. Failures
	// here MUST NOT impact the main trade pipeline; the dual SL on Binance
	// is the last-line safety net. Default settings.exit_agent_enabled=false
	// so the worker spins idle until a human flips it on. Default
	// settings.exit_agent_mode='shadow' so even when enabled it only writes
	// agent_exit_decisions rows without touching positions.
	{
		exitDecRepo := store.NewExitDecisionRepo(pool)
		exitSettings := exit.NewSettingsAdapter(settingsRepo, pool)
		exitStore := exit.NewPGStore(exitDecRepo)
		exitPosReader := exit.NewDBOpenPositionsReader(pool, posRepo, exitPriceProvider{p: marketProv})
		exitCtx := exit.NewDefaultContextProvider(
			exitKlineProvider{p: marketProv},
			macroReader,
			exitHistoricalProvider{pool: pool},
			exitPinnedProvider{repo: critiqueRepo},
			5,
		)
		// Resolve model up-front; SettingsAdapter applies the same fallback
		// at every Read() so the worker's first cfg read also picks the
		// correct model — but Agent itself stores model immutably. We
		// snapshot the *current* model so log/audit references stay stable
		// across config changes; cfg.Model in worker.processOne uses the
		// fresh value too (in DecisionMeta).
		exitModel := dbSettings.AgentScorerModel
		if dbSettings.ExitAgentModel != "" {
			exitModel = dbSettings.ExitAgentModel
		}
		exitAgent := exit.NewAgent(llmClient, exitModel)
		exitWorker := exit.NewWorker(exit.WorkerDeps{
			Reader:   exitPosReader,
			Ctx:      exitCtx,
			Decider:  exitAgent,
			Store:    exitStore,
			Settings: exitSettings,
			Cooldown: exit.NewRepoCooldownReader(exitDecRepo),
			Executor: noopExitExecutor{}, // Task 15 swaps with trade.ExitOrchestrator
			Recorder: exitRecorderAdapter{repo: exitDecRepo},
			Log:      logger.With().Str("c", "exit").Logger(),
		})
		go exitWorker.Start(shutCtx)
		logger.Info().
			Bool("enabled", dbSettings.ExitAgentEnabled).
			Str("mode", dbSettings.ExitAgentMode).
			Str("model", exitModel).
			Msg("exit worker started")

		// ── if-hold counterfact backfiller for agent_exit_decisions ────
		// Reuses the existing live-binance kline adapter (same one used
		// by the entry-side outcome backfiller). Runs every 5 min.
		// V1 simplification: ActualPnLPct is always nil — see
		// exitDecisionPendingAdapter doc.
		exitOutcomeWorker := outcome.NewExitOutcomeWorker(
			exitDecisionPendingAdapter{repo: exitDecRepo},
			outcomeKlineAdapter{client: newLivePerpClient()},
			exitDecisionWriterAdapter{repo: exitDecRepo},
			dbSettings.ExitAgentHorizonMin,
			24,
			logger.With().Str("c", "exit_outcome").Logger(),
		)
		go exitOutcomeWorker.Start(shutCtx, 5*time.Minute)
		logger.Info().Int("horizon_min", dbSettings.ExitAgentHorizonMin).Msg("exit outcome worker started")
	}

	// ── Phase 2 replay worker ────────────────────────────────────────────────
	// Polls replay_runs WHERE status='pending' every 1s. Web form is the only
	// pending-row producer; cmd/agent-eval --replay creates 'running' directly.
	//
	// Timeout: system_state.agent_scorer_timeout_ms is tuned for production
	// Haiku (5 s default), but replay can pick Sonnet/Opus which need more
	// headroom. Floor at 60 s here so model choice never deadlines a request.
	{
		apiKey, scorerModel, baseURL, timeoutMs := evalpkg.LoadLLMConfig(shutCtx, pool)
		if timeoutMs < 60_000 {
			timeoutMs = 60_000
		}
		llmClient := evalpkg.MakeLLMClient(apiKey, baseURL)
		replayWorker := evalpkg.NewWorker(pool, llmClient, scorerModel, timeoutMs, notifier, logger)
		go replayWorker.Run(shutCtx)
	}

	// ── startup recovery (BEFORE HTTP server) ────────────────────────────────
	recovery := reconcile.NewRecovery(pool, posRepo, orderRepo, historyRepo, systemRepo, trader, notifier, logger).WithPublisher(broker)
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
		time.Duration(reconcilerInterval)*time.Second).WithPublisher(broker)
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

// ptrLogger returns a pointer to l. Convenience for worker constructors
// that take *zerolog.Logger.
func ptrLogger(l zerolog.Logger) *zerolog.Logger { return &l }
