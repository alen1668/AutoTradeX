package idempotency

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChecker_LRUOnly(t *testing.T) {
	c := NewChecker(64, nil)
	ctx := context.Background()
	dup, err := c.Check(ctx, "s1", 100)
	require.NoError(t, err)
	assert.False(t, dup)

	dup2, err := c.Check(ctx, "s1", 100)
	require.NoError(t, err)
	assert.True(t, dup2)

	dup3, err := c.Check(ctx, "s1", 101)
	require.NoError(t, err)
	assert.False(t, dup3)
}

func TestChecker_LRUEviction(t *testing.T) {
	c := NewChecker(2, nil)
	ctx := context.Background()
	_, _ = c.Check(ctx, "s", 1)
	_, _ = c.Check(ctx, "s", 2)
	_, _ = c.Check(ctx, "s", 3)
	dup, _ := c.Check(ctx, "s", 1)
	assert.False(t, dup, "evicted key must be re-tested via repo (or treated as new in LRU-only mode)")
}
