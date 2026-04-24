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

func TestHandleEvent_SessionStartUpsertsFrame(t *testing.T) {
	m := newTestModule(t)
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionStart","raw_event":{},"agent_type":"cc"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.handleEvent(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
	if frames[0].AgentType != "cc" || frames[0].Status != agentpkg.StatusIdle {
		t.Fatalf("frame = %+v, want cc idle", frames[0])
	}
}

func TestHandleEvent_StopDoesNotPopFrame(t *testing.T) {
	m := newTestModule(t)
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			if event == "SessionStart" {
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})

	for _, body := range []string{
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionStart","raw_event":{},"agent_type":"cc"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"Stop","raw_event":{},"agent_type":"cc"}`,
	} {
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
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
}

func TestHandleEvent_SessionEndPopsFrame(t *testing.T) {
	m := newTestModule(t)
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			if event == "SessionEnd" {
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusClear}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})

	for _, body := range []string{
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionStart","raw_event":{},"agent_type":"cc"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionEnd","raw_event":{},"agent_type":"cc"}`,
	} {
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
}

func TestHandleEvent_SubagentDoesNotCreateFrame(t *testing.T) {
	m := newTestModule(t)
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			if event == "SubagentStart" {
				return agentpkg.DeriveResult{Valid: true, Detail: map[string]any{"agent_id": "sub-1"}}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	})

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SubagentStart","raw_event":{},"agent_type":"cc"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0", len(frames))
	}
}

func TestHandleEvent_OpenCodeSessionStartClearsPersistedSubagents(t *testing.T) {
	m := newTestModule(t)
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

	for _, body := range []string{
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionStart","raw_event":{},"agent_type":"opencode"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SubagentStart","raw_event":{},"agent_type":"opencode"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"SessionStart","raw_event":{},"agent_type":"opencode"}`,
	} {
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
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
	if len(frames[0].Subagents) != 0 {
		t.Fatalf("frame subagents = %#v, want empty after SessionStart cleanup", frames[0].Subagents)
	}
	if got := m.subagents["work"]; len(got) != 0 {
		t.Fatalf("in-memory subagents = %#v, want empty after SessionStart cleanup", got)
	}
}

func TestReplay_SkipsFramesWithStaleStartTime(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "work-code", Name: "work"}}}
	stale, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "stale",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origStart := processStartTimeFn
	processStartTimeFn = func(pid int) (string, error) { return "fresh", nil }
	t.Cleanup(func() { processStartTimeFn = origStart })

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	m.replayFromDB()

	got, err := m.frames.GetByIdentity("%5", 200, "stale")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got != nil || stale.FrameID == "" {
		t.Fatal("stale frame should be dropped during replay")
	}
}

func TestReplay_RestoresLiveFrames(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "work-code", Name: "work"}}}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusRunning,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	origStart := processStartTimeFn
	processStartTimeFn = func(pid int) (string, error) { return "live", nil }
	t.Cleanup(func() { processStartTimeFn = origStart })

	m.replayFromDB()

	if got := m.currentStatus["work"]; got != agentpkg.StatusRunning {
		t.Fatalf("currentStatus = %q, want running", got)
	}
}

func TestReplay_DropsDeadFramesBeforeRestore(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "work-code", Name: "work"}}}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
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
	origAlive := isPidAliveFn
	isPidAliveFn = func(pid int) bool { return pid != 200 }
	t.Cleanup(func() { isPidAliveFn = origAlive })

	if err := m.sweepOnce(); err != nil {
		t.Fatalf("sweepOnce: %v", err)
	}
	m.replayFromDB()

	if got := m.currentStatus["work"]; got != "" {
		t.Fatalf("currentStatus = %q, want empty", got)
	}
	got, err := m.frames.GetByIdentity("%5", 200, "dead")
	if err != nil {
		t.Fatalf("GetByIdentity: %v", err)
	}
	if got != nil {
		t.Fatal("dead frame should be deleted during replay")
	}
}

