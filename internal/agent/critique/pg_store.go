package critique

import (
	"context"
	"encoding/json"

	"github.com/lizhaojie/tvbot/internal/store"
)

// PGStore bridges Agent.Store to *store.CritiqueRepo. Field mapping is
// straightforward; pattern evidence/stats are packed into evidence_json.
type PGStore struct{ repo *store.CritiqueRepo }

func NewPGStore(repo *store.CritiqueRepo) *PGStore { return &PGStore{repo: repo} }

// Insert satisfies critique.Store. Translates the domain Critique +
// Patterns into CritiqueRow + CritiquePatternRow and delegates to
// CritiqueRepo.InsertWithPatterns (atomic via tx).
func (s *PGStore) Insert(ctx context.Context, c Critique, patterns []Pattern) (int64, error) {
	patternsJSON := c.PatternsJSON
	if len(patternsJSON) == 0 {
		patternsJSON = []byte("{}")
	}

	row := store.CritiqueRow{
		WindowStart:  c.WindowStart,
		WindowEnd:    c.WindowEnd,
		SampleSize:   c.SampleSize,
		Model:        c.Model,
		PromptHash:   c.PromptHash,
		PatternsJSON: patternsJSON,
		Summary:      c.Summary,
		LatencyMs:    c.LatencyMs,
		TokenIn:      c.TokenIn,
		TokenOut:     c.TokenOut,
		Status:       string(c.Status),
		ErrorMessage: c.ErrorMessage,
	}

	prows := make([]store.CritiquePatternRow, 0, len(patterns))
	for _, p := range patterns {
		ev, _ := json.Marshal(map[string]any{
			"evidence_signal_ids": p.EvidenceSignalIDs,
			"stats":               p.Stats,
		})
		if len(ev) == 0 {
			ev = []byte("{}")
		}
		prows = append(prows, store.CritiquePatternRow{
			PatternKey:   p.ID,
			Title:        p.Title,
			Suggestion:   p.SuggestionForPrompt,
			Confidence:   p.Confidence,
			EvidenceJSON: ev,
		})
	}

	return s.repo.InsertWithPatterns(ctx, row, prows)
}

// AutoPin pins all patterns of `critiqueID` whose confidence matches the
// filter (or all when confidence == "all"). pinned_by is set to "auto"
// so the operator can distinguish in the UI.
func (s *PGStore) AutoPin(ctx context.Context, critiqueID int64, confidence string) error {
	if confidence == "" || confidence == "off" {
		return nil
	}
	return s.repo.PinByConfidence(ctx, critiqueID, confidence, "auto")
}
