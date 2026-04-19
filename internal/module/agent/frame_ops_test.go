package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
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
