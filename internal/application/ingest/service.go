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
	AccountEquityFallback decimal.Decimal                              // used in dry_run/testnet when no real equity reader
	SecretLoader          func(ctx context.Context) (string, error)   // loads webhook secret from DB on each call
}

type Service struct {
	cfg          Config
	pool         *pgxpool.Pool
	signalRepo   *store.SignalRepo
	strategyRepo *store.StrategyRepo
	posRepo      *store.VirtualPositionRepo
	systemRepo   *store.SystemStateRepo
	idempotency  *idempotency.Checker
	risk         *risk.Pipeline
	trade        *trade.Service
	notifier     notify.Notifier
	log          zerolog.Logger
}

func NewService(cfg Config, pool *pgxpool.Pool,
	signalRepo *store.SignalRepo, strategyRepo *store.StrategyRepo,
	posRepo *store.VirtualPositionRepo, systemRepo *store.SystemStateRepo,
	idem *idempotency.Checker, riskPipe *risk.Pipeline, tradeSvc *trade.Service,
	notifier notify.Notifier, log zerolog.Logger,
) *Service {
	return &Service{
		cfg: cfg, pool: pool,
		signalRepo: signalRepo, strategyRepo: strategyRepo,
		posRepo: posRepo, systemRepo: systemRepo,
		idempotency: idem, risk: riskPipe, trade: tradeSvc,
		notifier: notifier, log: log,
	}
}

// IngestResult tells the caller what happened (for HTTP responses, tests).
type IngestResult struct {
	SignalID    int64
	Decision    string // accepted | duplicate | risk_denied | invalid | disarmed
	RuleName    string // populated when decision == risk_denied
	Reason      string
	ActionTaken string // open_long | open_short | close | close_and_open_long | close_and_open_short | noop
}

