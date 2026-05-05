package log

import (
	"context"
	"io"
	"os"

	"github.com/rs/zerolog"
)

type ctxKey int

const traceIDKey ctxKey = 0

// New returns a base logger writing to stderr.
func New(level string) zerolog.Logger {
	return newWith(os.Stderr, level)
}

func newWith(w io.Writer, level string) zerolog.Logger {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil || lvl == zerolog.NoLevel {
		lvl = zerolog.InfoLevel
	}
	return zerolog.New(w).Level(lvl).With().Timestamp().Logger()
}

// WithTraceID stores a trace id on the context.
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

// TraceIDFrom extracts a trace id from context.
func TraceIDFrom(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(traceIDKey).(string)
	return v, ok && v != ""
}

// FromContext returns a logger that automatically tags each log with the
// trace_id from the context (if present).
func FromContext(ctx context.Context, base zerolog.Logger) zerolog.Logger {
	if tid, ok := TraceIDFrom(ctx); ok {
		return base.With().Str("trace_id", tid).Logger()
	}
	return base
}
