package admin

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

const signalsPageSize = 20

type SignalsHandler struct {
	render        *Renderer
	pool          *pgxpool.Pool
	repo          *store.SignalRepo
	evalRepo      *store.AgentEvalRepo
	statusHandler *StatusHandler
}

func NewSignalsHandler(r *Renderer, pool *pgxpool.Pool, repo *store.SignalRepo, evalRepo *store.AgentEvalRepo, sh *StatusHandler) *SignalsHandler {
	return &SignalsHandler{render: r, pool: pool, repo: repo, evalRepo: evalRepo, statusHandler: sh}
}

// Detail renders the signal-detail page (id from path), including the
// agent evaluation card if one exists.
func (h *SignalsHandler) Detail(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	row, err := h.repo.GetByID(r.Context(), h.pool, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var eval *store.AgentEvaluation
	if h.evalRepo != nil {
		eval, _ = h.evalRepo.LatestForSignal(r.Context(), h.pool, id)
	}
	data := h.statusHandler.WithStatus(r, map[string]any{
		"Signal":     row,
		"Evaluation": eval,
	})
	h.render.Render(w, http.StatusOK, "signals/detail", data)
}

// signalsPageData carries everything the template needs to render filters,
// rows, and pagination links.
type signalsPageData struct {
	Filter     store.SignalFilter
	Strategies []string
	Symbols    []string
	Decisions  []string
	Page       int
	TotalPages int
	Total      int
	PrevQS     string // query string for prev-page link
	NextQS     string
}

func (h *SignalsHandler) Index(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := store.SignalFilter{
		Decision:   q.Get("decision"),
		StrategyID: q.Get("strategy"),
		Symbol:     q.Get("symbol"),
	}
	page := 1
	if v := q.Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	offset := (page - 1) * signalsPageSize
	rows, total, err := h.repo.ListPage(r.Context(), h.pool, filter, signalsPageSize, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	strategies, _ := h.repo.DistinctStrategies(r.Context(), h.pool)
	symbols, _ := h.repo.DistinctSymbols(r.Context(), h.pool)

	totalPages := (total + signalsPageSize - 1) / signalsPageSize
	if totalPages == 0 {
		totalPages = 1
	}

	page = clampPage(page, totalPages)

	data := h.statusHandler.WithStatus(r, map[string]any{
		"Signals": rows,
		"Page": signalsPageData{
			Filter:     filter,
			Strategies: strategies,
			Symbols:    symbols,
			Decisions:  []string{"accepted", "pending", "duplicate", "risk_denied", "disarmed", "invalid", "abandoned"},
			Page:       page,
			TotalPages: totalPages,
			Total:      total,
			PrevQS:     pageQS(filter, page-1),
			NextQS:     pageQS(filter, page+1),
		},
	})
	h.render.Render(w, http.StatusOK, "signals/index", data)
}

func clampPage(p, max int) int {
	if p < 1 {
		return 1
	}
	if p > max {
		return max
	}
	return p
}

func pageQS(f store.SignalFilter, page int) string {
	v := url.Values{}
	if f.Decision != "" {
		v.Set("decision", f.Decision)
	}
	if f.StrategyID != "" {
		v.Set("strategy", f.StrategyID)
	}
	if f.Symbol != "" {
		v.Set("symbol", f.Symbol)
	}
	v.Set("page", strconv.Itoa(page))
	return v.Encode()
}
