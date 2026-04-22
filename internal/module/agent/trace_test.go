package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

func TestHandleEvent_PersistAcceptedHookTrace(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%7", "work")
	m.tmux = fakeTmux
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "session-code-1", Name: "work"}},
	}
	m.registry.Register(&fakeAgentProvider{
		typeName: "codex",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{
				Valid:  true,
				Status: agentpkg.StatusRunning,
				Detail: map[string]any{"source": "test"},
			}
		},
	})

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(`{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"UserPromptSubmit",
		"raw_event":{"prompt":"hi"},
		"agent_type":"codex",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	page := waitForTraceChains(t, m, "work", 1)
	if got := len(page.Chains); got != 1 {
		t.Fatalf("len(page.Chains) = %d, want 1", got)
	}
	if page.Chains[0].TerminalStatus != "completed" {
		t.Fatalf("TerminalStatus = %q, want completed", page.Chains[0].TerminalStatus)
	}
	if page.Chains[0].LatestStepKind != "emit" {
		t.Fatalf("LatestStepKind = %q, want emit", page.Chains[0].LatestStepKind)
	}

	record, err := m.traces.GetChainRecord(page.Chains[0].ChainID)
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got := len(record.Steps); got < 5 {
		t.Fatalf("len(record.Steps) = %d, want >= 5", got)
	}
	if record.Steps[0].Kind != "trigger" {
		t.Fatalf("record.Steps[0].Kind = %q, want trigger", record.Steps[0].Kind)
	}
	if record.Steps[1].Kind != "verify" {
		t.Fatalf("record.Steps[1].Kind = %q, want verify", record.Steps[1].Kind)
	}
	if record.Steps[1].ParentStepID != record.Steps[0].StepID {
		t.Fatalf("verify ParentStepID = %q, want %q", record.Steps[1].ParentStepID, record.Steps[0].StepID)
	}
	if record.Steps[len(record.Steps)-1].Kind != "emit" {
		t.Fatalf("last step kind = %q, want emit", record.Steps[len(record.Steps)-1].Kind)
	}

	// Spec §3.5 phase is derived from each step's decision. Happy-path
	// decisions in this scenario: received/accepted → proposed,
	// created_frame → committed, projection_* → committed when changed /
	// proposed when unchanged, broadcasted → committed.
	wantPhase := map[string]string{
		"received":             "proposed",
		"accepted":             "proposed",
		"created_frame":        "committed",
		"updated_frame":        "committed",
		"deleted_frame":        "committed",
		"projection_changed":   "committed",
		"projection_unchanged": "proposed",
		"broadcasted":          "committed",
		"skipped":              "proposed",
	}
	for i, step := range record.Steps {
		if step.SourceKind != "hook" {
			t.Fatalf("step %d SourceKind = %q, want hook", i, step.SourceKind)
		}
		phase, ok := wantPhase[step.Decision]
		if !ok {
			t.Fatalf("step %d unexpected decision = %q", i, step.Decision)
		}
		if step.Phase != phase {
			t.Fatalf("step %d (%s/%s) Phase = %q, want %q", i, step.Kind, step.Decision, step.Phase, phase)
		}
		if step.Status != "success" {
			t.Fatalf("step %d Status = %q, want success", i, step.Status)
		}
		if step.ScenarioKey == "" {
			t.Fatalf("step %d ScenarioKey empty", i)
		}
		if step.Action == "" {
			t.Fatalf("step %d Action empty", i)
		}
		if step.Outcome == "" {
			t.Fatalf("step %d Outcome empty", i)
		}
		if string(step.DecisionPorts) != "[]" {
			t.Fatalf("step %d DecisionPorts = %s, want []", i, string(step.DecisionPorts))
		}
		if step.WatcherToken != nil {
			t.Fatalf("step %d WatcherToken = %v, want nil", i, step.WatcherToken)
		}
		// PR-1b-0: hook collector must populate the 11 new envelope fields.
		if step.TraceID != record.Chain.ChainID {
			t.Fatalf("step %d TraceID = %q, want chain id %q", i, step.TraceID, record.Chain.ChainID)
		}
		if step.OTelKind != "internal" {
			t.Fatalf("step %d OTelKind = %q, want internal", i, step.OTelKind)
		}
		if step.ReasonText == "" {
			t.Fatalf("step %d ReasonText empty", i)
		}
		if string(step.Attrs) != "{}" {
			t.Fatalf("step %d Attrs = %s, want {}", i, string(step.Attrs))
		}
		if string(step.InputRefs) != "[]" || string(step.OutputRefs) != "[]" || string(step.EvidenceRefs) != "[]" {
			t.Fatalf("step %d refs default = %s/%s/%s", i, string(step.InputRefs), string(step.OutputRefs), string(step.EvidenceRefs))
		}
		if step.StateBeforeRef != "" || step.StateAfterRef != "" {
			t.Fatalf("step %d state refs = %q/%q, want empty", i, step.StateBeforeRef, step.StateAfterRef)
		}
		if step.StartedAt == 0 || step.EndedAt == 0 {
			t.Fatalf("step %d started_at/ended_at = %d/%d, want non-zero", i, step.StartedAt, step.EndedAt)
		}
	}
}

func TestHandleEvent_VerifyRejectPersistsTerminalChain(t *testing.T) {
	m := newTestModule(t)
	origVerify := verifyEventFn
	verifyEventFn = func(_ *Module, _ EventRequest) verifyDecision {
		return verifyDecision{Accepted: false, Reason: "pid_not_in_pane_tree"}
	}
	t.Cleanup(func() {
		verifyEventFn = origVerify
	})

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(`{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"Stop",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}

	page := waitForTraceChains(t, m, "work", 1)
	if got := len(page.Chains); got != 1 {
		t.Fatalf("len(page.Chains) = %d, want 1", got)
	}
	if page.Chains[0].TerminalStatus != "completed" {
		t.Fatalf("TerminalStatus = %q, want completed", page.Chains[0].TerminalStatus)
	}
	if page.Chains[0].LatestDecision != "rejected" {
		t.Fatalf("LatestDecision = %q, want rejected", page.Chains[0].LatestDecision)
	}

	record, err := m.traces.GetChainRecord(page.Chains[0].ChainID)
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	if got := len(record.Steps); got != 2 {
		t.Fatalf("len(record.Steps) = %d, want 2", got)
	}
	if record.Steps[1].ParentStepID != record.Steps[0].StepID {
		t.Fatalf("verify ParentStepID = %q, want %q", record.Steps[1].ParentStepID, record.Steps[0].StepID)
	}
	// Verify step reflects rejection in outcome + phase; spec §3.5 line 511
	// is explicit that a pre-commit reject is still a successful observation
	// (status=success, outcome=rejected, phase=rejected).
	verify := record.Steps[1]
	if verify.Decision != "rejected" {
		t.Fatalf("verify Decision = %q, want rejected", verify.Decision)
	}
	if verify.Outcome != "rejected" {
		t.Fatalf("verify Outcome = %q, want rejected", verify.Outcome)
	}
	if verify.Phase != "rejected" {
		t.Fatalf("verify Phase = %q, want rejected", verify.Phase)
	}
	if verify.Status != "success" {
		t.Fatalf("verify Status = %q, want success (spec §3.5)", verify.Status)
	}
}

