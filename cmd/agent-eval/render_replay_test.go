//go:build never
// +build never

package main

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleReport() ReplayReport {
	pnl := -8.5
	return ReplayReport{
		Since:      "7d",
		PromptFile: "prompts/v2.tmpl",
		SampleSize: 2,
		WithPnL:    1,
		V1Spearman: 0.18,
		V2Spearman: 0.34,
		V1Buckets: []Bucket{
			{Label: "0-20"}, {Label: "20-40"}, {Label: "40-60"}, {Label: "60-80"},
			{Label: "80-100", Signals: 1, Trades: 1, AvgPnL: 5.2, WinPct: 100},
		},
		V2Buckets: []Bucket{
			{Label: "0-20", Signals: 1, Trades: 1, AvgPnL: -8.5, WinPct: 0},
			{Label: "20-40"}, {Label: "40-60"}, {Label: "60-80"}, {Label: "80-100"},
		},
		Flips: FlipMatrix{ApproveToAbandon: 1, ApproveToAbandonAvgPnL: -8.5},
		Rows: []ReplayRow{
			{SignalID: 1247, StrategyID: "supertrend-eth", Symbol: "ETHUSDC", Kind: "long",
				OldScore: 72, NewScore: 18, OldDecision: "approve", NewDecision: "abandon",
				OldReason: "old r", NewReason: "new r",
				PnLUSDC: &pnl, HasPnL: true},
			{SignalID: 1300, Error: "llm: timeout"},
		},
	}
}

func TestRenderText_ContainsHeadlines(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, renderReplayText(&buf, sampleReport()))
	out := buf.String()
	assert.Contains(t, out, "Replay 报告")
	assert.Contains(t, out, "prompts/v2.tmpl")
	assert.Contains(t, out, "Spearman")
	assert.Contains(t, out, "0.18")
	assert.Contains(t, out, "0.34")
	assert.Contains(t, out, "翻转矩阵")
	assert.Contains(t, out, "1247")
	assert.Contains(t, out, "ERROR") // error row marker
}

func TestRenderJSON_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, renderReplayJSON(&buf, sampleReport()))
	var back ReplayReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &back))
	assert.Equal(t, "prompts/v2.tmpl", back.PromptFile)
	assert.Equal(t, 0.34, back.V2Spearman)
	assert.Len(t, back.Rows, 2)
	assert.Equal(t, "llm: timeout", back.Rows[1].Error)
}

func TestRenderHTML_ContainsKeyMarkers(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, renderReplayHTML(&buf, sampleReport()))
	out := buf.String()
	assert.True(t, strings.Contains(out, "<html") || strings.Contains(out, "<body"))
	assert.Contains(t, out, "prompts/v2.tmpl")
	assert.Contains(t, out, "1247")
}

// TestRenderJSON_NaNBecomesNull guards against the encoding/json default
// rejection of NaN floats. ReplayReport / Bucket / FlipMatrix have custom
// MarshalJSON that converts NaN → null so small-sample reports
// (Spearman NaN, empty buckets) still serialize.
func TestRenderJSON_NaNBecomesNull(t *testing.T) {
	r := ReplayReport{
		Since:      "1h",
		PromptFile: "x.tmpl",
		V1Spearman: math.NaN(),
		V2Spearman: math.NaN(),
		V1Buckets: []Bucket{
			{Label: "0-20", AvgPnL: math.NaN(), WinPct: math.NaN()},
		},
		V2Buckets: []Bucket{
			{Label: "0-20", AvgPnL: math.NaN(), WinPct: math.NaN()},
		},
		Flips: FlipMatrix{
			ApproveToAbandonAvgPnL: math.NaN(),
			AbandonToApproveAvgPnL: math.NaN(),
		},
	}
	var buf bytes.Buffer
	require.NoError(t, renderReplayJSON(&buf, r),
		"NaN floats must serialize without error")
	out := buf.String()
	assert.Contains(t, out, `"v1_spearman": null`)
	assert.Contains(t, out, `"v2_spearman": null`)
	assert.Contains(t, out, `"avg_pnl": null`)
	assert.Contains(t, out, `"approve_to_abandon_avg_pnl": null`)
}
