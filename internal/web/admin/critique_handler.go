package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/lizhaojie/tvbot/internal/store"
)

// CritiqueHandler renders /eval/critique* pages and serves the pin /
// manual-run APIs.
type CritiqueHandler struct {
	render   *Renderer
	repo     *store.CritiqueRepo
	manualCh chan<- struct{}
	statusH  *StatusHandler
}

func NewCritiqueHandler(r *Renderer, repo *store.CritiqueRepo, manualCh chan<- struct{}) *CritiqueHandler {
	return &CritiqueHandler{render: r, repo: repo, manualCh: manualCh}
}

func (h *CritiqueHandler) WithStatus(s *StatusHandler) *CritiqueHandler {
	h.statusH = s
	return h
}

// critiqueListRow projects store.CritiqueRow into a template-friendly shape.
type critiqueListRow struct {
	ID             int64
	CreatedAtUnix  int64
	WindowStartFmt string
	WindowEndFmt   string
	SampleSize     int
	Status         string
	Summary        string
	ErrorMessage   string
}

// List handles GET /eval/critique.
func (h *CritiqueHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	rows, err := h.repo.List(ctx, 50)
	if err != nil {
		http.Error(w, "list: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	out := make([]critiqueListRow, 0, len(rows))
	for _, c := range rows {
		row := critiqueListRow{
			ID:             c.ID,
			CreatedAtUnix:  c.CreatedAt.Unix(),
			WindowStartFmt: c.WindowStart.UTC().Format("01-02"),
			WindowEndFmt:   c.WindowEnd.UTC().Format("01-02"),
			SampleSize:     c.SampleSize,
			Status:         c.Status,
		}
		if c.Summary != nil {
			row.Summary = *c.Summary
		}
		if c.ErrorMessage != nil {
			row.ErrorMessage = *c.ErrorMessage
		}
		out = append(out, row)
	}
	data := map[string]any{
		"Title":     "Agent 反思",
		"Critiques": out,
		"HasRows":   len(out) > 0,
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/critique_list", data)
}

type critiquePatternView struct {
	ID         int64
	PatternKey string
	Title      string
	Suggestion string
	Confidence string
	Pinned     bool
	PinnedAt   int64
	PinnedBy   string
}

// Detail handles GET /eval/critique/{id}.
func (h *CritiqueHandler) Detail(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	patterns, err := h.repo.PatternsByCritique(ctx, id)
	if err != nil {
		http.Error(w, "patterns: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	views := make([]critiquePatternView, 0, len(patterns))
	for _, p := range patterns {
		v := critiquePatternView{
			ID:         p.ID,
			PatternKey: p.PatternKey,
			Title:      p.Title,
			Suggestion: p.Suggestion,
			Confidence: p.Confidence,
			Pinned:     p.Pinned,
		}
		if p.PinnedAt != nil {
			v.PinnedAt = p.PinnedAt.Unix()
		}
		if p.PinnedBy != nil {
			v.PinnedBy = *p.PinnedBy
		}
		views = append(views, v)
	}
	data := map[string]any{
		"Title":      "反思详情",
		"CritiqueID": id,
		"Patterns":   views,
		"HasRows":    len(views) > 0,
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/critique_detail", data)
}

// Run handles POST /eval/critique/run. Enqueues a manual trigger; the
// worker may drop it via the 5min idempotency window.
func (h *CritiqueHandler) Run(w http.ResponseWriter, r *http.Request) {
	select {
	case h.manualCh <- struct{}{}:
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "queued"})
	default:
		http.Error(w, "queue full, try later", http.StatusServiceUnavailable)
	}
}

// SetPin handles POST /eval/critique/patterns/{id}/pin with JSON body
// {"pinned": true|false}.
func (h *CritiqueHandler) SetPin(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	var body struct {
		Pinned bool `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body: "+err.Error(), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := h.repo.SetPinned(ctx, id, body.Pinned, "manual"); err != nil {
		http.Error(w, "pin: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}
