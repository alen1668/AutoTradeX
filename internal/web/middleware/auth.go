package middleware

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
)

// RequireUser blocks the request unless the session contains a non-empty username.
func RequireUser(sess *scs.SessionManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := sess.GetString(r.Context(), "username")
			if user == "" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CurrentUser fetches the logged-in username; empty string when anon.
func CurrentUser(sess *scs.SessionManager, r *http.Request) string {
	return sess.GetString(r.Context(), "username")
}
