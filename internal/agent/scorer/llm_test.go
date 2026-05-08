package scorer

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicClient_Success(t *testing.T) {
	var gotBody string
	var gotAPIKey string
	var gotAnthropicVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotAPIKey = r.Header.Get("x-api-key")
		gotAnthropicVersion = r.Header.Get("anthropic-version")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
            "id": "msg_01",
            "model": "claude-haiku-4-5-20251001",
            "content": [{"type":"text","text":"{\"score\":75,\"decision\":\"approve\",\"reasoning\":\"近期表现稳定\"}"}],
            "usage": {"input_tokens": 1000, "output_tokens": 50}
        }`))
	}))
	defer srv.Close()

	c := NewAnthropicClient("sk-test-key", srv.URL)
	resp, err := c.Complete(context.Background(), CompleteRequest{
		Model:     "claude-haiku-4-5-20251001",
		Prompt:    "test prompt",
		MaxTokens: 256,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Text, `"score":75`)
	assert.Equal(t, 1000, resp.TokenIn)
	assert.Equal(t, 50, resp.TokenOut)
	assert.Equal(t, "sk-test-key", gotAPIKey)
	assert.Equal(t, "2023-06-01", gotAnthropicVersion)
	assert.Contains(t, gotBody, "test prompt")
	assert.Contains(t, gotBody, "claude-haiku-4-5-20251001")
}

func TestAnthropicClient_5xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewAnthropicClient("sk-x", srv.URL)
	_, err := c.Complete(context.Background(), CompleteRequest{Model: "m", Prompt: "p", MaxTokens: 10})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestAnthropicClient_401ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewAnthropicClient("sk-x", srv.URL)
	_, err := c.Complete(context.Background(), CompleteRequest{Model: "m", Prompt: "p", MaxTokens: 10})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestAnthropicClient_ContextTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	c := NewAnthropicClient("sk-x", srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Complete(ctx, CompleteRequest{Model: "m", Prompt: "p", MaxTokens: 10})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "deadline") || strings.Contains(err.Error(), "context"),
		"got: %v", err)
}

func TestAnthropicClient_GarbageJSONResponseReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json at all`))
	}))
	defer srv.Close()
	c := NewAnthropicClient("sk-x", srv.URL)
	_, err := c.Complete(context.Background(), CompleteRequest{Model: "m", Prompt: "p", MaxTokens: 10})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func TestAnthropicClient_EmptyContentReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[],"usage":{"input_tokens":1,"output_tokens":0}}`))
	}))
	defer srv.Close()
	c := NewAnthropicClient("sk-x", srv.URL)
	_, err := c.Complete(context.Background(), CompleteRequest{Model: "m", Prompt: "p", MaxTokens: 10})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty content")
}