func TestReplay_RestoresLegacySessionsWithoutFrames(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "legacy-code", Name: "legacy"},
		{Code: "work-code", Name: "work"},
	}}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			if event == "Stop" {
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	})
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusRunning,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	if err := m.events.Set("legacy", "Stop", json.RawMessage(`{}`), "cc", 11); err != nil {
		t.Fatalf("seed legacy event: %v", err)
	}
	origStart := processStartTimeFn
	processStartTimeFn = func(pid int) (string, error) { return "live", nil }
	t.Cleanup(func() { processStartTimeFn = origStart })

	m.replayFromDB()

	if got := m.currentStatus["work"]; got != agentpkg.StatusRunning {
		t.Fatalf("work currentStatus = %q, want running", got)
	}
	if got := m.currentStatus["legacy"]; got != agentpkg.StatusIdle {
		t.Fatalf("legacy currentStatus = %q, want idle", got)
	}
}

func TestSendSnapshot_CollapsesMultiplePanesInSession(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	fakeTmux.SetPaneSessionName("%6", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "work-code", Name: "work"}}}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "A",
		Status:           agentpkg.StatusIdle,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame A: %v", err)
	}
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%6",
		AgentType:        "codex",
		PID:              300,
		PPID:             100,
		ProcessStartTime: "B",
		Status:           agentpkg.StatusRunning,
		StartedAt:        20,
		LastSeenAt:       20,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame B: %v", err)
	}
	origStart := processStartTimeFn
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			return "A", nil
		}
		return "B", nil
	}
	t.Cleanup(func() { processStartTimeFn = origStart })

	broadcaster := core.NewEventsBroadcaster()
	sub := broadcaster.AddTestSubscriber()
	defer broadcaster.RemoveTestSubscriber(sub)

	m.sendSnapshot(sub)

	select {
	case msg := <-sub.SendCh():
		var env struct {
			Type    string `json:"type"`
			Session string `json:"session"`
			Value   string `json:"value"`
		}
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		if env.Session != "work-code" {
			t.Fatalf("session = %q, want work-code", env.Session)
		}
		if !strings.Contains(env.Value, `"agent_type":"codex"`) || !strings.Contains(env.Value, `"status":"running"`) {
			t.Fatalf("snapshot value = %s, want codex running", env.Value)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for snapshot")
	}
	select {
	case msg := <-sub.SendCh():
		t.Fatalf("unexpected extra snapshot: %s", msg)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestUpdateSubagents_StartAddsRef(t *testing.T) {
	start := agentpkg.SubagentRef{ID: "a", Type: "cc", StartedAt: 10}
	got := updateSubagents(nil, "SubagentStart", start)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0] != start {
		t.Fatalf("got[0] = %+v, want %+v", got[0], start)
	}
}

func TestUpdateSubagents_StartDuplicateIDKeepsExisting(t *testing.T) {
	existing := agentpkg.SubagentRef{ID: "a", Type: "cc", StartedAt: 10}
	dup := agentpkg.SubagentRef{ID: "a", Type: "cc", StartedAt: 20}
	got := updateSubagents([]agentpkg.SubagentRef{existing}, "SubagentStart", dup)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0] != existing {
		t.Fatalf("got[0] = %+v, want %+v (no overwrite)", got[0], existing)
	}
}

func TestUpdateSubagents_StopRemovesByID(t *testing.T) {
	a := agentpkg.SubagentRef{ID: "a", Type: "cc"}
	b := agentpkg.SubagentRef{ID: "b", Type: "cc"}
	got := updateSubagents([]agentpkg.SubagentRef{a, b}, "SubagentStop", agentpkg.SubagentRef{ID: "a"})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0] != b {
		t.Fatalf("got[0] = %+v, want %+v", got[0], b)
	}
}

func TestUpdateSubagents_StopIgnoresType(t *testing.T) {
	existing := agentpkg.SubagentRef{ID: "a", Type: "cc"}
	// Stop ref has Type="codex" but ID matches; Type must not participate in matching.
	got := updateSubagents([]agentpkg.SubagentRef{existing}, "SubagentStop", agentpkg.SubagentRef{ID: "a", Type: "codex"})
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0 (ID match removes regardless of Type)", len(got))
	}
}

