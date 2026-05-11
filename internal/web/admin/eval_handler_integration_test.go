//go:build integration

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/lizhaojie/tvbot/internal/eval"
)

func TestEvalHandler_Index_RespondsHappyPath(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, err := NewRenderer()
	require.NoError(t, err)
	h := NewEvalHandler(renderer, pool)

	req := httptest.NewRequest("GET", "/eval", nil)
	w := httptest.NewRecorder()
	h.Index(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "灰度评估")
	require.Contains(t, body, "0-20")    // bucket label rendered
	require.Contains(t, body, "80-100")  // last bucket
	require.Contains(t, body, "Spearman") // summary line
}

func TestEvalHandler_Index_IllegalSinceFallsBack(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)

	req := httptest.NewRequest("GET", "/eval?since=30d", nil)
	w := httptest.NewRecorder()
	h.Index(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, `value="3d" selected`)
}

func TestEvalHandler_Index_KnownSinceRetained(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)

	req := httptest.NewRequest("GET", "/eval?since=24h", nil)
	w := httptest.NewRecorder()
	h.Index(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `value="24h" selected`)
}

func TestEvalHandler_ReplayList_EmptyState(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)

	req := httptest.NewRequest("GET", "/eval/replays", nil)
	w := httptest.NewRecorder()
	h.ReplayList(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "尚无 replay 记录")
}

func TestEvalHandler_ReplayList_RendersRows(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)
	store := eval.NewStore(pool)

	for i := 0; i < 3; i++ {
		_, err := store.CreateRun(context.Background(), eval.ReplayRun{
			SinceWindow:  "7d",
			SinceCutoff:  time.Now().Unix(),
			Model:        "claude-sonnet-4-6",
			PromptText:   "p",
			PromptSHA256: "abcd1234ef567890",
			Status:       "done",
		})
		require.NoError(t, err)
	}

	req := httptest.NewRequest("GET", "/eval/replays", nil)
	w := httptest.NewRecorder()
	h.ReplayList(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "abcd1234") // sha8 prefix
	require.Contains(t, body, "claude-sonnet-4-6")
	require.Contains(t, body, "#1")
}
