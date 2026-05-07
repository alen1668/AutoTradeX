package admin

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
)

func dec(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

func TestAggregateIncome_GroupsByUTCDay(t *testing.T) {
	day1 := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	records := []IncomeRecord{
		{Type: "REALIZED_PNL", Income: dec("10.00"), Symbol: "ETHUSDT", Time: day1.Add(2 * time.Hour)},
		{Type: "REALIZED_PNL", Income: dec("5.00"), Symbol: "ETHUSDT", Time: day1.Add(20 * time.Hour)},
		{Type: "REALIZED_PNL", Income: dec("3.00"), Symbol: "ETHUSDT", Time: day2.Add(1 * time.Hour)},
	}
	got := aggregateIncome(records)
	assert.Equal(t, dec("15.00").String(), got[day1].RealizedPnL.String())
	assert.Equal(t, dec("3.00").String(), got[day2].RealizedPnL.String())
}

func TestAggregateIncome_SumsAllTypesIntoNet(t *testing.T) {
	day := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	records := []IncomeRecord{
		{Type: "REALIZED_PNL", Income: dec("10.00"), Time: day},
		{Type: "COMMISSION", Income: dec("-0.50"), Time: day},
		{Type: "FUNDING_FEE", Income: dec("-0.10"), Time: day},
	}
	got := aggregateIncome(records)
	bucket := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, dec("10.00").String(), got[bucket].RealizedPnL.String())
	assert.Equal(t, dec("-0.50").String(), got[bucket].Commission.String())
	assert.Equal(t, dec("-0.10").String(), got[bucket].FundingFee.String())
	assert.Equal(t, dec("9.40").String(), got[bucket].NetIncome.String())
}

func TestAggregateIncome_UnknownTypeStillCountsToNet(t *testing.T) {
	day := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	records := []IncomeRecord{
		{Type: "REALIZED_PNL", Income: dec("10.00"), Time: day},
		{Type: "INSURANCE_CLEAR", Income: dec("-2.00"), Time: day},
	}
	got := aggregateIncome(records)
	bucket := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, dec("10.00").String(), got[bucket].RealizedPnL.String())
	assert.Equal(t, dec("8.00").String(), got[bucket].NetIncome.String())
}

func TestAggregateIncome_Empty(t *testing.T) {
	got := aggregateIncome(nil)
	assert.Empty(t, got)
}

func TestIncomeCache_HitMiss(t *testing.T) {
	c := newIncomeCache(50 * time.Millisecond)
	since := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	data := []IncomeRecord{{Type: "REALIZED_PNL", Income: dec("1")}}

	if _, ok := c.get(since, until); ok {
		t.Fatal("expected miss on cold cache")
	}
	c.set(since, until, data)
	got, ok := c.get(since, until)
	assert.True(t, ok)
	assert.Equal(t, len(data), len(got))

	// different range → miss
	other := until.Add(24 * time.Hour)
	if _, ok := c.get(since, other); ok {
		t.Fatal("expected miss for different range")
	}
}

func TestIncomeCache_TTLExpiry(t *testing.T) {
	c := newIncomeCache(20 * time.Millisecond)
	since := time.Now()
	until := since.Add(time.Hour)
	c.set(since, until, []IncomeRecord{{}})
	if _, ok := c.get(since, until); !ok {
		t.Fatal("expected hit immediately")
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := c.get(since, until); ok {
		t.Fatal("expected miss after TTL")
	}
}
