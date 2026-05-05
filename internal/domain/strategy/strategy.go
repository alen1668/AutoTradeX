package strategy

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

type Config struct {
	ID            string
	Symbol        string
	Leverage      int
	SizeUSDC      decimal.Decimal
	StopLossPct   decimal.Decimal // 1.5 表示 1.5%
	TakeProfitPct decimal.Decimal // optional, zero means none
	MaxOpenUSDC   decimal.Decimal
	Enabled       bool
}

type Strategy struct {
	Config
}

func New(c Config) (*Strategy, error) {
	if c.ID == "" {
		return nil, errors.New("id required")
	}
	if c.Symbol == "" {
		return nil, errors.New("symbol required")
	}
	if c.Leverage <= 0 || c.Leverage > 125 {
		return nil, fmt.Errorf("leverage out of range: %d", c.Leverage)
	}
	if !c.SizeUSDC.IsPositive() {
		return nil, errors.New("size_usdc must be positive")
	}
	if !c.StopLossPct.IsPositive() {
		return nil, errors.New("stop_loss_pct must be positive")
	}
	if !c.MaxOpenUSDC.IsPositive() {
		return nil, errors.New("max_open_usdc must be positive")
	}
	if c.MaxOpenUSDC.LessThan(c.SizeUSDC) {
		return nil, fmt.Errorf("max_open_usdc %s < size_usdc %s",
			c.MaxOpenUSDC.String(), c.SizeUSDC.String())
	}
	return &Strategy{Config: c}, nil
}

func (s *Strategy) HasTakeProfit() bool { return s.TakeProfitPct.IsPositive() }

// NotionalUSDC returns the per-trade notional value: size * leverage.
func (s *Strategy) NotionalUSDC() decimal.Decimal {
	return s.SizeUSDC.Mul(decimal.NewFromInt(int64(s.Leverage)))
}
