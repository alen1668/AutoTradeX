package risk

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
)

type DailyLossBreakerRule struct {
	MaxDailyLossUSDC decimal.Decimal
}

func (r DailyLossBreakerRule) Name() string { return "daily_loss_breaker" }

func (r DailyLossBreakerRule) Check(_ context.Context, in Input) (Decision, error) {
	// 平仓信号永远允许（不能让熔断卡死现有持仓）
	if in.Signal != nil && in.Signal.Kind.IsExit() {
		return Allow(), nil
	}
	if in.BreakerTripped {
		return Deny("daily loss breaker tripped"), nil
	}
	loss := in.DailyPnLUSDC.Neg() // 亏损 → 正数
	if loss.GreaterThan(r.MaxDailyLossUSDC) {
		return Deny(fmt.Sprintf("daily loss %s > max %s",
			loss.StringFixed(2), r.MaxDailyLossUSDC.StringFixed(2))), nil
	}
	return Allow(), nil
}
