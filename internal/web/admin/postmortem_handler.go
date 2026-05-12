package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AllowedPostmortemSinces is the canonical window set for /eval/postmortem.
// Narrower windows like "1h" are useless here — outcomes need ≥ horizon (60min)
// to settle. "all" uses unix epoch as the cutoff.
var AllowedPostmortemSinces = []string{"7d", "14d", "30d", "all"}

// DefaultPostmortemSince is used when ?since= is missing or unknown.
const DefaultPostmortemSince = "14d"

// parsePostmortemSince returns (cutoff time, canonical-since-string).
// Unknown values silently fall back to DefaultPostmortemSince.
func parsePostmortemSince(s string) (time.Time, string) {
	switch s {
	case "7d":
		return time.Now().UTC().AddDate(0, 0, -7), "7d"
	case "30d":
		return time.Now().UTC().AddDate(0, 0, -30), "30d"
	case "all":
		return time.Unix(0, 0).UTC(), "all"
	case "14d":
		fallthrough
	default:
		return time.Now().UTC().AddDate(0, 0, -14), "14d"
	}
}

// PostmortemHandler renders /eval/postmortem — the strategy × outcome
// slice table over agent_evaluations. Pure read view; no mutations.
type PostmortemHandler struct {
	render  *Renderer
	pool    *pgxpool.Pool
	statusH *StatusHandler
}

func NewPostmortemHandler(r *Renderer, pool *pgxpool.Pool) *PostmortemHandler {
	return &PostmortemHandler{render: r, pool: pool}
}

func (h *PostmortemHandler) WithStatus(s *StatusHandler) *PostmortemHandler {
	h.statusH = s
	return h
}

type postmortemCell struct {
	Strategy  string
	Outcome   string
	Count     int
	AvgScore  string
	AvgPnLUSD string
	WinRate   string
}

// View handles GET /eval/postmortem.
func (h *PostmortemHandler) View(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	start, canonSince := parsePostmortemSince(r.URL.Query().Get("since"))
	end := time.Now().UTC()

	rows, err := h.pool.Query(ctx, `
SELECT COALESCE(s.strategy_id, '')                                          AS strategy_id,
       ae.outcome_label                                                      AS outcome,
       count(*)                                                              AS cnt,
       COALESCE(round(avg(ae.score)::numeric, 2)::text, '')                  AS avg_score,
       COALESCE(round(avg(ae.outcome_pnl_usd)::numeric, 4)::text, '')        AS avg_pnl_usd,
       COALESCE(
         round(
           sum(CASE WHEN ae.outcome_label='win' THEN 1 ELSE 0 END)::numeric
             / nullif(count(*), 0)::numeric,
           4
         )::text,
         '0'
       )                                                                     AS win_rate
FROM agent_evaluations ae
JOIN signals s ON s.id = ae.signal_id
WHERE ae.outcome_label IN ('win','loss','flat')
  AND ae.created_at BETWEEN $1 AND $2
GROUP BY s.strategy_id, ae.outcome_label
ORDER BY s.strategy_id, ae.outcome_label`, start, end)
	if err != nil {
		http.Error(w, "query: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer rows.Close()

	var cells []postmortemCell
	for rows.Next() {
		var c postmortemCell
		if err := rows.Scan(&c.Strategy, &c.Outcome, &c.Count, &c.AvgScore, &c.AvgPnLUSD, &c.WinRate); err != nil {
			http.Error(w, "scan: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		cells = append(cells, c)
	}

	data := map[string]any{
		"Title":     "Postmortem 切片",
		"Cells":     cells,
		"HasRows":   len(cells) > 0,
		"StartFmt":  start.Format("2006-01-02"),
		"EndFmt":    end.Format("2006-01-02"),
		"Since":     canonSince,
		"SinceOpts": AllowedPostmortemSinces,
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/postmortem", data)
}

// postmortemDetailRow projects one agent_evaluations row for the drill-down list.
type postmortemDetailRow struct {
	SignalID       int64
	CreatedAtUnix  int64
	Symbol         string
	Kind           string
	Score          int
	Decision       string
	Outcome        string
	PnLUSD         string
	PnLPct         string
	ReasoningShort string
}

// Details handles GET /eval/postmortem/details?strategy=&outcome=&since=.
// Lists individual evaluations behind one (strategy, outcome) cell.
func (h *PostmortemHandler) Details(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	strategy := r.URL.Query().Get("strategy")
	outcome := r.URL.Query().Get("outcome")
	if outcome != "win" && outcome != "loss" && outcome != "flat" {
		http.Error(w, "outcome must be one of win|loss|flat", http.StatusBadRequest)
		return
	}
	start, canonSince := parsePostmortemSince(r.URL.Query().Get("since"))
	end := time.Now().UTC()

	rows, err := h.pool.Query(ctx, `
SELECT ae.signal_id,
       EXTRACT(EPOCH FROM ae.created_at)::bigint,
       s.symbol,
       s.kind::text,
       COALESCE(ae.score, 0),
       ae.decision,
       ae.outcome_label,
       COALESCE(ae.outcome_pnl_usd::text, ''),
       COALESCE(ae.outcome_pnl_pct::text, ''),
       COALESCE(LEFT(ae.reasoning, 120), '')
FROM agent_evaluations ae
JOIN signals s ON s.id = ae.signal_id
WHERE COALESCE(s.strategy_id, '') = $1
  AND ae.outcome_label = $2
  AND ae.created_at BETWEEN $3 AND $4
ORDER BY ae.created_at DESC
LIMIT 200`, strategy, outcome, start, end)
	if err != nil {
		http.Error(w, "query: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer rows.Close()

	var details []postmortemDetailRow
	for rows.Next() {
		var d postmortemDetailRow
		if err := rows.Scan(&d.SignalID, &d.CreatedAtUnix, &d.Symbol, &d.Kind,
			&d.Score, &d.Decision, &d.Outcome, &d.PnLUSD, &d.PnLPct, &d.ReasoningShort); err != nil {
			http.Error(w, "scan: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		details = append(details, d)
	}

	data := map[string]any{
		"Title":    "Postmortem 明细",
		"Strategy": strategy,
		"Outcome":  outcome,
		"Since":    canonSince,
		"Rows":     details,
		"HasRows":  len(details) > 0,
	}
	if h.statusH != nil {
		data = h.statusH.WithStatus(r, data)
	}
	h.render.Render(w, http.StatusOK, "eval/postmortem_details", data)
}
