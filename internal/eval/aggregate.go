package eval

import (
	"math"
	"sort"
)

// Spearman computes the Spearman rank correlation. Inputs must be of equal
// length and have at least 2 elements; otherwise NaN. Ties handled with
// average ranks. Excludes ERROR rows by upstream filtering, not here.
func Spearman(scores []int, pnls []float64) float64 {
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

// Bucketize partitions rows into 5 buckets by scoreOf(row). Boundaries:
//
//	[0,20) [20,40) [40,60) [60,80) [80,100]
//
// 100 included in last bucket.
func Bucketize(rows []ReplayRow, scoreOf func(ReplayRow) int) []Bucket {
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

// FlipMatrixOf counts the four old×new decision combinations. Rows with
// Error or with non-{approve,abandon} decisions are skipped.
func FlipMatrixOf(rows []ReplayRow) FlipMatrix {
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

// SortByDeltaScoreDesc sorts in place by |new-old| descending, stable.
func SortByDeltaScoreDesc(rows []ReplayRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		di := absInt(rows[i].NewScore - rows[i].OldScore)
		dj := absInt(rows[j].NewScore - rows[j].OldScore)
		return di > dj
	})
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ExtractScoresAndPnLs pulls the slices Spearman needs from a row list,
// filtering out Error rows and rows without PnL.
func ExtractScoresAndPnLs(rows []ReplayRow, scoreOf func(ReplayRow) int) (scores []int, pnls []float64) {
	for _, r := range rows {
		if r.Error != "" || !r.HasPnL || r.PnLUSDC == nil {
			continue
		}
		scores = append(scores, scoreOf(r))
		pnls = append(pnls, *r.PnLUSDC)
	}
	return
}
