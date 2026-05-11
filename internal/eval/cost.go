package eval

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CostEstimate is the result of EstimateCost. Source distinguishes the
// "based on N history rows" case from the "fallback hardcode" case so
// the UI can label the estimate with appropriate uncertainty.
type CostEstimate struct {
	SampleCount int
	AvgTokenIn  int
	AvgTokenOut int
	InPriceUSD  float64 // USD per token (price-table / 1M)
	OutPriceUSD float64
	TotalUSD    float64
	Source      string // "history" | "fallback"
}

// fallbackUsage is the cold-start estimate when we have <5 historical
// evaluations for the chosen model. Numbers are rough — see spec §8.2.
var fallbackUsage = map[string]struct{ In, Out int }{
	"claude-sonnet-4-6":         {In: 3500, Out: 800},
	"claude-opus-4-7":           {In: 3500, Out: 800},
	"claude-haiku-4-5-20251001": {In: 3500, Out: 600},
}

// priceTable is the Anthropic public price per 1M tokens at the time of
// writing. Hardcoded here for simplicity; a future task can move this to
// system_state if pricing diverges by tenant.
var priceTable = map[string]struct{ In, Out float64 }{
	"claude-sonnet-4-6":         {In: 3.0, Out: 15.0},
	"claude-opus-4-7":           {In: 15.0, Out: 75.0},
	"claude-haiku-4-5-20251001": {In: 1.0, Out: 5.0},
}

// EstimateCost projects the USD cost of replaying `since` window of signals
// through `model`. Algorithm:
//  1. Count signals in the window (agent_score IS NOT NULL).
//  2. Average token_in/token_out from agent_evaluations of the same model
//     (last 100 rows). If <5 history rows, fall back to fallbackUsage.
//  3. Multiply by priceTable[model].
//
// Returns an error only when `model` is unknown to priceTable. DB errors
// during step 1/2 are surfaced; callers should still show fallback to the
// user with a warning.
func EstimateCost(ctx context.Context, pool *pgxpool.Pool, since, model string) (CostEstimate, error) {
	price, ok := priceTable[model]
	if !ok {
		return CostEstimate{}, fmt.Errorf("unknown model %q", model)
	}

	cutoff, ok := ParseSince(since)
	if !ok {
		return CostEstimate{}, fmt.Errorf("unknown since window %q", since)
	}

	var sampleCount int
	if err := pool.QueryRow(ctx, `
SELECT COUNT(*) FROM signals
WHERE agent_score IS NOT NULL AND received_at >= $1`, cutoff).Scan(&sampleCount); err != nil {
		return CostEstimate{}, fmt.Errorf("count signals: %w", err)
	}

	var historyCount int
	var avgIn, avgOut float64
	if err := pool.QueryRow(ctx, `
SELECT COUNT(*), COALESCE(AVG(token_in), 0), COALESCE(AVG(token_out), 0)
  FROM (
    SELECT token_in, token_out
      FROM agent_evaluations
     WHERE model = $1 AND token_in IS NOT NULL AND token_out IS NOT NULL
     ORDER BY id DESC
     LIMIT 100
  ) x`, model).Scan(&historyCount, &avgIn, &avgOut); err != nil {
		return CostEstimate{}, fmt.Errorf("avg tokens: %w", err)
	}

	source := "history"
	if historyCount < 5 {
		fb, ok := fallbackUsage[model]
		if !ok {
			return CostEstimate{}, fmt.Errorf("no fallback for model %q", model)
		}
		avgIn, avgOut = float64(fb.In), float64(fb.Out)
		source = "fallback"
	}

	est := CostEstimate{
		SampleCount: sampleCount,
		AvgTokenIn:  int(avgIn),
		AvgTokenOut: int(avgOut),
		InPriceUSD:  price.In / 1_000_000,
		OutPriceUSD: price.Out / 1_000_000,
		Source:      source,
	}
	est.TotalUSD = float64(sampleCount) * (float64(est.AvgTokenIn)*est.InPriceUSD + float64(est.AvgTokenOut)*est.OutPriceUSD)
	return est, nil
}
