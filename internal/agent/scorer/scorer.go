package scorer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/store"
)

// EvalRepo is the persistence side of the scorer. *store.AgentEvalRepo
// satisfies it. Tests use a captureing fake.
type EvalRepo interface {
	Insert(ctx context.Context, q store.Querier, e store.AgentEvaluation) error
}

// Factory bundles the long-lived dependencies of LLMScorer (LLM client,
// eval repo, pool, log, health tracker). cmd/tvbot/main.go constructs
// one Factory at boot; the ingest hook calls WithSignal per signal to
// produce a one-shot Scorer instance bound to that signal_id.
type Factory struct {
	client LLMClient
	repo   EvalRepo
	pool   *pgxpool.Pool
	log    zerolog.Logger
	health *HealthTracker
}

func NewFactory(client LLMClient, repo EvalRepo, pool *pgxpool.Pool, log zerolog.Logger) *Factory {
	return &Factory{
		client: client, repo: repo, pool: pool, log: log,
		health: NewHealthTracker(10 * time.Minute),
	}
}

// Health exposes the rolling-failure tracker for the alert layer.
func (f *Factory) Health() *HealthTracker { return f.health }

// WithSignal returns a single-use LLMScorer bound to (signalID, model,
// timeout). Model and timeout come from settings — read fresh each
// signal so config changes take effect without a restart.
func (f *Factory) WithSignal(signalID int64, model string, timeoutMs int) *LLMScorer {
	return &LLMScorer{
		client: f.client, repo: f.repo, pool: f.pool, log: f.log,
		health:    f.health,
		model:     model,
		timeoutMs: timeoutMs,
		signalID:  signalID,
	}
}

// LLMScorer renders the prompt, calls the LLM, parses the JSON response,
// updates the rolling health tracker, and persists an agent_evaluations
// row. It NEVER returns a non-nil error: any failure path produces a
// ScoreResult with Decision="failed" so the ingest hook can apply
// fail-mode policy uniformly.
type LLMScorer struct {
	client    LLMClient
	repo      EvalRepo
	pool      *pgxpool.Pool
	log       zerolog.Logger
	health    *HealthTracker
	model     string
	timeoutMs int
	signalID  int64
}

// llmJSON is the shape we ask the LLM to return. Pointer fields so we
// can detect missing keys (zero-int = score 0 vs. missing = parse fail).
type llmJSON struct {
	Score     *int    `json:"score"`
	Decision  *string `json:"decision"`
	Reasoning *string `json:"reasoning"`
}

func (s *LLMScorer) Score(ctx context.Context, in ScoreInput) (ScoreResult, error) {
	promptText, hash, err := RenderPrompt(in)
	if err != nil {
		return s.failed(in, "", "", "render prompt: "+err.Error(), 0, nil), nil
	}

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(s.timeoutMs)*time.Millisecond)
	defer cancel()
	start := time.Now()
	resp, llmErr := s.client.Complete(callCtx, CompleteRequest{
		Model:     s.model,
		Prompt:    promptText,
		MaxTokens: 512,
	})
	latency := int(time.Since(start).Milliseconds())

	if llmErr != nil {
		s.health.RecordFailure()
		return s.failed(in, promptText, hash, llmErr.Error(), latency, nil), nil
	}

	var parsed llmJSON
	parseErr := json.Unmarshal([]byte(resp.Text), &parsed)
	if parseErr != nil || parsed.Score == nil || parsed.Decision == nil || parsed.Reasoning == nil {
		s.health.RecordFailure()
		why := "non-JSON or missing fields"
		if parseErr != nil {
			why = parseErr.Error()
		}
		return s.failed(in, promptText, hash, why, latency, &resp.Text), nil
	}
	if *parsed.Score < 0 || *parsed.Score > 100 {
		s.health.RecordFailure()
		return s.failed(in, promptText, hash,
			fmt.Sprintf("score %d out of [0,100]", *parsed.Score), latency, &resp.Text), nil
	}
	if *parsed.Decision != "approve" && *parsed.Decision != "abandon" {
		s.health.RecordFailure()
		return s.failed(in, promptText, hash,
			fmt.Sprintf("decision %q must be approve|abandon", *parsed.Decision), latency, &resp.Text), nil
	}

	s.health.RecordSuccess()
	result := ScoreResult{
		Score:       *parsed.Score,
		Decision:    *parsed.Decision,
		Reasoning:   *parsed.Reasoning,
		Model:       s.model,
		LatencyMs:   latency,
		TokenIn:     resp.TokenIn,
		TokenOut:    resp.TokenOut,
		PromptHash:  hash,
		PromptText:  promptText,
		ResponseRaw: resp.Text,
	}
	rawCopy := resp.Text
	s.persistEval(ctx, in, result, &rawCopy)
	return result, nil
}

func (s *LLMScorer) failed(in ScoreInput, promptText, hash, reason string, latency int, rawResp *string) ScoreResult {
	res := ScoreResult{
		Score:       -1,
		Decision:    "failed",
		Reasoning:   reason,
		Model:       s.model,
		LatencyMs:   latency,
		PromptHash:  hash,
		PromptText:  promptText,
		ResponseRaw: stringDeref(rawResp),
	}
	s.persistEval(context.Background(), in, res, rawResp)
	return res
}

// persistEval writes the agent_evaluations row. Best-effort: log warn on
// error and move on (the trade decision is already known to the caller
// at this point, persistence failure must not change behavior).
func (s *LLMScorer) persistEval(ctx context.Context, in ScoreInput, r ScoreResult, rawResp *string) {
	histJSON, _ := json.Marshal(struct {
		SymbolHistory   []HistoricalTrade  `json:"symbol_history"`
		StrategyHistory []HistoricalTrade  `json:"strategy_history"`
		Portfolio       *PortfolioSnapshot `json:"portfolio"`
		Market          *MarketContext     `json:"market"`
		HighVolWindows  []string           `json:"high_vol_windows"`
	}{
		SymbolHistory:   in.SymbolHistory,
		StrategyHistory: in.StrategyHistory,
		Portfolio:       in.Portfolio,
		Market:          in.Market,
		HighVolWindows:  in.HighVolWindows,
	})
	var scorePtr *int
	if r.Score >= 0 {
		v := r.Score
		scorePtr = &v
	}
	var tokInPtr, tokOutPtr *int
	if r.TokenIn > 0 {
		v := r.TokenIn
		tokInPtr = &v
	}
	if r.TokenOut > 0 {
		v := r.TokenOut
		tokOutPtr = &v
	}
	err := s.repo.Insert(ctx, s.pool, store.AgentEvaluation{
		SignalID:    s.signalID,
		Model:       r.Model,
		PromptHash:  r.PromptHash,
		Score:       scorePtr,
		Decision:    r.Decision,
		Reasoning:   r.Reasoning,
		HistoryJSON: histJSON,
		PromptText:  r.PromptText,
		ResponseRaw: rawResp,
		LatencyMs:   r.LatencyMs,
		TokenIn:     tokInPtr,
		TokenOut:    tokOutPtr,
	})
	if err != nil {
		s.log.Warn().Err(err).Int64("signal_id", s.signalID).
			Msg("scorer: persist agent_evaluation failed (non-fatal)")
	}
}

func stringDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
