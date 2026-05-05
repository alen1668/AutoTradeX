package middleware

import (
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/log"
)

// statusRecorder wraps ResponseWriter to capture the response status.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// RequestLogger emits one structured log line per request, tagged with trace_id.
func RequestLogger(base zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rec, r)
			logger := log.FromContext(r.Context(), base)
			logger.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", rec.status).
				Dur("duration", time.Since(start)).
				Msg("http")
		})
	}
}
