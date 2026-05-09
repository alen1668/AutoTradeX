package reconcile

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/trade"
)

func TestPositionSidesMatch(t *testing.T) {
	assert.True(t, positionSidesMatch("long", decimal.NewFromInt(1)))
	assert.False(t, positionSidesMatch("long", decimal.NewFromInt(-1)))
	assert.False(t, positionSidesMatch("long", decimal.Zero))
	assert.True(t, positionSidesMatch("short", decimal.NewFromInt(-1)))
	assert.False(t, positionSidesMatch("short", decimal.NewFromInt(1)))
	assert.False(t, positionSidesMatch("flat", decimal.NewFromInt(1)))
}

// fakeAllPositionsLister is the test stand-in for AllPositionsLister.
type fakeAllPositionsLister struct{ pos []trade.Position }

func (f *fakeAllPositionsLister) AllPositions(_ context.Context) ([]trade.Position, error) {
	return f.pos, nil
}

// captureNotifier records sent messages for assertions.
type captureNotifier struct{ msgs []notify.Message }

func (c *captureNotifier) Send(_ context.Context, m notify.Message) error {
	c.msgs = append(c.msgs, m)
	return nil
}

func TestDetectGhost_AlertsOnUnknownSymbol(t *testing.T) {
	cap := &captureNotifier{}
	r := &Recovery{
		notifier: cap,
		log:      zerolog.Nop(),
		trader: &mixedTrader{lister: &fakeAllPositionsLister{pos: []trade.Position{
			{Symbol: "BTCUSDT", Qty: decimal.NewFromFloat(0.01), EntryPrice: decimal.NewFromInt(80000)},
			{Symbol: "ETHUSDT", Qty: decimal.NewFromFloat(0.5), EntryPrice: decimal.NewFromInt(2300)},
		}}},
	}
	active := map[string]bool{"BTCUSDT": true} // ETHUSDT is the ghost
	r.detectGhostPositions(context.Background(), active)

	require.Len(t, cap.msgs, 1, "exactly one ghost should fire")
	assert.Contains(t, cap.msgs[0].Title, "幽灵")
	assert.Contains(t, cap.msgs[0].Body, "ETHUSDT")
	assert.Equal(t, notify.SeverityCritical, cap.msgs[0].Severity)
}

func TestDetectGhost_NoAlertsWhenAllKnown(t *testing.T) {
	cap := &captureNotifier{}
	r := &Recovery{
		notifier: cap,
		log:      zerolog.Nop(),
		trader: &mixedTrader{lister: &fakeAllPositionsLister{pos: []trade.Position{
			{Symbol: "BTCUSDT", Qty: decimal.NewFromFloat(0.01), EntryPrice: decimal.NewFromInt(80000)},
		}}},
	}
	active := map[string]bool{"BTCUSDT": true}
	r.detectGhostPositions(context.Background(), active)
	assert.Empty(t, cap.msgs)
}

func TestDetectGhost_TraderNoListerCapability(t *testing.T) {
	// Traders that don't implement AllPositionsLister (e.g. DryRunTrader)
	// cause a clean skip — no panic, no alert.
	cap := &captureNotifier{}
	r := &Recovery{
		notifier: cap,
		log:      zerolog.Nop(),
		trader:   &nonListerTrader{},
	}
	r.detectGhostPositions(context.Background(), map[string]bool{})
	assert.Empty(t, cap.msgs)
}

// mixedTrader satisfies trade.Trader and embeds the lister so the type
// assertion in detectGhostPositions succeeds.
type mixedTrader struct {
	lister *fakeAllPositionsLister
}

func (m *mixedTrader) Place(context.Context, trade.OrderRequest) (*trade.OrderResult, error) {
	return nil, nil
}
func (m *mixedTrader) Cancel(context.Context, string, string) error { return nil }
func (m *mixedTrader) GetOrder(context.Context, string, string) (*trade.OrderResult, error) {
	return nil, nil
}
func (m *mixedTrader) GetPositionRisk(context.Context, string) (*trade.Position, error) {
	return nil, nil
}
func (m *mixedTrader) AllPositions(ctx context.Context) ([]trade.Position, error) {
	return m.lister.AllPositions(ctx)
}

type nonListerTrader struct{}

func (n *nonListerTrader) Place(context.Context, trade.OrderRequest) (*trade.OrderResult, error) {
	return nil, nil
}
func (n *nonListerTrader) Cancel(context.Context, string, string) error { return nil }
func (n *nonListerTrader) GetOrder(context.Context, string, string) (*trade.OrderResult, error) {
	return nil, nil
}
func (n *nonListerTrader) GetPositionRisk(context.Context, string) (*trade.Position, error) {
	return nil, nil
}
