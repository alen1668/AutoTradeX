package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"

	"github.com/lizhaojie/tvbot/internal/application/trade"
	"github.com/lizhaojie/tvbot/internal/domain/position"
	sigpkg "github.com/lizhaojie/tvbot/internal/domain/signal"
	"github.com/lizhaojie/tvbot/internal/idempotency"
	"github.com/lizhaojie/tvbot/internal/notify"
	"github.com/lizhaojie/tvbot/internal/risk"
	"github.com/lizhaojie/tvbot/internal/store"
)

type Config struct {
	AccountEquityFallback decimal.Decimal                            // used in dry_run/testnet when no real equity reader
	SecretLoader          func(ctx context.Context) (string, error) // loads webhook secret from DB on each call
}

type Service struct {
	cfg          Config
	pool         *pgxpool.Pool
	signalRepo   *store.SignalRepo
	settingsRepo *store.SettingsRepo
	strategyRepo *store.StrategyRepo
	posRepo      *store.VirtualPositionRepo
	systemRepo   *store.SystemStateRepo
	idempotency  *idempotency.Checker
	risk         *risk.Pipeline
	trade        *trade.Service
	notifier     notify.Notifier
	agent        *AgentHook // nil = agent layer not wired (behavior identical to pre-agent bot)
	log          zerolog.Logger
}

func NewService(cfg Config, pool *pgxpool.Pool,
	signalRepo *store.SignalRepo, settingsRepo *store.SettingsRepo,
	strategyRepo *store.StrategyRepo,
	posRepo *store.VirtualPositionRepo, systemRepo *store.SystemStateRepo,
	idem *idempotency.Checker, riskPipe *risk.Pipeline, tradeSvc *trade.Service,
	notifier notify.Notifier, agent *AgentHook, log zerolog.Logger,
) *Service {
	return &Service{
		cfg: cfg, pool: pool,
		signalRepo: signalRepo, settingsRepo: settingsRepo,
		strategyRepo: strategyRepo,
		posRepo: posRepo, systemRepo: systemRepo,
		idempotency: idem, risk: riskPipe, trade: tradeSvc,
		notifier: notifier, agent: agent, log: log,
	}
}

// IngestResult tells the caller what happened (for HTTP responses, tests).
type IngestResult struct {
	SignalID    int64
	Decision    string // accepted | duplicate | risk_denied | invalid | disarmed | pending
	RuleName    string // populated when decision == risk_denied
	Reason      string
	ActionTaken string // open_long | open_short | close | close_and_open_long | close_and_open_short | noop
}

// ReceiveResult is what Receive returns to the HTTP layer. It's a strict
// subset of IngestResult since the synchronous fast path can't yet know
// the action taken.
type ReceiveResult struct {
	SignalID   int64
	StrategyID string // populated only when Decision == "pending"
	Decision   string // "pending" | "duplicate" | "invalid"
	Reason     string
}

// Receive runs the fast path: parse, verify secret, idempotency check,
// insert a 'pending' row, return. The dispatcher (caller) then submits
// (StrategyID, SignalID) to a per-strategy worker that calls Process.
func (s *Service) Receive(ctx context.Context, body []byte, clientIP net.IP) (*ReceiveResult, error) {
	sig, err := sigpkg.Parse(body)
	if err != nil {
		_ = s.recordInvalid(ctx, body, clientIP, err.Error())
		return &ReceiveResult{Decision: "invalid", Reason: err.Error()}, nil
	}
	secret, err := s.cfg.SecretLoader(ctx)
	if err != nil {
		return nil, fmt.Errorf("secret loader: %w", err)
	}
	if sig.Secret != secret {
		_ = s.recordInvalid(ctx, body, clientIP, "secret mismatch")
		return &ReceiveResult{Decision: "invalid", Reason: "secret mismatch"}, nil
	}
	dup, err := s.idempotency.Check(ctx, sig.StrategyID, sig.TVTimestampMs)
	if err != nil {
		return nil, fmt.Errorf("idempotency: %w", err)
	}
	if dup {
		id, _, _ := s.signalRepo.Insert(ctx, s.pool, signalRowFrom(sig, clientIP, "duplicate", "lru hit"))
		return &ReceiveResult{SignalID: id, Decision: "duplicate"}, nil
	}
	signalID, isDup, err := s.signalRepo.Insert(ctx, s.pool, signalRowFrom(sig, clientIP, "pending", ""))
	if err != nil {
		return nil, err
	}
	if isDup {
		return &ReceiveResult{SignalID: signalID, Decision: "duplicate"}, nil
	}
	return &ReceiveResult{SignalID: signalID, StrategyID: sig.StrategyID, Decision: "pending"}, nil
}

