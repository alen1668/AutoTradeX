package admin

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

const historyPageSize = 20

type PositionsHandler struct {
	render        *Renderer
	pool          *pgxpool.Pool
	posRepo       *store.VirtualPositionRepo
	strategyRepo  *store.StrategyRepo
	historyRepo   *store.PositionHistoryRepo
	statusHandler *StatusHandler
}

func NewPositionsHandler(r *Renderer, pool *pgxpool.Pool,
	posRepo *store.VirtualPositionRepo, strategyRepo *store.StrategyRepo,
	historyRepo *store.PositionHistoryRepo, sh *StatusHandler) *PositionsHandler {
	return &PositionsHandler{render: r, pool: pool, posRepo: posRepo,
		strategyRepo: strategyRepo, historyRepo: historyRepo, statusHandler: sh}
}

func (h *PositionsHandler) Index(w http.ResponseWriter, r *http.Request) {
	strategies, err := h.strategyRepo.List(r.Context(), h.pool, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var active []*store.VirtualPositionRow
	for _, s := range strategies {
		vp, err := h.posRepo.GetActiveByStrategy(r.Context(), h.pool, s.ID)
		if err == nil {
			active = append(active, vp)
		} else if err != pgx.ErrNoRows {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	// Recent history across all strategies, paginated.
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * historyPageSize
	history, err := h.historyRepo.ListAll(r.Context(), h.pool, historyPageSize, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	total, err := h.historyRepo.CountAll(r.Context(), h.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	totalPages := (total + historyPageSize - 1) / historyPageSize
	if totalPages < 1 {
		totalPages = 1
	}
	data := h.statusHandler.WithStatus(r, map[string]any{
		"Active":     active,
		"History":    history,
		"Page":       page,
		"TotalPages": totalPages,
		"Total":      total,
		"HasPrev":    page > 1,
		"HasNext":    page < totalPages,
	})
	h.render.Render(w, http.StatusOK, "positions/index", data)
}
