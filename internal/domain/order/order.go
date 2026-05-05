package order

import (
	"time"

	"github.com/shopspring/decimal"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusSubmitted Status = "submitted"
	StatusPartial   Status = "partial"
	StatusFilled    Status = "filled"
	StatusCanceled  Status = "canceled"
	StatusRejected  Status = "rejected"
	StatusExpired   Status = "expired"
)

func (s Status) IsTerminal() bool {
	return s == StatusFilled || s == StatusCanceled || s == StatusRejected || s == StatusExpired
}

func (s Status) CanTransitionTo(next Status) bool {
	if s.IsTerminal() {
		return false
	}
	switch s {
	case StatusPending:
		return next == StatusSubmitted || next == StatusCanceled || next == StatusRejected || next == StatusExpired
	case StatusSubmitted:
		return next == StatusPartial || next == StatusFilled || next == StatusCanceled || next == StatusRejected || next == StatusExpired
	case StatusPartial:
		return next == StatusFilled || next == StatusCanceled || next == StatusExpired
	}
	return false
}

type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

type Type string

const (
	TypeMarket           Type = "MARKET"
	TypeStop             Type = "STOP"
	TypeStopMarket       Type = "STOP_MARKET"
	TypeTakeProfitMarket Type = "TAKE_PROFIT_MARKET"
)

type Purpose string

const (
	PurposeEntry      Purpose = "entry"
	PurposeExit       Purpose = "exit"
	PurposeStop       Purpose = "stop"
	PurposeBackupStop Purpose = "backup_stop"
	PurposeTakeProfit Purpose = "take_profit"
)

type Order struct {
	ID                int64
	VirtualPositionID int64
	StrategyID        string
	Symbol            string
	Side              Side
	Type              Type
	Purpose           Purpose
	Qty               decimal.Decimal
	Price             decimal.Decimal // optional
	StopPrice         decimal.Decimal // optional
	ClientOrderID     string
	ExchangeOrderID   string
	Status            Status
	FilledQty         decimal.Decimal
	AvgFillPrice      decimal.Decimal
	FeesUSDC          decimal.Decimal
	SubmittedAt       time.Time
	FilledAt          time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}
