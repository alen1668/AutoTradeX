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

func TestStatus_OpeningCanShortcutToClosed(t *testing.T) {
	// 开仓单失败/取消，从未真正成交时直接进 closed
	assert.True(t, StatusOpening.CanTransitionTo(StatusClosed))
}

func TestStatus_UnknownSourceDeniesAll(t *testing.T) {
	assert.False(t, Status("").CanTransitionTo(StatusOpen))
	assert.False(t, Status("garbage").CanTransitionTo(StatusOpen))
	assert.False(t, StatusClosed.CanTransitionTo(StatusClosed))
}
