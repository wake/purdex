package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// fakeSessionProvider is defined in fakes_test.go (shared test fixture).

func newTestModule(t *testing.T) *Module {
	t.Helper()
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("open agent event store: %v", err)
	}
	t.Cleanup(func() { events.Close() })
	m := New(events)
	m.registry = agentpkg.NewRegistry()
	origVerify := verifyEventFn
	verifyEventFn = func(*Module, EventRequest) verifyDecision { return verifyDecision{Accepted: true} }
	t.Cleanup(func() { verifyEventFn = origVerify })
	origReadProcessInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1, ExePath: "/usr/local/bin/claude", Argv: []string{"claude"}}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origReadProcessInfo })
	origProcessStartTime := processStartTimeFn
	processStartTimeFn = func(pid int) (string, error) {
		return "Sun Apr 20 01:30:00 2026", nil
	}
	t.Cleanup(func() { processStartTimeFn = origProcessStartTime })
	origIsPidAlive := isPidAliveFn
	isPidAliveFn = func(pid int) bool { return true }
	t.Cleanup(func() { isPidAliveFn = origIsPidAlive })
	return m
}

func TestHandleEvent_StoresAndReturns(t *testing.T) {
	m := newTestModule(t)

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":36649,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"agent:lifecycle:start","raw_event":{"session_id":"abc"},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status field: want ok, got %s", resp["status"])
	}

	// Accepted v2 hooks should not keep dual-written legacy rows around.
	ev, err := m.events.Get("work")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev != nil {
		t.Fatalf("legacy event row should be cleared, got %+v", ev)
	}
}

func TestHandleEvent_BadJSON(t *testing.T) {
	m := newTestModule(t)

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandleEvent_RejectsLegacyPayload(t *testing.T) {
	m := newTestModule(t)

	body := `{"tmux_session":"work","event_name":"agent:lifecycle:stop","raw_event":{},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d (body: %s)", w.Code, w.Body.String())
	}

	ev, err := m.events.Get("work")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev != nil {
		t.Error("legacy payload should not be stored in DB")
	}
}

// TestHandleEvent_StoresAgentType verifies that accepted hooks project agent
// identity into frames instead of keeping a dual-written legacy row.
func TestHandleEvent_StoresAgentType(t *testing.T) {
	m := newTestModule(t)
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})

	body := `{"tmux_session":"dev","tmux_pane_id":"%9","sender_pid":99,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"Stop","raw_event":{},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	frames, err := m.frames.ListByPane("%9")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
	if frames[0].AgentType != "cc" {
		t.Errorf("agent_type: want cc, got %q", frames[0].AgentType)
	}
	ev, err := m.events.Get("dev")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev != nil {
		t.Fatalf("legacy event row should be cleared, got %+v", ev)
	}
}

func TestHandleEvent_AcceptedV2HookRemovesLegacyRow(t *testing.T) {
	m := newTestModule(t)
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})
	if err := m.events.Set("dev", "Stop", json.RawMessage(`{}`), "cc", 1); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	body := `{"tmux_session":"dev","tmux_pane_id":"%9","sender_pid":99,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"Stop","raw_event":{},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	ev, err := m.events.Get("dev")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev != nil {
		t.Fatalf("legacy event row should be cleared, got %+v", ev)
	}
}

func TestHandleEvent_RejectsUncertainV2Payload_WhenVerifyEnabled(t *testing.T) {
	m := newTestModule(t)
	verifyEventFn = defaultVerifyEvent
	m.tmux = tmux.NewFakeExecutor()

	body := `{"tmux_session":"dev","tmux_pane_id":"%9","sender_pid":99,"sender_uncertain":true,"event_name":"Stop","raw_event":{},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d (body: %s)", w.Code, w.Body.String())
	}

	ev, err := m.events.Get("dev")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev != nil {
		t.Fatal("uncertain payload should not be stored after verify")
	}
}

func TestHandleEvent_RejectedHookDoesNotOverwriteSession(t *testing.T) {
	m := newTestModule(t)
	verifyEventFn = func(*Module, EventRequest) verifyDecision {
		return verifyDecision{Reason: "identify_mismatch"}
	}

	if err := m.events.Set("work", "Stop", json.RawMessage(`{}`), "cc", 1); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":36649,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"Stop","raw_event":{},"agent_type":"codex"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d (body: %s)", w.Code, w.Body.String())
	}

	ev, err := m.events.Get("work")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev == nil {
		t.Fatal("seeded event disappeared")
	}
	if ev.AgentType != "cc" {
		t.Fatalf("agent_type = %q, want cc", ev.AgentType)
	}
}

