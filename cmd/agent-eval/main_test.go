//go:build integration

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Structural test: cmd must create replay runs directly in 'running' state.
// A regression to 'pending' would race the Phase 2 worker that polls pending
// rows every 1s. We assert against the source literal so a future "tidy up"
// back to pending is loud.
func TestCmd_CreateRunUsesRunningStatus(t *testing.T) {
	b, err := os.ReadFile("main.go")
	require.NoError(t, err)
	src := string(b)
	require.Contains(t, src, `Status:       "running"`,
		"cmd must create replay runs directly in 'running' state (Phase 2 race)")
	require.NotContains(t, src, `Status:       "pending"`,
		"cmd must not create with 'pending' — Phase 2 worker would race-claim")
}
