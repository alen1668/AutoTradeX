package exit

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"text/template"
	"time"

	"github.com/shopspring/decimal"
)

//go:embed templates/v1.tmpl
var promptTemplate string

var tmpl = template.Must(template.New("exit_v1").
	Funcs(template.FuncMap{
		"positionAgeMin": func(d time.Duration) int { return int(d / time.Minute) },
		"nilDec": func(p *decimal.Decimal) string {
			if p == nil {
				return "(未挂)"
			}
			return p.String()
		},
	}).
	Parse(promptTemplate))

// RenderPrompt fills the v1 template with Input. Returns the raw string
// the LLMClient should send.
func RenderPrompt(in Input) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, in); err != nil {
		return "", fmt.Errorf("template execute: %w", err)
	}
	return buf.String(), nil
}

var promptHashCache = func() string {
	sum := sha256.Sum256([]byte(promptTemplate))
	return hex.EncodeToString(sum[:])[:8]
}()

// PromptHash returns the first 8 hex chars of sha256(template). Used in
// agent_exit_decisions.prompt_hash for prompt-version slicing in /eval.
func PromptHash() string { return promptHashCache }
