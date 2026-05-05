package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeishu_PostsTextMessage(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok"}`))
	}))
	defer srv.Close()

	n := NewFeishu(srv.URL)
	require.NoError(t, n.Send(context.Background(), Message{
		Title:    "Trade",
		Body:     "Opened LONG ETH @ 2300",
		Severity: SeverityInfo,
	}))
	assert.Equal(t, "text", got["msg_type"])
	content := got["content"].(map[string]any)
	text := content["text"].(string)
	assert.Contains(t, text, "Trade")
	assert.Contains(t, text, "Opened LONG")
}

func TestFeishu_NonZeroCodeIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":19021,"msg":"sign verification failed"}`))
	}))
	defer srv.Close()
	n := NewFeishu(srv.URL)
	err := n.Send(context.Background(), Message{Title: "x", Body: "y"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "19021")
}

func TestFeishu_HTTPErrorIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	n := NewFeishu(srv.URL)
	err := n.Send(context.Background(), Message{Title: "x"})
	require.Error(t, err)
}
