package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIPWhitelist_AllowsListed(t *testing.T) {
	loader := func(ctx context.Context) ([]string, error) {
		return []string{"127.0.0.1"}, nil
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})
	wrapper := IPWhitelist(loader)(next)

	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	wrapper.ServeHTTP(w, req)

	assert.True(t, called)
	assert.Equal(t, 200, w.Code)
}

func TestIPWhitelist_DeniesUnlisted(t *testing.T) {
	loader := func(ctx context.Context) ([]string, error) {
		return []string{"127.0.0.1"}, nil
	}
	wrapper := IPWhitelist(loader)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	wrapper.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestIPWhitelist_EmptyAllowsAll(t *testing.T) {
	loader := func(ctx context.Context) ([]string, error) {
		return []string{}, nil
	}
	called := false
	wrapper := IPWhitelist(loader)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	wrapper.ServeHTTP(w, req)
	assert.True(t, called)
	assert.Equal(t, 200, w.Code)
}

func TestIPWhitelist_LoaderError(t *testing.T) {
	loader := func(ctx context.Context) ([]string, error) {
		return nil, fmt.Errorf("db error")
	}
	wrapper := IPWhitelist(loader)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	wrapper.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestIPWhitelist_AllowsCIDR(t *testing.T) {
	loader := func(ctx context.Context) ([]string, error) {
		return []string{"10.0.0.0/8"}, nil
	}
	called := false
	wrapper := IPWhitelist(loader)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	req := httptest.NewRequest(http.MethodPost, "/webhook/tv", nil)
	req.RemoteAddr = "10.10.10.1:1234"
	w := httptest.NewRecorder()
	wrapper.ServeHTTP(w, req)
	assert.True(t, called)
	assert.Equal(t, 200, w.Code)
}