func TestUpdateSubagents_StopMissingIsNoop(t *testing.T) {
	a := agentpkg.SubagentRef{ID: "a", Type: "cc"}
	got := updateSubagents([]agentpkg.SubagentRef{a}, "SubagentStop", agentpkg.SubagentRef{ID: "b"})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0] != a {
		t.Fatalf("got[0] = %+v, want %+v", got[0], a)
	}
}

func TestSendSnapshot_IncludesLegacySessionsWithoutFrames(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "legacy-code", Name: "legacy"},
		{Code: "work-code", Name: "work"},
	}}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(event string, _ json.RawMessage) agentpkg.DeriveResult {
			if event == "Stop" {
				return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
			}
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	})
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%5",
		AgentType:        "cc",
		PID:              200,
		PPID:             100,
		ProcessStartTime: "live",
		Status:           agentpkg.StatusRunning,
		StartedAt:        10,
		LastSeenAt:       10,
		Verified:         true,
	}); err != nil {
		t.Fatalf("Upsert frame: %v", err)
	}
	if err := m.events.Set("legacy", "Stop", json.RawMessage(`{}`), "cc", 11); err != nil {
		t.Fatalf("seed legacy event: %v", err)
	}
	origStart := processStartTimeFn
	processStartTimeFn = func(pid int) (string, error) { return "live", nil }
	t.Cleanup(func() { processStartTimeFn = origStart })

	broadcaster := core.NewEventsBroadcaster()
	sub := broadcaster.AddTestSubscriber()
	defer broadcaster.RemoveTestSubscriber(sub)

	m.sendSnapshot(sub)

	var messages []string
	for len(messages) < 2 {
		select {
		case msg := <-sub.SendCh():
			messages = append(messages, string(msg))
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("timed out waiting for snapshot %d", len(messages)+1)
		}
	}

	var first struct {
		Session string `json:"session"`
		Value   string `json:"value"`
	}
	if err := json.Unmarshal([]byte(messages[0]), &first); err != nil {
		t.Fatalf("unmarshal first snapshot: %v", err)
	}
	if first.Session != "work-code" || !strings.Contains(first.Value, `"status":"running"`) {
		t.Fatalf("first snapshot = %s, want work running", messages[0])
	}
	var second struct {
		Session string `json:"session"`
		Value   string `json:"value"`
	}
	if err := json.Unmarshal([]byte(messages[1]), &second); err != nil {
		t.Fatalf("unmarshal second snapshot: %v", err)
	}
	if second.Session != "legacy-code" || !strings.Contains(second.Value, `"status":"idle"`) {
		t.Fatalf("second snapshot = %s, want legacy idle", messages[1])
	}
	select {
	case msg := <-sub.SendCh():
		t.Fatalf("unexpected extra snapshot: %s", msg)
	case <-time.After(20 * time.Millisecond):
	}
}

// ---------------------------------------------------------------------------
// Proxy subagent detection (Phase 2 PR-2b, plan §1.4 + §2.5)
// ---------------------------------------------------------------------------

// newProxyTestModule sets up a module with a fake tmux, session provider and
// registered cc/codex providers so each proxy test can tailor the PPID chain
// and liveness seams independently.
func newProxyTestModule(t *testing.T) *Module {
	t.Helper()
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "work-code", Name: "work"}}}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive:   func(string, json.RawMessage) agentpkg.DeriveResult { return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle} },
	})
	m.registry.Register(&fakeAgentProvider{
		typeName: "codex",
		derive:   func(string, json.RawMessage) agentpkg.DeriveResult { return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle} },
	})
	return m
}

