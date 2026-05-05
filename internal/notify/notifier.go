package notify

import (
	"context"
	"errors"
	"strings"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityCritical Severity = "critical"
)

// Message is a structured notification. Adapters render it differently per channel.
type Message struct {
	Title    string
	Body     string         // markdown-friendly plain text
	Severity Severity
	Fields   map[string]any // optional extra context
}

// Notifier is the abstract send interface — Feishu, Telegram, and any future
// channel implement it. Multi composes several into one.
type Notifier interface {
	Send(ctx context.Context, m Message) error
}

// NoOp drops every message. Used as the default when no channel is configured.
type NoOp struct{}

func (NoOp) Send(_ context.Context, _ Message) error { return nil }

// Multi fans a single Send call out to N notifiers. It always tries every
// notifier even if an earlier one errors, then returns the joined error (or nil).
type Multi struct{ inner []Notifier }

func NewMulti(notifiers ...Notifier) *Multi { return &Multi{inner: notifiers} }

func (m *Multi) Send(ctx context.Context, msg Message) error {
	var errs []string
	for _, n := range m.inner {
		if err := n.Send(ctx, msg); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.New("notifier errors: " + strings.Join(errs, "; "))
}
