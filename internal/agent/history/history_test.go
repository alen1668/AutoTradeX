package history

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/store"
)

type fakeQ struct {
	bySymbol   []*store.PositionHistoryRow
	byStrategy []*store.PositionHistoryRow
	err        error
}

func (f *fakeQ) ListBySymbolAndStrategy(ctx context.Context, _ store.Querier, _ string, _ string, limit int) ([]*store.PositionHistoryRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	if limit < len(f.bySymbol) {
		return f.bySymbol[:limit], nil
	}
	return f.bySymbol, nil
}
func (f *fakeQ) ListByStrategy(ctx context.Context, _ store.Querier, _ string, limit int) ([]*store.PositionHistoryRow, error) {
	if f.err != nil {
		return nil, f.err
	}
	if limit < len(f.byStrategy) {
		return f.byStrategy[:limit], nil
	}
	return f.byStrategy, nil
}

func makeHistRow(symbol, side string, pnl int64, durSec int) *store.PositionHistoryRow {
	return &store.PositionHistoryRow{
		Symbol:          symbol,
		Side:            side,
		EntryFillPrice:  decimal.NewFromInt(2280),
		ExitFillPrice:   decimal.NewFromInt(2310),
		PnLUSDC:         decimal.NewFromInt(pnl),
		CloseReason:     "tp",
		DurationSeconds: durSec,
		OpenedAt:        time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC),
		ClosedAt:        time.Date(2024, 5, 1, 12, 45, 0, 0, time.UTC),
	}
}

func TestProvider_SymbolHistory_MapsRows(t *testing.T) {
	fq := &fakeQ{bySymbol: []*store.PositionHistoryRow{makeHistRow("ETHUSDC", "long", 30, 2700)}}
	p := New(fq, nil)
	out, err := p.SymbolHistory(context.Background(), "s1", "ETHUSDC", 20)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "long", out[0].Direction)
	assert.Equal(t, "tp", out[0].ExitReason)
	assert.Equal(t, 45, out[0].DurationMin, "duration_seconds=2700 should map to 45 min")
	assert.Equal(t, "30", out[0].PnLUSD.String())
}

func TestProvider_SymbolHistory_LimitRespected(t *testing.T) {
	rows := make([]*store.PositionHistoryRow, 30)
	for i := range rows {
		rows[i] = makeHistRow("ETHUSDC", "long", 1, 60)
	}
	fq := &fakeQ{bySymbol: rows}
	p := New(fq, nil)
	out, _ := p.SymbolHistory(context.Background(), "s1", "ETHUSDC", 5)
	assert.Len(t, out, 5)
}

func TestProvider_StrategyHistory(t *testing.T) {
	fq := &fakeQ{byStrategy: []*store.PositionHistoryRow{makeHistRow("BTCUSDC", "short", 50, 7200)}}
	p := New(fq, nil)
	out, _ := p.StrategyHistory(context.Background(), "s1", 20)
	require.Len(t, out, 1)
	assert.Equal(t, "BTCUSDC", out[0].Symbol)
	assert.Equal(t, 120, out[0].DurationMin)
}

func TestProvider_DBError_ReturnsEmptyNoErr(t *testing.T) {
	fq := &fakeQ{err: errors.New("db down")}
	p := New(fq, nil)
	out, err := p.SymbolHistory(context.Background(), "s1", "ETHUSDC", 20)
	assert.NoError(t, err, "scorer's history layer must be degradable: never bubble DB errors")
	assert.Empty(t, out)

	out, err = p.StrategyHistory(context.Background(), "s1", 20)
	assert.NoError(t, err)
	assert.Empty(t, out)
}

func TestProvider_EmptyResult(t *testing.T) {
	fq := &fakeQ{}
	p := New(fq, nil)
	out, err := p.SymbolHistory(context.Background(), "s1", "ETHUSDC", 20)
	assert.NoError(t, err)
	assert.Empty(t, out)
}
