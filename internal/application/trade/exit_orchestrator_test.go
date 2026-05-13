package trade

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	tradepkg "github.com/lizhaojie/tvbot/internal/trade"
)

type fakeExitTrader struct {
	placed   []tradepkg.OrderRequest
	canceled []string
	placeErr error
}

func (f *fakeExitTrader) Place(_ context.Context, r tradepkg.OrderRequest) (*tradepkg.OrderResult, error) {
	f.placed = append(f.placed, r)
	if f.placeErr != nil {
		return nil, f.placeErr
	}
	return &tradepkg.OrderResult{ClientOrderID: r.ClientOrderID, Status: tradepkg.OrderStatusSubmitted}, nil
}
func (f *fakeExitTrader) Cancel(_ context.Context, _ string, cid string) error {
	f.canceled = append(f.canceled, cid)
	return nil
}
func (f *fakeExitTrader) GetOrder(_ context.Context, _, _ string) (*tradepkg.OrderResult, error) {
	return nil, nil
}
func (f *fakeExitTrader) GetPositionRisk(_ context.Context, _ string) (*tradepkg.Position, error) {
	return nil, nil
}

type fakeExitVP struct {
	pos ExitPositionView
	err error
}

func (f *fakeExitVP) GetByIDForExit(_ context.Context, _ int64) (ExitPositionView, error) {
	return f.pos, f.err
}

type fakeExitOrders struct {
	clientID  map[int64]string
	stopPrice map[int64]decimal.Decimal
	updates   []struct {
		ID     int64
		Status string
	}
}

func (f *fakeExitOrders) GetClientOrderIDByID(_ context.Context, id int64) (string, error) {
	if cid, ok := f.clientID[id]; ok {
		return cid, nil
	}
	return "", errors.New("not found")
}
func (f *fakeExitOrders) UpdateStatus(_ context.Context, id int64, status string) error {
	f.updates = append(f.updates, struct {
		ID     int64
		Status string
	}{id, status})
	return nil
}
func (f *fakeExitOrders) StopPriceByID(_ context.Context, id int64) (decimal.Decimal, error) {
	if p, ok := f.stopPrice[id]; ok {
		return p, nil
	}
	return decimal.Zero, nil
}

func basicLongPos() ExitPositionView {
	return ExitPositionView{
		ID:                42,
		StrategyID:        "s",
		Symbol:            "ETHUSDC",
		Side:              "long",
		Qty:               decimal.NewFromFloat(0.1),
		StopOrderID:       100,
		BackupStopOrderID: 101,
		TakeProfitOrderID: 102,
		Status:            "open",
	}
}

func TestExitOrchestrator_TightenSL_ReplacesOldStop(t *testing.T) {
	pos := basicLongPos()
	bin := &fakeExitTrader{}
	ord := &fakeExitOrders{
		clientID:  map[int64]string{100: "old-stop-cid"},
		stopPrice: map[int64]decimal.Decimal{100: decimal.NewFromFloat(2280)},
	}
	o := NewExitOrchestrator(nil, bin, &fakeExitVP{pos: pos}, ord)
	if err := o.TightenSL(context.Background(), pos.ID, decimal.NewFromFloat(2310)); err != nil {
		t.Fatalf("TightenSL: %v", err)
	}
	if len(ord.updates) != 1 || ord.updates[0].Status != "canceled" {
		t.Errorf("expected canceled update, got %v", ord.updates)
	}
	if len(bin.canceled) != 1 || bin.canceled[0] != "old-stop-cid" {
		t.Errorf("expected cancel of old stop cid, got %v", bin.canceled)
	}
	if len(bin.placed) != 1 {
		t.Fatalf("expected 1 place, got %d", len(bin.placed))
	}
	if !bin.placed[0].StopPrice.Equal(decimal.NewFromFloat(2310)) {
		t.Errorf("new stop wrong: %v", bin.placed[0].StopPrice)
	}
	if bin.placed[0].Side != tradepkg.OrderSideSell {
		t.Errorf("expected SELL exit side for long, got %v", bin.placed[0].Side)
	}
}

