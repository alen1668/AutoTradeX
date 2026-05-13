// Package exit implements the Exit Agent — periodic LLM-driven decisions
// over open virtual_positions. The package is split into pure layers:
// types (this file), parse (LLM JSON → Decision + validation), prompt
// (template render), agent (orchestration: gather context → LLM → parse),
// pg_store (Decision → DB row), settings_adapter (store.Settings →
// Config), worker (cron + concurrency + execution dispatch).
//
// Exit Agent is an additive layer over the trade pipeline. Failures here
// must NEVER block the main webhook → risk → scorer → trade chain. The
// existing dual SL on Binance is the last-line safety net.
package exit

import (
	"time"

	"github.com/shopspring/decimal"
)

// Action is one of the four discrete LLM outputs.
type Action string

const (
	ActionHold        Action = "hold"
	ActionTightenSL   Action = "tighten_sl"
	ActionTakePartial Action = "take_partial"
	ActionExitNow     Action = "exit_now"
)

// IsValid reports whether s is one of the four enum values.
func (a Action) IsValid() bool {
	switch a {
	case ActionHold, ActionTightenSL, ActionTakePartial, ActionExitNow:
		return true
	}
	return false
}

// Confidence ranks the LLM's certainty.
type Confidence string

const (
	ConfHigh   Confidence = "high"
	ConfMedium Confidence = "medium"
	ConfLow    Confidence = "low"
)

func (c Confidence) IsValid() bool {
	switch c {
	case ConfHigh, ConfMedium, ConfLow:
		return true
	}
	return false
}

// Rank returns 3=high, 2=medium, 1=low, 0=invalid. Used for "≥ threshold"
// comparisons in worker constraint checks.
func (c Confidence) Rank() int {
	switch c {
	case ConfHigh:
		return 3
	case ConfMedium:
		return 2
	case ConfLow:
		return 1
	}
	return 0
}

// Mode mirrors agent_exit_decisions.mode.
type Mode string

const (
	ModeShadow Mode = "shadow"
	ModeActive Mode = "active"
)

// ExecStatus mirrors agent_exit_decisions.execution_status.
type ExecStatus string

const (
	ExecSuccess           ExecStatus = "success"
	ExecSkippedConstraint ExecStatus = "skipped_constraint"
	ExecFailed            ExecStatus = "failed"
)

// PositionSnapshot is the state of one open virtual_position at decision
// time. Worker assembles this from VirtualPositionRepo + last-trade price.
type PositionSnapshot struct {
	VirtualPositionID int64
	StrategyID        string
	Symbol            string
	Side              string // "long" | "short"
	EntryFillPrice    decimal.Decimal
	CurrentPrice      decimal.Decimal
	Qty               decimal.Decimal
	UnrealizedPnLUSD  decimal.Decimal
	UnrealizedPnLPct  decimal.Decimal // signed; (current-entry)/entry * sign(side)
	PositionAge       time.Duration
	CurrentSLPrice    *decimal.Decimal // nil if no SL on book
	CurrentTPPrice    *decimal.Decimal
}

// HistoricalStats summarises the last N days of agent_evaluations
// outcome for this strategy×symbol pair. Used by prompt to ground LLM.
type HistoricalStats struct {
	SampleSize     int
	WinRate        decimal.Decimal // 0..1
	AvgWinPct      decimal.Decimal // signed positive
	AvgLossPct     decimal.Decimal // signed negative
	AvgHoldMinutes int
}

// MacroBundle is what the prompt template sees from the macro side.
// Workers read this from existing macrocontext.Reader.
type MacroBundle struct {
	Regime         string
	PerpFunding    string // pre-formatted, e.g. "+0.024%"
	PerpOIDelta    string // e.g. "+5.2% (24h)"
	UpcomingEvents string // joined string, "(无)" when empty
	NewsImpact     string // high|medium|low|none|"(无)"
	NewsSummary    string
}

// PinnedPattern is the Exit-prompt's view of a critique pinned pattern.
// Worker maps from critique repo rows.
type PinnedPattern struct {
	Title      string
	Suggestion string
}

// Input is what the agent needs to render the prompt and decide.
type Input struct {
	Position      PositionSnapshot
	KlineSnapshot string // pre-formatted multi-period text block
	Macro         MacroBundle
	Historical    HistoricalStats
	Pinned        []PinnedPattern
}

// Decision is the parsed + validated LLM output, ready to persist.
type Decision struct {
	Action          Action
	Confidence      Confidence
	Reasoning       string
	ProposedSLPrice *decimal.Decimal // required iff Action == TightenSL
	PartialPct      *decimal.Decimal // required iff Action == TakePartial; range (0, 0.5]
}

// Config is the runtime knobs surface used by Worker (and applies to
// Agent.Decide via cfg-derived constraints). Loaded by SettingsAdapter.
type Config struct {
	Enabled                  bool
	Mode                     Mode
	Model                    string
	ScanInterval             time.Duration
	MinPositionAge           time.Duration
	DecisionCooldown         time.Duration
	RequireConfidenceForExit Confidence
	HorizonMin               int
	MaxConcurrent            int
}