func TestHandleEvent_ErrorGuardBlocksFrameMutation(t *testing.T) {
	m := newTestModule(t)
	m.currentStatus["work"] = agentpkg.StatusError
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	})

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":36649,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"PostToolUse","raw_event":{},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0", len(frames))
	}
}

// --- Bug 0: buildNormalized always includes Subagents field ---

func TestBuildNormalized_EmptySubagentsIsNotNil(t *testing.T) {
	m := newTestModule(t)

	result := agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
	normalized := m.buildNormalized("work", "UserPromptSubmit", "cc", 1, result)

	// Subagents must be a non-nil empty slice so JSON serializes as []
	if normalized.Subagents == nil {
		t.Fatal("Subagents should be non-nil empty slice, got nil")
	}
	if len(normalized.Subagents) != 0 {
		t.Errorf("Subagents length: want 0, got %d", len(normalized.Subagents))
	}

	// Verify JSON: field must be present as "subagents":[]
	data, _ := json.Marshal(normalized)
	if !strings.Contains(string(data), `"subagents":[]`) {
		t.Errorf("JSON should contain \"subagents\":[], got %s", string(data))
	}
}

func TestBuildNormalized_WithSubagents(t *testing.T) {
	m := newTestModule(t)
	m.mu.Lock()
	m.subagents["work"] = []string{"agent-1", "agent-2"}
	m.mu.Unlock()

	result := agentpkg.DeriveResult{Valid: true}
	normalized := m.buildNormalized("work", "SubagentStart", "cc", 1, result)
	if len(normalized.Subagents) != 2 {
		t.Fatalf("Subagents: want 2, got %d", len(normalized.Subagents))
	}
}

// --- Bug 0b: RenameSession transfers in-memory state ---

func TestRenameSession(t *testing.T) {
	m := newTestModule(t)
	m.mu.Lock()
	m.subagents["old-session"] = []string{"agent-1"}
	m.currentStatus["old-session"] = agentpkg.StatusRunning
	m.mu.Unlock()

	m.RenameSession("old-session", "new-session")

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.subagents["old-session"]; ok {
		t.Error("old-session should be removed from subagents")
	}
	if _, ok := m.currentStatus["old-session"]; ok {
		t.Error("old-session should be removed from currentStatus")
	}
	if subs := m.subagents["new-session"]; len(subs) != 1 || subs[0] != "agent-1" {
		t.Errorf("new-session subagents: want [agent-1], got %v", subs)
	}
	if m.currentStatus["new-session"] != agentpkg.StatusRunning {
		t.Errorf("new-session status: want running, got %s", m.currentStatus["new-session"])
	}
}

func TestRenameSession_NoOldData(t *testing.T) {
	m := newTestModule(t)
	// Should not panic or create entries when old name doesn't exist
	m.RenameSession("nonexistent", "new-name")

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.subagents["new-name"]; ok {
		t.Error("should not create subagents entry for non-existent old name")
	}
}

// --- Bug 0b: RenameSessionAtomic runs callback under lock ---

