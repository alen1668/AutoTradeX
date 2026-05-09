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

// RenderPrompt renders the v1 prompt and returns sha256(prompt)[:8] hex
// as the version hash. Any change in template or input changes the hash —
// the agent_evaluations table indexes (model, prompt_hash) so evaluating
// "v3 prompt vs v2" is a single GROUP BY.
func RenderPrompt(in ScoreInput) (text string, hash string, err error) {
	if in.Signal == nil {
		return "", "", fmt.Errorf("RenderPrompt: nil Signal")
	}
	if in.Strategy == nil {
		return "", "", fmt.Errorf("RenderPrompt: nil Strategy")
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
	if err := compiledTemplate.Execute(&buf, ctx); err != nil {
		return "", "", fmt.Errorf("execute template: %w", err)
	}
	rendered := buf.String()
	sum := sha256.Sum256([]byte(rendered))
	return rendered, hex.EncodeToString(sum[:4]), nil
}