// Ingest runs the full pipeline for a single webhook payload.
func (s *Service) Ingest(ctx context.Context, body []byte, clientIP net.IP) (*IngestResult, error) {
	// 1) Parse
	sig, err := sigpkg.Parse(body)
	if err != nil {
		// Record minimal signals row with decision=invalid
		_ = s.recordInvalid(ctx, body, clientIP, err.Error())
		return &IngestResult{Decision: "invalid", Reason: err.Error()}, nil
	}
	secret, err := s.cfg.SecretLoader(ctx)
	if err != nil {
		return nil, fmt.Errorf("secret loader: %w", err)
	}
	if sig.Secret != secret {
		_ = s.recordInvalid(ctx, body, clientIP, "secret mismatch")
		return &IngestResult{Decision: "invalid", Reason: "secret mismatch"}, nil
	}

	// 2) Idempotency
	dup, err := s.idempotency.Check(ctx, sig.StrategyID, sig.TVTimestampMs)
	if err != nil {
		return nil, fmt.Errorf("idempotency: %w", err)
	}
	if dup {
		// Insert signals row anyway with decision='duplicate' for audit (will hit UNIQUE → existing row)
		id, _, _ := s.signalRepo.Insert(ctx, s.pool, signalRowFrom(sig, clientIP, "duplicate", "lru hit"))
		return &IngestResult{SignalID: id, Decision: "duplicate"}, nil
	}

	// 3) Insert pending signal
	signalID, isDup, err := s.signalRepo.Insert(ctx, s.pool, signalRowFrom(sig, clientIP, "pending", ""))
	if err != nil {
		return nil, err
	}
	if isDup {
		// LRU missed but DB has it (e.g. after restart) → record duplicate
		return &IngestResult{SignalID: signalID, Decision: "duplicate"}, nil
	}

	// 4) Load context + run risk
	loadCtx, err := loadAll(ctx, s.pool, s.pool, s.strategyRepo, s.posRepo, s.systemRepo, sig.StrategyID)
	if err != nil {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "invalid", "load context: "+err.Error())
		return &IngestResult{SignalID: signalID, Decision: "invalid", Reason: err.Error()}, nil
	}
	if !loadCtx.Strategy.Enabled {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "risk_denied", "strategy disabled")
		return &IngestResult{SignalID: signalID, Decision: "risk_denied", Reason: "strategy disabled"}, nil
	}

	state, err := s.systemRepo.Get(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	if !state.Armed {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "disarmed", "system not armed")
		return &IngestResult{SignalID: signalID, Decision: "disarmed", Reason: "system not armed"}, nil
	}

	in := buildRiskInput(sig, loadCtx, s.cfg.AccountEquityFallback, clientIP)
	dec, err := s.risk.Run(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("risk pipeline: %w", err)
	}
	if !dec.Allowed {
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "risk_denied",
			fmt.Sprintf("%s: %s", dec.RuleName, dec.Reason))
		_ = s.notifier.Send(ctx, notify.Message{
			Title:    "Signal denied",
			Body:     fmt.Sprintf("%s denied by %s: %s", sig.StrategyID, dec.RuleName, dec.Reason),
			Severity: notify.SeverityWarn,
		})
		return &IngestResult{
			SignalID: signalID, Decision: "risk_denied",
			RuleName: dec.RuleName, Reason: dec.Reason,
		}, nil
	}

	// 5) Decide action
	action := position.Decide(loadCtx.CurrentPosition, sig.Kind)
	res := &IngestResult{SignalID: signalID, Decision: "accepted", ActionTaken: string(action)}

	switch action {
	case position.ActionNoOp:
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "noop")
		return res, nil

	case position.ActionOpenLong, position.ActionOpenShort:
		side := position.SideLong
		if action == position.ActionOpenShort {
			side = position.SideShort
		}
		if _, err := s.trade.OpenPosition(ctx, trade.OpenInput{
			Strategy:    loadCtx.Strategy,
			Side:        side,
			SignalPrice: sig.Price,
			SignalID:    signalID,
			TraceID:     sig.TraceID(),
		}); err != nil {
			_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "open failed: "+err.Error())
			_ = s.notifier.Send(ctx, notify.Message{
				Title: "Open failed", Body: err.Error(), Severity: notify.SeverityCritical,
			})
			return nil, err
		}
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", string(action))
		_ = s.notifier.Send(ctx, notify.Message{
			Title: "Open " + string(action),
			Body:  fmt.Sprintf("%s @ signal=%s", sig.StrategyID, sig.Price),
		})
		return res, nil

	case position.ActionClose:
		if loadCtx.CurrentPosition == nil {
			return res, nil
		}
		if _, err := s.trade.ClosePosition(ctx, closeInputFromLoad(loadCtx.CurrentPosition, sig, "signal")); err != nil {
			_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "close failed: "+err.Error())
			_ = s.notifier.Send(ctx, notify.Message{
				Title: "Close failed", Body: err.Error(), Severity: notify.SeverityCritical,
			})
			return nil, err
		}
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", "close")
		_ = s.notifier.Send(ctx, notify.Message{Title: "Closed", Body: sig.StrategyID})
		return res, nil

	case position.ActionCloseAndOpenLong, position.ActionCloseAndOpenShort:
		// Close first, then open opposite.
		if loadCtx.CurrentPosition != nil {
			if _, err := s.trade.ClosePosition(ctx, closeInputFromLoad(loadCtx.CurrentPosition, sig, "signal")); err != nil {
				return nil, err
			}
		}
		side := position.SideLong
		if action == position.ActionCloseAndOpenShort {
			side = position.SideShort
		}
		if _, err := s.trade.OpenPosition(ctx, trade.OpenInput{
			Strategy: loadCtx.Strategy, Side: side, SignalPrice: sig.Price,
			SignalID: signalID, TraceID: sig.TraceID(),
		}); err != nil {
			return nil, err
		}
		_ = s.signalRepo.UpdateDecision(ctx, s.pool, signalID, "accepted", string(action))
		_ = s.notifier.Send(ctx, notify.Message{Title: "Reverse " + string(action), Body: sig.StrategyID})
		return res, nil
	}
	return res, nil
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
