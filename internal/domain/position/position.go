package position

import (
	"time"

	"github.com/shopspring/decimal"
)

type Side string

const (
	SideLong  Side = "long"
	SideShort Side = "short"
)

type Status string

const (
	StatusOpening Status = "opening"
	StatusOpen    Status = "open"
	StatusClosing Status = "closing"
	StatusClosed  Status = "closed"
)

func (s Status) IsActive() bool {
	return s == StatusOpening || s == StatusOpen || s == StatusClosing
}

func (s Status) CanTransitionTo(next Status) bool {
	switch s {
	case StatusOpening:
		return next == StatusOpen || next == StatusClosed
	case StatusOpen:
		return next == StatusClosing || next == StatusClosed
	case StatusClosing:
		return next == StatusClosed
	}
	return false
}

type VirtualPosition struct {
	ID                int64
	StrategyID        string
	Symbol            string
	Side              Side
	Qty               decimal.Decimal
	EntrySignalPrice  decimal.Decimal
	EntryFillPrice    decimal.Decimal // zero until filled
	EntrySignalID     int64
	EntryOrderID      int64
	StopOrderID       int64
	BackupStopOrderID int64
	TakeProfitOrderID int64
	Status            Status
	OpenedAt          time.Time
	ClosedAt          time.Time
}
