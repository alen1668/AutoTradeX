package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/lizhaojie/tvbot/internal/log"
)

const headerTraceID = "X-Trace-ID"

// TraceID middleware extracts or generates a trace ID and stores it on the
// request context. Echo it back to the client in the response header.
func TraceID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid := r.Header.Get(headerTraceID)
		if tid == "" {
			tid = uuid.NewString()
		}
		ctx := log.WithTraceID(r.Context(), tid)
		w.Header().Set(headerTraceID, tid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
