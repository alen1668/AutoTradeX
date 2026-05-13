package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/store"
)

// ExitHandler renders /eval/exit list + detail.
type ExitHandler struct {
	render  *Renderer
	repo    *store.ExitDecisionRepo
	statusH *StatusHandler
}

func NewExitHandler(r *Renderer, repo *store.ExitDecisionRepo) *ExitHandler {
	return &ExitHandler{render: r, repo: repo}
}

func (h *ExitHandler) WithStatus(s *StatusHandler) *ExitHandler {
	h.statusH = s
	return h
}

type exitListRow struct {
	ID                 int64
	CreatedAtUnix      int64
	StrategyID         string
	Symbol             string
	Side               string
	Action             string
	Confidence         string
	UnrealizedPnLPct   string
	Mode               string
	ExecutionStatus    string
	IfHoldLabel        string
	IfHoldPnLPct       string
}

type exitStats struct {
	Total      int
	HoldN      int
	TightenN   int
	PartialN   int
	ExitNowN   int
	ImprovedN  int
	WorsenedN  int
	UnchangedN int
}

// List handles GET /eval/exit?mode=&action=&limit=
func (h *ExitHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	rows, err := h.repo.List(ctx, store.ExitDecisionListFilter{
		Mode:   q.Get("mode"),
		Action: q.Get("action"),
		Limit:  limit,
	})
	if err != nil {
		http.Error(w, "list: "+err.Error(), http.StatusServiceUnavailable)
		return
	}

	out := make([]exitListRow, 0, len(rows))
	stats := exitStats{Total: len(rows)}
	for _, x := range rows {
		stats.bumpAction(x.Action)
		stats.bumpLabel(x.IfHoldLabel)
		view := exitListRow{
			ID:               x.ID,
			CreatedAtUnix:    x.CreatedAt.Unix(),
			StrategyID:       x.StrategyID,
			Symbol:           x.Symbol,
			Side:             x.Side,
			Action:           x.Action,
			Confidence:       x.Confidence,
			UnrealizedPnLPct: x.UnrealizedPnLPct.StringFixed(2),
			Mode:             x.Mode,
		}
		if x.ExecutionStatus != nil {
			view.ExecutionStatus = *x.ExecutionStatus
		}
		if x.IfHoldLabel != nil {
			view.IfHoldLabel = *x.IfHoldLabel
		}
		if x.IfHoldPnLPct != nil {
			view.IfHoldPnLPct = x.IfHoldPnLPct.Mul(decimal.NewFromInt(100)).StringFixed(2)
		}
		out = append(out, view)
	}

	data := map[string]any{
		"Title":       "Exit Agent",
		"Decisions":   out,
		"Stats":       stats,
		"FilterMode":  q.Get("mode"),
		"FilterAction": q.Get("action"),
		"HasRows":     len(out) > 0,
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/exit_list", data)
}

func (s *exitStats) bumpAction(a string) {
	switch a {
	case "hold":
		s.HoldN++
	case "tighten_sl":
		s.TightenN++
	case "take_partial":
		s.PartialN++
	case "exit_now":
		s.ExitNowN++
	}
}

func (s *exitStats) bumpLabel(l *string) {
	if l == nil {
		return
	}
	switch *l {
	case "improved":
		s.ImprovedN++
	case "worsened":
		s.WorsenedN++
	case "unchanged":
		s.UnchangedN++
	}
}

type exitDetailView struct {
	ID                int64
	CreatedAtUnix     int64
	StrategyID        string
	Symbol            string
	Side              string
	EntryFillPrice    string
	CurrentPrice      string
	Qty               string
	UnrealizedPnLUSD  string
	UnrealizedPnLPct  string
	PositionAgeMin    int
	CurrentSLPrice    string
	CurrentTPPrice    string
	Action            string
	Confidence        string
	Reasoning         string
	ProposedSLPrice   string
	PartialPct        string
	Model             string
	PromptHash        string
	LatencyMs         int
	TokenIn           int
	TokenOut          int
	Mode              string
	ExecutedAtUnix    int64
	ExecutionStatus   string
	ExecutionError    string
	OutcomeHorizonMin int
	IfHoldPnLPct      string
	IfHoldLabel       string
	OutcomeComputedAtUnix int64
}

// Detail handles GET /eval/exit/{id}
func (h *ExitHandler) Detail(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	row, err := h.repo.GetByID(ctx, id)
	if err != nil || row == nil {
		http.NotFound(w, r)
		return
	}

	v := exitDetailView{
		ID:               row.ID,
		CreatedAtUnix:    row.CreatedAt.Unix(),
		StrategyID:       row.StrategyID,
		Symbol:           row.Symbol,
		Side:             row.Side,
		EntryFillPrice:   row.EntryFillPrice.StringFixed(4),
		CurrentPrice:     row.CurrentPrice.StringFixed(4),
		Qty:              row.Qty.StringFixed(6),
		UnrealizedPnLUSD: row.UnrealizedPnLUSD.StringFixed(2),
		UnrealizedPnLPct: row.UnrealizedPnLPct.StringFixed(2),
		PositionAgeMin:   row.PositionAgeSec / 60,
		Action:           row.Action,
		Confidence:       row.Confidence,
		Reasoning:        row.Reasoning,
		Model:            row.Model,
		PromptHash:       row.PromptHash,
		Mode:             row.Mode,
	}
	if row.CurrentSLPrice != nil {
		v.CurrentSLPrice = row.CurrentSLPrice.StringFixed(4)
	}
	if row.CurrentTPPrice != nil {
		v.CurrentTPPrice = row.CurrentTPPrice.StringFixed(4)
	}
	if row.ProposedSLPrice != nil {
		v.ProposedSLPrice = row.ProposedSLPrice.StringFixed(4)
	}
	if row.PartialPct != nil {
		v.PartialPct = row.PartialPct.StringFixed(2)
	}
	if row.LatencyMs != nil {
		v.LatencyMs = *row.LatencyMs
	}
	if row.TokenIn != nil {
		v.TokenIn = *row.TokenIn
	}
	if row.TokenOut != nil {
		v.TokenOut = *row.TokenOut
	}
	if row.ExecutedAt != nil {
		v.ExecutedAtUnix = row.ExecutedAt.Unix()
	}
	if row.ExecutionStatus != nil {
		v.ExecutionStatus = *row.ExecutionStatus
	}
	if row.ExecutionError != nil {
		v.ExecutionError = *row.ExecutionError
	}
	if row.OutcomeHorizonMin != nil {
		v.OutcomeHorizonMin = *row.OutcomeHorizonMin
	}
	if row.IfHoldPnLPct != nil {
		v.IfHoldPnLPct = row.IfHoldPnLPct.Mul(decimal.NewFromInt(100)).StringFixed(2)
	}
	if row.IfHoldLabel != nil {
		v.IfHoldLabel = *row.IfHoldLabel
	}
	if row.OutcomeComputedAt != nil {
		v.OutcomeComputedAtUnix = row.OutcomeComputedAt.Unix()
	}

	data := map[string]any{
		"Title": "Exit 决策详情",
		"V":     v,
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/exit_detail", data)
}
