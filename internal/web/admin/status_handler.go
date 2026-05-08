package admin

import (
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/config"
	"github.com/lizhaojie/tvbot/internal/store"
)

type StatusHandler struct {
	render       *Renderer
	pool         *pgxpool.Pool
	systemRepo   *store.SystemStateRepo
	settingsRepo *store.SettingsRepo
	strategyRepo *store.StrategyRepo
	posRepo      *store.VirtualPositionRepo
	mode         config.BotMode
}

func NewStatusHandler(r *Renderer, pool *pgxpool.Pool,
	systemRepo *store.SystemStateRepo, settingsRepo *store.SettingsRepo,
	strategyRepo *store.StrategyRepo,
	posRepo *store.VirtualPositionRepo, mode config.BotMode) *StatusHandler {
	return &StatusHandler{render: r, pool: pool, systemRepo: systemRepo,
		settingsRepo: settingsRepo,
		strategyRepo: strategyRepo, posRepo: posRepo, mode: mode}
}

// StatusData holds the data rendered into the status bar partial.
type StatusData struct {
	Mode             string
	Armed            bool
	BreakerTripped   bool
	DailyPnL         string
	DailyPnLNegative bool
	ActivePositions  int
	AgentEnabled     bool
}

// Build queries the DB and returns a populated StatusData.
func (h *StatusHandler) Build(r *http.Request) (StatusData, error) {
	s, err := h.systemRepo.Get(r.Context(), h.pool)
	if err != nil {
		return StatusData{}, err
	}
	strategies, err := h.strategyRepo.List(r.Context(), h.pool, false)
	if err != nil {
		return StatusData{}, err
	}
	var active int
	for _, st := range strategies {
		_, err := h.posRepo.GetActiveByStrategy(r.Context(), h.pool, st.ID)
		if err == nil {
			active++
		} else if err != pgx.ErrNoRows {
			return StatusData{}, err
		}
	}
	// Settings query failure must not break the status bar; fall back to false.
	var agentEnabled bool
	if h.settingsRepo != nil {
		if cfg, err := h.settingsRepo.Get(r.Context(), h.pool); err == nil {
			agentEnabled = cfg.AgentScorerEnabled
		}
	}
	return StatusData{
		Mode:             string(h.mode),
		Armed:            s.Armed,
		BreakerTripped:   s.BreakerTripped,
		DailyPnL:         s.DailyPnLUSDC.StringFixed(2),
		DailyPnLNegative: s.DailyPnLUSDC.LessThan(decimal.Zero),
		ActivePositions:  active,
		AgentEnabled:     agentEnabled,
	}, nil
}

// Partial responds with just the status bar partial (for HTMX hx-get).
func (h *StatusHandler) Partial(w http.ResponseWriter, r *http.Request) {
	data, err := h.Build(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.render.RenderPartial(w, "status_bar", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
