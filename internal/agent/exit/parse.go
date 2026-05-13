package exit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// rawDecision is the JSON wire shape. Numbers come in as float64 so we
// keep the contract permissive; conversion to decimal happens after
// validation.
type rawDecision struct {
	Action          string   `json:"action"`
	Confidence      string   `json:"confidence"`
	Reasoning       string   `json:"reasoning"`
	ProposedSLPrice *float64 `json:"proposed_sl_price"`
	PartialPct      *float64 `json:"partial_pct"`
}

// Parse turns an LLM completion into a validated Decision.
//
// Tolerates two common LLM mishaps even when the prompt forbids them:
// (1) leading text before the JSON ("Sure, here is the JSON: ..."),
// (2) wrapping in ```json fences. Anything past the last `}` is dropped.
//
// Returns an error if the JSON is malformed, fields are out of enum, or
// action-specific required fields are missing/out of range.
func Parse(s string) (Decision, error) {
	body := extractJSON(s)
	if body == "" {
		return Decision{}, errors.New("no JSON object found in response")
	}
	var r rawDecision
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return Decision{}, fmt.Errorf("json: %w", err)
	}

	a := Action(r.Action)
	if !a.IsValid() {
		return Decision{}, fmt.Errorf("invalid action %q", r.Action)
	}
	c := Confidence(r.Confidence)
	if !c.IsValid() {
		return Decision{}, fmt.Errorf("invalid confidence %q", r.Confidence)
	}
	if strings.TrimSpace(r.Reasoning) == "" {
		return Decision{}, errors.New("empty reasoning")
	}

	d := Decision{
		Action:     a,
		Confidence: c,
		Reasoning:  r.Reasoning,
	}

	switch a {
	case ActionTightenSL:
		if r.ProposedSLPrice == nil {
			return Decision{}, errors.New("tighten_sl requires proposed_sl_price")
		}
		p := decimal.NewFromFloat(*r.ProposedSLPrice)
		d.ProposedSLPrice = &p
	case ActionTakePartial:
		if r.PartialPct == nil {
			return Decision{}, errors.New("take_partial requires partial_pct")
		}
		if *r.PartialPct <= 0 || *r.PartialPct > 0.5 {
			return Decision{}, fmt.Errorf("partial_pct out of range (0, 0.5]: %v", *r.PartialPct)
		}
		p := decimal.NewFromFloat(*r.PartialPct)
		d.PartialPct = &p
	}
	return d, nil
}

// extractJSON returns the substring from the first '{' to the last '}'
// (inclusive). Returns "" if no balanced range exists. Handles ```json
// fences and leading prose.
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return ""
	}
	return s[start : end+1]
}
