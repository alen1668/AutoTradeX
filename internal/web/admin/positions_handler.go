package admin

import (
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lizhaojie/tvbot/internal/store"
)

type PositionsHandler struct {
	render       *Renderer
	pool         *pgxpool.Pool
	posRepo      *store.VirtualPositionRepo
	strategyRepo *store.StrategyRepo
	historyRepo  *store.PositionHistoryRepo
}

func NewPositionsHandler(r *Renderer, pool *pgxpool.Pool,
	posRepo *store.VirtualPositionRepo, strategyRepo *store.StrategyRepo,
	historyRepo *store.PositionHistoryRepo) *PositionsHandler {
	return &PositionsHandler{render: r, pool: pool, posRepo: posRepo,
		strategyRepo: strategyRepo, historyRepo: historyRepo}
}

func (h *PositionsHandler) Index(w http.ResponseWriter, r *http.Request) {
	strategies, err := h.strategyRepo.List(r.Context(), h.pool)
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
	// Recent history across all strategies (limit 50 per strategy)
	var allHistory []*store.PositionHistoryRow
	for _, s := range strategies {
		hist, err := h.historyRepo.ListByStrategy(r.Context(), h.pool, s.ID, 50)
		if err == nil {
			allHistory = append(allHistory, hist...)
		}
	}
	h.render.Render(w, http.StatusOK, "positions/index", map[string]any{
		"Active":  active,
		"History": allHistory,
	})
}
