package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/risk"
)

func TestIPWhitelist_AllowsListed(t *testing.T) {
	rule, err := risk.NewIPWhitelistRule([]string{"127.0.0.1"})
	require.NoError(t, err)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})
	wrapper := IPWhitelist(rule)(next)

	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	wrapper.ServeHTTP(w, req)

	assert.True(t, called)
	assert.Equal(t, 200, w.Code)
}

func TestIPWhitelist_DeniesUnlisted(t *testing.T) {
	rule, err := risk.NewIPWhitelistRule([]string{"127.0.0.1"})
	require.NoError(t, err)
	wrapper := IPWhitelist(rule)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	wrapper.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}
