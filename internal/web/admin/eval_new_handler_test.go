//go:build integration

package admin

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/eval"
)

func TestEvalNewHandler_GetRendersForm(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, err := NewRenderer()
	require.NoError(t, err)
	h := NewEvalNewHandler(renderer, pool)

	req := httptest.NewRequest("GET", "/eval/replays/new", nil)
	w := httptest.NewRecorder()
	h.GetNew(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "新建 Replay")
	require.Contains(t, body, `name="since"`)
	require.Contains(t, body, `name="prompt_file"`)
	require.Contains(t, body, `name="prompt_text"`)
	require.Contains(t, body, `name="max_n"`)
	require.Contains(t, body, `name="concurrency"`)
	require.Contains(t, body, `name="model"`)
	// Default since=3d selected
	require.True(t, strings.Contains(body, `value="3d" selected`),
		"3d must be selected by default; got body: %s", body)
}

// newPreviewRequest builds a multipart form POST with the given fields.
// Prompt is sent via textarea, not file.
func newPreviewRequest(t *testing.T, path, since, model, promptText string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.WriteField("since", since))
	require.NoError(t, mw.WriteField("model", model))
	require.NoError(t, mw.WriteField("prompt_text", promptText))
	require.NoError(t, mw.WriteField("max_n", "10"))
	require.NoError(t, mw.WriteField("concurrency", "3"))
	require.NoError(t, mw.WriteField("prompt_name", "test.tmpl"))
	require.NoError(t, mw.Close())

	req := httptest.NewRequest("POST", path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestEvalNewHandler_PreviewHappyPath(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalNewHandler(renderer, pool)

	req := newPreviewRequest(t, "/eval/replays/preview", "1h", "claude-sonnet-4-6", "hello {{ .Symbol }}")
	w := httptest.NewRecorder()
	h.PostPreview(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "确认提交")
	require.Contains(t, body, "claude-sonnet-4-6")
	require.Contains(t, body, "test.tmpl")
}

func TestEvalNewHandler_PreviewMissingPrompt(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalNewHandler(renderer, pool)

	req := newPreviewRequest(t, "/eval/replays/preview", "1h", "claude-sonnet-4-6", "")
	w := httptest.NewRecorder()
	h.PostPreview(w, req)

	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Contains(t, w.Body.String(), "不能都为空")
}

func TestEvalNewHandler_PreviewBadModel(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalNewHandler(renderer, pool)

	req := newPreviewRequest(t, "/eval/replays/preview", "1h", "gpt-3", "anything")
	w := httptest.NewRecorder()
	h.PostPreview(w, req)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Contains(t, w.Body.String(), "不在支持列表")
}

func TestEvalNewHandler_PreviewBadSince(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalNewHandler(renderer, pool)

	req := newPreviewRequest(t, "/eval/replays/preview", "30d", "claude-sonnet-4-6", "x")
	w := httptest.NewRecorder()
	h.PostPreview(w, req)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Contains(t, w.Body.String(), "不在允许列表")
}

func TestEvalNewHandler_PreviewBadTemplate(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalNewHandler(renderer, pool)

	req := newPreviewRequest(t, "/eval/replays/preview", "1h", "claude-sonnet-4-6", "{{ .Bogus ")
	w := httptest.NewRecorder()
	h.PostPreview(w, req)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code)
	require.Contains(t, w.Body.String(), "Prompt 模板语法错误")
}

func TestEvalNewHandler_CreateInsertsPending(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalNewHandler(renderer, pool)

	req := newPreviewRequest(t, "/eval/replays", "1h", "claude-sonnet-4-6", "hello {{ .Symbol }}")
	w := httptest.NewRecorder()
	h.PostCreate(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)
	location := w.Header().Get("Location")
	require.Regexp(t, `^/eval/replays/\d+$`, location)

	// Verify row landed with status='pending'.
	store := eval.NewStore(pool)
	runs, _, err := store.ListRuns(req.Context(), 0, 5)
	require.NoError(t, err)
	require.NotEmpty(t, runs)
	require.Equal(t, "pending", runs[0].Status)
	require.Equal(t, "claude-sonnet-4-6", runs[0].Model)
}

func TestEvalNewHandler_PreviewEscapesXSS(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalNewHandler(renderer, pool)

	xss := `<script>alert(1)</script> {{ .Symbol }}`
	req := newPreviewRequest(t, "/eval/replays/preview", "1h", "claude-sonnet-4-6", xss)
	w := httptest.NewRecorder()
	h.PostPreview(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.NotContains(t, body, "<script>alert(1)</script>",
		"prompt body must be HTML-escaped in <pre>")
}
