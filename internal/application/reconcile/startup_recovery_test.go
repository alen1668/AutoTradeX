package reconcile

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func TestPositionSidesMatch(t *testing.T) {
	assert.True(t, positionSidesMatch("long", decimal.NewFromInt(1)))
	assert.False(t, positionSidesMatch("long", decimal.NewFromInt(-1)))
	assert.False(t, positionSidesMatch("long", decimal.Zero))
	assert.True(t, positionSidesMatch("short", decimal.NewFromInt(-1)))
	assert.False(t, positionSidesMatch("short", decimal.NewFromInt(1)))
	assert.False(t, positionSidesMatch("flat", decimal.NewFromInt(1)))
}
