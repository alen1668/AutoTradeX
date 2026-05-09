package scorer

// ModelOption is one entry in the user-facing model picker. ID is the
// Anthropic API model identifier; Label is the display name shown in the
// settings UI.
type ModelOption struct {
	ID    string
	Label string
}

// SupportedModels is the allowlist of Claude models exposed in the
// settings UI. Both the admin form select and the POST handler validate
// against this list. Keep ordered most-capable first.
var SupportedModels = []ModelOption{
	{ID: "claude-opus-4-7", Label: "Claude Opus 4.7"},
	{ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6"},
	{ID: "claude-haiku-4-5-20251001", Label: "Claude Haiku 4.5"},
}

// DefaultModel is used as the fallback when no model is configured (e.g.
// agent-eval --replay against a fresh DB) and matches the column default
// set by migration 0008.
const DefaultModel = "claude-sonnet-4-6"

// IsSupportedModel reports whether id matches an entry in SupportedModels.
func IsSupportedModel(id string) bool {
	for _, m := range SupportedModels {
		if m.ID == id {
			return true
		}
	}
	return false
}
