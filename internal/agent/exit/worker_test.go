package exit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
)

type fakeReader struct {
	positions []PositionSnapshot
	err       error
}

func (f *fakeReader) ListOpen(_ context.Context) ([]PositionSnapshot, error) {
	return f.positions, f.err
}

type fakeContext struct{}

func (fakeContext) Build(_ context.Context, _ PositionSnapshot) (Input, error) {
	return Input{}, nil
}

type fakeDecider struct {
	d    Decision
	meta DecisionMeta
	err  error
	n    int
	mu   sync.Mutex
}

func (f *fakeDecider) Decide(_ context.Context, _ Input) (Decision, DecisionMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return f.d, f.meta, f.err
}

type fakeStore struct {
	rows []struct {
		Pos  PositionSnapshot
		Dec  Decision
		Meta DecisionMeta
		Mode Mode
	}
	mu sync.Mutex
	id int64
}

func (f *fakeStore) Insert(_ context.Context, p PositionSnapshot, d Decision, m DecisionMeta, mode Mode) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.id++
	f.rows = append(f.rows, struct {
		Pos  PositionSnapshot
		Dec  Decision
		Meta DecisionMeta
		Mode Mode
	}{p, d, m, mode})
	return f.id, nil
}

type fakeSettingsReader struct{ cfg Config }

func (f *fakeSettingsReader) Read(_ context.Context) (Config, error) { return f.cfg, nil }

type fakeCooldown struct{ last *time.Time }

func (f *fakeCooldown) LastDecisionAt(_ context.Context, _ int64) (*time.Time, error) {
	return f.last, nil
}

type recordingExecutor struct {
	calls []string
	err   error
	mu    sync.Mutex
}

func (r *recordingExecutor) TightenSL(_ context.Context, _ int64, _ decimal.Decimal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "tighten")
	return r.err
}
func (r *recordingExecutor) TakePartial(_ context.Context, _ int64, _ decimal.Decimal) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "partial")
	return r.err
}
func (r *recordingExecutor) ExitNow(_ context.Context, _ int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, "exit")
	return r.err
}

type fakeRecorder struct {
	mu    sync.Mutex
	calls []struct {
		ID     int64
		Status string
		Err    string
	}
}

func (f *fakeRecorder) SetExecution(_ context.Context, id int64, _ *time.Time, status, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, struct {
		ID     int64
		Status string
		Err    string
	}{id, status, errMsg})
	return nil
}

func basicShadowCfg() Config {
	return Config{
		Enabled: true, Mode: ModeShadow, Model: "claude-sonnet-4-6",
		ScanInterval:             5 * time.Minute,
		MinPositionAge:           time.Minute,
		DecisionCooldown:         15 * time.Minute,
		RequireConfidenceForExit: ConfHigh,
		HorizonMin:               60,
		MaxConcurrent:            4,
	}
}

func openPos(id int64, ageMin int) PositionSnapshot {
	return PositionSnapshot{
		VirtualPositionID: id, StrategyID: "s", Symbol: "ETHUSDC", Side: "long",
		EntryFillPrice: decimal.NewFromFloat(2300),
		CurrentPrice:   decimal.NewFromFloat(2330),
		Qty:            decimal.NewFromFloat(0.1),
		PositionAge:    time.Duration(ageMin) * time.Minute,
	}
}

func decPtr(f float64) *decimal.Decimal { d := decimal.NewFromFloat(f); return &d }

func TestWorker_ShadowWritesNoExecute(t *testing.T) {
	d := &fakeDecider{d: Decision{Action: ActionTightenSL, Confidence: ConfHigh, Reasoning: "r",
		ProposedSLPrice: decPtr(2310)}}
	st := &fakeStore{}
	exe := &recordingExecutor{}
	w := NewWorker(WorkerDeps{
		Reader:   &fakeReader{positions: []PositionSnapshot{openPos(1, 30)}},
		Ctx:      fakeContext{},
		Decider:  d, Store: st,
		Settings: &fakeSettingsReader{cfg: basicShadowCfg()},
		Cooldown: &fakeCooldown{},
		Executor: exe,
		Log:      zerolog.Nop(),
	})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(st.rows) != 1 {
		t.Fatalf("rows: %d", len(st.rows))
	}
	if st.rows[0].Mode != ModeShadow {
		t.Errorf("mode: %v", st.rows[0].Mode)
	}
	if len(exe.calls) != 0 {
		t.Errorf("shadow leaked to executor: %v", exe.calls)
	}
}

