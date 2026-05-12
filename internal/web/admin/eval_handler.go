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
	"github.com/lizhaojie/tvbot/internal/store"
)

// EvalHandler renders /eval/* pages. All pages are read-only in Phase 1.
type EvalHandler struct {
	render   *Renderer
	pool     *pgxpool.Pool
	store    *eval.Store
	newsRepo *store.NewsSnapshotsRepo
	broker   *eval.Broker
	statusH  *StatusHandler
}

func NewEvalHandler(r *Renderer, pool *pgxpool.Pool) *EvalHandler {
	var evalStore *eval.Store
	var newsRepo *store.NewsSnapshotsRepo
	if pool != nil {
		evalStore = eval.NewStore(pool)
		newsRepo = store.NewNewsSnapshotsRepo(pool)
	}
	return &EvalHandler{render: r, pool: pool, store: evalStore, newsRepo: newsRepo}
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

// ABCompare handles GET /eval/ab/{id}. Renders a Phase 3 A/B scatter
// (Chart.js) using the replay_run_rows already persisted by Phase 2,
// alongside the flip matrix from the run's summary_json. When the run
// isn't done yet, shows a "运行尚未完成" hint linking back to detail.
func (h *EvalHandler) ABCompare(w http.ResponseWriter, r *http.Request) {
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

	var rows []eval.ReplayRow
	if run.Status == "done" {
		rows, err = h.store.ListRows(ctx, id, 2000)
		if err != nil {
			http.Error(w, "rows: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
	}

	rowsJSON, _ := json.Marshal(rows)
	data := map[string]any{
		"Run":      run,
		"Rows":     rows,
		"RowsJSON": template.JS(rowsJSON),
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/ab", data)
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
	// Set headers BEFORE the first Write so they make it into the
	// status line. http.NewResponseController walks the Unwrap() chain
	// (logger.statusRecorder, scs.sessionResponseWriter) to reach the
	// underlying Flusher.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	rc := http.NewResponseController(w)

	id, ch := h.broker.Subscribe()
	defer h.broker.Unsubscribe(id)

	fmt.Fprintf(w, "event: ready\ndata: {\"id\":%d}\n\n", id)
	if err := rc.Flush(); err != nil {
		// First flush failure → underlying writer can't stream.
		fmt.Fprintf(w, "event: error\ndata: {\"err\":\"flush_unsupported\"}\n\n")
		return
	}

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
			_ = rc.Flush()
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

// newsListRow is the per-row DTO for the /eval/news list page.
type newsListRow struct {
	ID             int64
	MeasuredAtUnix int64 // for the unix2human template helper
	Impact         string
	HeadlineCount  int
	Summary        string
	TokenIn        int
	TokenOut       int
	LatencyMs      int
	ErrorMessage   string
	HasError       bool
}

// NewsList handles GET /eval/news. Lists the most recent news_snapshots.
// Cursor pagination via ?cursor=<id> (rows with id < cursor).
func (h *EvalHandler) NewsList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r)
	defer cancel()

	var cursor int64
	if c := r.URL.Query().Get("cursor"); c != "" {
		_, _ = fmt.Sscanf(c, "%d", &cursor)
	}
	rows := []newsListRow{}
	var nextCursor int64
	if h.newsRepo != nil {
		recs, err := h.newsRepo.ListRecent(ctx, h.pool, 20, cursor)
		if err != nil {
			http.Error(w, "list: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		for _, rec := range recs {
			var phCount int
			if len(rec.PerHeadline) > 0 {
				var arr []any
				_ = json.Unmarshal(rec.PerHeadline, &arr)
				phCount = len(arr)
			}
			row := newsListRow{
				ID:             rec.ID,
				MeasuredAtUnix: rec.MeasuredAt.Unix(),
				Impact:         rec.Impact,
				HeadlineCount:  phCount,
				Summary:        rec.Summary,
			}
			if rec.LLMTokensIn != nil {
				row.TokenIn = *rec.LLMTokensIn
			}
			if rec.LLMTokensOut != nil {
				row.TokenOut = *rec.LLMTokensOut
			}
			if rec.LLMLatencyMs != nil {
				row.LatencyMs = *rec.LLMLatencyMs
			}
			if rec.ErrorMessage != nil {
				row.ErrorMessage = *rec.ErrorMessage
				row.HasError = true
			}
			rows = append(rows, row)
		}
		if len(recs) == 20 {
			nextCursor = recs[len(recs)-1].ID
		}
	}
	data := map[string]any{
		"Rows":       rows,
		"NextCursor": nextCursor,
		"HasRows":    len(rows) > 0,
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/news_list", data)
}

// newsHeadlineView is one headline + judgment block for the detail page.
type newsHeadlineView struct {
	Index  int
	Title  string
	URL    string
	Source string
	Impact string
	Reason string
}

// NewsDetail handles GET /eval/news/{id}. Renders the full audit trail for
// one news_snapshots row: per_headline grid, reasoning, full prompt + raw
// LLM response, raw cryptopanic JSON.
func (h *EvalHandler) NewsDetail(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := withTimeout(r)
	defer cancel()

	id := parseInt64ID(r)
	if id <= 0 {
		http.NotFound(w, r)
		return
	}
	if h.newsRepo == nil {
		http.Error(w, "news repo not wired", http.StatusServiceUnavailable)
		return
	}
	rec, err := h.newsRepo.Get(ctx, h.pool, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// per_headline JSON → []newsHeadlineView (best-effort; corrupt JSON ⇒ empty list).
	headlines := []newsHeadlineView{}
	if len(rec.PerHeadline) > 0 {
		var ph []map[string]any
		if json.Unmarshal(rec.PerHeadline, &ph) == nil {
			// raw_headlines holds the upstream "source" field; we join by index.
			var raws []map[string]any
			_ = json.Unmarshal(rec.RawHeadlines, &raws)
			for i, h := range ph {
				view := newsHeadlineView{Index: i}
				if t, ok := h["title"].(string); ok {
					view.Title = t
				}
				if u, ok := h["url"].(string); ok {
					view.URL = u
				}
				if im, ok := h["impact"].(string); ok {
					view.Impact = im
				}
				if r, ok := h["reason"].(string); ok {
					view.Reason = r
				}
				if i < len(raws) {
					if src, ok := raws[i]["source"].(map[string]any); ok {
						if title, ok := src["title"].(string); ok {
							view.Source = title
						}
					}
				}
				headlines = append(headlines, view)
			}
		}
	}

	// raw_headlines and per_headline pretty-printed for the detail toggles.
	prettyRaw := prettyJSON(rec.RawHeadlines)
	prettyPer := prettyJSON(rec.PerHeadline)

	data := map[string]any{
		"Rec":            rec,
		"MeasuredAtUnix": rec.MeasuredAt.Unix(),
		"Headlines":      headlines,
		"PrettyRaw":      prettyRaw,
		"PrettyPer":      prettyPer,
		"HasError":       rec.ErrorMessage != nil,
		"ErrorMessage":   safeStr(rec.ErrorMessage),
		"ResponseRaw":    safeStr(rec.ResponseRaw),
		"TokenIn":        safeInt(rec.LLMTokensIn),
		"TokenOut":       safeInt(rec.LLMTokensOut),
		"LatencyMs":      safeInt(rec.LLMLatencyMs),
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/news_detail", data)
}

func prettyJSON(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	var anyVal any
	if err := json.Unmarshal(b, &anyVal); err != nil {
		return string(b)
	}
	out, err := json.MarshalIndent(anyVal, "", "  ")
	if err != nil {
		return string(b)
	}
	return string(out)
}

func safeStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func safeInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
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