func seedFrame(t *testing.T, m *Module, paneID, agentType string, pid int, startTime string, lastSeenAt int64) store.Frame {
	t.Helper()
	f, err := m.frames.Upsert(store.Frame{
		PaneID:           paneID,
		AgentType:        agentType,
		PID:              pid,
		PPID:             1,
		ProcessStartTime: startTime,
		Status:           agentpkg.StatusIdle,
		StartedAt:        lastSeenAt,
		LastSeenAt:       lastSeenAt,
		Verified:         true,
	})
	if err != nil {
		t.Fatalf("seed frame %s pid=%d: %v", agentType, pid, err)
	}
	return f
}

// PR1 — direct PPID hit attaches proxy ref to cc parent.
func TestProxySubagent_DirectPPIDAttachesToCCParent(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	origStart := processStartTimeFn
	origAlive := isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 100}, nil
	}
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t100", nil
		}
		return "other", nil
	}
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		processStartTimeFn = origStart
		isPidAliveFn = origAlive
	})

	req := EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart",
		AgentType: "codex", SenderPID: 200, SenderStartTime: "t200",
	}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "proxy_subagent_attached" {
		t.Fatalf("reason = %q, want proxy_subagent_attached (meta=%+v)", meta.Reason, meta)
	}
	if meta.Decision != "updated_frame" {
		t.Fatalf("decision = %q, want updated_frame", meta.Decision)
	}
	if meta.FrameID != parent.FrameID {
		t.Fatalf("FrameID = %q, want parent %q", meta.FrameID, parent.FrameID)
	}

	frames, err := m.frames.ListByPane("%5")
	if err != nil {
		t.Fatalf("ListByPane: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (no standalone codex frame)", len(frames))
	}
	if frames[0].AgentType != "cc" {
		t.Fatalf("remaining frame.AgentType = %q, want cc", frames[0].AgentType)
	}
	if len(frames[0].Subagents) != 1 {
		t.Fatalf("parent.Subagents len = %d, want 1 (proxy ref attached)", len(frames[0].Subagents))
	}
	ref := frames[0].Subagents[0]
	if ref.Type != "codex" || !ref.IsProxy || ref.SourcePID != 200 || ref.SourceStartTime != "t200" {
		t.Fatalf("proxy ref = %+v, want type=codex is_proxy source_pid=200 source_start_time=t200", ref)
	}
}

// PR2 — tree walk through codex-companion to cc.
func TestProxySubagent_TreeWalkThroughCodexCompanion(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	origStart := processStartTimeFn
	origAlive := isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 300:
			return agentpkg.ProcessInfo{PID: 300, PPID: 200}, nil
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		case 100:
			return agentpkg.ProcessInfo{PID: 100, PPID: 1}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t100", nil
		}
		return "other", nil
	}
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		processStartTimeFn = origStart
		isPidAliveFn = origAlive
	})

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 300, SenderStartTime: "t300"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "proxy_subagent_attached" {
		t.Fatalf("reason = %q, want proxy_subagent_attached", meta.Reason)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (tree walk collapses codex into cc)", len(frames))
	}
}

// PR3 — chain depth 6 exceeds proxyMaxDepth=5 → fallback.
func TestProxySubagent_TreeWalkDepthLimit(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	// chain: 700→600→500→400→300→200→100(cc)
	chain := map[int]int{700: 600, 600: 500, 500: 400, 400: 300, 300: 200, 200: 100, 100: 1}
	origInfo := readProcessInfoFn
	origStart := processStartTimeFn
	origAlive := isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if ppid, ok := chain[pid]; ok {
			return agentpkg.ProcessInfo{PID: pid, PPID: ppid}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t100", nil
		}
		return "other", nil
	}
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		processStartTimeFn = origStart
		isPidAliveFn = origAlive
	})

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 700, SenderStartTime: "t700"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason == "proxy_subagent_attached" {
		t.Fatalf("proxy attached but chain exceeds depth 5: %+v", meta)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frame count = %d, want 2 (cc + new codex frame; proxy not applied)", len(frames))
	}
}

