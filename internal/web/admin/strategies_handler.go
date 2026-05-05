package admin

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/store"
)

type StrategiesHandler struct {
	render        *Renderer
	repo          *store.StrategyRepo
	pool          *pgxpool.Pool
	statusHandler *StatusHandler
}

func NewStrategiesHandler(r *Renderer, repo *store.StrategyRepo, pool *pgxpool.Pool, sh *StatusHandler) *StrategiesHandler {
	return &StrategiesHandler{render: r, repo: repo, pool: pool, statusHandler: sh}
}

func (h *StrategiesHandler) Index(w http.ResponseWriter, r *http.Request) {
	rows, err := h.repo.List(r.Context(), h.pool)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := h.statusHandler.WithStatus(r, map[string]any{
		"Strategies": rows,
	})
	h.render.Render(w, http.StatusOK, "strategies/index", data)
}

func (h *StrategiesHandler) New(w http.ResponseWriter, r *http.Request) {
	data := h.statusHandler.WithStatus(r, map[string]any{
		"Strategy": &store.StrategyRow{Enabled: true, Leverage: 5},
		"IsNew":    true,
	})
	h.render.Render(w, http.StatusOK, "strategies/edit", data)
}

func (h *StrategiesHandler) Edit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := h.repo.Get(r.Context(), h.pool, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	data := h.statusHandler.WithStatus(r, map[string]any{
		"Strategy": row,
		"IsNew":    false,
	})
	h.render.Render(w, http.StatusOK, "strategies/edit", data)
}

func (h *StrategiesHandler) Save(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	row, err := parseStrategyForm(r)
	if err != nil {
		data := h.statusHandler.WithStatus(r, map[string]any{
			"Strategy": row, "IsNew": chi.URLParam(r, "id") == "", "Error": err.Error(),
		})
		h.render.Render(w, http.StatusBadRequest, "strategies/edit", data)
		return
	}
	if chi.URLParam(r, "id") == "" {
		err = h.repo.Create(r.Context(), h.pool, *row)
	} else {
		row.ID = chi.URLParam(r, "id")
		err = h.repo.Update(r.Context(), h.pool, *row)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/strategies", http.StatusSeeOther)
}

func (h *StrategiesHandler) Toggle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	row, err := h.repo.Get(r.Context(), h.pool, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	row.Enabled = !row.Enabled
	if err := h.repo.Update(r.Context(), h.pool, *row); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// HTMX response: render just the strategy_row partial for this single row
	if err := h.render.RenderPartial(w, "strategy_row", row); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StrategiesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.repo.Delete(r.Context(), h.pool, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/strategies", http.StatusSeeOther)
}

func parseStrategyForm(r *http.Request) (*store.StrategyRow, error) {
	var row store.StrategyRow
	row.ID = r.FormValue("id")
	row.Symbol = r.FormValue("symbol")
	leverage, err := strconv.Atoi(r.FormValue("leverage"))
	if err != nil {
		return &row, err
	}
	row.Leverage = leverage
	row.SizeUSDC, err = decimal.NewFromString(r.FormValue("size_usdc"))
	if err != nil {
		return &row, err
	}
	row.StopLossPct, err = decimal.NewFromString(r.FormValue("stop_loss_pct"))
	if err != nil {
		return &row, err
	}
	if v := r.FormValue("take_profit_pct"); v != "" {
		row.TakeProfitPct, err = decimal.NewFromString(v)
		if err != nil {
			return &row, err
		}
	}
	row.MaxOpenUSDC, err = decimal.NewFromString(r.FormValue("max_open_usdc"))
	if err != nil {
		return &row, err
	}
	row.Enabled = r.FormValue("enabled") == "on"
	return &row, nil
}
