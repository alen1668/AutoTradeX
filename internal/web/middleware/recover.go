package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/log"
)

// Recoverer catches panics, logs them, and returns 500.
func Recoverer(base zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rv := recover(); rv != nil {
					logger := log.FromContext(r.Context(), base)
					logger.Error().
						Interface("panic", rv).
						Bytes("stack", debug.Stack()).
						Str("path", r.URL.Path).
						Msg("panic recovered")
					http.Error(w, fmt.Sprintf("internal error"), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
