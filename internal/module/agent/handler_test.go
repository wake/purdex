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
	agentcodex "github.com/wake/purdex/internal/agent/codex"
	"github.com/wake/purdex/internal/agent/opencode"
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
	if m.traceSink != nil {
		t.Cleanup(func() { m.traceSink.Close() })
	}
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

func TestHandleEvent_OpenCodeSessionEndClearsErrorState(t *testing.T) {
	m := newTestModule(t)
	m.registry.Register(&fakeAgentProvider{
		typeName: "opencode",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			switch event {
			case "SessionEnd":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusClear}
			default:
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
			}
		},
	})

	for _, body := range []string{
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionStart","raw_event":{},"agent_type":"opencode"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionEnd","raw_event":{},"agent_type":"opencode"}`,
	} {
		if strings.Contains(body, `"SessionEnd"`) {
			m.currentStatus["work"] = agentpkg.StatusError
		}
		req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		m.handleEvent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	}

	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0", len(frames))
	}
	if got := m.currentStatus["work"]; got != "" {
		t.Fatalf("currentStatus = %q, want cleared", got)
	}
}

func TestHandleEvent_OpenCodeStopDoesNotClearError(t *testing.T) {
	m := newTestModule(t)
	m.currentStatus["work"] = agentpkg.StatusError
	m.registry.Register(&fakeAgentProvider{
		typeName: "opencode",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"Stop","raw_event":{},"agent_type":"opencode"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := m.currentStatus["work"]; got != agentpkg.StatusError {
		t.Fatalf("currentStatus = %q, want error", got)
	}
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0", len(frames))
	}
}

func TestHandleEvent_OpenCodeValidSubagentBroadcasts(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(&fakeAgentProvider{
		typeName: "opencode",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			switch event {
			case "SessionStart":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
			case "SubagentStart":
				return agentpkg.DeriveResult{Valid: true, Detail: map[string]any{"agent_id": "call-1", "agent_type": "Explore"}}
			default:
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
			}
		},
	})
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	for _, body := range []string{
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionStart","raw_event":{},"agent_type":"opencode"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SubagentStart","raw_event":{},"agent_type":"opencode"}`,
	} {
		req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		m.handleEvent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	}

	select {
	case <-sub.SendCh():
		// SessionStart broadcast
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for SessionStart broadcast")
	}

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
		if env.Type != "hook" {
			t.Fatalf("broadcast type = %q, want hook", env.Type)
		}
		if env.Session != "code-work" {
			t.Fatalf("broadcast session = %q, want code-work", env.Session)
		}
		if !strings.Contains(env.Value, `"raw_event_name":"SubagentStart"`) {
			t.Fatalf("broadcast value missing raw_event_name: %s", env.Value)
		}
		if !strings.Contains(env.Value, `"id":"call-1"`) {
			t.Fatalf("broadcast value missing subagent id=call-1: %s", env.Value)
		}
		// Type is canonical agent family (opencode), NOT opencode's per-subagent
		// sub-variant from detail.agent_type ("Explore"). See Phase 2 PR-2a
		// round-1 codex P2: SubagentRef.Type should stay stable for
		// agent-family lookup (SPA color table etc).
		if !strings.Contains(env.Value, `"type":"opencode"`) {
			t.Fatalf("broadcast value missing subagent type=opencode: %s", env.Value)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for SubagentStart broadcast")
	}
}

