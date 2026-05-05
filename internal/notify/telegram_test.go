package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTelegram_PostsMarkdownMessage(t *testing.T) {
	var got map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	n := NewTelegram(srv.URL, "TOKEN", "CHAT")
	require.NoError(t, n.Send(context.Background(), Message{Title: "X", Body: "Y", Severity: SeverityWarn}))

	assert.True(t, strings.HasPrefix(path, "/botTOKEN/sendMessage"))
	assert.Equal(t, "CHAT", got["chat_id"])
	text := got["text"].(string)
	assert.Contains(t, text, "X")
	assert.Contains(t, text, "Y")
	assert.Contains(t, text, "WARN")
}

func TestTelegram_OkFalseIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"bad chat"}`))
	}))
	defer srv.Close()
	n := NewTelegram(srv.URL, "T", "C")
	err := n.Send(context.Background(), Message{Title: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad chat")
}
