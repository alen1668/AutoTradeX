package scorer

import "context"

// StubScorer is a Scorer for tests. It returns a fixed ScoreResult/error
// from its fields. SimulateBlock, when non-nil, makes Score block until
// either the channel is closed/sent on or the context is canceled — used
// to test fail_mode timeout paths in ingest service.
type StubScorer struct {
	Result        ScoreResult
	Err           error
	SimulateBlock <-chan struct{}
	Calls         int
}

// Score implements Scorer.
func (s *StubScorer) Score(ctx context.Context, in ScoreInput) (ScoreResult, error) {
	s.Calls++
	if s.SimulateBlock != nil {
		select {
		case <-ctx.Done():
			return ScoreResult{
				Score:     -1,
				Decision:  "failed",
				Reasoning: ctx.Err().Error(),
			}, nil
		case <-s.SimulateBlock:
		}
	}
	return s.Result, s.Err
}
