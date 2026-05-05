package position

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatus_TransitionsAreLinear(t *testing.T) {
	// opening -> open -> closing -> closed, no rewinds
	assert.True(t, StatusOpening.CanTransitionTo(StatusOpen))
	assert.False(t, StatusOpen.CanTransitionTo(StatusOpening))
	assert.True(t, StatusOpen.CanTransitionTo(StatusClosing))
	assert.True(t, StatusClosing.CanTransitionTo(StatusClosed))
	assert.False(t, StatusClosed.CanTransitionTo(StatusClosing))
	assert.False(t, StatusClosed.CanTransitionTo(StatusOpen))
	// 同状态不允许（必须真正前进）
	assert.False(t, StatusOpen.CanTransitionTo(StatusOpen))
}

func TestStatus_IsActive(t *testing.T) {
	assert.True(t, StatusOpening.IsActive())
	assert.True(t, StatusOpen.IsActive())
	assert.True(t, StatusClosing.IsActive())
	assert.False(t, StatusClosed.IsActive())
}
