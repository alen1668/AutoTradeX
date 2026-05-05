package risk

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"
)

type TotalLeverageRule struct {
	MaxLeverage decimal.Decimal
}

func (r TotalLeverageRule) Name() string { return "total_leverage" }

func (r TotalLeverageRule) Check(_ context.Context, in Input) (Decision, error) {
	if in.Signal == nil || !in.Signal.Kind.IsEntry() {
		return Allow(), nil
	}
	if !in.AccountEquity.IsPositive() {
		return Deny("account equity unavailable or non-positive"), nil
	}
	added := in.Strategy.NotionalUSDC()
	totalAfter := in.OpenNotionalSum.Add(added)
	leverage := totalAfter.Div(in.AccountEquity)
	if leverage.GreaterThan(r.MaxLeverage) {
		return Deny(fmt.Sprintf("leverage %s > max_total_leverage %s",
			leverage.StringFixed(2), r.MaxLeverage.StringFixed(2))), nil
	}
	return Allow(), nil
}
