package critique

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"text/template"
	"time"
)

//go:embed templates/v1.tmpl
var tmplText string

var compiled = template.Must(template.New("critique").Parse(tmplText))

// RenderInput is the shape consumed by the embedded template. Producers
// (Agent.Run via PGDataReader) build this from DB queries.
type RenderInput struct {
	WindowStart     time.Time
	WindowEnd       time.Time
	SampleSize      int
	PreviousSummary string
	Aggregates      []AggregateRow
	Details         []DetailRow
}

// AggregateRow is one row of the strategy × regime × outcome pivot.
// Numeric fields are pre-formatted strings so the template stays simple
// and tolerant of NaN / empty values.
type AggregateRow struct {
	StrategyID string
	Regime     string
	Outcome    string
	Count      int
	AvgScore   string
	AvgPnLUSD  string
	WinRate    string
}

// DetailRow is one evaluation displayed in the detail table.
type DetailRow struct {
	SignalID       int64
	StrategyID     string
	Symbol         string
	Kind           string
	Score          int
	Decision       string
	Outcome        string
	PnLPct         string
	ReasoningShort string
}

// RenderPrompt renders the embedded v1 template and returns (text, 8-hex
// sha256 prefix, error). Hash format matches scorer.RenderPrompt so the
// dashboard can show prompt-version diffs uniformly.
func RenderPrompt(in RenderInput) (text, hash string, err error) {
	var buf bytes.Buffer
	if err := compiled.Execute(&buf, in); err != nil {
		return "", "", err
	}
	rendered := buf.String()
	sum := sha256.Sum256([]byte(rendered))
	return rendered, hex.EncodeToString(sum[:4]), nil
}
