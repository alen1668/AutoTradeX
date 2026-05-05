package risk

import (
	"context"
	"fmt"
)

type MaxPositionRule struct{}

func (MaxPositionRule) Name() string { return "max_position" }

func (MaxPositionRule) Check(_ context.Context, in Input) (Decision, error) {
	if in.Signal == nil || !in.Signal.Kind.IsEntry() {
		return Allow(), nil
	}
	if in.Strategy == nil {
		return Deny("strategy not loaded"), nil
	}
	notional := in.Strategy.NotionalUSDC()
	if notional.GreaterThan(in.Strategy.MaxOpenUSDC) {
		return Deny(fmt.Sprintf("notional %s > strategy.max_open_usdc %s",
			notional.String(), in.Strategy.MaxOpenUSDC.String())), nil
	}
	return Allow(), nil
}