func TestWorker_ActiveDispatchesByAction(t *testing.T) {
	cfg := basicShadowCfg()
	cfg.Mode = ModeActive
	cases := []struct {
		name   string
		dec    Decision
		expect string
	}{
		{"tighten", Decision{Action: ActionTightenSL, Confidence: ConfHigh, Reasoning: "r", ProposedSLPrice: decPtr(2310)}, "tighten"},
		{"partial", Decision{Action: ActionTakePartial, Confidence: ConfMedium, Reasoning: "r", PartialPct: decPtr(0.5)}, "partial"},
		{"exit", Decision{Action: ActionExitNow, Confidence: ConfHigh, Reasoning: "r"}, "exit"},
		{"hold", Decision{Action: ActionHold, Confidence: ConfMedium, Reasoning: "r"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exe := &recordingExecutor{}
			w := NewWorker(WorkerDeps{
				Reader:   &fakeReader{positions: []PositionSnapshot{openPos(1, 30)}},
				Ctx:      fakeContext{},
				Decider:  &fakeDecider{d: tc.dec},
				Store:    &fakeStore{}, Settings: &fakeSettingsReader{cfg: cfg},
				Cooldown: &fakeCooldown{}, Executor: exe, Log: zerolog.Nop(),
			})
			_ = w.RunOnce(context.Background())
			got := ""
			if len(exe.calls) > 0 {
				got = exe.calls[0]
			}
			if got != tc.expect {
				t.Errorf("got %q, want %q", got, tc.expect)
			}
		})
	}
}

func TestWorker_ConstraintViolation_TightenSLLooser(t *testing.T) {
	cfg := basicShadowCfg()
	cfg.Mode = ModeActive
	pos := openPos(1, 30)
	sl := decimal.NewFromFloat(2320)
	pos.CurrentSLPrice = &sl // current SL is tighter than proposed
	exe := &recordingExecutor{}
	st := &fakeStore{}
	w := NewWorker(WorkerDeps{
		Reader: &fakeReader{positions: []PositionSnapshot{pos}}, Ctx: fakeContext{},
		Decider: &fakeDecider{d: Decision{Action: ActionTightenSL, Confidence: ConfHigh, Reasoning: "r",
			ProposedSLPrice: decPtr(2310)}}, // looser than 2320
		Store: st, Settings: &fakeSettingsReader{cfg: cfg},
		Cooldown: &fakeCooldown{}, Executor: exe, Log: zerolog.Nop(),
	})
	_ = w.RunOnce(context.Background())
	if len(exe.calls) != 0 {
		t.Errorf("looser SL should be rejected, executor called: %v", exe.calls)
	}
	if len(st.rows) != 1 {
		t.Fatalf("expect 1 row written")
	}
	if st.rows[0].Dec.Action != ActionHold {
		t.Errorf("expected rewrite to hold, got %v", st.rows[0].Dec.Action)
	}
	if !strings.Contains(st.rows[0].Dec.Reasoning, "constraint_violated") {
		t.Errorf("reasoning lacks constraint marker: %q", st.rows[0].Dec.Reasoning)
	}
}

func TestWorker_ConstraintViolation_ExitNowLowConf(t *testing.T) {
	cfg := basicShadowCfg()
	cfg.Mode = ModeActive
	cfg.RequireConfidenceForExit = ConfHigh
	exe := &recordingExecutor{}
	st := &fakeStore{}
	w := NewWorker(WorkerDeps{
		Reader: &fakeReader{positions: []PositionSnapshot{openPos(1, 30)}}, Ctx: fakeContext{},
		Decider: &fakeDecider{d: Decision{Action: ActionExitNow, Confidence: ConfMedium, Reasoning: "r"}},
		Store:   st, Settings: &fakeSettingsReader{cfg: cfg},
		Cooldown: &fakeCooldown{}, Executor: exe, Log: zerolog.Nop(),
	})
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(exe.calls) != 0 {
		t.Errorf("low-conf exit should be rejected, got %v", exe.calls)
	}
	if st.rows[0].Dec.Action != ActionHold {
		t.Errorf("expected rewrite to hold")
	}
}

func TestWorker_AgeFilterSkips(t *testing.T) {
	d := &fakeDecider{}
	w := NewWorker(WorkerDeps{
		Reader: &fakeReader{positions: []PositionSnapshot{openPos(1, 0)}}, // age=0 < min 60s
		Ctx:    fakeContext{}, Decider: d, Store: &fakeStore{},
		Settings: &fakeSettingsReader{cfg: basicShadowCfg()}, Cooldown: &fakeCooldown{},
		Executor: &recordingExecutor{}, Log: zerolog.Nop(),
	})
	_ = w.RunOnce(context.Background())
	if d.n != 0 {
		t.Errorf("decider should be skipped, called %d", d.n)
	}
}

