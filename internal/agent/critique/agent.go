package critique

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/lizhaojie/tvbot/internal/agent/scorer"
)

// Config controls one critique run. Sourced from settings.
type Config struct {
	Model       string
	WindowDays  int
	MinSample   int
	MaxPinned   int
	TimeoutMs   int
	DetailLimit int // default 200
	// AutoPinConfidence: off | high | medium | low | all. After a successful
	// LLM call, Agent.Run asks Store to pin all patterns whose confidence
	// matches. "off" preserves the original "human in loop" model.
	AutoPinConfidence string
}

// Store is the persistence side. Implemented by pg_store.go via CritiqueRepo.
type Store interface {
	Insert(ctx context.Context, c Critique, patterns []Pattern) (int64, error)
	// AutoPin marks pinned=true (pinned_by='auto') on patterns of the given
	// critique whose confidence matches the filter. confidence "all" matches
	// any. "off" should be filtered at the caller — implementations may
	// treat "off" as a no-op for safety.
	AutoPin(ctx context.Context, critiqueID int64, confidence string) error
}

// Agent runs the LLM-based critique reflection. One Agent is constructed
// at startup; Worker (worker.go) drives Agent.Run on cron / manual trigger.
type Agent struct {
	llm   scorer.LLMClient
	data  DataReader
	store Store
	cfg   Config
	log   zerolog.Logger
}

func NewAgent(llm scorer.LLMClient, data DataReader, store Store, cfg Config, log *zerolog.Logger) *Agent {
	z := zerolog.Nop()
	if log != nil {
		z = *log
	}
	if cfg.DetailLimit <= 0 {
		cfg.DetailLimit = 200
	}
	return &Agent{llm: llm, data: data, store: store, cfg: cfg, log: z}
}

// Run executes one critique cycle. Persists a row in every case (done /
// failed / insufficient_sample). Returns error only for systemic failures
// (DB query at the very front). Per-row LLM/parse failures are recorded
// as status=failed and return nil.
func (a *Agent) Run(ctx context.Context) error {
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -a.cfg.WindowDays)

	dets, err := a.data.Details(ctx, start, end, a.cfg.DetailLimit)
	if err != nil {
		a.log.Warn().Err(err).Msg("critique: details query failed")
		return err
	}
	if len(dets) < a.cfg.MinSample {
		_, _ = a.store.Insert(ctx, Critique{
			WindowStart: start, WindowEnd: end, SampleSize: len(dets),
			Model: a.cfg.Model, PromptHash: "",
			Status: StatusInsufficientSample,
		}, nil)
		return nil
	}

	aggs, err := a.data.Aggregates(ctx, start, end)
	if err != nil {
		a.log.Warn().Err(err).Msg("critique: aggregates query failed (continuing without)")
	}
	prev, _ := a.data.PreviousSummary(ctx) // best-effort

	promptText, hash, err := RenderPrompt(RenderInput{
		WindowStart: start, WindowEnd: end, SampleSize: len(dets),
		PreviousSummary: prev,
		Aggregates:      aggs,
		Details:         dets,
	})
	if err != nil {
		msg := "render: " + err.Error()
		_, _ = a.store.Insert(ctx, Critique{
			WindowStart: start, WindowEnd: end, SampleSize: len(dets),
			Model: a.cfg.Model, PromptHash: "",
			Status: StatusFailed, ErrorMessage: &msg,
		}, nil)
		return nil
	}

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(a.cfg.TimeoutMs)*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	resp, llmErr := a.llm.Complete(callCtx, scorer.CompleteRequest{
		Model:     a.cfg.Model,
		Prompt:    promptText,
		MaxTokens: 2048,
	})
	lat := int(time.Since(t0).Milliseconds())

	c := Critique{
		WindowStart: start, WindowEnd: end, SampleSize: len(dets),
		Model: a.cfg.Model, PromptHash: hash, LatencyMs: &lat,
	}
	if llmErr != nil {
		msg := "llm: " + llmErr.Error()
		c.Status = StatusFailed
		c.ErrorMessage = &msg
		_, _ = a.store.Insert(ctx, c, nil)
		return nil
	}
	c.TokenIn = &resp.TokenIn
	c.TokenOut = &resp.TokenOut

	parsed, perr := parsePatternSet(resp.Text)
	if perr != nil {
		msg := "parse: " + perr.Error()
		c.Status = StatusFailed
		c.ErrorMessage = &msg
		_, _ = a.store.Insert(ctx, c, nil)
		return nil
	}
	c.Status = StatusDone
	summaryCopy := parsed.Summary
	c.Summary = &summaryCopy
	patternsJSON, _ := json.Marshal(parsed)
	c.PatternsJSON = patternsJSON

	newID, err := a.store.Insert(ctx, c, parsed.Patterns)
	if err != nil {
		a.log.Warn().Err(err).Msg("critique: insert failed (logged, not retried this cycle)")
		return nil
	}
	// Auto-pin per Config. "off" / unset disables — preserves original
	// "human in loop" model. Failure is non-fatal — operator can still
	// pin manually via /eval/critique.
	if conf := a.cfg.AutoPinConfidence; conf != "" && conf != "off" {
		if err := a.store.AutoPin(ctx, newID, conf); err != nil {
			a.log.Warn().Err(err).Str("confidence", conf).Int64("critique_id", newID).
				Msg("critique: auto-pin failed (logged, operator can pin manually)")
		} else {
			a.log.Info().Str("confidence", conf).Int64("critique_id", newID).
				Msg("critique: auto-pinned patterns")
		}
	}
	return nil
}

// parsePatternSet uses scorer.ExtractJSON to strip any markdown fence,
// then unmarshals and validates structural invariants.
func parsePatternSet(raw string) (PatternSet, error) {
	body := scorer.ExtractJSON(raw)
	var ps PatternSet
	if err := json.Unmarshal([]byte(body), &ps); err != nil {
		return ps, err
	}
	if ps.Summary == "" && len(ps.Patterns) == 0 {
		return ps, fmt.Errorf("empty patterns and summary")
	}
	seen := map[string]struct{}{}
	for _, p := range ps.Patterns {
		if p.ID == "" {
			return ps, fmt.Errorf("pattern missing id")
		}
		if _, dup := seen[p.ID]; dup {
			return ps, fmt.Errorf("duplicate pattern id %q", p.ID)
		}
		seen[p.ID] = struct{}{}
		if p.Confidence != "high" && p.Confidence != "medium" && p.Confidence != "low" {
			return ps, fmt.Errorf("confidence %q not in {high,medium,low}", p.Confidence)
		}
	}
	return ps, nil
}
