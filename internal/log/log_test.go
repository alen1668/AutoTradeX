package log

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWriterRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := newWith(&buf, "warn")
	logger.Info().Msg("info-msg")
	logger.Warn().Msg("warn-msg")
	out := buf.String()
	assert.NotContains(t, out, "info-msg")
	assert.Contains(t, out, "warn-msg")
}

func TestTraceIDInjectedIntoContext(t *testing.T) {
	ctx := context.Background()
	tid := "trace-abc"
	ctx = WithTraceID(ctx, tid)
	got, ok := TraceIDFrom(ctx)
	require.True(t, ok)
	assert.Equal(t, tid, got)
}

func TestFromContextEmbedsTraceID(t *testing.T) {
	var buf bytes.Buffer
	base := newWith(&buf, "debug")
	ctx := WithTraceID(context.Background(), "trace-xyz")
	logger := FromContext(ctx, base)
	logger.Info().Msg("hi")
	out := buf.String()
	assert.Contains(t, out, "trace-xyz")
}

func TestNewWith_InvalidLevelDefaultsInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := newWith(&buf, "garbage")
	assert.Equal(t, zerolog.InfoLevel, logger.GetLevel())
	_ = strings.TrimSpace(buf.String())
}
