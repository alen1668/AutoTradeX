package exit

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestParse_HoldMinimal(t *testing.T) {
	in := `{"action":"hold","confidence":"medium","reasoning":"持仓走势仍符合开仓逻辑"}`
	d, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Action != ActionHold || d.Confidence != ConfMedium {
		t.Errorf("got %+v", d)
	}
}

func TestParse_TightenSL_RequiresProposedPrice(t *testing.T) {
	in := `{"action":"tighten_sl","confidence":"high","reasoning":"锁部分浮盈"}`
	_, err := Parse(in)
	if err == nil || !strings.Contains(err.Error(), "proposed_sl_price") {
		t.Fatalf("want missing proposed_sl_price error, got %v", err)
	}
}

func TestParse_TightenSL_OK(t *testing.T) {
	in := `{"action":"tighten_sl","confidence":"high","reasoning":"r","proposed_sl_price":2310.5}`
	d, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.ProposedSLPrice == nil || !d.ProposedSLPrice.Equal(decimal.NewFromFloat(2310.5)) {
		t.Errorf("price wrong: %+v", d.ProposedSLPrice)
	}
}

func TestParse_TakePartial_RequiresPctInRange(t *testing.T) {
	cases := map[string]string{
		"missing":  `{"action":"take_partial","confidence":"medium","reasoning":"r"}`,
		"too_low":  `{"action":"take_partial","confidence":"medium","reasoning":"r","partial_pct":0}`,
		"too_high": `{"action":"take_partial","confidence":"medium","reasoning":"r","partial_pct":0.6}`,
	}
	for name, in := range cases {
		_, err := Parse(in)
		if err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestParse_TakePartial_OK(t *testing.T) {
	in := `{"action":"take_partial","confidence":"medium","reasoning":"r","partial_pct":0.5}`
	d, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.PartialPct == nil || !d.PartialPct.Equal(decimal.NewFromFloat(0.5)) {
		t.Errorf("pct: %+v", d.PartialPct)
	}
}

func TestParse_ExitNow(t *testing.T) {
	in := `{"action":"exit_now","confidence":"high","reasoning":"news high reverse"}`
	d, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Action != ActionExitNow {
		t.Errorf("action: %v", d.Action)
	}
}

func TestParse_StripsCodeFenceAndPreamble(t *testing.T) {
	in := "Sure, here:\n```json\n{\"action\":\"hold\",\"confidence\":\"low\",\"reasoning\":\"r\"}\n```"
	d, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Action != ActionHold {
		t.Errorf("action: %v", d.Action)
	}
}

func TestParse_BadAction(t *testing.T) {
	in := `{"action":"flip","confidence":"low","reasoning":"r"}`
	_, err := Parse(in)
	if err == nil || !strings.Contains(err.Error(), "action") {
		t.Errorf("want action error, got %v", err)
	}
}

func TestParse_BadConfidence(t *testing.T) {
	in := `{"action":"hold","confidence":"yes","reasoning":"r"}`
	_, err := Parse(in)
	if err == nil || !strings.Contains(err.Error(), "confidence") {
		t.Errorf("want confidence error, got %v", err)
	}
}

func TestParse_EmptyReasoningRejected(t *testing.T) {
	in := `{"action":"hold","confidence":"low","reasoning":""}`
	_, err := Parse(in)
	if err == nil {
		t.Error("want reasoning error")
	}
}
