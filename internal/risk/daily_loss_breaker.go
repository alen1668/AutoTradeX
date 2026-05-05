package risk

import (
	"context"
	"fmt"
)

type DailyLossBreakerRule struct {
	Settings SettingsProvider
}

func (r DailyLossBreakerRule) Name() string { return "daily_loss_breaker" }

func (r DailyLossBreakerRule) Check(ctx context.Context, in Input) (Decision, error) {
	// 平仓信号永远允许（不能让熔断卡死现有持仓）
	if in.Signal != nil && in.Signal.Kind.IsExit() {
		return Allow(), nil
	}
	if in.BreakerTripped {
		return Deny("daily loss breaker tripped"), nil
	}
	_, maxLoss, err := r.Settings.Get(ctx)
	if err != nil {
		return Decision{}, fmt.Errorf("read settings: %w", err)
	}
	loss := in.DailyPnLUSDC.Neg() // 亏损 → 正数
	if loss.GreaterThan(maxLoss) {
		return Deny(fmt.Sprintf("daily loss %s > max %s",
			loss.StringFixed(2), maxLoss.StringFixed(2))), nil
	}
	return Allow(), nil
}
