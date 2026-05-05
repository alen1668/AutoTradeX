package order

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatus_LifecycleAllowed(t *testing.T) {
	// pending -> submitted -> partial -> filled
	assert.True(t, StatusPending.CanTransitionTo(StatusSubmitted))
	assert.True(t, StatusSubmitted.CanTransitionTo(StatusPartial))
	assert.True(t, StatusSubmitted.CanTransitionTo(StatusFilled))
	assert.True(t, StatusPartial.CanTransitionTo(StatusFilled))

	// terminal canceled / rejected / expired 不能再前进
	assert.False(t, StatusFilled.CanTransitionTo(StatusPartial))
	assert.False(t, StatusCanceled.CanTransitionTo(StatusFilled))
	assert.False(t, StatusRejected.CanTransitionTo(StatusFilled))
	assert.False(t, StatusExpired.CanTransitionTo(StatusFilled))

	// pending/submitted 可以被取消/拒绝
	assert.True(t, StatusPending.CanTransitionTo(StatusCanceled))
	assert.True(t, StatusSubmitted.CanTransitionTo(StatusCanceled))
	assert.True(t, StatusSubmitted.CanTransitionTo(StatusRejected))
}

func TestPurpose_String(t *testing.T) {
	assert.Equal(t, "entry", string(PurposeEntry))
	assert.Equal(t, "backup_stop", string(PurposeBackupStop))
}
