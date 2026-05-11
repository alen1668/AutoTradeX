//go:build integration

package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
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