// HB1 (Lights Phase 2 plan §2.8) — the WS broadcast payload for a native cc
// SubagentStart carries SubagentRef objects (id + type + started_at) not
// bare id strings, and native refs omit is_proxy (omitempty).
func TestHandleEvent_BroadcastPayloadCarriesSubagentRefs(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			switch event {
			case "SessionStart":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
			case "SubagentStart":
				return agentpkg.DeriveResult{Valid: true, Detail: map[string]any{"agent_id": "sub-1"}}
			default:
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
			}
		},
	})
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	for _, body := range []string{
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionStart","raw_event":{},"agent_type":"cc"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SubagentStart","raw_event":{"agent_id":"sub-1"},"agent_type":"cc"}`,
	} {
		req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		m.handleEvent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	}

	// Drain the SessionStart broadcast.
	select {
	case <-sub.SendCh():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for SessionStart broadcast")
	}

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
		// Decode the inner normalized event payload.
		var payload struct {
			Subagents []map[string]any `json:"subagents"`
		}
		if err := json.Unmarshal([]byte(env.Value), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if len(payload.Subagents) != 1 {
			t.Fatalf("subagents len = %d, want 1 (payload=%s)", len(payload.Subagents), env.Value)
		}
		ref := payload.Subagents[0]
		if ref["type"] != "cc" {
			t.Errorf("subagents[0].type = %v, want cc", ref["type"])
		}
		startedAt, ok := ref["started_at"].(float64)
		if !ok || startedAt == 0 {
			t.Errorf("subagents[0].started_at should be a non-zero number, got %v", ref["started_at"])
		}
		if _, hasProxy := ref["is_proxy"]; hasProxy {
			t.Errorf("subagents[0] should not contain is_proxy for native ref: %v", ref)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for SubagentStart broadcast")
	}
}

func TestHandleEvent_OpenCodeMalformedSubagentDoesNotBroadcast(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(&fakeAgentProvider{
		typeName: "opencode",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			switch event {
			case "SessionStart":
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
			case "SubagentStart":
				return agentpkg.DeriveResult{Valid: true, Detail: map[string]any{"agent_type": "Explore"}}
			default:
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
			}
		},
	})
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	for _, body := range []string{
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionStart","raw_event":{},"agent_type":"opencode"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SubagentStart","raw_event":{},"agent_type":"opencode"}`,
	} {
		req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		m.handleEvent(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
	}

	select {
	case <-sub.SendCh():
		// SessionStart broadcast
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for SessionStart broadcast")
	}

	select {
	case msg := <-sub.SendCh():
		t.Fatalf("unexpected malformed subagent broadcast: %s", string(msg))
	case <-time.After(50 * time.Millisecond):
		// expected
	}

	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
	if len(frames[0].Subagents) != 0 {
		t.Fatalf("frame subagents = %#v, want empty", frames[0].Subagents)
	}
	if got := m.subagents["work"]; len(got) != 0 {
		t.Fatalf("in-memory subagents = %#v, want empty", got)
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
	m.subagents["work"] = []agentpkg.SubagentRef{
		{ID: "agent-1", Type: "cc"},
		{ID: "agent-2", Type: "cc"},
	}
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
	m.subagents["old-session"] = []agentpkg.SubagentRef{{ID: "agent-1", Type: "cc"}}
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
	if subs := m.subagents["new-session"]; len(subs) != 1 || subs[0].ID != "agent-1" {
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
	m.subagents["old-name"] = []agentpkg.SubagentRef{{ID: "agent-1", Type: "cc"}}
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
	if subs := m.subagents["new-name"]; len(subs) != 1 || subs[0].ID != "agent-1" {
		t.Errorf("subagents should be transferred to new-name, got %v", subs)
	}
	if _, ok := m.subagents["old-name"]; ok {
		t.Error("old-name entry should be removed")
	}
}

func TestRenameSessionAtomic_CallbackErrorSkipsTransfer(t *testing.T) {
	m := newTestModule(t)
	m.mu.Lock()
	m.subagents["old-name"] = []agentpkg.SubagentRef{{ID: "agent-1", Type: "cc"}}
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

func TestHandleHookStatus_OpenCodeRegistered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := newTestModule(t)
	m.registry.Register(opencode.NewProvider())

	req := httptest.NewRequest("GET", "/api/hooks/opencode/status", nil)
	req.SetPathValue("agent", "opencode")
	w := httptest.NewRecorder()

	m.handleHookStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", w.Code, w.Body.String())
	}
	var body agentpkg.HookStatus
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Installed {
		t.Fatalf("expected not installed by default, got %+v", body)
	}
	if len(body.Issues) == 0 {
		t.Fatalf("expected issues for missing plugin, got %+v", body)
	}
}

func TestHandleHookSetup_OpenCodeInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := newTestModule(t)
	m.registry.Register(opencode.NewProvider())

	body := strings.NewReader(`{"action":"install"}`)
	req := httptest.NewRequest("POST", "/api/hooks/opencode/setup", body)
	req.SetPathValue("agent", "opencode")
	w := httptest.NewRecorder()

	m.handleHookSetup(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", w.Code, w.Body.String())
	}
	pluginPath := filepath.Join(home, ".config", "opencode", "plugins", "pdx-agent-hooks.js")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Fatalf("expected plugin install, stat err=%v", err)
	}
	var bodyStatus agentpkg.HookStatus
	if err := json.NewDecoder(w.Body).Decode(&bodyStatus); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bodyStatus.Installed {
		t.Fatalf("expected installed status, got %+v", bodyStatus)
	}
	if !bodyStatus.Events["SubagentStart"].Installed {
		t.Fatalf("expected SubagentStart installed, got %+v", bodyStatus.Events)
	}
}

