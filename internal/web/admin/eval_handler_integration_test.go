//go:build integration

package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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

func TestEvalHandler_ReplayDetail_NotFound(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)

	r := chi.NewRouter()
	r.Get("/eval/replays/{id}", h.ReplayDetail)

	req := httptest.NewRequest("GET", "/eval/replays/9999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestEvalHandler_ReplayDetail_RendersSummary(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)
	r := chi.NewRouter()
	r.Get("/eval/replays/{id}", h.ReplayDetail)

	store := eval.NewStore(pool)
	id, _ := store.CreateRun(context.Background(), eval.ReplayRun{
		SinceWindow:  "7d",
		SinceCutoff:  time.Now().Unix(),
		Model:        "m",
		PromptText:   "p",
		PromptSHA256: "sha",
		Status:       "running",
	})
	rep := eval.ReplayReport{SampleSize: 42, WithPnL: 30, V1Spearman: 0.3, V2Spearman: 0.5,
		V1Buckets: []eval.Bucket{{Label: "0-20"}, {Label: "20-40"}, {Label: "40-60"}, {Label: "60-80"}, {Label: "80-100"}},
		V2Buckets: []eval.Bucket{{Label: "0-20"}, {Label: "20-40"}, {Label: "40-60"}, {Label: "60-80"}, {Label: "80-100"}}}
	require.NoError(t, store.MarkRunDone(context.Background(), id, &rep, 42, 0))

	req := httptest.NewRequest("GET", fmt.Sprintf("/eval/replays/%d", id), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "0.5000") // V2Spearman
	require.Contains(t, body, "42")     // SampleSize
}

func TestEvalHandler_ReplayDetail_EscapesPrompt(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)
	r := chi.NewRouter()
	r.Get("/eval/replays/{id}", h.ReplayDetail)

	store := eval.NewStore(pool)
	id, _ := store.CreateRun(context.Background(), eval.ReplayRun{
		SinceWindow:  "7d",
		SinceCutoff:  time.Now().Unix(),
		Model:        "m",
		PromptText:   `<script>alert("xss")</script>`,
		PromptSHA256: "sha",
		Status:       "done",
	})
	require.NoError(t, store.MarkRunDone(context.Background(), id, &eval.ReplayReport{
		V1Buckets: []eval.Bucket{{Label: "0-20"}, {Label: "20-40"}, {Label: "40-60"}, {Label: "60-80"}, {Label: "80-100"}},
		V2Buckets: []eval.Bucket{{Label: "0-20"}, {Label: "20-40"}, {Label: "40-60"}, {Label: "60-80"}, {Label: "80-100"}},
	}, 0, 0))

	req := httptest.NewRequest("GET", fmt.Sprintf("/eval/replays/%d", id), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.NotContains(t, w.Body.String(), `<script>alert("xss")</script>`)
	require.Contains(t, w.Body.String(), `&lt;script&gt;`)
}

func TestEvalHandler_RowsPartial_ReturnsHTMLFragment(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)
	r := chi.NewRouter()
	r.Get("/eval/replays/{id}/rows", h.ReplayRowsPartial)

	store := eval.NewStore(pool)
	runID, _ := store.CreateRun(context.Background(), eval.ReplayRun{
		SinceWindow:  "7d",
		SinceCutoff:  time.Now().Unix(),
		Model:        "m",
		PromptText:   "p",
		PromptSHA256: "sha",
		Status:       "running",
	})
	var sigID int64
	err := pool.QueryRow(context.Background(), `
INSERT INTO signals (strategy_id, symbol, kind, signal_price, tv_timestamp_ms,
                     raw_payload, decision, trace_id)
VALUES ('s', 'BTCUSDT', 'long', 50000, $1, '{}'::jsonb, 'accepted', 'tx')
RETURNING id`, time.Now().UnixMilli()).Scan(&sigID)
	require.NoError(t, err)

	pnl := 5.5
	require.NoError(t, store.InsertRow(context.Background(), runID, eval.ReplayRow{
		SignalID: sigID, NewScore: 80, OldScore: 30, PnLUSDC: &pnl,
		NewDecision: "approve", OldDecision: "abandon",
	}))

	req := httptest.NewRequest("GET",
		fmt.Sprintf("/eval/replays/%d/rows", runID), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	body := w.Body.String()
	require.Contains(t, body, "BTCUSDT")
	require.Contains(t, body, "50") // Δ = |80-30|
	require.Contains(t, body, "5.50") // PnL
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

func TestEvalHandler_ReplayDetail_PendingShowsWaitingCard(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)
	r := chi.NewRouter()
	r.Get("/eval/replays/{id}", h.ReplayDetail)

	store := eval.NewStore(pool)
	id, _ := store.CreateRun(context.Background(), eval.ReplayRun{
		SinceWindow: "1h", SinceCutoff: time.Now().Unix(),
		Model: "claude-sonnet-4-6", PromptText: "p", PromptSHA256: "h",
		Status: "pending",
	})

	req := httptest.NewRequest("GET", fmt.Sprintf("/eval/replays/%d", id), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "等待执行")
	// HTMX polling attribute present on pending state.
	require.Contains(t, body, `hx-trigger="every 2s"`)
}

