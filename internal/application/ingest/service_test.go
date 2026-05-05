//go:build integration

package ingest

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	mig, err := filepath.Abs("../../../migrations/0001_init.sql")
	require.NoError(t, err)
	data, err := os.ReadFile(mig)
	require.NoError(t, err)
	body := extractGooseUp(string(data))
	_, err = p.Exec(context.Background(), body)
	require.NoError(t, err)

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
	signalRepo := store.NewSignalRepo(p)
	strategyRepo := store.NewStrategyRepo(p)
	posRepo := store.NewVirtualPositionRepo(p)
	systemRepo := store.NewSystemStateRepo(p)
	orderRepo := store.NewOrderRepo(p)
	historyRepo := store.NewPositionHistoryRepo(p)
	idem := idempotency.NewChecker(1024, signalRepo).WithPool(p)
	pipe := risk.NewPipeline(
		risk.MaxPositionRule{},
		risk.TotalLeverageRule{MaxLeverage: decimal.NewFromInt(10)},
		risk.DailyLossBreakerRule{MaxDailyLossUSDC: decimal.NewFromInt(1000)},
	)
	tradeSvc := apptrade.NewService(p, orderRepo, posRepo, historyRepo, tradepkg.NewDryRunTrader())
	return NewService(Config{
		AccountEquityFallback: decimal.NewFromInt(10000),
		WebhookSecret:         "secret",
	}, p, signalRepo, strategyRepo, posRepo, systemRepo, idem, pipe, tradeSvc,
		notify.NoOp{}, zerolog.Nop())
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