// Process runs the slow path on an already-inserted pending signal. It's
// idempotent: if the row is no longer 'pending' (already processed by a
// concurrent worker, or finalized by an earlier run), it returns nil and
// does nothing.
func (s *Service) Process(ctx context.Context, signalID int64) error {
	row, err := s.signalRepo.GetByID(ctx, s.pool, signalID)
	if err != nil {
		return fmt.Errorf("load signal %d: %w", signalID, err)
	}
	if row.Decision != "pending" {
		return nil
	}
	sig, err := sigpkg.Parse(row.RawPayload)
	if err != nil {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "invalid", "re-parse: "+err.Error())
		return nil
	}
	return s.processSignal(ctx, sig, signalID, row.ClientIP)
}

// Ingest is a back-compat synchronous wrapper: Receive + Process in one
// call. Used by tests and any caller that wants the old synchronous API.
func (s *Service) Ingest(ctx context.Context, body []byte, clientIP net.IP) (*IngestResult, error) {
	rec, err := s.Receive(ctx, body, clientIP)
	if err != nil {
		return nil, err
	}
	if rec.Decision != "pending" {
		return &IngestResult{SignalID: rec.SignalID, Decision: rec.Decision, Reason: rec.Reason}, nil
	}
	if err := s.Process(ctx, rec.SignalID); err != nil {
		return nil, err
	}
	row, err := s.signalRepo.GetByID(ctx, s.pool, rec.SignalID)
	if err != nil {
		return nil, err
	}
	out := &IngestResult{
		SignalID: rec.SignalID,
		Decision: row.Decision,
		Reason:   row.DecisionReason,
	}
	// On accepted-success paths processSignal writes the action name into
	// decision_reason ("open_long", "close", "close_and_open_short",
	// "noop", etc.). Surface that as ActionTaken for callers that still
	// rely on the old IngestResult shape.
	if row.Decision == "accepted" {
		out.ActionTaken = row.DecisionReason
	}
	return out, nil
}

