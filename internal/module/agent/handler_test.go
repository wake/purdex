package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
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
	return m
}

func TestHandleEvent_StoresAndReturns(t *testing.T) {
	m := newTestModule(t)

	body := `{"tmux_session":"work","event_name":"agent:lifecycle:start","raw_event":{"session_id":"abc"}}`
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

	// Verify stored in AgentEventStore
	ev, err := m.events.Get("work")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev == nil {
		t.Fatal("event not found in store")
	}
	if ev.EventName != "agent:lifecycle:start" {
		t.Errorf("event_name: want agent:lifecycle:start, got %s", ev.EventName)
	}
	if string(ev.RawEvent) != `{"session_id":"abc"}` {
		t.Errorf("raw_event: want {\"session_id\":\"abc\"}, got %s", string(ev.RawEvent))
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

func TestHandleEvent_MissingTmuxSession(t *testing.T) {
	m := newTestModule(t)

	body := `{"tmux_session":"","event_name":"agent:lifecycle:stop","raw_event":{}}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	// Spec: empty tmux_session is still OK (returns 200) but skips DB storage.
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

	// Verify NOT stored — empty tmux_session should be skipped.
	ev, err := m.events.Get("")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev != nil {
		t.Error("event with empty tmux_session should not be stored in DB, but was found")
	}
}

// TestHandleEvent_StoresAgentType verifies that agent_type from the request
// body is persisted and can be read back from the store.
func TestHandleEvent_StoresAgentType(t *testing.T) {
	m := newTestModule(t)

	body := `{"tmux_session":"dev","event_name":"Stop","raw_event":{},"agent_type":"cc"}`
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
	if ev == nil {
		t.Fatal("event not stored")
	}
	if ev.AgentType != "cc" {
		t.Errorf("agent_type: want cc, got %q", ev.AgentType)
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

// --- Bug 3: SubagentStart guard via events.Get ---

func TestHandleSubagentEvent_NoEntryStartIgnored(t *testing.T) {
	m := newTestModule(t)
	// No DB entry for "work" → session is unknown to the daemon

	result := agentpkg.DeriveResult{
		Valid:  true,
		Detail: map[string]any{"agent_id": "late-agent"},
	}
	m.handleSubagentEvent("work", "SubagentStart", result)

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.subagents["work"]; ok {
		t.Error("SubagentStart should be ignored when session has no DB entry")
	}
}

func TestHandleSubagentEvent_StartAfterClearIgnored(t *testing.T) {
	m := newTestModule(t)
	// Register a fake provider whose DeriveStatus returns StatusClear
	provider := &fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusClear}
		},
	}
	m.registry.Register(provider)
	_ = m.events.Set("work", "SessionEnd", json.RawMessage(`{}`), "cc", 1)

	result := agentpkg.DeriveResult{
		Valid:  true,
		Detail: map[string]any{"agent_id": "late-agent"},
	}
	m.handleSubagentEvent("work", "SubagentStart", result)

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.subagents["work"]; ok {
		t.Error("SubagentStart should be ignored when latest event is StatusClear")
	}
}

func TestHandleSubagentEvent_StartAcceptedWhenSessionActive(t *testing.T) {
	m := newTestModule(t)
	// Register a fake provider whose DeriveStatus returns StatusRunning
	provider := &fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	}
	m.registry.Register(provider)
	_ = m.events.Set("work", "UserPromptSubmit", json.RawMessage(`{}`), "cc", 1)

	result := agentpkg.DeriveResult{
		Valid:  true,
		Detail: map[string]any{"agent_id": "agent-1"},
	}
	m.handleSubagentEvent("work", "SubagentStart", result)

	m.mu.Lock()
	defer m.mu.Unlock()
	if subs := m.subagents["work"]; len(subs) != 1 || subs[0] != "agent-1" {
		t.Errorf("SubagentStart should be accepted for active session, got %v", subs)
	}
}

// Bug 2 A1: compact SessionStart edge case — DB has entry but currentStatus
// is empty (because compact returns Valid:false).  events.Get-based guard
// must accept SubagentStart in this case.
func TestHandleSubagentEvent_CompactSessionStartAccepted(t *testing.T) {
	m := newTestModule(t)
	provider := &fakeAgentProvider{
		typeName: "cc",
		derive: func(eventName string, _ json.RawMessage) agentpkg.DeriveResult {
			// Compact SessionStart returns Valid:false
			return agentpkg.DeriveResult{Valid: false}
		},
	}
	m.registry.Register(provider)
	_ = m.events.Set("work", "SessionStart", json.RawMessage(`{"source":"compact"}`), "cc", 1)
	// Note: m.currentStatus["work"] is NOT populated (Valid:false skipped)

	result := agentpkg.DeriveResult{
		Valid:  true,
		Detail: map[string]any{"agent_id": "agent-1"},
	}
	m.handleSubagentEvent("work", "SubagentStart", result)

	m.mu.Lock()
	defer m.mu.Unlock()
	if subs := m.subagents["work"]; len(subs) != 1 {
		t.Errorf("SubagentStart should be accepted for compact-started session, got %v", subs)
	}
}

// --- Bug 2 (refined): checkAliveAll uses IsAlive tiebreaker for orphans ---

func TestCheckAliveAll_OrphanNotInTmuxDeleted(t *testing.T) {
	m := newTestModule(t)
	provider := &fakeAgentProvider{typeName: "cc"}
	m.registry.Register(provider)
	_ = m.events.Set("dead-session", "UserPromptSubmit", json.RawMessage(`{}`), "cc", 1)

	m.mu.Lock()
	m.subagents["dead-session"] = []string{"agent-1"}
	m.currentStatus["dead-session"] = agentpkg.StatusRunning
	m.mu.Unlock()

	// Fake tmux with NO sessions → HasSession("dead-session") returns false
	fake := tmux.NewFakeExecutor()
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	m.checkAliveAll(sub)

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.subagents["dead-session"]; ok {
		t.Error("orphan not in tmux should be deleted")
	}
	if ev, _ := m.events.Get("dead-session"); ev != nil {
		t.Error("orphan DB entry should be deleted")
	}
}

func TestCheckAliveAll_OrphanStillInTmuxPreserved(t *testing.T) {
	m := newTestModule(t)
	provider := &fakeAgentProvider{typeName: "cc"}
	m.registry.Register(provider)
	_ = m.events.Set("transient-session", "UserPromptSubmit", json.RawMessage(`{}`), "cc", 1)

	m.mu.Lock()
	m.subagents["transient-session"] = []string{"agent-1"}
	m.currentStatus["transient-session"] = agentpkg.StatusRunning
	m.mu.Unlock()

	// Fake tmux HAS the session → HasSession returns true, but the
	// fakeSessionProvider omits it (simulating transient ListSessions hiccup)
	fake := tmux.NewFakeExecutor()
	fake.AddSession("transient-session", "/tmp")
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	m.checkAliveAll(sub)

	m.mu.Lock()
	defer m.mu.Unlock()
	if subs := m.subagents["transient-session"]; len(subs) != 1 {
		t.Error("orphan still in tmux should NOT be deleted (transient case)")
	}
	if ev, _ := m.events.Get("transient-session"); ev == nil {
		t.Error("orphan still in tmux should preserve DB entry")
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