// PR4 — walk reaches init without frame; fallback new frame.
func TestProxySubagent_SkipsWhenNoAncestorHasFrame(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid <= 1 {
			return agentpkg.ProcessInfo{PID: pid, PPID: 0}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 9999, SenderStartTime: "t9999"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason == "proxy_subagent_attached" {
		t.Fatalf("should not proxy when no ancestor has a frame; meta=%+v", meta)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "codex" {
		t.Fatalf("frames = %+v, want single new codex frame", frames)
	}
}

// PR5 — same-type ancestor hard stop.
func TestProxySubagent_SkipsWhenParentSameType(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 100}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason == "proxy_subagent_attached" {
		t.Fatalf("should not proxy cc→cc (same-type hard stop); meta=%+v", meta)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frame count = %d, want 2 (two cc frames, no proxy)", len(frames))
	}
}

// PR6 — dead parent doesn't count; walk continues (and falls back here).
func TestProxySubagent_SkipsWhenParentPidDead(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(pid int) bool { return pid != 100 } // cc parent dead
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
	})

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason == "proxy_subagent_attached" {
		t.Fatalf("should not proxy onto dead cc frame; meta=%+v", meta)
	}
}

// PR7 — non-SessionStart events skip proxy path.
func TestProxySubagent_SkipsWhenEventNotSessionStart(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 100}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "UserPromptSubmit", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason == "proxy_subagent_attached" {
		t.Fatalf("non-SessionStart must not trigger proxy path; meta=%+v", meta)
	}
}

// PR8 — trace meta decision/reason/FrameID + before/after snapshots.
func TestProxySubagent_TraceMetaCorrect(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	origStart := processStartTimeFn
	origAlive := isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 100}, nil
	}
	processStartTimeFn = func(pid int) (string, error) { return "t100", nil }
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		processStartTimeFn = origStart
		isPidAliveFn = origAlive
	})

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Decision != "updated_frame" || meta.Reason != "proxy_subagent_attached" {
		t.Fatalf("trace meta = %q/%q, want updated_frame/proxy_subagent_attached", meta.Decision, meta.Reason)
	}
	if meta.FrameID != parent.FrameID {
		t.Fatalf("FrameID = %q, want parent %q", meta.FrameID, parent.FrameID)
	}
	beforeMap, ok := meta.Before.(map[string]any)
	if !ok {
		t.Fatalf("Before = %T, want map[string]any", meta.Before)
	}
	beforeSubs, _ := beforeMap["subagents"].([]agentpkg.SubagentRef)
	if len(beforeSubs) != 0 {
		t.Fatalf("Before.subagents len = %d, want 0 (pre-attach)", len(beforeSubs))
	}
	afterMap, ok := meta.After.(map[string]any)
	if !ok {
		t.Fatalf("After = %T, want map[string]any", meta.After)
	}
	afterSubs, _ := afterMap["subagents"].([]agentpkg.SubagentRef)
	if len(afterSubs) != 1 {
		t.Fatalf("After.subagents len = %d, want 1 (post-attach)", len(afterSubs))
	}
}

// PR9 — re-hook with same PID+StartTime is idempotent (first-write wins).
func TestProxySubagent_DoesNotDoubleAttachOnReHook(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	origStart := processStartTimeFn
	origAlive := isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 100}, nil
	}
	processStartTimeFn = func(pid int) (string, error) { return "t100", nil }
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		processStartTimeFn = origStart
		isPidAliveFn = origAlive
	})

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	for i := 0; i < 2; i++ {
		_, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, int64(100+i))
		if err != nil {
			t.Fatalf("applyFrameEvent #%d: %v", i, err)
		}
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
	if len(frames[0].Subagents) != 1 {
		t.Fatalf("Subagents len = %d, want 1 (no double attach)", len(frames[0].Subagents))
	}
	if frames[0].Subagents[0].StartedAt != 100 {
		t.Fatalf("StartedAt = %d, want 100 (first-write wins)", frames[0].Subagents[0].StartedAt)
	}
}

