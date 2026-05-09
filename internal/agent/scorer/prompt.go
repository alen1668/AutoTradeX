package scorer

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"text/template"
	"time"
)

//go:embed templates/v1.tmpl
var promptTemplate string

var compiledTemplate = template.Must(template.New("prompt").Parse(promptTemplate))

// promptCtx wraps ScoreInput with pre-formatted derived fields so the
// template stays simple and decoupled from domain types.
type promptCtx struct {
	Input         ScoreInput
	StrategyID    string
	Signal        signalView
	SignalTimeUTC string
}

type signalView struct {
	Symbol string
	Kind   string
	Price  string
}

// RenderPromptWithTemplate renders the given template against a promptCtx
// constructed from in. The hash is sha256(rendered)[:8] hex.
//
// The replay tool (cmd/agent-eval) passes a template parsed from an external
// file; the production scorer passes the embedded compiledTemplate. Both
// share this wrapping logic so they see the same set of available fields.
func RenderPromptWithTemplate(in ScoreInput, tmpl *template.Template) (text string, hash string, err error) {
	if in.Signal == nil {
		return "", "", fmt.Errorf("RenderPromptWithTemplate: nil Signal")
	}
	if in.Strategy == nil {
		return "", "", fmt.Errorf("RenderPromptWithTemplate: nil Strategy")
	}
	sigTime := time.UnixMilli(in.Signal.TVTimestampMs).UTC().Format("2006-01-02 15:04:05")
	ctx := promptCtx{
		Input:      in,
		StrategyID: in.Strategy.ID,
		Signal: signalView{
			Symbol: in.Signal.Symbol,
			Kind:   string(in.Signal.Kind),
			Price:  in.Signal.Price.String(),
		},
		SignalTimeUTC: sigTime,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return "", "", fmt.Errorf("execute template: %w", err)
	}
	rendered := buf.String()
	sum := sha256.Sum256([]byte(rendered))
	return rendered, hex.EncodeToString(sum[:4]), nil
}

// RenderPrompt renders the embedded v1 prompt. Used by the production scorer.
func RenderPrompt(in ScoreInput) (text string, hash string, err error) {
	return RenderPromptWithTemplate(in, compiledTemplate)
}
