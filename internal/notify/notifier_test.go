package notify

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorder struct {
	got []Message
	err error
}

func (r *recorder) Send(_ context.Context, m Message) error {
	r.got = append(r.got, m)
	return r.err
}

func TestNoOp(t *testing.T) {
	n := NoOp{}
	require.NoError(t, n.Send(context.Background(), Message{Title: "x"}))
}

func TestMulti_FansOut(t *testing.T) {
	a := &recorder{}
	b := &recorder{}
	n := NewMulti(a, b)
	require.NoError(t, n.Send(context.Background(), Message{Title: "hi"}))
	assert.Len(t, a.got, 1)
	assert.Len(t, b.got, 1)
}

func TestMulti_AggregatesErrors(t *testing.T) {
	a := &recorder{}
	b := &recorder{err: errors.New("boom")}
	n := NewMulti(a, b)
	err := n.Send(context.Background(), Message{Title: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	assert.Len(t, a.got, 1, "first notifier still called even if second errs")
}

func TestSeverity_Constants(t *testing.T) {
	assert.Equal(t, "info", string(SeverityInfo))
	assert.Equal(t, "warn", string(SeverityWarn))
	assert.Equal(t, "critical", string(SeverityCritical))
}
