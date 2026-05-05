package middleware

import (
	"context"
	"net/http"

	"github.com/lizhaojie/tvbot/internal/risk"
	"github.com/lizhaojie/tvbot/internal/web/webhook"
)

// IPWhitelistLoader returns the current whitelist entries (CIDRs/IPs).
// Reads from the DB on each request so changes take effect immediately.
type IPWhitelistLoader func(ctx context.Context) ([]string, error)

// IPWhitelist gates a request based on the configured CIDR/IP whitelist.
// The loader is called on every request so changes take effect without restart.
// Returns 403 + JSON for blocked IPs, 500 if the loader fails.
// When the whitelist is empty, all IPs are allowed (dev/no-restriction mode).
func IPWhitelist(loader IPWhitelistLoader) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entries, err := loader(r.Context())
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"whitelist load failed"}`))
				return
			}
			// Empty whitelist means no restriction (dev mode)
			if len(entries) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			rule, err := risk.NewIPWhitelistRule(entries)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"whitelist invalid"}`))
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
