package outcome_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/eval/outcome"
	"github.com/lizhaojie/tvbot/internal/store"
)

type fakeSettingsRepo struct {
	s *store.Settings
}

func (f *fakeSettingsRepo) Get(ctx context.Context, q store.Querier) (*store.Settings, error) {
	return f.s, nil
}

func TestSettingsAdapter_Read(t *testing.T) {
	s := &store.Settings{
		OutcomeHorizonMin:       45,
		OutcomeWinThresholdPct:  decimal.NewFromFloat(0.005),
		OutcomeLossThresholdPct: decimal.NewFromFloat(-0.005),
		OutcomeBatchSize:        100,
		OutcomeScanIntervalMin:  3,
		OutcomeStaleCutoffH:     12,
	}
	a := outcome.NewSettingsAdapter(&fakeSettingsRepo{s: s}, nil)
	got, err := a.Read(context.Background())
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.HorizonMin != 45 {
		t.Errorf("HorizonMin: got %d, want 45", got.HorizonMin)
	}
	if !got.WinThresh.Equal(decimal.NewFromFloat(0.005)) {
		t.Errorf("WinThresh: got %v, want 0.005", got.WinThresh)
	}
	if !got.LossThresh.Equal(decimal.NewFromFloat(-0.005)) {
		t.Errorf("LossThresh: got %v, want -0.005", got.LossThresh)
	}
	if got.BatchSize != 100 {
		t.Errorf("BatchSize: got %d, want 100", got.BatchSize)
	}
	if got.ScanInterval.Minutes() != 3 {
		t.Errorf("ScanInterval: got %v, want 3min", got.ScanInterval)
	}
	if got.StaleCutoffH != 12 {
		t.Errorf("StaleCutoffH: got %d, want 12", got.StaleCutoffH)
	}
}