func TestRenameSessionAtomic_Success(t *testing.T) {
	m := newTestModule(t)
	m.mu.Lock()
	m.subagents["old-name"] = []string{"agent-1"}
	m.currentStatus["old-name"] = agentpkg.StatusRunning
	m.mu.Unlock()

	called := false
	err := m.RenameSessionAtomic("old-name", "new-name", func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("callback should be invoked")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if subs := m.subagents["new-name"]; len(subs) != 1 || subs[0] != "agent-1" {
		t.Errorf("subagents should be transferred to new-name, got %v", subs)
	}
	if _, ok := m.subagents["old-name"]; ok {
		t.Error("old-name entry should be removed")
	}
}

func TestRenameSessionAtomic_CallbackErrorSkipsTransfer(t *testing.T) {
	m := newTestModule(t)
	m.mu.Lock()
	m.subagents["old-name"] = []string{"agent-1"}
	m.mu.Unlock()

	wantErr := errStub("rename failed")
	err := m.RenameSessionAtomic("old-name", "new-name", func() error {
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("err: want %v, got %v", wantErr, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Transfer should NOT have happened
	if _, ok := m.subagents["new-name"]; ok {
		t.Error("subagents should NOT be transferred on callback error")
	}
	if _, ok := m.subagents["old-name"]; !ok {
		t.Error("old-name entry should remain on callback error")
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }

// --- Activity watch integration tests ---

func TestActivityWatch_YellowLightRecovery(t *testing.T) {
	m := newTestModule(t)

	fake := tmux.NewFakeExecutor()
	m.prober = probe.New(fake)
	m.prober.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool { return info.ExePath == "/usr/local/bin/claude" })
	m.prober.RegisterReadiness("cc", agentcc.NewReadinessChecker(fake))

	provider := &fakeAgentProvider{
		typeName: "cc",
		derive: func(eventName string, raw json.RawMessage) agentpkg.DeriveResult {
			if eventName == "Notification" {
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusWaiting}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	}
	m.registry.Register(provider)

	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "s1", Name: "work"}},
	}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}

	fake.SetPaneCommand("work:", "claude")
	fake.SetPaneContent("work:", "Allow  Deny")
	fake.SetPaneSessionName("%5", "work")

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":36649,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"Notification","raw_event":{"type":"notification","notification_type":"permission_prompt"},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	w := httptest.NewRecorder()
	m.handleEvent(w, req)

	m.mu.Lock()
	_, watching := m.activeWatchers["work"]
	m.mu.Unlock()
	if !watching {
		t.Fatal("expected active watcher after waiting status")
	}

	time.Sleep(100 * time.Millisecond)
	fake.SetPaneContent("work:", "⠋ Processing your request...")

	time.Sleep(700 * time.Millisecond)

	m.mu.Lock()
	_, stillWatching := m.activeWatchers["work"]
	status := m.currentStatus["work"]
	m.mu.Unlock()

	if stillWatching {
		t.Fatal("watcher should have stopped after activity detection")
	}
	if status != agentpkg.StatusRunning {
		t.Fatalf("expected status running after activity, got %s", status)
	}
}

// --- Task 9: GET /api/agent/{agent}/statusline/status ---

func TestHandleStatuslineStatus_UnknownAgent(t *testing.T) {
	m := newTestModule(t)
	// No provider registered — expect 404 "unknown agent"
	req := httptest.NewRequest("GET", "/api/agent/cc/statusline/status", nil)
	req.SetPathValue("agent", "cc")
	w := httptest.NewRecorder()
	m.handleStatuslineStatus(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandleStatuslineStatus_UnsupportedAgent(t *testing.T) {
	m := newTestModule(t)
	// Path value other than "cc" should be rejected before registry lookup.
	req := httptest.NewRequest("GET", "/api/agent/codex/statusline/status", nil)
	req.SetPathValue("agent", "codex")
	w := httptest.NewRecorder()
	m.handleStatuslineStatus(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandleStatuslineStatus_CC_Registered(t *testing.T) {
	m := newTestModule(t)
	// Real CC provider with nil deps — CheckStatusline only uses ccSettingsPath
	// + detectStatuslineMode, neither of which need prober/tmux/cfg.
	ccProvider := agentcc.NewProvider(nil, nil, nil, nil)
	m.registry.Register(ccProvider)

	req := httptest.NewRequest("GET", "/api/agent/cc/statusline/status", nil)
	req.SetPathValue("agent", "cc")
	w := httptest.NewRecorder()
	m.handleStatuslineStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", w.Code, w.Body.String())
	}
	var body agentpkg.StatuslineState
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.SettingsPath == "" {
		t.Errorf("expected settingsPath to be populated")
	}
	// Mode depends on host env (CC may or may not be installed). Just assert a valid value.
	switch body.Mode {
	case "none", "pdx", "wrapped", "unmanaged":
	default:
		t.Errorf("unexpected mode: %q", body.Mode)
	}
}

func TestActivityWatch_HookEventSupersedes(t *testing.T) {
	m := newTestModule(t)

	fake := tmux.NewFakeExecutor()
	m.prober = probe.New(fake)
	m.prober.RegisterIdentifier("cc", func(info agentpkg.ProcessInfo) bool { return info.ExePath == "/usr/local/bin/claude" })

	provider := &fakeAgentProvider{
		typeName: "cc",
		derive: func(eventName string, raw json.RawMessage) agentpkg.DeriveResult {
			switch eventName {
			case "Notification":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusWaiting}
			case "UserPromptSubmit":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	}
	m.registry.Register(provider)
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "s1", Name: "work"}},
	}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}

	fake.SetPaneCommand("work:", "claude")
	fake.SetPaneContent("work:", "Allow  Deny")
	fake.SetPaneSessionName("%5", "work")

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":36649,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"Notification","raw_event":{"type":"notification","notification_type":"permission_prompt"},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	w := httptest.NewRecorder()
	m.handleEvent(w, req)

	body2 := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":36649,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"UserPromptSubmit","raw_event":{},"agent_type":"cc"}`
	req2 := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body2))
	w2 := httptest.NewRecorder()
	m.handleEvent(w2, req2)

	m.mu.Lock()
	_, watching := m.activeWatchers["work"]
	m.mu.Unlock()

	if !watching {
		t.Fatal("watcher should remain active for running status after hook event")
	}
}

func TestActivityWatch_StartsForRunningStatus(t *testing.T) {
	m := newTestModule(t)

	fake := tmux.NewFakeExecutor()
	m.prober = probe.New(fake)
	fake.SetPaneSessionName("%5", "work")
	provider := &fakeAgentProvider{
		typeName: "codex",
		derive: func(eventName string, raw json.RawMessage) agentpkg.DeriveResult {
			if eventName == "UserPromptSubmit" {
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	}
	m.registry.Register(provider)

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":36649,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"UserPromptSubmit","raw_event":{},"agent_type":"codex"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	w := httptest.NewRecorder()
	m.handleEvent(w, req)

	m.mu.Lock()
	_, watching := m.activeWatchers["work"]
	m.mu.Unlock()
	if !watching {
		t.Fatal("expected active watcher after running status")
	}
}

func TestActivityWatch_ShellPromptDeadPidTriggersSweep(t *testing.T) {
	m := newTestModule(t)
	fake := tmux.NewFakeExecutor()
	fake.SetPaneSessionName("%5", "work")
	m.tmux = fake
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "s1", Name: "work"}},
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "codex",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "dead",
		Status:           agentpkg.StatusRunning,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	m.currentStatus["work"] = agentpkg.StatusRunning
	m.activeWatchers["work"] = "codex"
	origAlive := isPidAliveFn
	isPidAliveFn = func(pid int) bool { return false }
	t.Cleanup(func() { isPidAliveFn = origAlive })

	cb := m.onActivityDetected("work", "codex")
	cb("work:", probe.ActivitySignalShellPrompt)

	if got := m.currentStatus["work"]; got != "" {
		t.Fatalf("currentStatus = %q, want cleared", got)
	}
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0", len(frames))
	}
	if _, watching := m.activeWatchers["work"]; watching {
		t.Fatal("watcher should be cleared after sweep")
	}
}

// --- Task 10: POST /api/agent/{agent}/statusline/setup ---

func TestHandleStatuslineSetup_InstallPdx(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := newTestModule(t)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))

	body := strings.NewReader(`{"action":"install","mode":"pdx"}`)
	req := httptest.NewRequest("POST", "/api/agent/cc/statusline/setup", body)
	req.SetPathValue("agent", "cc")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleStatuslineSetup(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(data), "statusline-proxy") {
		t.Errorf("settings.json did not install statusline-proxy: %s", data)
	}
}

func TestHandleStatuslineSetup_InstallWrap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := newTestModule(t)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))

	body := strings.NewReader(`{"action":"install","mode":"wrap","inner":"ccstatusline --format compact"}`)
	req := httptest.NewRequest("POST", "/api/agent/cc/statusline/setup", body)
	req.SetPathValue("agent", "cc")
	w := httptest.NewRecorder()

	m.handleStatuslineSetup(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(data), "--inner 'ccstatusline --format compact'") {
		t.Errorf("wrap inner not properly embedded: %s", data)
	}
}

func TestHandleStatuslineSetup_InstallWrapMissingInner(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := newTestModule(t)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))

	body := strings.NewReader(`{"action":"install","mode":"wrap"}`)
	req := httptest.NewRequest("POST", "/api/agent/cc/statusline/setup", body)
	req.SetPathValue("agent", "cc")
	w := httptest.NewRecorder()

	m.handleStatuslineSetup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

