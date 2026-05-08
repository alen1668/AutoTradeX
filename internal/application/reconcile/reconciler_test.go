package reconcile

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/trade"
)

type fakeTrader struct {
	mu      sync.Mutex
	results map[string]*trade.OrderResult
	errs    map[string]error
}

func (f *fakeTrader) Place(_ context.Context, _ trade.OrderRequest) (*trade.OrderResult, error) {
	return nil, errors.New("not impl")
}
func (f *fakeTrader) Cancel(_ context.Context, _, _ string) error { return nil }
func (f *fakeTrader) GetOrder(_ context.Context, _, cid string) (*trade.OrderResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.errs[cid]; ok {
		return nil, e
	}
	if r, ok := f.results[cid]; ok {
		return r, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeTrader) GetPositionRisk(_ context.Context, _ string) (*trade.Position, error) {
	return &trade.Position{}, nil
}

func TestReconciler_Constructor(t *testing.T) {
	// Covers compile + happy path with no orders
	r := New(nil, nil, nil, nil, &fakeTrader{}, notify.NoOp{}, zerolog.Nop(), 50*time.Millisecond)
	require.NotNil(t, r)
}

func TestIsProtective(t *testing.T) {
	assert.True(t, isProtective("stop"))
	assert.True(t, isProtective("backup_stop"))
	assert.True(t, isProtective("take_profit"))
	assert.False(t, isProtective("entry"))
	assert.False(t, isProtective("exit"))
}