func TestHandleDetect_IncludesOpenCode(t *testing.T) {
	binDir := t.TempDir()
	for name, content := range map[string]string{
		"opencode": "#!/bin/sh\necho 1.2.3\n",
	} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)

	m := newTestModule(t)
	req := httptest.NewRequest("GET", "/api/agents/detect", nil)
	w := httptest.NewRecorder()

	m.handleDetect(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", w.Code, w.Body.String())
	}
	var body map[string]struct {
		Installed bool   `json:"installed"`
		Path      string `json:"path"`
		Version   string `json:"version"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body["opencode"].Installed {
		t.Fatalf("expected opencode detected, got %+v", body)
	}
	if body["opencode"].Version != "1.2.3" {
		t.Fatalf("opencode version = %q, want 1.2.3", body["opencode"].Version)
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

func TestHandleAgentStatusTestNonceSignalsAndBroadcastsWhenObserverPresent(t *testing.T) {
	env := newHandlerTestEnv(t)
	nonce := "__pdx_test_0123abcd"
	obs := env.module.registerTestObserver(nonce)
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

	got := make([]testStage, 0, 2)
	deadline := time.After(500 * time.Millisecond)
	for len(got) < 2 {
		select {
		case stage := <-obs.stages:
			got = append(got, stage)
		case <-deadline:
			t.Fatalf("timed out waiting for test stages; got=%v", got)
		}
	}
	if got[0] != testStageReceived || got[1] != testStageBroadcast {
		t.Fatalf("stage sequence = %v, want [received broadcast]", got)
	}

	select {
	case msg := <-sub.SendCh():
		if !strings.Contains(string(msg), `"type":"agent.status"`) || !strings.Contains(string(msg), `"session":"`+nonce+`"`) {
			t.Fatalf("unexpected broadcast: %s", string(msg))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for test-nonce broadcast")
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

func TestHandleAgentStatusTestPrefixWithoutObserverFallsThroughToProduction(t *testing.T) {
	env := newHandlerTestEnv(t)
	env.module.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-1", Name: "__pdx_test_real"}}}
	sub := env.module.core.Events.AddTestSubscriber()
	defer env.module.core.Events.RemoveTestSubscriber(sub)

	body := `{"tmux_session":"__pdx_test_real","agent_type":"cc","raw_status":{"model":{"display_name":"prod-like"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/agent/status", strings.NewReader(body))
	w := httptest.NewRecorder()
	env.module.handleAgentStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}

	select {
	case msg := <-sub.SendCh():
		if !strings.Contains(string(msg), `"type":"agent.status"`) || !strings.Contains(string(msg), `"session":"code-1"`) {
			t.Fatalf("unexpected broadcast: %s", string(msg))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for production-path status broadcast")
	}

	env.module.snapshotMu.RLock()
	_, persisted := env.module.statusSnapshots["code-1"]
	env.module.snapshotMu.RUnlock()
	if !persisted {
		t.Fatal("expected production-path payload to persist in statusSnapshots")
	}
}

// --- Phase 1: Catalog miss (Valid=false → early-return + trace) ---

// catalogMissModule wires up a Module + cc provider + tmux + sessions + core
// in the minimum configuration required to exercise the catalog-miss path
// end-to-end (including trace persistence and broadcast suppression).
func catalogMissModule(t *testing.T) *Module {
	t.Helper()
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.prober = probe.New(fakeTmux)
	m.registry.Register(agentcc.NewProvider(nil, nil, nil, nil))
	return m
}

func catalogMissRequest() *http.Request {
	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":36649,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"FutureMysteryEvent","raw_event":{"foo":"bar"},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// H1: catalog miss returns 200 with explicit reason and the trace chain
// records a verify-kind step with decision=skipped, reason=event_not_in_catalog.
// It must NOT contain frame / projection / emit steps.
func TestHandleEvent_CatalogMiss_WritesTraceAndReturnsOK(t *testing.T) {
	m := catalogMissModule(t)
	w := httptest.NewRecorder()
	m.handleEvent(w, catalogMissRequest())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s), want 200", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("status field = %q, want ok", resp["status"])
	}
	if resp["reason"] != "event_not_in_catalog" {
		t.Errorf("reason field = %q, want event_not_in_catalog", resp["reason"])
	}

	page := waitForTraceChains(t, m, "work", 1)
	if len(page.Chains) != 1 {
		t.Fatalf("len(page.Chains) = %d, want 1", len(page.Chains))
	}
	chain := page.Chains[0]
	if chain.TerminalStatus != "completed" {
		t.Errorf("TerminalStatus = %q, want completed", chain.TerminalStatus)
	}
	if chain.TerminalReason != "event_not_in_catalog" {
		t.Errorf("TerminalReason = %q, want event_not_in_catalog", chain.TerminalReason)
	}
	if chain.LatestStepKind != "verify" {
		t.Errorf("LatestStepKind = %q, want verify (catalog miss reuses verify kind)", chain.LatestStepKind)
	}
	if chain.LatestDecision != "skipped" {
		t.Errorf("LatestDecision = %q, want skipped", chain.LatestDecision)
	}

	record, err := m.traces.GetChainRecord(chain.ChainID)
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	// Expect exactly: trigger + verify(accepted) + verify(skipped, catalog).
	// Frame / projection / emit MUST NOT appear.
	for _, step := range record.Steps {
		switch step.Kind {
		case "frame", "projection", "emit":
			t.Errorf("unexpected step kind %q in catalog-miss chain (steps=%+v)", step.Kind, record.Steps)
		}
	}
	// Find the catalog-miss step explicitly.
	var foundCatalog bool
	for _, step := range record.Steps {
		if step.Kind == "verify" && step.Decision == "skipped" && step.Reason == "event_not_in_catalog" {
			foundCatalog = true
		}
	}
	if !foundCatalog {
		t.Errorf("no verify-step with decision=skipped reason=event_not_in_catalog (steps=%+v)", record.Steps)
	}
}

// H2: catalog miss must not mutate currentStatus / subagents / broadcast.
func TestHandleEvent_CatalogMiss_NoStatusUpdate(t *testing.T) {
	m := catalogMissModule(t)
	// Seed pre-existing state to verify it's untouched.
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusRunning
	m.subagents["work"] = []agentpkg.SubagentRef{{ID: "existing-sub", Type: "cc"}}
	m.mu.Unlock()

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	w := httptest.NewRecorder()
	m.handleEvent(w, catalogMissRequest())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	m.mu.Lock()
	gotStatus := m.currentStatus["work"]
	gotSubs := append([]agentpkg.SubagentRef(nil), m.subagents["work"]...)
	m.mu.Unlock()
	if gotStatus != agentpkg.StatusRunning {
		t.Errorf("currentStatus = %q, want running (catalog miss must not mutate)", gotStatus)
	}
	if len(gotSubs) != 1 || gotSubs[0].ID != "existing-sub" {
		t.Errorf("subagents = %v, want [existing-sub]", gotSubs)
	}

	select {
	case msg := <-sub.SendCh():
		t.Errorf("unexpected broadcast on catalog miss: %s", string(msg))
	case <-time.After(50 * time.Millisecond):
		// expected — no broadcast
	}
}

// H3: catalog miss must not start an activity watcher.
func TestHandleEvent_CatalogMiss_NoActivityWatch(t *testing.T) {
	m := catalogMissModule(t)
	w := httptest.NewRecorder()
	m.handleEvent(w, catalogMissRequest())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	m.mu.Lock()
	_, watching := m.activeWatchers["work"]
	m.mu.Unlock()
	if watching {
		t.Error("catalog miss should not start an activity watcher")
	}
}

// H4: when provider returns Valid=false with a non-empty Reason (known event
// with unmappable payload — e.g. cc SessionStart{source:compact}), the handler
// must surface that reason rather than mislabeling it as event_not_in_catalog.
// Without this distinction every Claude Code session compaction would write
// false-positive catalog-miss trace rows.
func TestHandleEvent_InvalidWithReason_UsesProviderReason(t *testing.T) {
	m := catalogMissModule(t)
	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":36649,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionStart","raw_event":{"source":"compact"},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s), want 200", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["reason"] != "compact_ignored" {
		t.Errorf("reason field = %q, want compact_ignored (provider's reason should pass through)", resp["reason"])
	}

	page := waitForTraceChains(t, m, "work", 1)
	if len(page.Chains) != 1 {
		t.Fatalf("len(page.Chains) = %d, want 1", len(page.Chains))
	}
	chain := page.Chains[0]
	if chain.TerminalReason != "compact_ignored" {
		t.Errorf("TerminalReason = %q, want compact_ignored", chain.TerminalReason)
	}
}

