package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/eval"
)

// EvalHandler renders /eval/* pages. All pages are read-only in Phase 1.
type EvalHandler struct {
	render  *Renderer
	pool    *pgxpool.Pool
	store   *eval.Store
	broker  *eval.Broker   // nil-safe; when nil, /eval/stream returns 503
	statusH *StatusHandler // injected later via WithStatus; nil-safe for tests
}

func NewEvalHandler(r *Renderer, pool *pgxpool.Pool) *EvalHandler {
	var store *eval.Store
	if pool != nil {
		store = eval.NewStore(pool)
	}
	return &EvalHandler{render: r, pool: pool, store: store}
}

// WithStatus injects the global status handler so the layout can render the
// status bar. Called by cmd/tvbot/main.go after both are constructed.
func (h *EvalHandler) WithStatus(s *StatusHandler) *EvalHandler {
	h.statusH = s
	return h
}

// WithBroker wires the Phase 3 SSE broker. Required before /eval/stream
// can serve real-time events.
func (h *EvalHandler) WithBroker(b *eval.Broker) *EvalHandler {
	h.broker = b
	return h
}

// Stream is the SSE endpoint that pushes EvalEvents to the browser as
// they happen. Each connection becomes one Broker subscriber for its
// lifetime. Returns 503 when no broker is wired (tests / pre-Phase-3
// startup).
func (h *EvalHandler) Stream(w http.ResponseWriter, r *http.Request) {
	if h.broker == nil {
		http.Error(w, "broker not configured", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	id, ch := h.broker.Subscribe()
	defer h.broker.Unsubscribe(id)

	fmt.Fprintf(w, "event: ready\ndata: {\"id\":%d}\n\n", id)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case evt, open := <-ch:
			if !open {
				return // broker dropped us as slow client
			}
			raw, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", raw)
			flusher.Flush()
		}
	}
}

// Index handles GET /eval. Renders the grayscale-period score-bucket × PnL
// report. URL params: ?since=1h|24h|3d|7d (default 3d; anything else
// silently falls back to 3d).
func (h *EvalHandler) Index(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r)
	defer cancel()

	since := r.URL.Query().Get("since")
	if since == "" {
		since = eval.DefaultSince
	}
	report, err := eval.LoadEvalReport(ctx, h.pool, since)
	if err != nil {
		http.Error(w, "load: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	// Phase 3: 24h initial snapshot injected as window.EVAL_INIT so the
	// front-end charts have content before the first SSE event lands.
	init, ierr := eval.LoadInitSnapshot(ctx, h.pool)
	if ierr != nil {
		init = eval.InitData{} // degrade gracefully — charts start empty
	}
	initJSON, jerr := json.Marshal(init)
	if jerr != nil {
		initJSON = []byte("null")
	}

	data := map[string]any{
		"Report":    report,
		"Since":     report.Since, // post-fallback canonical value
		"SinceOpts": eval.AllowedSinces,
		"InitJSON":  template.JS(initJSON), // marked safe; we control the content
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/index", data)
}

// ReplayList handles GET /eval/replays. Returns 20 most recent runs.
// Cursor-based pagination: ?cursor=<id> returns rows with id < cursor.
func (h *EvalHandler) ReplayList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r)
	defer cancel()

	var cursor int64
	if c := r.URL.Query().Get("cursor"); c != "" {
		_, _ = fmt.Sscanf(c, "%d", &cursor)
	}
	runs, next, err := h.store.ListRuns(ctx, cursor, 20)
	if err != nil {
		http.Error(w, "list: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	data := map[string]any{
		"Runs":   runs,
		"Next":   next,
		"Cursor": cursor,
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/replays_list", data)
}

// ReplayDetail handles GET /eval/replays/{id}.
// Renders the run summary (buckets / flip matrix / prompt) and triggers
// a lazy HTMX fetch for the per-signal row detail.
func (h *EvalHandler) ReplayDetail(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r)
	defer cancel()

	id := parseInt64ID(r)
	if id <= 0 {
		http.NotFound(w, r)
		return
	}
	run, err := h.store.GetRun(ctx, id)
	if err != nil {
		http.Error(w, "load: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	if run == nil {
		http.NotFound(w, r)
		return
	}
	data := map[string]any{"Run": run}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/replays_detail", data)
}

// evalRowView is the per-signal row DTO rendered into the lazy HTMX partial.
// Distinct from eval.ReplayRow so we can join the signals table inline
// without overloading eval.ReplayRow.Kind.
type evalRowView struct {
	SignalID    int64
	Symbol      string
	CreatedAt   int64 // unix seconds; rendered via unix2human
	ProdScore   int
	ReplayScore int
	Delta       int     // ABS(ReplayScore - ProdScore)
	PnL         float64 // 0 when HasPnL=false
	HasPnL      bool
	Error       string
}

// ReplayRowsPartial handles GET /eval/replays/{id}/rows (HTMX lazy fragment).
// Returns up to 200 rows ordered by |Δscore| DESC, hydrated with signal
// symbol + created_at via a single JOIN against signals.
func (h *EvalHandler) ReplayRowsPartial(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r)
	defer cancel()

	id := parseInt64ID(r)
	if id <= 0 {
		http.NotFound(w, r)
		return
	}
	rows, err := h.store.ListRows(ctx, id, 200)
	if err != nil {
		http.Error(w, "load: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	views := make([]evalRowView, len(rows))
	for i, rr := range rows {
		delta := rr.NewScore - rr.OldScore
		if delta < 0 {
			delta = -delta
		}
		v := evalRowView{
			SignalID:    rr.SignalID,
			ProdScore:   rr.OldScore,
			ReplayScore: rr.NewScore,
			Delta:       delta,
			HasPnL:      rr.HasPnL,
			Error:       rr.Error,
		}
		if rr.PnLUSDC != nil {
			v.PnL = *rr.PnLUSDC
		}
		views[i] = v
	}

	// Hydrate symbol + created_at in one query.
	if len(views) > 0 {
		ids := make([]int64, len(views))
		for i, v := range views {
			ids[i] = v.SignalID
		}
		meta := map[int64]struct {
			Symbol string
			Unix   int64
		}{}
		qrows, qerr := h.pool.Query(ctx, `
SELECT id, symbol, extract(epoch from received_at)::bigint
FROM signals WHERE id = ANY($1)`, ids)
		if qerr == nil {
			defer qrows.Close()
			for qrows.Next() {
				var sid int64
				var sym string
				var ts int64
				if scanErr := qrows.Scan(&sid, &sym, &ts); scanErr == nil {
					meta[sid] = struct {
						Symbol string
						Unix   int64
					}{sym, ts}
				}
			}
		}
		for i, v := range views {
			if m, ok := meta[v.SignalID]; ok {
				views[i].Symbol = m.Symbol
				views[i].CreatedAt = m.Unix
			}
		}
	}

	if err := h.render.RenderPartial(w, "eval_replay_rows", map[string]any{
		"Rows":  views,
		"Total": len(views),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// withTimeout shortens r.Context() to 5s; used by all eval queries.
func withTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 5*time.Second)
}

// parseInt64ID parses chi URL param "id" as int64. Returns 0 on failure.
func parseInt64ID(r *http.Request) int64 {
	var id int64
	_, _ = fmt.Sscanf(chi.URLParam(r, "id"), "%d", &id)
	return id
}
