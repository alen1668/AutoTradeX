package signal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type Kind string

const (
	KindLong      Kind = "long"
	KindShort     Kind = "short"
	KindExitLong  Kind = "exit_long"
	KindExitShort Kind = "exit_short"
)

func (k Kind) IsEntry() bool { return k == KindLong || k == KindShort }
func (k Kind) IsExit() bool  { return k == KindExitLong || k == KindExitShort }

func parseKind(s string) (Kind, error) {
	norm := strings.ToLower(strings.TrimSpace(s))
	norm = strings.ReplaceAll(norm, " ", "_")
	switch norm {
	case "long":
		return KindLong, nil
	case "short":
		return KindShort, nil
	case "exit_long":
		return KindExitLong, nil
	case "exit_short":
		return KindExitShort, nil
	}
	return "", fmt.Errorf("unknown signal kind: %q", s)
}

// parseKindFromAction disambiguates TradingView's {{strategy.order.action}}
// (which is "buy"/"sell") using {{strategy.position_size}} (the position size
// AFTER the order). The four legal combinations:
//
//	buy + size > 0  → long       (entry, opening or adding to a long)
//	buy + size == 0 → exit_short (closing a short)
//	sell + size < 0 → short      (entry, opening or adding to a short)
//	sell + size == 0 → exit_long (closing a long)
//
// buy+negative or sell+positive are inconsistent and rejected.
func parseKindFromAction(action string, positionSize decimal.Decimal) (Kind, error) {
	norm := strings.ToLower(strings.TrimSpace(action))
	switch norm {
	case "buy":
		switch {
		case positionSize.IsPositive():
			return KindLong, nil
		case positionSize.IsZero():
			return KindExitShort, nil
		default:
			return "", fmt.Errorf("buy with negative position_size %s is inconsistent", positionSize.String())
		}
	case "sell":
		switch {
		case positionSize.IsNegative():
			return KindShort, nil
		case positionSize.IsZero():
			return KindExitLong, nil
		default:
			return "", fmt.Errorf("sell with positive position_size %s is inconsistent", positionSize.String())
		}
	}
	return "", fmt.Errorf("unknown action: %q", action)
}

// isBuySell reports whether the signal field is TradingView's "buy"/"sell"
// (which need position_size to disambiguate) vs an explicit kind name.
func isBuySell(s string) bool {
	norm := strings.ToLower(strings.TrimSpace(s))
	return norm == "buy" || norm == "sell"
}

// Signal is the parsed, validated webhook payload.
type Signal struct {
	StrategyID    string
	Symbol        string
	Kind          Kind
	Price         decimal.Decimal
	TVTimestamp   time.Time
	TVTimestampMs int64
	Secret        string
	Raw           json.RawMessage
}

// payload mirrors the wire format. price is json.Number to accept both
// "2312.14" and 2312.14. timestamp is RawMessage because TradingView's
// {{time}} placeholder emits an RFC3339 string ("2026-05-06T15:29:00Z"),
// while curl tests / custom Pine alerts emit Unix millis as a number.
type payload struct {
	StrategyID   *string          `json:"strategy_id"`
	Symbol       *string          `json:"symbol"`
	Signal       *string          `json:"signal"`
	Price        *json.Number     `json:"price"`
	Timestamp    *json.RawMessage `json:"timestamp"`
	Secret       *string          `json:"secret"`
	PositionSize *json.Number     `json:"position_size"` // optional; required when signal is "buy"/"sell"
}

// parseTimestamp accepts either Unix milliseconds (number) or an RFC3339
// string. Returns Unix millis.
func parseTimestamp(raw json.RawMessage) (int64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, errors.New("empty")
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return 0, fmt.Errorf("string: %w", err)
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return 0, fmt.Errorf("RFC3339: %w", err)
		}
		return t.UnixMilli(), nil
	}
	var ms int64
	if err := json.Unmarshal(trimmed, &ms); err != nil {
		return 0, fmt.Errorf("int64: %w", err)
	}
	return ms, nil
}

// TraceID is a deterministic id useful for correlating logs/DB rows for a
// single TV alert. Suitable for use as the trace id when none was provided
// by an HTTP middleware. Format: tv-<strategy_id>-<tv_timestamp_ms>.
func (s *Signal) TraceID() string {
	return fmt.Sprintf("tv-%s-%d", s.StrategyID, s.TVTimestampMs)
}

func Parse(body []byte) (*Signal, error) {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	var p payload
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if p.StrategyID == nil || *p.StrategyID == "" {
		return nil, errors.New("strategy_id required")
	}
	if p.Symbol == nil || *p.Symbol == "" {
		return nil, errors.New("symbol required")
	}
	if p.Signal == nil {
		return nil, errors.New("signal required")
	}
	if p.Price == nil {
		return nil, errors.New("price required")
	}
	if p.Timestamp == nil {
		return nil, errors.New("timestamp required")
	}
	if p.Secret == nil || *p.Secret == "" {
		return nil, errors.New("secret required")
	}
	var kind Kind
	if isBuySell(*p.Signal) {
		if p.PositionSize == nil {
			return nil, errors.New("position_size required for buy/sell signal")
		}
		size, err := decimal.NewFromString(string(*p.PositionSize))
		if err != nil {
			return nil, fmt.Errorf("position_size invalid: %w", err)
		}
		kind, err = parseKindFromAction(*p.Signal, size)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		kind, err = parseKind(*p.Signal)
		if err != nil {
			return nil, err
		}
	}
	price, err := decimal.NewFromString(string(*p.Price))
	if err != nil {
		return nil, fmt.Errorf("price invalid: %w", err)
	}
	if !price.IsPositive() {
		return nil, fmt.Errorf("price must be positive, got %s", price.String())
	}
	tsMs, err := parseTimestamp(*p.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("timestamp invalid: %w", err)
	}
	if tsMs <= 0 {
		return nil, fmt.Errorf("timestamp must be > 0, got %d", tsMs)
	}
	return &Signal{
		StrategyID:    *p.StrategyID,
		Symbol:        strings.ToUpper(*p.Symbol),
		Kind:          kind,
		Price:         price,
		TVTimestamp:   time.UnixMilli(tsMs).UTC(),
		TVTimestampMs: tsMs,
		Secret:        *p.Secret,
		Raw:           json.RawMessage(body),
	}, nil
}
