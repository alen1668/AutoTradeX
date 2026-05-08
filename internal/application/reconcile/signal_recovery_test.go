//go:build integration

package reconcile

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/store"
)

// fakeSubmitter records what got dispatched.
type fakeSubmitter struct {
	got []int64
}

func (f *fakeSubmitter) Submit(_ string, signalID int64) {
	f.got = append(f.got, signalID)
}

func insertPendingSignal(t *testing.T, pool *pgxpool.Pool, repo *store.SignalRepo,
	receivedAt time.Time, decision string) int64 {
	t.Helper()
	id, _, err := repo.Insert(context.Background(), pool, store.SignalRow{
		StrategyID:    "s1",
		Symbol:        "ETHUSDT",
		Kind:          "long",
		SignalPrice:   decimal.NewFromInt(1),
		TVTimestampMs: receivedAt.UnixMilli(),
		ReceivedAt:    receivedAt,
		RawPayload:    json.RawMessage(`{}`),
		ClientIP:      net.IPv4(127, 0, 0, 1),
		Decision:      decision,
		TraceID:       "t-" + receivedAt.Format("150405.000"),
	})
	require.NoError(t, err)
	return id
}

func TestSignalRecovery_EnqueuesYoungAbandonsOld(t *testing.T) {
	pool := setupDB(t)
	signalRepo := store.NewSignalRepo(pool)
	ctx := context.Background()

	young := insertPendingSignal(t, pool, signalRepo,
		time.Now().UTC().Add(-2*time.Minute), "pending")
	old := insertPendingSignal(t, pool, signalRepo,
		time.Now().UTC().Add(-30*time.Minute), "pending")

	sub := &fakeSubmitter{}
	rec := NewSignalRecovery(pool, signalRepo, sub, notify.NoOp{}, zerolog.Nop(),
		10*time.Minute)
	require.NoError(t, rec.Run(ctx))

	assert.Equal(t, []int64{young}, sub.got, "young pending should be re-enqueued")

	row, err := signalRepo.GetByID(ctx, pool, old)
	require.NoError(t, err)
	assert.Equal(t, "abandoned", row.Decision)
}
