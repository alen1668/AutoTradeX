//go:build integration

package ingest

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
	apptrade "github.com/lizhaojie/tvbot/internal/application/trade"
	"github.com/lizhaojie/tvbot/internal/idempotency"
	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/risk"
	"github.com/lizhaojie/tvbot/internal/store"
	tradepkg "github.com/lizhaojie/tvbot/internal/trade"
)

func setupDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip()
	}
	pool, err := dockertest.NewPool("")
	require.NoError(t, err)
	pool.MaxWait = 60 * time.Second

	res, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: "postgres", Tag: "16-alpine",
		Env: []string{"POSTGRES_USER=test", "POSTGRES_PASSWORD=test", "POSTGRES_DB=test"},
	}, func(c *docker.HostConfig) { c.AutoRemove = true })
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Purge(res) })

	dsn := "postgres://test:test@" + res.GetHostPort("5432/tcp") + "/test?sslmode=disable"
	var p *pgxpool.Pool
	require.NoError(t, pool.Retry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		pp, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return err
		}
		if err := pp.Ping(ctx); err != nil {
			pp.Close()
			return err
		}
		p = pp
		return nil
	}))
	t.Cleanup(p.Close)

	migDir, err := filepath.Abs("../../../migrations")
	require.NoError(t, err)
	entries, err := os.ReadDir(migDir)
	require.NoError(t, err)
	var files []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".sql" {
			files = append(files, filepath.Join(migDir, e.Name()))
		}
	}
	sort.Strings(files)
	for _, f := range files {
		data, err := os.ReadFile(f)
		require.NoError(t, err)
		body := extractGooseUp(string(data))
		_, err = p.Exec(context.Background(), body)
		require.NoError(t, err, "applying %s", f)
	}

	// seed strategy + arm
	_, err = p.Exec(context.Background(), `
INSERT INTO strategies(id, symbol, leverage, size_usdc, stop_loss_pct, take_profit_pct, max_open_usdc, enabled)
VALUES('s', 'ETHUSDC', 5, 100, 1.5, 3.0, 1000, true)`)
	require.NoError(t, err)
	_, err = p.Exec(context.Background(), `UPDATE system_state SET armed=true, armed_by='test'`)
	require.NoError(t, err)

	return p
}

// extractGooseUp duplicates the helper used in store/testhelpers_test.go
// (kept inline because dockertest helpers in store/ are package-private).
func extractGooseUp(s string) string {
	const begin = "-- +goose StatementBegin"
	const end = "-- +goose StatementEnd"
	i := indexAfter(s, begin)
	if i < 0 {
		return s
	}
	body := s[i:]
	j := indexAfter(body, end)
	if j < 0 {
		return body
	}
	return body[:j-len(end)]
}

func indexAfter(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i + len(sub)
		}
	}
	return -1
}

func newService(t *testing.T, p *pgxpool.Pool) *Service {
	t.Helper()
	return newServiceWithAgent(t, p, nil)
}

// newServiceWithAgent constructs an ingest.Service with an optional
// AgentHook. Pass nil to keep the pre-agent behavior (used by all
// non-scorer tests so existing assertions stay valid).
func newServiceWithAgent(t *testing.T, p *pgxpool.Pool, agent *AgentHook) *Service {
	t.Helper()
	signalRepo := store.NewSignalRepo(p)
	settingsRepo := store.NewSettingsRepo(p)
	strategyRepo := store.NewStrategyRepo(p)
	posRepo := store.NewVirtualPositionRepo(p)
	systemRepo := store.NewSystemStateRepo(p)
	orderRepo := store.NewOrderRepo(p)
	historyRepo := store.NewPositionHistoryRepo(p)
	idem := idempotency.NewChecker(1024, signalRepo).WithPool(p)
	pipe := risk.NewPipeline(
		risk.MaxPositionRule{},
		risk.TotalLeverageRule{Settings: risk.NewStaticSettings(decimal.NewFromInt(10), decimal.Zero)},
		risk.DailyLossBreakerRule{Settings: risk.NewStaticSettings(decimal.Zero, decimal.NewFromInt(1000))},
	)
	tradeSvc := apptrade.NewService(p, orderRepo, posRepo, historyRepo, tradepkg.NewDryRunTrader())
	return NewService(Config{
		AccountEquityFallback: decimal.NewFromInt(10000),
		SecretLoader: func(_ context.Context) (string, error) {
			return "secret", nil
		},
	}, p, signalRepo, settingsRepo, strategyRepo, posRepo, systemRepo, idem, pipe, tradeSvc,
		notify.NoOp{}, agent, zerolog.Nop())
}

