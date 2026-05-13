package exit

import (
	"context"
	"time"

	"github.com/lizhaojie/tvbot/internal/store"
)

// RepoCooldownReader implements CooldownReader on top of ExitDecisionRepo.
type RepoCooldownReader struct{ repo *store.ExitDecisionRepo }

func NewRepoCooldownReader(repo *store.ExitDecisionRepo) *RepoCooldownReader {
	return &RepoCooldownReader{repo: repo}
}

func (r *RepoCooldownReader) LastDecisionAt(ctx context.Context, positionID int64) (*time.Time, error) {
	row, err := r.repo.LastForPosition(ctx, positionID)
	if err != nil || row == nil {
		return nil, err
	}
	return &row.CreatedAt, nil
}
