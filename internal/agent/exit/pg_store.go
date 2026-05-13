package exit

import (
	"context"

	"github.com/lizhaojie/tvbot/internal/store"
)

// PGStore translates a domain Decision + DecisionMeta + PositionSnapshot
// into a store.ExitDecisionRow and persists it. Satisfies Worker.Store.
type PGStore struct{ repo *store.ExitDecisionRepo }

func NewPGStore(repo *store.ExitDecisionRepo) *PGStore { return &PGStore{repo: repo} }

func (s *PGStore) Insert(
	ctx context.Context,
	pos PositionSnapshot,
	d Decision,
	meta DecisionMeta,
	mode Mode,
) (int64, error) {
	row := store.ExitDecisionRow{
		VirtualPositionID: pos.VirtualPositionID,
		StrategyID:        pos.StrategyID,
		Symbol:            pos.Symbol,
		Side:              pos.Side,
		EntryFillPrice:    pos.EntryFillPrice,
		CurrentPrice:      pos.CurrentPrice,
		Qty:               pos.Qty,
		UnrealizedPnLUSD:  pos.UnrealizedPnLUSD,
		UnrealizedPnLPct:  pos.UnrealizedPnLPct,
		PositionAgeSec:    int(pos.PositionAge.Seconds()),
		CurrentSLPrice:    pos.CurrentSLPrice,
		CurrentTPPrice:    pos.CurrentTPPrice,
		Action:            string(d.Action),
		Confidence:        string(d.Confidence),
		Reasoning:         d.Reasoning,
		ProposedSLPrice:   d.ProposedSLPrice,
		PartialPct:        d.PartialPct,
		Model:             meta.Model,
		PromptHash:        meta.PromptHash,
		LatencyMs:         &meta.LatencyMs,
		TokenIn:           &meta.TokenIn,
		TokenOut:          &meta.TokenOut,
		Mode:              string(mode),
	}
	return s.repo.Insert(ctx, row)
}
