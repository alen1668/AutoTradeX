package middleware

import (
	"net/http"

	"github.com/lizhaojie/tvbot/internal/risk"
	"github.com/lizhaojie/tvbot/internal/web/webhook"
)

// IPWhitelist gates a request based on the configured CIDR/IP whitelist.
// Returns 403 + JSON for blocked IPs.
// When rule has an empty list (no entries configured), all IPs are allowed.
func IPWhitelist(rule *risk.IPWhitelistRule) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rule.Empty() {
				next.ServeHTTP(w, r)
				return
			}
			ip := webhook.ClientIP(r)
			dec, _ := rule.Check(r.Context(), risk.Input{ClientIP: ip})
			if !dec.Allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"forbidden"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