func TestIngest_OpenLongDryRun(t *testing.T) {
	p := setupDB(t)
	svc := newService(t, p)

	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"100","timestamp":1714723504000,"secret":"secret"}`)
	res, err := svc.Ingest(context.Background(), body, net.ParseIP("127.0.0.1"))
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "accepted", res.Decision)
	assert.Equal(t, "open_long", res.ActionTaken)

	// Verify state
	var sigCount, vpCount, orderCount int
	require.NoError(t, p.QueryRow(context.Background(),
		`SELECT count(*) FROM signals`).Scan(&sigCount))
	require.NoError(t, p.QueryRow(context.Background(),
		`SELECT count(*) FROM virtual_positions WHERE status='open'`).Scan(&vpCount))
	require.NoError(t, p.QueryRow(context.Background(),
		`SELECT count(*) FROM orders`).Scan(&orderCount))
	assert.Equal(t, 1, sigCount)
	assert.Equal(t, 1, vpCount)
	assert.GreaterOrEqual(t, orderCount, 3, "entry + main stop + backup stop (+ optional take_profit)")

	// Idempotent: same signal again → duplicate
	res2, err := svc.Ingest(context.Background(), body, net.ParseIP("127.0.0.1"))
	require.NoError(t, err)
	assert.Equal(t, "duplicate", res2.Decision)
}

func TestIngest_RejectsBadSecret(t *testing.T) {
	p := setupDB(t)
	svc := newService(t, p)
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"100","timestamp":1,"secret":"WRONG"}`)
	res, err := svc.Ingest(context.Background(), body, nil)
	require.NoError(t, err)
	assert.Equal(t, "invalid", res.Decision)
}

func TestIngest_DisarmedSystemRejects(t *testing.T) {
	p := setupDB(t)
	_, _ = p.Exec(context.Background(), `UPDATE system_state SET armed=false`)
	svc := newService(t, p)
	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"100","timestamp":1,"secret":"secret"}`)
	res, _ := svc.Ingest(context.Background(), body, net.ParseIP("127.0.0.1"))
	assert.Equal(t, "disarmed", res.Decision)
}

// silence unused
var _ = json.RawMessage(nil)

// TestService_ReceiveThenProcess_MatchesIngest exercises the new async-friendly
// split: Receive returns immediately with a 'pending' signal, then Process
// finalizes it. The combined effect should match what the synchronous Ingest
// wrapper produces.
func TestService_ReceiveThenProcess_MatchesIngest(t *testing.T) {
	p := setupDB(t)
	svc := newService(t, p)
	ctx := context.Background()

	body := []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"100","timestamp":1714723900000,"secret":"secret"}`)

	rec, err := svc.Receive(ctx, body, net.IPv4(127, 0, 0, 1))
	require.NoError(t, err)
	require.Equal(t, "pending", rec.Decision)
	require.Equal(t, "s", rec.StrategyID)
	require.Greater(t, rec.SignalID, int64(0))

	require.NoError(t, svc.Process(ctx, rec.SignalID))

	signalRepo := store.NewSignalRepo(p)
	row, err := signalRepo.GetByID(ctx, p, rec.SignalID)
	require.NoError(t, err)
	assert.NotEqual(t, "pending", row.Decision, "Process should finalize the decision")
	assert.Equal(t, "accepted", row.Decision)
}

// === Agent scorer integration scenarios (Task 5.3) ============================

// captureNotifier records every Send so tests can assert on alerts.
type captureNotifier struct{ msgs []notify.Message }

func (c *captureNotifier) Send(_ context.Context, m notify.Message) error {
	c.msgs = append(c.msgs, m)
	return nil
}

