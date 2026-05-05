package signal

import (
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
// "2312.14" and 2312.14.
type payload struct {
	StrategyID *string      `json:"strategy_id"`
	Symbol     *string      `json:"symbol"`
	Signal     *string      `json:"signal"`
	Price      *json.Number `json:"price"`
	Timestamp  *int64       `json:"timestamp"`
	Secret     *string      `json:"secret"`
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
	kind, err := parseKind(*p.Signal)
	if err != nil {
		return nil, err
	}
	price, err := decimal.NewFromString(string(*p.Price))
	if err != nil {
		return nil, fmt.Errorf("price invalid: %w", err)
	}
	if !price.IsPositive() {
		return nil, fmt.Errorf("price must be positive, got %s", price.String())
	}
	if *p.Timestamp <= 0 {
		return nil, fmt.Errorf("timestamp must be > 0, got %d", *p.Timestamp)
	}
	return &Signal{
		StrategyID:    *p.StrategyID,
		Symbol:        strings.ToUpper(*p.Symbol),
		Kind:          kind,
		Price:         price,
		TVTimestamp:   time.UnixMilli(*p.Timestamp).UTC(),
		TVTimestampMs: *p.Timestamp,
		Secret:        *p.Secret,
		Raw:           json.RawMessage(body),
	}, nil
}
