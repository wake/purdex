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

	for i, step := range record.Steps {
		if step.SourceKind != "hook" {
			t.Fatalf("step %d SourceKind = %q, want hook", i, step.SourceKind)
		}
		if step.Phase != "committed" {
			t.Fatalf("step %d Phase = %q, want committed", i, step.Phase)
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
	// Verify step reflects rejection in outcome/status, not the hook-path defaults.
	verify := record.Steps[1]
	if verify.Decision != "rejected" {
		t.Fatalf("verify Decision = %q, want rejected", verify.Decision)
	}
	if verify.Outcome != "rejected" {
		t.Fatalf("verify Outcome = %q, want rejected", verify.Outcome)
	}
	if verify.Status != "failure" {
		t.Fatalf("verify Status = %q, want failure", verify.Status)
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
	if emit.Status != "success" {
		t.Fatalf("emit Status = %q, want success", emit.Status)
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