// PR10 — pane filter: cross-pane PPID must not match.
func TestProxySubagent_CrossPaneAncestorNotMatched(t *testing.T) {
	m := newProxyTestModule(t)
	m.tmux.(*tmux.FakeExecutor).SetPaneSessionName("%7", "work2")
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 100}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	req := EventRequest{TmuxSession: "work2", TmuxPaneID: "%7", EventName: "SessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason == "proxy_subagent_attached" {
		t.Fatalf("cross-pane PPID should not match; meta=%+v", meta)
	}
	framesP5, _ := m.frames.ListByPane("%5")
	if len(framesP5) != 1 || len(framesP5[0].Subagents) != 0 {
		t.Fatalf("pane %%5 frames = %+v, want undisturbed cc", framesP5)
	}
	framesP7, _ := m.frames.ListByPane("%7")
	if len(framesP7) != 1 || framesP7[0].AgentType != "codex" {
		t.Fatalf("pane %%7 frames = %+v, want new codex frame", framesP7)
	}
}

// PR11 — self-cycle PPID == PID must not loop.
func TestProxySubagent_SelfCycleGuard(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 300}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: pid}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	done := make(chan struct{})
	go func() {
		_, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
		if err != nil {
			t.Errorf("applyFrameEvent: %v", err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("applyFrameEvent hung on self-cycle")
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "codex" {
		t.Fatalf("frames = %+v, want single codex (no proxy due to self-cycle guard)", frames)
	}
}

// PR12 — ancestor process read error → partial chain, fallback.
func TestProxySubagent_PartialChainOnReadError(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 300}, nil
		}
		return agentpkg.ProcessInfo{}, errFake
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason == "proxy_subagent_attached" {
		t.Fatalf("read error should abort walk; meta=%+v", meta)
	}
}

// PR13 — stale frame (PID reused) skipped by start-time mismatch.
func TestProxySubagent_SkipsStaleFrameByStartTimeMismatch(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	origStart := processStartTimeFn
	origAlive := isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t_different", nil
		}
		return "other", nil
	}
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		processStartTimeFn = origStart
		isPidAliveFn = origAlive
	})

	beforeParent, _ := m.frames.GetByIdentity("%5", 100, "t100")

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason == "proxy_subagent_attached" {
		t.Fatalf("stale (start-time mismatch) parent should not proxy-attach; meta=%+v", meta)
	}
	afterParent, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if afterParent == nil {
		t.Fatal("stale cc frame went missing (should remain for sweep)")
	}
	if afterParent.LastSeenAt != beforeParent.LastSeenAt {
		t.Fatalf("stale cc.LastSeenAt = %d, want unchanged %d", afterParent.LastSeenAt, beforeParent.LastSeenAt)
	}
}

// PR14 — same-type ancestor (codex) in chain halts walk before cc.
func TestProxySubagent_SameTypeAncestorStopsWalk(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	seedFrame(t, m, "%5", "codex", 200, "t200", 20)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 400:
			return agentpkg.ProcessInfo{PID: 400, PPID: 300}, nil
		case 300:
			return agentpkg.ProcessInfo{PID: 300, PPID: 200}, nil
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 400, SenderStartTime: "t400"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason == "proxy_subagent_attached" {
		t.Fatalf("walk should halt at same-type codex ancestor, not reach cc; meta=%+v", meta)
	}
}

// PR15 — start-time read error aborts walk (does NOT fall through to outer cross-type ancestor).
func TestProxySubagent_AbortsWalkOnStartTimeReadError(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	seedFrame(t, m, "%5", "cc", 50, "t50", 5)

	origInfo := readProcessInfoFn
	origStart := processStartTimeFn
	origAlive := isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		case 100:
			return agentpkg.ProcessInfo{PID: 100, PPID: 50}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "", errFake
		}
		return "other", nil
	}
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		processStartTimeFn = origStart
		isPidAliveFn = origAlive
	})

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason == "proxy_subagent_attached" {
		t.Fatalf("start_time read error should abort walk (not fall through to outer cc frame); meta=%+v", meta)
	}
}

var errFake = fakeErr("fake read error")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }
