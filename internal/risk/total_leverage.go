package risk

import (
	"context"
	"fmt"
)

type TotalLeverageRule struct {
	Settings SettingsProvider
}

func (r TotalLeverageRule) Name() string { return "total_leverage" }

func (r TotalLeverageRule) Check(ctx context.Context, in Input) (Decision, error) {
	if in.Signal == nil || !in.Signal.Kind.IsEntry() {
		return Allow(), nil
	}
	if !in.AccountEquity.IsPositive() {
		return Deny("account equity unavailable or non-positive"), nil
	}
	maxLev, _, err := r.Settings.Get(ctx)
	if err != nil {
		return Decision{}, fmt.Errorf("read settings: %w", err)
	}
	added := in.Strategy.NotionalUSDC()
	totalAfter := in.OpenNotionalSum.Add(added)
	leverage := totalAfter.Div(in.AccountEquity)
	if leverage.GreaterThan(maxLev) {
		return Deny(fmt.Sprintf("leverage %s > max_total_leverage %s",
			leverage.StringFixed(2), maxLev.StringFixed(2))), nil
	}
	return Allow(), nil
}
