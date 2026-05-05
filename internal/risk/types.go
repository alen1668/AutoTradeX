package risk

import (
	"context"
	"net"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/domain/position"
	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/domain/strategy"
)

// SettingsProvider supplies dynamic risk thresholds at evaluation time.
// Implementations should be safe for concurrent use.
type SettingsProvider interface {
	Get(ctx context.Context) (decimal.Decimal, decimal.Decimal, error)
	// returns (maxTotalLeverage, maxDailyLossUSDC, error)
}

// staticSettings is a SettingsProvider that always returns the same values.
// Used in tests and for backward compatibility with the existing rule constructors.
type staticSettings struct {
	maxLeverage  decimal.Decimal
	maxDailyLoss decimal.Decimal
}

func (s staticSettings) Get(_ context.Context) (decimal.Decimal, decimal.Decimal, error) {
	return s.maxLeverage, s.maxDailyLoss, nil
}

// NewStaticSettings is exported for tests / older call sites that have static
// values. Prefer a real DB-backed provider in production.
func NewStaticSettings(maxLeverage, maxDailyLoss decimal.Decimal) SettingsProvider {
	return staticSettings{maxLeverage: maxLeverage, maxDailyLoss: maxDailyLoss}
}

// Input 包含一条信号经风控判断时所需的全部上下文。
type Input struct {
	Signal          *sigpkg.Signal
	Strategy        *strategy.Strategy
	CurrentPosition *position.VirtualPosition // 该策略当前活跃仓位（nil 即空仓）
	OpenNotionalSum decimal.Decimal           // 全局所有活跃仓位的名义价值之和（USDC）
	AccountEquity   decimal.Decimal           // 账户权益（USDC）
	DailyPnLUSDC    decimal.Decimal           // 当日盈亏
	BreakerTripped  bool
	ClientIP        net.IP
}

type Decision struct {
	Allowed  bool
	RuleName string
	Reason   string
}

func Allow() Decision             { return Decision{Allowed: true} }
func Deny(reason string) Decision { return Decision{Allowed: false, Reason: reason} }

type Rule interface {
	Name() string
	Check(ctx context.Context, in Input) (Decision, error)
}
