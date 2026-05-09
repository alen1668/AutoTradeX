package main

import (
	"encoding/json"
	"math"
	"sort"
)

// nilIfNaN returns nil when v is NaN (so encoding/json emits null instead
// of failing). Bucket / FlipMatrix / ReplayReport use it via MarshalJSON.
func nilIfNaN(v float64) any {
	if math.IsNaN(v) {
		return nil
	}
	return v
}

// MarshalJSON for Bucket — turns NaN AvgPnL/WinPct into JSON null.
func (b Bucket) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Label   string `json:"label"`
		Signals int    `json:"signals"`
		Trades  int    `json:"trades"`
		AvgPnL  any    `json:"avg_pnl"`
		WinPct  any    `json:"win_pct"`
	}{b.Label, b.Signals, b.Trades, nilIfNaN(b.AvgPnL), nilIfNaN(b.WinPct)})
}

// MarshalJSON for FlipMatrix — turns NaN flip-quality avg PnL into JSON null.
func (m FlipMatrix) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ApproveToApprove       int `json:"approve_to_approve"`
		ApproveToAbandon       int `json:"approve_to_abandon"`
		AbandonToApprove       int `json:"abandon_to_approve"`
		AbandonToAbandon       int `json:"abandon_to_abandon"`
		ApproveToAbandonAvgPnL any `json:"approve_to_abandon_avg_pnl"`
		AbandonToApproveAvgPnL any `json:"abandon_to_approve_avg_pnl"`
	}{m.ApproveToApprove, m.ApproveToAbandon, m.AbandonToApprove, m.AbandonToAbandon,
		nilIfNaN(m.ApproveToAbandonAvgPnL), nilIfNaN(m.AbandonToApproveAvgPnL)})
}

// ReplayRow is the per-signal record produced by replayOne. Aggregation
// functions operate on slices of these. PnLUSDC is nil when the signal had
// no closed trade; HasPnL distinguishes nil from "0.0 PnL".
type ReplayRow struct {
	SignalID    int64
	StrategyID  string
	Symbol      string
	Kind        string
	OldScore    int
	OldDecision string
	OldReason   string
	NewScore    int
	NewDecision string
	NewReason   string
	PnLUSDC     *float64
	HasPnL      bool
	Error       string // non-empty when LLM call / parse failed for new prompt
}

// Bucket is one row of the 5-tier score-vs-PnL summary.
type Bucket struct {
	Label   string
	Signals int     // count of signals in bucket
	Trades  int     // count of has-PnL signals in bucket
	AvgPnL  float64 // mean of PnLs over Trades; NaN if Trades==0
	WinPct  float64 // % of Trades with PnL>0; NaN if Trades==0
}

// FlipMatrix counts the four old×new decision combinations and the avg PnL
// for the two true-flip cells.
type FlipMatrix struct {
	ApproveToApprove       int
	ApproveToAbandon       int
	AbandonToApprove       int
	AbandonToAbandon       int
	ApproveToAbandonAvgPnL float64 // NaN if no has-PnL flips of this kind
	AbandonToApproveAvgPnL float64
}

// spearman computes the Spearman rank correlation. Inputs must be of equal
// length and have at least 2 elements; otherwise NaN. Ties handled with
// average ranks. Excludes ERROR rows by upstream filtering, not here.
func spearman(scores []int, pnls []float64) float64 {
	n := len(scores)
	if n != len(pnls) || n < 2 {
		return math.NaN()
	}
	scoreF := make([]float64, n)
	for i, s := range scores {
		scoreF[i] = float64(s)
	}
	rs := averageRanks(scoreF)
	rp := averageRanks(pnls)
	return pearson(rs, rp)
}

// averageRanks returns ranks (1..n) for xs, with tied values getting the
// average of their span. Stable wrt original order.
func averageRanks(xs []float64) []float64 {
	n := len(xs)
	type ix struct {
		v float64
		i int
	}
	pairs := make([]ix, n)
	for i, v := range xs {
		pairs[i] = ix{v, i}
	}
	sort.SliceStable(pairs, func(a, b int) bool { return pairs[a].v < pairs[b].v })
	ranks := make([]float64, n)
	i := 0
	for i < n {
		j := i + 1
		for j < n && pairs[j].v == pairs[i].v {
			j++
		}
		// items [i, j) all share the same value → average rank.
		avg := float64(i+j+1) / 2.0
		for k := i; k < j; k++ {
			ranks[pairs[k].i] = avg
		}
		i = j
	}
	return ranks
}