func TestWorker_CooldownSkips(t *testing.T) {
	last := time.Now().Add(-1 * time.Minute) // < 15min cooldown
	d := &fakeDecider{}
	w := NewWorker(WorkerDeps{
		Reader: &fakeReader{positions: []PositionSnapshot{openPos(1, 30)}}, Ctx: fakeContext{},
		Decider: d, Store: &fakeStore{}, Settings: &fakeSettingsReader{cfg: basicShadowCfg()},
		Cooldown: &fakeCooldown{last: &last}, Executor: &recordingExecutor{}, Log: zerolog.Nop(),
	})
	_ = w.RunOnce(context.Background())
	if d.n != 0 {
		t.Errorf("cooldown should skip, called %d", d.n)
	}
}

func TestWorker_DisabledNoOp(t *testing.T) {
	d := &fakeDecider{}
	cfg := basicShadowCfg()
	cfg.Enabled = false
	w := NewWorker(WorkerDeps{
		Reader: &fakeReader{positions: []PositionSnapshot{openPos(1, 30)}}, Ctx: fakeContext{},
		Decider: d, Store: &fakeStore{}, Settings: &fakeSettingsReader{cfg: cfg},
		Cooldown: &fakeCooldown{}, Executor: &recordingExecutor{}, Log: zerolog.Nop(),
	})
	_ = w.RunOnce(context.Background())
	if d.n != 0 {
		t.Errorf("disabled should skip all, called %d", d.n)
	}
}

func TestWorker_LLMErrorWritesHoldRow(t *testing.T) {
	st := &fakeStore{}
	w := NewWorker(WorkerDeps{
		Reader: &fakeReader{positions: []PositionSnapshot{openPos(1, 30)}}, Ctx: fakeContext{},
		Decider: &fakeDecider{err: errors.New("503 upstream")},
		Store:   st, Settings: &fakeSettingsReader{cfg: basicShadowCfg()},
		Cooldown: &fakeCooldown{}, Executor: &recordingExecutor{}, Log: zerolog.Nop(),
	})
	_ = w.RunOnce(context.Background())
	if len(st.rows) != 1 || st.rows[0].Dec.Action != ActionHold {
		t.Errorf("LLM error should write hold row, got %+v", st.rows)
	}
	if !strings.Contains(st.rows[0].Dec.Reasoning, "llm_error") {
		t.Errorf("reasoning missing llm_error marker: %q", st.rows[0].Dec.Reasoning)
	}
}

func TestWorker_ActiveSuccessRecordsSuccess(t *testing.T) {
	cfg := basicShadowCfg()
	cfg.Mode = ModeActive
	rec := &fakeRecorder{}
	w := NewWorker(WorkerDeps{
		Reader: &fakeReader{positions: []PositionSnapshot{openPos(1, 30)}}, Ctx: fakeContext{},
		Decider: &fakeDecider{d: Decision{Action: ActionExitNow, Confidence: ConfHigh, Reasoning: "r"}},
		Store:   &fakeStore{}, Settings: &fakeSettingsReader{cfg: cfg},
		Cooldown: &fakeCooldown{}, Executor: &recordingExecutor{}, Recorder: rec, Log: zerolog.Nop(),
	})
	_ = w.RunOnce(context.Background())
	if len(rec.calls) != 1 || rec.calls[0].Status != "success" {
		t.Errorf("expected 1 success record, got %+v", rec.calls)
	}
}

func TestWorker_ActiveExecutorErrorRecordsFailed(t *testing.T) {
	cfg := basicShadowCfg()
	cfg.Mode = ModeActive
	rec := &fakeRecorder{}
	w := NewWorker(WorkerDeps{
		Reader: &fakeReader{positions: []PositionSnapshot{openPos(1, 30)}}, Ctx: fakeContext{},
		Decider: &fakeDecider{d: Decision{Action: ActionExitNow, Confidence: ConfHigh, Reasoning: "r"}},
		Store:   &fakeStore{}, Settings: &fakeSettingsReader{cfg: cfg},
		Cooldown: &fakeCooldown{},
		Executor: &recordingExecutor{err: errors.New("binance 5xx")},
		Recorder: rec, Log: zerolog.Nop(),
	})
	_ = w.RunOnce(context.Background())
	if len(rec.calls) != 1 || rec.calls[0].Status != "failed" {
		t.Errorf("expected 1 failed record, got %+v", rec.calls)
	}
}