// H_ErrorGuardCodexSessionEnd: when codex enters StatusError (e.g. via
// StopFailure), a subsequent SessionEnd must be allowed to clear the error
// state. Without the guard exception, codex sessions would stay stuck red
// even after the real session terminates, defeating the cleanup purpose of
// the new SessionEnd→Clear mapping. Mirrors opencode's existing exception.
func TestHandleEvent_CodexSessionEndClearsErrorGuard(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.prober = probe.New(fakeTmux)
	m.registry.Register(agentcodex.NewProvider())
	// Stub identify to accept codex events through verify.
	origRead := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1, ExePath: "/usr/local/bin/codex", Argv: []string{"codex"}}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origRead })

	// Seed Error state for the session (e.g. from a prior StopFailure).
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusError
	m.mu.Unlock()

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":36649,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionEnd","raw_event":{},"agent_type":"codex"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s), want 200", w.Code, w.Body.String())
	}
	// After Clear, the session entry should have been removed from
	// currentStatus (StatusClear handler at handler.go:~210 deletes it).
	m.mu.Lock()
	_, stillPresent := m.currentStatus["work"]
	m.mu.Unlock()
	if stillPresent {
		t.Errorf("currentStatus[\"work\"] should have been cleared by SessionEnd, but error guard blocked it")
	}
}

// H5: any invalid result (including known-but-unmappable like compact) must
// clear the legacy agent_events row for the session so replay/snapshot logic
// does not later resurface a stale event on top of one we already chose to
// skip. This is the regression flagged by the codex attack-view review:
// catalog-miss early-return previously skipped m.events.Delete entirely.
func TestHandleEvent_InvalidResult_ClearsLegacyEventRow(t *testing.T) {
	m := catalogMissModule(t)

	// Seed a stale legacy row for this session.
	if err := m.events.Set("work", "OldEvent", json.RawMessage(`{"old":"row"}`), "cc", 0); err != nil {
		t.Fatalf("seed legacy event: %v", err)
	}
	// Sanity: row exists.
	if ev, _ := m.events.Get("work"); ev == nil {
		t.Fatal("seed failed: legacy row missing")
	}

	w := httptest.NewRecorder()
	m.handleEvent(w, catalogMissRequest())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	ev, err := m.events.Get("work")
	if err != nil {
		t.Fatalf("events.Get: %v", err)
	}
	if ev != nil {
		t.Errorf("legacy row should be cleared on invalid result, got %+v", ev)
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