func TestHandleEvent_ErrorGuardEmitSkippedPersistsSkippedOutcome(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%7", "work")
	m.tmux = fakeTmux
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "session-code-1", Name: "work"}},
	}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{
				Valid:  true,
				Status: agentpkg.StatusRunning,
				Detail: map[string]any{"source": "test"},
			}
		},
	})
	// Seed the session into an error state so the non-clearing event triggers
	// the error-guard skipped emit path.
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusError
	m.mu.Unlock()

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(`{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"PostToolUse",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	page := waitForTraceChains(t, m, "work", 1)
	if got := len(page.Chains); got != 1 {
		t.Fatalf("len(page.Chains) = %d, want 1", got)
	}
	record, err := m.traces.GetChainRecord(page.Chains[0].ChainID)
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	var emit *store.TraceStep
	for i := range record.Steps {
		if record.Steps[i].Kind == "emit" {
			emit = &record.Steps[i]
			break
		}
	}
	if emit == nil {
		t.Fatalf("no emit step in chain; got kinds=%v", stepKinds(record.Steps))
	}
	if emit.Decision != "skipped" {
		t.Fatalf("emit Decision = %q, want skipped", emit.Decision)
	}
	if emit.Outcome != "skipped" {
		t.Fatalf("emit Outcome = %q, want skipped", emit.Outcome)
	}
	if emit.Phase != "proposed" {
		t.Fatalf("emit Phase = %q, want proposed (observation without commit)", emit.Phase)
	}
	if emit.Status != "success" {
		t.Fatalf("emit Status = %q, want success", emit.Status)
	}
}

// TestDeriveOutcomeFromDecision covers the explicit decision→outcome vocabulary
// map. The round-2 adversarial review called out that an unknown decision
// silently folded into "emitted" — we now return "" for unknown and add a
// dedicated negative-path assertion.
func TestDeriveOutcomeFromDecision(t *testing.T) {
	cases := map[string]string{
		// Verify lifecycle
		"received": "received",
		"accepted": "accepted",
		"rejected": "rejected",
		// Frame apply
		"created_frame": "created_frame",
		"updated_frame": "updated_frame",
		"deleted_frame": "deleted_frame",
		"skipped":       "skipped",
		// Projection
		"projection_changed":   "projection_changed",
		"projection_unchanged": "projection_unchanged",
		// Emit
		"broadcasted": "broadcasted",
		// Empty decision is treated as skipped by legacy callers.
		"": "skipped",
	}
	for decision, want := range cases {
		if got := deriveOutcomeFromDecision(decision); got != want {
			t.Errorf("deriveOutcomeFromDecision(%q) = %q, want %q", decision, got, want)
		}
	}
}

func TestDeriveOutcomeFromDecision_UnknownDecisionDoesNotMapToEmitted(t *testing.T) {
	if got := deriveOutcomeFromDecision("totally_bogus_decision"); got != "" {
		t.Fatalf("unknown decision outcome = %q, want empty (must not default to emitted)", got)
	}
}

// TestDerivePhaseFromDecision covers the per-decision phase classifier. Spec
// §3.5 Phase ∈ {proposed, committed, rejected}: verify/skipped/unchanged are
// observations without commit; frame upserts and successful emits commit.
func TestDerivePhaseFromDecision(t *testing.T) {
	cases := map[string]string{
		"received":             "proposed",
		"accepted":             "proposed",
		"rejected":             "rejected",
		"created_frame":        "committed",
		"updated_frame":        "committed",
		"deleted_frame":        "committed",
		"skipped":              "proposed",
		"projection_changed":   "committed",
		"projection_unchanged": "proposed",
		"broadcasted":          "committed",
		"":                     "proposed",
	}
	for decision, want := range cases {
		if got := derivePhaseFromDecision(decision); got != want {
			t.Errorf("derivePhaseFromDecision(%q) = %q, want %q", decision, got, want)
		}
	}
}

func TestHandleEvent_Emit_PhaseIsCommittedWhenBroadcasted(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%7", "work")
	m.tmux = fakeTmux
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "session-code-1", Name: "work"}},
	}
	m.registry.Register(&fakeAgentProvider{
		typeName: "codex",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	})

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(`{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"UserPromptSubmit",
		"raw_event":{"prompt":"hi"},
		"agent_type":"codex",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	page := waitForTraceChains(t, m, "work", 1)
	record, err := m.traces.GetChainRecord(page.Chains[0].ChainID)
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	emit := record.Steps[len(record.Steps)-1]
	if emit.Kind != "emit" {
		t.Fatalf("last step kind = %q, want emit", emit.Kind)
	}
	if emit.Decision != "broadcasted" {
		t.Fatalf("emit Decision = %q, want broadcasted", emit.Decision)
	}
	if emit.Phase != "committed" {
		t.Fatalf("emit Phase = %q, want committed", emit.Phase)
	}
}

func stepKinds(steps []store.TraceStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Kind
	}
	return out
}

func waitForTraceChains(t *testing.T, m *Module, tmuxSession string, want int) store.TraceChainPage {
	t.Helper()

	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		page, err := m.traces.ListChains(store.TraceListFilter{TmuxSession: tmuxSession, Limit: 10})
		if err != nil {
			t.Fatalf("ListChains: %v", err)
		}
		if len(page.Chains) >= want {
			return page
		}
		if time.Now().After(deadline) {
			return page
		}
		time.Sleep(10 * time.Millisecond)
	}
}