func pearson(xs, ys []float64) float64 {
	n := len(xs)
	if n < 2 {
		return math.NaN()
	}
	var mx, my float64
	for i := 0; i < n; i++ {
		mx += xs[i]
		my += ys[i]
	}
	mx /= float64(n)
	my /= float64(n)
	var num, dx2, dy2 float64
	for i := 0; i < n; i++ {
		dx := xs[i] - mx
		dy := ys[i] - my
		num += dx * dy
		dx2 += dx * dx
		dy2 += dy * dy
	}
	if dx2 == 0 || dy2 == 0 {
		return math.NaN()
	}
	return num / math.Sqrt(dx2*dy2)
}

// bucketize partitions rows into 5 buckets by scoreOf(row). Boundaries:
//
//	[0,20) [20,40) [40,60) [60,80) [80,100]
//
// 100 included in last bucket.
func bucketize(rows []ReplayRow, scoreOf func(ReplayRow) int) []Bucket {
	bs := []Bucket{
		{Label: "0-20"}, {Label: "20-40"}, {Label: "40-60"},
		{Label: "60-80"}, {Label: "80-100"},
	}
	pnls := make([][]float64, 5)
	for _, r := range rows {
		if r.Error != "" {
			continue
		}
		s := scoreOf(r)
		idx := bucketIndex(s)
		bs[idx].Signals++
		if r.HasPnL && r.PnLUSDC != nil {
			bs[idx].Trades++
			pnls[idx] = append(pnls[idx], *r.PnLUSDC)
		}
	}
	for i := range bs {
		if bs[i].Trades == 0 {
			bs[i].AvgPnL = math.NaN()
			bs[i].WinPct = math.NaN()
			continue
		}
		var sum float64
		var wins int
		for _, p := range pnls[i] {
			sum += p
			if p > 0 {
				wins++
			}
		}
		bs[i].AvgPnL = sum / float64(bs[i].Trades)
		bs[i].WinPct = 100 * float64(wins) / float64(bs[i].Trades)
	}
	return bs
}

func bucketIndex(s int) int {
	switch {
	case s < 20:
		return 0
	case s < 40:
		return 1
	case s < 60:
		return 2
	case s < 80:
		return 3
	default:
		return 4
	}
}

// flipMatrix counts the four old×new decision combinations. Rows with
// Error or with non-{approve,abandon} decisions are skipped.
func flipMatrix(rows []ReplayRow) FlipMatrix {
	var m FlipMatrix
	var a2bSum, b2aSum float64
	var a2bN, b2aN int
	for _, r := range rows {
		if r.Error != "" {
			continue
		}
		switch {
		case r.OldDecision == "approve" && r.NewDecision == "approve":
			m.ApproveToApprove++
		case r.OldDecision == "approve" && r.NewDecision == "abandon":
			m.ApproveToAbandon++
			if r.HasPnL && r.PnLUSDC != nil {
				a2bSum += *r.PnLUSDC
				a2bN++
			}
		case r.OldDecision == "abandon" && r.NewDecision == "approve":
			m.AbandonToApprove++
			if r.HasPnL && r.PnLUSDC != nil {
				b2aSum += *r.PnLUSDC
				b2aN++
			}
		case r.OldDecision == "abandon" && r.NewDecision == "abandon":
			m.AbandonToAbandon++
		}
	}
	if a2bN == 0 {
		m.ApproveToAbandonAvgPnL = math.NaN()
	} else {
		m.ApproveToAbandonAvgPnL = a2bSum / float64(a2bN)
	}
	if b2aN == 0 {
		m.AbandonToApproveAvgPnL = math.NaN()
	} else {
		m.AbandonToApproveAvgPnL = b2aSum / float64(b2aN)
	}
	return m
}

// sortByDeltaScoreDesc sorts in place by |new-old| descending, stable.
func sortByDeltaScoreDesc(rows []ReplayRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		di := abs(rows[i].NewScore - rows[i].OldScore)
		dj := abs(rows[j].NewScore - rows[j].OldScore)
		return di > dj
	})
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// extractScoresAndPnLs pulls the slices Spearman needs from a row list,
// filtering out Error rows and rows without PnL.
func extractScoresAndPnLs(rows []ReplayRow, scoreOf func(ReplayRow) int) (scores []int, pnls []float64) {
	for _, r := range rows {
		if r.Error != "" || !r.HasPnL || r.PnLUSDC == nil {
			continue
		}
		scores = append(scores, scoreOf(r))
		pnls = append(pnls, *r.PnLUSDC)
	}
	return
}
