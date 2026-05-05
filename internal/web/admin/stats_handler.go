package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// DailyStat holds aggregated statistics for a single UTC day.
type DailyStat struct {
	Date      time.Time
	Trades    int
	WinTrades int
	PnLUSDC   decimal.Decimal
	WinRate   decimal.Decimal // 0..100
	IsCumNeg  bool
	CumPnL    decimal.Decimal
}

// Totals holds period-level aggregate figures.
type Totals struct {
	TotalTrades  int
	TotalWins    int
	TotalPnLUSDC decimal.Decimal
	TotalWinRate decimal.Decimal
	BestDay      decimal.Decimal
	WorstDay     decimal.Decimal
}

// StatsHandler renders the /stats page.
type StatsHandler struct {
	render  *Renderer
	pool    *pgxpool.Pool
	statusH *StatusHandler
}

// NewStatsHandler constructs a StatsHandler.
func NewStatsHandler(r *Renderer, pool *pgxpool.Pool, statusH *StatusHandler) *StatsHandler {
	return &StatsHandler{render: r, pool: pool, statusH: statusH}
}

// Index handles GET /stats.
func (h *StatsHandler) Index(w http.ResponseWriter, r *http.Request) {
	stats, totals, err := h.queryDaily(r.Context(), 30)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := h.statusH.WithStatus(r, map[string]any{
		"Stats":  stats,
		"Totals": totals,
	})
	h.render.Render(w, http.StatusOK, "stats/index", data)
}

// queryDaily returns per-day stats for the last `days` days, newest first,
// plus period-level totals. Rows are ordered oldest→newest in the DB query so
// cumulative PnL can be computed incrementally; the slice is then reversed
// before returning so the HTML table shows newest first.
func (h *StatsHandler) queryDaily(ctx context.Context, days int) ([]DailyStat, Totals, error) {
	rows, err := h.pool.Query(ctx, `
SELECT
  date_trunc('day', closed_at AT TIME ZONE 'UTC')::date AS day,
  count(*)::int                                          AS trades,
  count(*) FILTER (WHERE pnl_usdc > 0)::int              AS wins,
  COALESCE(sum(pnl_usdc), 0)                             AS pnl
FROM position_history
WHERE closed_at >= now() - make_interval(days => $1)
GROUP BY day
ORDER BY day ASC`, days)
	if err != nil {
		return nil, Totals{}, err
	}
	defer rows.Close()

	var raw []DailyStat
	for rows.Next() {
		var d DailyStat
		if err := rows.Scan(&d.Date, &d.Trades, &d.WinTrades, &d.PnLUSDC); err != nil {
			return nil, Totals{}, err
		}
		raw = append(raw, d)
	}
	if err := rows.Err(); err != nil {
		return nil, Totals{}, err
	}

	// Compute cumulative PnL and win rate (oldest→newest order).
	cum := decimal.Zero
	for i := range raw {
		cum = cum.Add(raw[i].PnLUSDC)
		raw[i].CumPnL = cum
		raw[i].IsCumNeg = cum.IsNegative()
		if raw[i].Trades > 0 {
			raw[i].WinRate = decimal.NewFromInt(int64(raw[i].WinTrades)).
				Div(decimal.NewFromInt(int64(raw[i].Trades))).
				Mul(decimal.NewFromInt(100))
		}
	}

	// Aggregate period-level totals.
	var totals Totals
	for _, d := range raw {
		totals.TotalTrades += d.Trades
		totals.TotalWins += d.WinTrades
		totals.TotalPnLUSDC = totals.TotalPnLUSDC.Add(d.PnLUSDC)
		if d.PnLUSDC.GreaterThan(totals.BestDay) {
			totals.BestDay = d.PnLUSDC
		}
		if d.PnLUSDC.LessThan(totals.WorstDay) {
			totals.WorstDay = d.PnLUSDC
		}
	}
	if totals.TotalTrades > 0 {
		totals.TotalWinRate = decimal.NewFromInt(int64(totals.TotalWins)).
			Div(decimal.NewFromInt(int64(totals.TotalTrades))).
			Mul(decimal.NewFromInt(100))
	}

	// Reverse for display (newest first in the table).
	out := make([]DailyStat, len(raw))
	for i := range raw {
		out[i] = raw[len(raw)-1-i]
	}
	return out, totals, nil
}
