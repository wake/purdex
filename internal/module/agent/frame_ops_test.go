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