func TestEvalHandler_ReplayDetail_RunningShowsProgressBar(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)
	r := chi.NewRouter()
	r.Get("/eval/replays/{id}", h.ReplayDetail)

	store := eval.NewStore(pool)
	id, _ := store.CreateRun(context.Background(), eval.ReplayRun{
		SinceWindow: "1h", SinceCutoff: time.Now().Unix(),
		Model: "claude-sonnet-4-6", PromptText: "p", PromptSHA256: "h",
		Status: "running",
	})
	require.NoError(t, store.MarkRunRunning(context.Background(), id, 100))
	require.NoError(t, store.UpdateProgress(context.Background(), id, 42, 1))

	req := httptest.NewRequest("GET", fmt.Sprintf("/eval/replays/%d", id), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "42/100")
	require.Contains(t, body, `hx-trigger="every 2s"`)
}

func TestEvalHandler_ReplayDetail_AbortedShowsNotice(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)
	r := chi.NewRouter()
	r.Get("/eval/replays/{id}", h.ReplayDetail)

	store := eval.NewStore(pool)
	id, _ := store.CreateRun(context.Background(), eval.ReplayRun{
		SinceWindow: "1h", SinceCutoff: time.Now().Unix(),
		Model: "claude-sonnet-4-6", PromptText: "p", PromptSHA256: "h",
		Status: "aborted",
	})

	req := httptest.NewRequest("GET", fmt.Sprintf("/eval/replays/%d", id), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, "进程重启时被中止")
	require.NotContains(t, body, `hx-trigger="every 2s"`,
		"aborted state must NOT poll")
}

func TestEvalHandler_ReplayList_HasNewButton(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)

	req := httptest.NewRequest("GET", "/eval/replays", nil)
	w := httptest.NewRecorder()
	h.ReplayList(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	require.Contains(t, body, `href="/eval/replays/new"`)
	require.Contains(t, body, "新建 Replay")
}

func TestEvalHandler_ReplayDetail_DoneStopsPolling(t *testing.T) {
	pool := newEvalTestPool(t)
	renderer, _ := NewRenderer()
	h := NewEvalHandler(renderer, pool)
	r := chi.NewRouter()
	r.Get("/eval/replays/{id}", h.ReplayDetail)

	store := eval.NewStore(pool)
	id, _ := store.CreateRun(context.Background(), eval.ReplayRun{
		SinceWindow: "1h", SinceCutoff: time.Now().Unix(),
		Model: "claude-sonnet-4-6", PromptText: "p", PromptSHA256: "h",
		Status: "running",
	})
	rep := eval.ReplayReport{SampleSize: 1}
	require.NoError(t, store.MarkRunDone(context.Background(), id, &rep, 1, 0))

	req := httptest.NewRequest("GET", fmt.Sprintf("/eval/replays/%d", id), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	body := w.Body.String()
	require.NotContains(t, body, `hx-trigger="every 2s"`)
}
