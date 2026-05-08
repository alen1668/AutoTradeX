package trade

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
)

// IncomeRecord is the exchange-agnostic shape of one income event from the
// account: a realized P&L line, a commission charge, or a funding payment.
type IncomeRecord struct {
	Type   string          // "REALIZED_PNL" | "COMMISSION" | "FUNDING_FEE" | other
	Income decimal.Decimal // signed; commission is negative, realized P&L can be either
	Symbol string
	Time   time.Time // UTC
}

// IncomeFetcher fetches income events from the exchange for a time window.
// BinanceTrader implements it.
type IncomeFetcher interface {
	Income(ctx context.Context, since, until time.Time) ([]IncomeRecord, error)
}