// newServiceWithFullDeps builds a Service letting the caller pick the
// AgentHook AND the notifier. Used by the agent scenario tests.
func newServiceWithFullDeps(t *testing.T, p *pgxpool.Pool, agent *AgentHook, n notify.Notifier) *Service {
	t.Helper()
	signalRepo := store.NewSignalRepo(p)
	settingsRepo := store.NewSettingsRepo(p)
	strategyRepo := store.NewStrategyRepo(p)
	posRepo := store.NewVirtualPositionRepo(p)
	systemRepo := store.NewSystemStateRepo(p)
	orderRepo := store.NewOrderRepo(p)
	historyRepo := store.NewPositionHistoryRepo(p)
	idem := idempotency.NewChecker(1024, signalRepo).WithPool(p)
	pipe := risk.NewPipeline(
		risk.MaxPositionRule{},
		risk.TotalLeverageRule{Settings: risk.NewStaticSettings(decimal.NewFromInt(10), decimal.Zero)},
		risk.DailyLossBreakerRule{Settings: risk.NewStaticSettings(decimal.Zero, decimal.NewFromInt(1000))},
	)
	tradeSvc := apptrade.NewService(p, orderRepo, posRepo, historyRepo, tradepkg.NewDryRunTrader())
	return NewService(Config{
		AccountEquityFallback: decimal.NewFromInt(10000),
		SecretLoader:          func(_ context.Context) (string, error) { return "secret", nil },
	}, p, signalRepo, settingsRepo, strategyRepo, posRepo, systemRepo,
		idem, pipe, tradeSvc, n, agent, zerolog.Nop())
}

func setAgentSettings(t *testing.T, p *pgxpool.Pool, enabled, dryRun bool, threshold int, failMode string) {
	t.Helper()
	repo := store.NewSettingsRepo(p)
	require.NoError(t, repo.UpdateAgentScorer(context.Background(), p,
		enabled, "claude-haiku-4-5-20251001", threshold, 5000, 20, failMode, dryRun))
}

func countVirtualPositions(t *testing.T, p *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, p.QueryRow(context.Background(),
		`SELECT count(*) FROM virtual_positions`).Scan(&n))
	return n
}

func openLongBody() []byte {
	return []byte(`{"strategy_id":"s","symbol":"ETHUSDC","signal":"Long","price":"100","timestamp":1714723504000,"secret":"secret"}`)
}