// processSignal executes the risk pipeline + trade action for a parsed,
// already-inserted pending signal. Splits out so Receive/Process can share
// the body without Ingest needing to be the only entry point.
func (s *Service) processSignal(ctx context.Context, sig *sigpkg.Signal, signalID int64, clientIP net.IP) error {
	loadCtx, err := loadAll(ctx, s.pool, s.pool, s.strategyRepo, s.posRepo, s.systemRepo, sig.StrategyID)
	if err != nil {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "invalid", "load context: "+err.Error())
		return nil
	}
	if !loadCtx.Strategy.Enabled {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "risk_denied", "strategy disabled")
		return nil
	}
	if loadCtx.StrategyArchived {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "risk_denied", "strategy archived")
		return nil
	}
	state, err := s.systemRepo.Get(ctx, s.pool)
	if err != nil {
		return err
	}
	if !state.Armed {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "disarmed", "system not armed")
		return nil
	}

	in := buildRiskInput(sig, loadCtx, s.cfg.AccountEquityFallback, clientIP)
	dec, err := s.risk.Run(ctx, in)
	if err != nil {
		return fmt.Errorf("risk pipeline: %w", err)
	}
	if !dec.Allowed {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "risk_denied",
			fmt.Sprintf("%s: %s", dec.RuleName, dec.Reason))
		_ = s.notifier.Send(ctx, notify.BuildDeniedMessage(sig.StrategyID, sig.Symbol, string(sig.Kind), dec.RuleName, dec.Reason))
		return nil
	}

	// === Agent 评分 (新增) ===
	// Always read latest settings so config changes take effect without
	// a restart. agent==nil OR agent_scorer_enabled=false → trade verdict
	// with -1 score (no rows written) — strict equivalence to pre-agent
	// behavior (test scenario A guards this contract).
	settings, settingsErr := s.settingsRepo.Get(ctx, s.pool)
	if settingsErr != nil {
		s.log.Warn().Err(settingsErr).Msg("agent: settings load failed; treating as scorer disabled")
		settings = &store.Settings{}
	}
	verdict := agentVerdict{Action: "trade", Score: -1, DryRun: settings.AgentScorerDryRun}
	if s.agent != nil {
		verdict = s.agent.evaluate(ctx, s.pool, s.log, s.notifier, settings, sig, loadCtx.Strategy, signalID)
	}
	if verdict.Score >= 0 {
		if err := s.signalRepo.UpdateAgentResult(ctx, s.pool, signalID, verdict.Score, verdict.Decision, verdict.DryRun); err != nil {
			s.log.Warn().Err(err).Int64("signal_id", signalID).Msg("agent: UpdateAgentResult failed (non-fatal)")
		}
	} else if verdict.Decision == "failed" {
		if err := s.signalRepo.UpdateAgentFailed(ctx, s.pool, signalID, verdict.DryRun); err != nil {
			s.log.Warn().Err(err).Int64("signal_id", signalID).Msg("agent: UpdateAgentFailed failed (non-fatal)")
		}
	}
	if verdict.Action == "abandon" {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "abandoned", abandonReason(verdict))
		_ = s.notifier.Send(ctx, notify.BuildAgentAbandonedMessage(
			sig.StrategyID, sig.Symbol, string(sig.Kind), verdict.Score, verdict.Reasoning))
		return nil
	}
	// === Agent 评分结束 ===

	action := position.Decide(loadCtx.CurrentPosition, sig.Kind)

	switch action {
	case position.ActionNoOp:
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "noop")
		return nil

	case position.ActionOpenLong, position.ActionOpenShort:
		side := position.SideLong
		if action == position.ActionOpenShort {
			side = position.SideShort
		}
		openRes, err := s.trade.OpenPosition(ctx, trade.OpenInput{
			Strategy:    loadCtx.Strategy,
			Side:        side,
			SignalPrice: sig.Price,
			SignalID:    signalID,
			TraceID:     sig.TraceID(),
		})
		if err != nil {
			_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "open failed: "+err.Error())
			_ = s.notifier.Send(ctx, notify.BuildOpenFailedMessage(sig.StrategyID, sig.Symbol, string(sig.Kind), err.Error()))
			return err
		}
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", string(action))
		_ = s.notifier.Send(ctx, notify.BuildOpenMessage(
			loadCtx.Strategy.ID, loadCtx.Strategy.Symbol, loadCtx.Strategy.Leverage,
			string(side), sig.Price, openRes.EntryFillPrice, openRes.Qty))
		return nil

	case position.ActionClose:
		if loadCtx.CurrentPosition == nil {
			_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "noop")
			return nil
		}
		pos := loadCtx.CurrentPosition
		closeRes, err := s.trade.ClosePosition(ctx, closeInputFromLoad(pos, sig, "signal"))
		if err != nil {
			_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "close failed: "+err.Error())
			_ = s.notifier.Send(ctx, notify.BuildCloseFailedMessage(sig.StrategyID, sig.Symbol, err.Error()))
			return err
		}
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "close")
		_ = s.notifier.Send(ctx, notify.BuildCloseMessage(
			loadCtx.Strategy.ID, loadCtx.Strategy.Symbol, string(pos.Side), notify.CloseReasonSignal,
			pos.EntryFillPrice, closeRes.ExitFillPrice, pos.Qty, closeRes.PnLUSDC))
		return nil

	case position.ActionCloseAndOpenLong, position.ActionCloseAndOpenShort:
		if loadCtx.CurrentPosition != nil {
			pos := loadCtx.CurrentPosition
			closeRes, err := s.trade.ClosePosition(ctx, closeInputFromLoad(pos, sig, "signal"))
			if err != nil {
				_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "reverse close failed: "+err.Error())
				_ = s.notifier.Send(ctx, notify.BuildCloseFailedMessage(sig.StrategyID, sig.Symbol, err.Error()))
				return err
			}
			_ = s.notifier.Send(ctx, notify.BuildCloseMessage(
				loadCtx.Strategy.ID, loadCtx.Strategy.Symbol, string(pos.Side), notify.CloseReasonSignal,
				pos.EntryFillPrice, closeRes.ExitFillPrice, pos.Qty, closeRes.PnLUSDC))
		}
		side := position.SideLong
		if action == position.ActionCloseAndOpenShort {
			side = position.SideShort
		}
		openRes, err := s.trade.OpenPosition(ctx, trade.OpenInput{
			Strategy: loadCtx.Strategy, Side: side, SignalPrice: sig.Price,
			SignalID: signalID, TraceID: sig.TraceID(),
		})
		if err != nil {
			_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "reverse open failed: "+err.Error())
			_ = s.notifier.Send(ctx, notify.BuildOpenFailedMessage(sig.StrategyID, sig.Symbol, string(sig.Kind), err.Error()))
			return err
		}
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", string(action))
		_ = s.notifier.Send(ctx, notify.BuildOpenMessage(
			loadCtx.Strategy.ID, loadCtx.Strategy.Symbol, loadCtx.Strategy.Leverage,
			string(side), sig.Price, openRes.EntryFillPrice, openRes.Qty))
		return nil
	}
	return nil
}