func TestExitOrchestrator_TightenSL_RejectsLooserPrice(t *testing.T) {
	pos := basicLongPos()
	bin := &fakeExitTrader{}
	ord := &fakeExitOrders{
		clientID:  map[int64]string{100: "old"},
		stopPrice: map[int64]decimal.Decimal{100: decimal.NewFromFloat(2300)},
	}
	o := NewExitOrchestrator(nil, bin, &fakeExitVP{pos: pos}, ord)
	err := o.TightenSL(context.Background(), pos.ID, decimal.NewFromFloat(2290)) // looser for long
	if !errors.Is(err, ErrConstraintViolated) {
		t.Errorf("want ErrConstraintViolated, got %v", err)
	}
	if len(bin.placed) != 0 {
		t.Errorf("nothing should be placed on rejection, got %v", bin.placed)
	}
}

func TestExitOrchestrator_TightenSL_NoExistingStopFails(t *testing.T) {
	pos := basicLongPos()
	pos.StopOrderID = 0
	o := NewExitOrchestrator(nil, &fakeExitTrader{}, &fakeExitVP{pos: pos}, &fakeExitOrders{})
	err := o.TightenSL(context.Background(), pos.ID, decimal.NewFromFloat(2310))
	if err == nil {
		t.Error("expected error when no current stop")
	}
}

func TestExitOrchestrator_TakePartial_HalvesQty(t *testing.T) {
	pos := basicLongPos()
	bin := &fakeExitTrader{}
	o := NewExitOrchestrator(nil, bin, &fakeExitVP{pos: pos}, &fakeExitOrders{})
	if err := o.TakePartial(context.Background(), pos.ID, decimal.NewFromFloat(0.5)); err != nil {
		t.Fatalf("TakePartial: %v", err)
	}
	if len(bin.placed) != 1 {
		t.Fatalf("expected 1 place, got %d", len(bin.placed))
	}
	want := decimal.NewFromFloat(0.05) // 0.1 * 0.5
	if !bin.placed[0].Qty.Equal(want) {
		t.Errorf("partial qty: got %v want %v", bin.placed[0].Qty, want)
	}
	if bin.placed[0].Type != tradepkg.OrderTypeMarket {
		t.Errorf("expected MARKET, got %v", bin.placed[0].Type)
	}
}

func TestExitOrchestrator_TakePartial_RejectsOutOfRange(t *testing.T) {
	o := NewExitOrchestrator(nil, &fakeExitTrader{}, &fakeExitVP{pos: basicLongPos()}, &fakeExitOrders{})
	for _, pct := range []float64{0, -0.1, 0.51, 1} {
		err := o.TakePartial(context.Background(), 42, decimal.NewFromFloat(pct))
		if !errors.Is(err, ErrConstraintViolated) {
			t.Errorf("pct=%v: want ErrConstraintViolated, got %v", pct, err)
		}
	}
}

func TestExitOrchestrator_ExitNow_CancelsProtectionsAndCloses(t *testing.T) {
	pos := basicLongPos()
	bin := &fakeExitTrader{}
	ord := &fakeExitOrders{
		clientID: map[int64]string{100: "stop-cid", 101: "backup-cid", 102: "tp-cid"},
	}
	o := NewExitOrchestrator(nil, bin, &fakeExitVP{pos: pos}, ord)
	if err := o.ExitNow(context.Background(), pos.ID); err != nil {
		t.Fatalf("ExitNow: %v", err)
	}
	if len(bin.canceled) != 3 {
		t.Errorf("expected 3 cancels, got %v", bin.canceled)
	}
	if len(bin.placed) != 1 {
		t.Fatalf("expected 1 close place, got %d", len(bin.placed))
	}
	if !bin.placed[0].Qty.Equal(pos.Qty) {
		t.Errorf("close qty: got %v want %v", bin.placed[0].Qty, pos.Qty)
	}
}

func TestExitOrchestrator_ExitNow_ShortFlipsSide(t *testing.T) {
	pos := basicLongPos()
	pos.Side = "short"
	bin := &fakeExitTrader{}
	o := NewExitOrchestrator(nil, bin, &fakeExitVP{pos: pos}, &fakeExitOrders{})
	_ = o.ExitNow(context.Background(), pos.ID)
	if bin.placed[0].Side != tradepkg.OrderSideBuy {
		t.Errorf("short close should be BUY, got %v", bin.placed[0].Side)
	}
}

func TestExitOrchestrator_NotOpenStatusFails(t *testing.T) {
	pos := basicLongPos()
	pos.Status = "closing"
	o := NewExitOrchestrator(nil, &fakeExitTrader{}, &fakeExitVP{pos: pos}, &fakeExitOrders{})
	if err := o.ExitNow(context.Background(), pos.ID); !errors.Is(err, ErrPositionNotFound) {
		t.Errorf("want ErrPositionNotFound, got %v", err)
	}
}