func TestHandleStatuslineSetup_RemoveUnmanagedRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Pre-populate with unmanaged statusLine
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"),
		[]byte(`{"statusLine":{"type":"command","command":"ccstatusline"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	m := newTestModule(t)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))

	body := strings.NewReader(`{"action":"remove"}`)
	req := httptest.NewRequest("POST", "/api/agent/cc/statusline/setup", body)
	req.SetPathValue("agent", "cc")
	w := httptest.NewRecorder()

	m.handleStatuslineSetup(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("status %d, want 409", w.Code)
	}
}

func TestHandleStatuslineSetup_BadAction(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := newTestModule(t)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))

	body := strings.NewReader(`{"action":"uninstall"}`)
	req := httptest.NewRequest("POST", "/api/agent/cc/statusline/setup", body)
	req.SetPathValue("agent", "cc")
	w := httptest.NewRecorder()

	m.handleStatuslineSetup(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

// --- Task 11: POST /api/agent/status ---

func TestHandleAgentStatus_BroadcastsOnSessionMatch(t *testing.T) {
	m := newTestModule(t)
	fake := tmux.NewFakeExecutor()
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Name: "sess1", Code: "code-1"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	body := `{"tmux_session":"sess1","agent_type":"cc","raw_status":{"model":{"display_name":"Sonnet"}}}`
	req := httptest.NewRequest("POST", "/api/agent/status", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleAgentStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}
	// Drain the broadcast channel — expect exactly one agent.status event for code-1.
	select {
	case msg := <-sub.SendCh():
		var env struct {
			Type    string `json:"type"`
			Session string `json:"session"`
			Value   string `json:"value"`
		}
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatalf("unmarshal broadcast: %v", err)
		}
		if env.Type != "agent.status" {
			t.Errorf("broadcast type = %q, want agent.status", env.Type)
		}
		if env.Session != "code-1" {
			t.Errorf("broadcast session = %q, want code-1", env.Session)
		}
		// Value should be a JSON-encoded statusSnapshot with agent_type + status.
		if !strings.Contains(env.Value, `"agent_type":"cc"`) {
			t.Errorf("broadcast value missing agent_type: %s", env.Value)
		}
		if !strings.Contains(env.Value, `"display_name":"Sonnet"`) {
			t.Errorf("broadcast value missing raw_status: %s", env.Value)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timed out waiting for broadcast")
	}
}

func TestHandleAgentStatus_NoBroadcastOnUnknownSession(t *testing.T) {
	m := newTestModule(t)
	fake := tmux.NewFakeExecutor()
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	body := `{"tmux_session":"unknown","agent_type":"cc","raw_status":{}}`
	req := httptest.NewRequest("POST", "/api/agent/status", strings.NewReader(body))
	w := httptest.NewRecorder()

	m.handleAgentStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status %d, want 200", w.Code)
	}
	// No broadcast: channel should stay empty for a brief window.
	select {
	case msg := <-sub.SendCh():
		t.Errorf("unexpected broadcast: %s", msg)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestHandleAgentStatus_BadAgentType(t *testing.T) {
	m := newTestModule(t)
	body := `{"tmux_session":"x","agent_type":"codex","raw_status":{}}`
	req := httptest.NewRequest("POST", "/api/agent/status", strings.NewReader(body))
	w := httptest.NewRecorder()

	m.handleAgentStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", w.Code)
	}
}

// --- Task 12: statusline snapshot replay + cleared broadcast ---

func TestSendStatuslineSnapshot_ReplaysToSubscriber(t *testing.T) {
	m := newTestModule(t)
	m.snapshotMu.Lock()
	m.statusSnapshots = map[string]statusSnapshot{
		"code-a": {AgentType: "cc", Status: json.RawMessage(`{"model":{"display_name":"A"}}`)},
		"code-b": {AgentType: "cc", Status: json.RawMessage(`{"model":{"display_name":"B"}}`)},
	}
	m.snapshotMu.Unlock()
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor()}
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	m.sendStatuslineSnapshot(sub)

	// Read both expected replays (order not guaranteed — maps).
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case msg := <-sub.SendCh():
			var env struct {
				Type    string `json:"type"`
				Session string `json:"session"`
				Value   string `json:"value"`
			}
			if err := json.Unmarshal(msg, &env); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if env.Type != "agent.status" {
				t.Errorf("type = %q, want agent.status", env.Type)
			}
			seen[env.Session] = true
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("timed out after %d events", i)
		}
	}
	if !seen["code-a"] || !seen["code-b"] {
		t.Errorf("missing replay; seen=%v", seen)
	}
}

// --- Task 3 (#481): statusline self-test nonce detection in handleAgentStatus ---

// handlerTestEnv bundles a Module wired with a usable core.Core.  Used by the
// statusline self-test nonce tests: handleAgentStatus's test-nonce branch
// bypasses resolveSessionCode but still calls core.Events.Broadcast, so core
// must be non-nil.  No session provider is needed for the test-nonce path.
type handlerTestEnv struct {
	module *Module
}

func newHandlerTestEnv(t *testing.T) *handlerTestEnv {
	t.Helper()
	m := newTestModule(t)
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor()}
	return &handlerTestEnv{module: m}
}

func TestHandleAgentStatusTestNonceSignalsObserver(t *testing.T) {
	env := newHandlerTestEnv(t)
	nonce := "__pdx_test_0123abcd"
	ch := env.module.registerTestObserver(nonce)
	defer env.module.deregisterTestObserver(nonce)
	sub := env.module.core.Events.AddTestSubscriber()
	defer env.module.core.Events.RemoveTestSubscriber(sub)

	body := `{"tmux_session":"` + nonce + `","agent_type":"cc","raw_status":{"model":{"display_name":"pipeline-test"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/agent/status", strings.NewReader(body))
	w := httptest.NewRecorder()
	env.module.handleAgentStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}

	// The test handler signals once with the raw payload; broadcast is the
	// test handler's job, not handleAgentStatus's. Verify we got the signal
	// and that handleAgentStatus did NOT broadcast on its own.
	select {
	case sig := <-ch:
		if !strings.Contains(string(sig.raw), `"display_name":"pipeline-test"`) {
			t.Fatalf("signal raw = %s, want payload containing display_name:pipeline-test", string(sig.raw))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for test observer signal")
	}

	select {
	case msg := <-sub.SendCh():
		t.Fatalf("handleAgentStatus broadcast unexpectedly: %s", string(msg))
	default:
	}

	// Snapshot map must NOT hold the test nonce (display map is real sessions only).
	env.module.snapshotMu.RLock()
	_, persisted := env.module.statusSnapshots[nonce]
	env.module.snapshotMu.RUnlock()
	if persisted {
		t.Fatal("test nonce leaked into statusSnapshots")
	}
}

func TestHandleAgentStatusTestNonceWithoutObserverIsSilent(t *testing.T) {
	env := newHandlerTestEnv(t)
	body := `{"tmux_session":"__pdx_test_deadbeef","agent_type":"cc","raw_status":{}}`
	req := httptest.NewRequest(http.MethodPost, "/api/agent/status", strings.NewReader(body))
	w := httptest.NewRecorder()
	env.module.handleAgentStatus(w, req) // must not panic, must return 200
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
}

func TestHandleStatuslineSetup_RemoveBroadcastsCleared(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Seed settings.json with a pdx-mode statusline (so remove succeeds, not refused).
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"),
		[]byte(`{"statusLine":{"type":"command","command":"/opt/bin/pdx statusline-proxy"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	m := newTestModule(t)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: tmux.NewFakeExecutor()}
	// Seed a cached snapshot so we can assert it gets cleared.
	m.snapshotMu.Lock()
	m.statusSnapshots = map[string]statusSnapshot{"code-x": {AgentType: "cc", Status: json.RawMessage(`{}`)}}
	m.snapshotMu.Unlock()
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	body := strings.NewReader(`{"action":"remove"}`)
	req := httptest.NewRequest("POST", "/api/agent/cc/statusline/setup", body)
	req.SetPathValue("agent", "cc")
	w := httptest.NewRecorder()

	m.handleStatuslineSetup(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body: %s", w.Code, w.Body.String())
	}

	// Check: statusSnapshots cleared.
	m.snapshotMu.RLock()
	n := len(m.statusSnapshots)
	m.snapshotMu.RUnlock()
	if n != 0 {
		t.Errorf("snapshot map size = %d, want 0", n)
	}

	// Check: agent.status.cleared was broadcast. Drain up to 1 event.
	foundCleared := false
	for i := 0; i < 3; i++ {
		select {
		case msg := <-sub.SendCh():
			var env struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(msg, &env)
			if env.Type == "agent.status.cleared" {
				foundCleared = true
			}
		case <-time.After(50 * time.Millisecond):
			i = 3 // break
		}
	}
	if !foundCleared {
		t.Error("agent.status.cleared broadcast not seen")
	}
}
