package admin

import (
	"context"
	"net/http"
	"sort"
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
	// BySymbol = { "ETHUSDT": +12.5, "BTCUSDT": -3.2, ... }. Empty if no
	// trades that day.
	BySymbol map[string]decimal.Decimal
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
	income  IncomeFetcher // nil in dry_run mode
	cache   *incomeCache
}

func NewStatsHandler(r *Renderer, pool *pgxpool.Pool, statusH *StatusHandler, income IncomeFetcher) *StatsHandler {
	return &StatsHandler{render: r, pool: pool, statusH: statusH, income: income, cache: newIncomeCache(30 * time.Second)}
}

// Index handles GET /stats.
func (h *StatsHandler) Index(w http.ResponseWriter, r *http.Request) {
	stats, totals, symbols, err := h.queryDaily(r.Context(), 30)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := h.statusH.WithStatus(r, map[string]any{
		"Stats":   stats,
		"Totals":  totals,
		"Symbols": symbols, // sorted, drives chart datasets + colors
	})
	h.render.Render(w, http.StatusOK, "stats/index", data)
}

func (h *StatsHandler) fetchBinanceDaily(ctx context.Context, since, until time.Time) ([]IncomeRecord, error) {
	records, ok := h.cache.get(since, until)
	if !ok {
		var err error
		records, err = h.income.Income(ctx, since, until)
		if err != nil {
			return nil, err
		}
		h.cache.set(since, until, records)
	}
	return records, nil
}

// queryDaily returns per-day stats for the last `days` days. When an
// IncomeFetcher is wired, the per-symbol P&L breakdown comes from Binance's
// /fapi/v1/income (REALIZED_PNL + COMMISSION + FUNDING_FEE per symbol).
// Otherwise, falls back to position_history grouped by (day, symbol).
//
// Returns: stats (newest first), period totals, sorted distinct symbols.
func (h *StatsHandler) queryDaily(ctx context.Context, days int) ([]DailyStat, Totals, []string, error) {
	// 1) Trade counts and win rate from DB (Binance income has no symbol
	//    grouping in our domain).
	rows, err := h.pool.Query(ctx, `
SELECT
  date_trunc('day', closed_at AT TIME ZONE 'UTC')::date AS day,
  symbol,
  count(*)::int                                          AS trades,
  count(*) FILTER (WHERE pnl_usdc > 0)::int              AS wins,
  COALESCE(sum(pnl_usdc), 0)                             AS pnl
FROM position_history
WHERE closed_at >= now() - make_interval(days => $1)
GROUP BY day, symbol
ORDER BY day ASC, symbol ASC`, days)
	if err != nil {
		return nil, Totals{}, nil, err
	}
	defer rows.Close()

	// dayMap: day → DailyStat (totals + per-symbol pnl from DB).
	dayMap := make(map[time.Time]*DailyStat)
	symbolSet := make(map[string]struct{})
	for rows.Next() {
		var (
			day        time.Time
			symbol     string
			trades     int
			wins       int
			pnl        decimal.Decimal
		)
		if err := rows.Scan(&day, &symbol, &trades, &wins, &pnl); err != nil {
			return nil, Totals{}, nil, err
		}
		d, ok := dayMap[day]
		if !ok {
			d = &DailyStat{Date: day, BySymbol: make(map[string]decimal.Decimal)}
			dayMap[day] = d
		}
		d.Trades += trades
		d.WinTrades += wins
		d.PnLUSDC = d.PnLUSDC.Add(pnl)
		d.BySymbol[symbol] = d.BySymbol[symbol].Add(pnl)
		symbolSet[symbol] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, Totals{}, nil, err
	}

	// 2) When Income API is wired, override per-day per-symbol P&L with the
	//    exchange's authoritative net income (incl. funding/commission).
	if h.income != nil {
		until := time.Now().UTC().Add(24 * time.Hour)
		since := until.AddDate(0, 0, -days-1)
		records, err := h.fetchBinanceDaily(ctx, since, until)
		if err == nil {
			bySymbol := aggregateIncomeBySymbol(records)
			for day, perSym := range bySymbol {
				d, ok := dayMap[day]
				if !ok {
					// Day with income but no closed positions in DB (e.g.
					// funding-only). Surface it so chart matches account.
					d = &DailyStat{Date: day, BySymbol: make(map[string]decimal.Decimal)}
					dayMap[day] = d
				}
				d.PnLUSDC = decimal.Zero
				d.BySymbol = make(map[string]decimal.Decimal, len(perSym))
				for sym, v := range perSym {
					d.BySymbol[sym] = v
					d.PnLUSDC = d.PnLUSDC.Add(v)
					symbolSet[sym] = struct{}{}
				}
			}
		}
		// Soft fallback on income error: leave DB values.
	}

	// 3) Sort symbols + days for deterministic output.
	symbols := make([]string, 0, len(symbolSet))
	for s := range symbolSet {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)

	days_ := make([]time.Time, 0, len(dayMap))
	for d := range dayMap {
		days_ = append(days_, d)
	}
	sort.Slice(days_, func(i, j int) bool { return days_[i].Before(days_[j]) })

	raw := make([]DailyStat, 0, len(days_))
	for _, day := range days_ {
		raw = append(raw, *dayMap[day])
	}

	// 4) Cumulative + win rate, oldest→newest.
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

	// 5) Period totals.
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

	// 6) Reverse for table display (newest first). Symbols stays in
	//    deterministic ascending order so dataset colors are stable.
	out := make([]DailyStat, len(raw))
	for i := range raw {
		out[i] = raw[len(raw)-1-i]
	}
	return out, totals, symbols, nil
}
