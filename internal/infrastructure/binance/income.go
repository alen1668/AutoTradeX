package binance

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/web/admin"
)

// Binance returns at most 1000 records per call. We page back from `until`
// toward `since` by shrinking the window when a full page is returned.
const incomePageLimit = 1000

// Income fetches income history (REALIZED_PNL, COMMISSION, FUNDING_FEE,
// etc.) from Binance for the given UTC window. Implements admin.IncomeFetcher.
func (t *Trader) Income(ctx context.Context, since, until time.Time) ([]admin.IncomeRecord, error) {
	cctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	var out []admin.IncomeRecord
	endMs := until.UnixMilli()
	startMs := since.UnixMilli()

	for {
		raw, err := t.client.NewGetIncomeHistoryService().
			StartTime(startMs).
			EndTime(endMs).
			Limit(incomePageLimit).
			Do(cctx)
		if err != nil {
			return nil, fmt.Errorf("binance income history: %w", err)
		}
		for _, r := range raw {
			inc, _ := decimal.NewFromString(r.Income)
			out = append(out, admin.IncomeRecord{
				Type:   r.IncomeType,
				Income: inc,
				Symbol: r.Symbol,
				Time:   time.UnixMilli(r.Time).UTC(),
			})
		}
		if len(raw) < incomePageLimit {
			break
		}
		// Shrink window: page back. Oldest record's time becomes new EndTime.
		oldest := raw[0].Time
		for _, r := range raw {
			if r.Time < oldest {
				oldest = r.Time
			}
		}
		if oldest <= startMs {
			break
		}
		endMs = oldest - 1
	}
	return out, nil
}
