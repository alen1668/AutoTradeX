package main

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSpearman_PerfectPositive(t *testing.T) {
	scores := []int{10, 20, 30, 40, 50}
	pnls := []float64{1, 2, 3, 4, 5}
	rho := spearman(scores, pnls)
	assert.InDelta(t, 1.0, rho, 1e-9)
}

func TestSpearman_PerfectNegative(t *testing.T) {
	scores := []int{10, 20, 30, 40, 50}
	pnls := []float64{5, 4, 3, 2, 1}
	rho := spearman(scores, pnls)
	assert.InDelta(t, -1.0, rho, 1e-9)
}

func TestSpearman_Ties(t *testing.T) {
	scores := []int{50, 50, 60, 60, 70}
	pnls := []float64{1, 2, 3, 4, 5}
	rho := spearman(scores, pnls)
	assert.True(t, rho > 0.9 && rho < 1.0, "got %v", rho)
}

func TestSpearman_EmptyOrSingle(t *testing.T) {
	assert.True(t, math.IsNaN(spearman(nil, nil)))
	assert.True(t, math.IsNaN(spearman([]int{50}, []float64{1})))
}

func TestBucketize_FiveBuckets(t *testing.T) {
	rows := []ReplayRow{
		{NewScore: 10, PnLUSDC: ptrF(-1), HasPnL: true},
		{NewScore: 25, PnLUSDC: ptrF(2), HasPnL: true},
		{NewScore: 50, PnLUSDC: ptrF(3), HasPnL: true},
		{NewScore: 75, PnLUSDC: ptrF(4), HasPnL: true},
		{NewScore: 90, PnLUSDC: ptrF(5), HasPnL: true},
		{NewScore: 95, PnLUSDC: nil, HasPnL: false},
	}
	bs := bucketize(rows, func(r ReplayRow) int { return r.NewScore })
	assert.Equal(t, "0-20", bs[0].Label)
	assert.Equal(t, 1, bs[0].Signals)
	assert.Equal(t, "80-100", bs[4].Label)
	assert.Equal(t, 2, bs[4].Signals)
	assert.Equal(t, 1, bs[4].Trades)
	assert.InDelta(t, 5.0, bs[4].AvgPnL, 1e-9)
}

func TestBucketize_BoundariesGoUpward(t *testing.T) {
	rows := []ReplayRow{
		{NewScore: 20, PnLUSDC: ptrF(1), HasPnL: true},
		{NewScore: 80, PnLUSDC: ptrF(1), HasPnL: true},
	}
	bs := bucketize(rows, func(r ReplayRow) int { return r.NewScore })
	assert.Equal(t, 1, bs[1].Signals, "20 in 20-40")
	assert.Equal(t, 1, bs[4].Signals, "80 in 80-100")
}

func TestFlipMatrix_AllFour(t *testing.T) {
	rows := []ReplayRow{
		{OldDecision: "approve", NewDecision: "approve"},
		{OldDecision: "approve", NewDecision: "abandon", PnLUSDC: ptrF(-3), HasPnL: true},
		{OldDecision: "approve", NewDecision: "abandon", PnLUSDC: ptrF(-1), HasPnL: true},
		{OldDecision: "abandon", NewDecision: "approve", PnLUSDC: ptrF(2), HasPnL: true},
		{OldDecision: "abandon", NewDecision: "abandon"},
	}
	m := flipMatrix(rows)
	assert.Equal(t, 1, m.ApproveToApprove)
	assert.Equal(t, 2, m.ApproveToAbandon)
	assert.Equal(t, 1, m.AbandonToApprove)
	assert.Equal(t, 1, m.AbandonToAbandon)
	assert.InDelta(t, -2.0, m.ApproveToAbandonAvgPnL, 1e-9)
	assert.InDelta(t, 2.0, m.AbandonToApproveAvgPnL, 1e-9)
}

func TestFlipMatrix_NoFlipPnL(t *testing.T) {
	rows := []ReplayRow{
		{OldDecision: "approve", NewDecision: "abandon", HasPnL: false},
	}
	m := flipMatrix(rows)
	assert.True(t, math.IsNaN(m.ApproveToAbandonAvgPnL))
}

func TestSortByDeltaScoreDesc(t *testing.T) {
	rows := []ReplayRow{
		{SignalID: 1, OldScore: 50, NewScore: 52},
		{SignalID: 2, OldScore: 50, NewScore: 90},
		{SignalID: 3, OldScore: 50, NewScore: 10},
	}
	sortByDeltaScoreDesc(rows)
	assert.Equal(t, int64(2), rows[0].SignalID)
	assert.Equal(t, int64(3), rows[1].SignalID)
	assert.Equal(t, int64(1), rows[2].SignalID)
}

func ptrF(v float64) *float64 { return &v }