// Scenario A: scorer disabled → behavior IDENTICAL to pre-agent code path.
// This is the critical regression — tests assert no agent_evaluations rows
// and all three signals.agent_* columns are NULL.
func TestProcess_ScorerDisabled_BehaviorUnchanged(t *testing.T) {
	p := setupDB(t)
	hook := &AgentHook{scorerOverride: &scorer.StubScorer{
		Result: scorer.ScoreResult{Score: 30, Decision: "abandon", Reasoning: "should NOT run"},
	}}
	svc := newServiceWithFullDeps(t, p, hook, notify.NoOp{})
	// settings.agent_scorer_enabled stays at its default (false) — confirm:
	settings, _ := store.NewSettingsRepo(p).Get(context.Background(), p)
	require.False(t, settings.AgentScorerEnabled)

	res, err := svc.Ingest(context.Background(), openLongBody(), net.ParseIP("127.0.0.1"))
	require.NoError(t, err)
	assert.Equal(t, "accepted", res.Decision)

	signalRepo := store.NewSignalRepo(p)
	row, _ := signalRepo.GetByID(context.Background(), p, res.SignalID)
	assert.Nil(t, row.AgentScore, "scorer disabled must leave agent_score NULL")
	assert.Nil(t, row.AgentDecision)
	assert.Nil(t, row.AgentDryRun)

	var n int
	require.NoError(t, p.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_evaluations`).Scan(&n))
	assert.Equal(t, 0, n, "scorer disabled must write no eval rows")

	// And the trade went through
	assert.Equal(t, 1, countVirtualPositions(t, p))
}

// Scenario B: dry_run + agent wants to abandon → trade STILL goes through,
// agent_decision='abandon' + agent_dry_run=true is the grayscale-period
// "wanted to reject but let through" sample.
func TestProcess_DryRun_AgentAbandon_StillTrades(t *testing.T) {
	p := setupDB(t)
	setAgentSettings(t, p, true, true, 60, "open")
	stub := &scorer.StubScorer{
		Result: scorer.ScoreResult{Score: 30, Decision: "abandon", Reasoning: "连亏"},
	}
	hook := &AgentHook{scorerOverride: stub}
	cap := &captureNotifier{}
	svc := newServiceWithFullDeps(t, p, hook, cap)

	res, err := svc.Ingest(context.Background(), openLongBody(), net.ParseIP("127.0.0.1"))
	require.NoError(t, err)
	assert.Equal(t, "accepted", res.Decision, "dry_run still trades")
	assert.Equal(t, 1, stub.Calls)

	row, _ := store.NewSignalRepo(p).GetByID(context.Background(), p, res.SignalID)
	require.NotNil(t, row.AgentScore)
	assert.Equal(t, 30, *row.AgentScore)
	require.NotNil(t, row.AgentDecision)
	assert.Equal(t, "abandon", *row.AgentDecision)
	require.NotNil(t, row.AgentDryRun)
	assert.True(t, *row.AgentDryRun, "dry_run flag must be true on this sample")

	assert.Equal(t, 1, countVirtualPositions(t, p), "trade should have been placed")

	// No agent-abandon notification in dry_run (only operational alerts allowed).
	for _, m := range cap.msgs {
		assert.NotContains(t, m.Title, "Agent 拒单", "dry_run period must not noise the alert channel")
	}
}

// Scenario C: non-dry_run + agent rejects → signal abandoned, no trade,
// warn notification sent.
func TestProcess_AgentAbandon_BlocksTrade(t *testing.T) {
	p := setupDB(t)
	setAgentSettings(t, p, true, false, 60, "open")
	stub := &scorer.StubScorer{
		Result: scorer.ScoreResult{Score: 30, Decision: "abandon", Reasoning: "高波动+连亏"},
	}
	hook := &AgentHook{scorerOverride: stub}
	cap := &captureNotifier{}
	svc := newServiceWithFullDeps(t, p, hook, cap)

	res, err := svc.Ingest(context.Background(), openLongBody(), net.ParseIP("127.0.0.1"))
	require.NoError(t, err)
	assert.Equal(t, "abandoned", res.Decision)

	row, _ := store.NewSignalRepo(p).GetByID(context.Background(), p, res.SignalID)
	require.NotNil(t, row.AgentScore)
	assert.Equal(t, 30, *row.AgentScore)
	require.NotNil(t, row.AgentDecision)
	assert.Equal(t, "abandon", *row.AgentDecision)
	assert.Equal(t, 0, countVirtualPositions(t, p), "no trade when agent abandons")

	// Warn notification fired
	var sawAbandon bool
	for _, m := range cap.msgs {
		if m.Severity == notify.SeverityWarn && contains(m.Title, "Agent 拒单") {
			sawAbandon = true
		}
	}
	assert.True(t, sawAbandon, "agent abandon must trigger warn notification")
}

// Scenario D: LLM failure + fail_mode=open → trade goes through,
// agent_decision='failed', agent_score NULL.
func TestProcess_LLMFailed_FailOpen_StillTrades(t *testing.T) {
	p := setupDB(t)
	setAgentSettings(t, p, true, false, 60, "open")
	stub := &scorer.StubScorer{
		Result: scorer.ScoreResult{Score: -1, Decision: "failed", Reasoning: "context deadline exceeded"},
	}
	hook := &AgentHook{scorerOverride: stub}
	svc := newServiceWithFullDeps(t, p, hook, notify.NoOp{})

	res, err := svc.Ingest(context.Background(), openLongBody(), net.ParseIP("127.0.0.1"))
	require.NoError(t, err)
	assert.Equal(t, "accepted", res.Decision, "fail_mode=open keeps trading on LLM failure")

	row, _ := store.NewSignalRepo(p).GetByID(context.Background(), p, res.SignalID)
	assert.Nil(t, row.AgentScore, "failed verdict must leave score NULL")
	require.NotNil(t, row.AgentDecision)
	assert.Equal(t, "failed", *row.AgentDecision)
	assert.Equal(t, 1, countVirtualPositions(t, p))
}

// Scenario E: LLM failure + fail_mode=closed → signal abandoned.
func TestProcess_LLMFailed_FailClosed_Abandons(t *testing.T) {
	p := setupDB(t)
	setAgentSettings(t, p, true, false, 60, "closed")
	stub := &scorer.StubScorer{
		Result: scorer.ScoreResult{Score: -1, Decision: "failed", Reasoning: "net err"},
	}
	hook := &AgentHook{scorerOverride: stub}
	svc := newServiceWithFullDeps(t, p, hook, notify.NoOp{})

	res, err := svc.Ingest(context.Background(), openLongBody(), net.ParseIP("127.0.0.1"))
	require.NoError(t, err)
	assert.Equal(t, "abandoned", res.Decision)

	row, _ := store.NewSignalRepo(p).GetByID(context.Background(), p, res.SignalID)
	assert.Nil(t, row.AgentScore)
	require.NotNil(t, row.AgentDecision)
	assert.Equal(t, "failed", *row.AgentDecision)
	assert.Equal(t, 0, countVirtualPositions(t, p), "fail_mode=closed must block")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