func (s *Service) recordInvalid(ctx context.Context, body []byte, ip net.IP, reason string) error {
	row := store.SignalRow{
		StrategyID: "_invalid_", Symbol: "_invalid_", Kind: "long", // placeholders that satisfy enum
		SignalPrice: decimal.NewFromInt(0), TVTimestampMs: time.Now().UnixMilli(),
		ReceivedAt: time.Now().UTC(), RawPayload: json.RawMessage(body),
		ClientIP: ip, Decision: "invalid", DecisionReason: reason, TraceID: "n/a",
	}
	_, _, err := s.signalRepo.Insert(ctx, s.pool, row)
	return err
}

func signalRowFrom(sig *sigpkg.Signal, ip net.IP, decision, reason string) store.SignalRow {
	return store.SignalRow{
		StrategyID: sig.StrategyID, Symbol: sig.Symbol, Kind: string(sig.Kind),
		SignalPrice: sig.Price, TVTimestampMs: sig.TVTimestampMs,
		ReceivedAt: time.Now().UTC(), RawPayload: sig.Raw, ClientIP: ip,
		Decision: decision, DecisionReason: reason, TraceID: sig.TraceID(),
	}
}

// closeInputFromLoad builds a CloseInput from an active virtual position and incoming signal.
func closeInputFromLoad(pos *position.VirtualPosition, sig *sigpkg.Signal, reason string) trade.CloseInput {
	return trade.CloseInput{
		VirtualPositionID: pos.ID, StrategyID: pos.StrategyID, Symbol: pos.Symbol,
		Side: pos.Side, Qty: pos.Qty, EntryFillPrice: pos.EntryFillPrice,
		StopOrderID: pos.StopOrderID, BackupStopOrderID: pos.BackupStopOrderID,
		TakeProfitOrderID: pos.TakeProfitOrderID, OpenedAt: pos.OpenedAt,
		EntrySignalPrice: pos.EntrySignalPrice, ExitSignalPrice: sig.Price,
		CloseReason: reason, TraceID: sig.TraceID(),
	}
}
