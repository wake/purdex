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

// U6 — R2 regression: a native subagent whose provider-supplied agent_id
// happens to equal a synthesized proxy ID must NOT collide with a proxy ref
// of the same ID string on the same list. Identity is kind-aware: proxy
// matches by SourcePID+SourceStartTime, native matches by ID, cross-kind
// never matches.
func TestUpdateSubagents_ProxyNativeIDNamespacesAreIsolated(t *testing.T) {
	// A native ref with a pathologically-shaped ID that happens to match
	// the proxy ID synthesis pattern proxy:<type>:<pid>:<start_time>.
	native := agentpkg.SubagentRef{ID: "proxy:cc:200:t200", Type: "cc"}
	list := []agentpkg.SubagentRef{native}

	// A real proxy SessionStart synthesizes the same-shaped ID but sets
	// IsProxy=true + source identity fields. It must NOT be shadowed by the
	// native ref already present — append expected.
	proxy := agentpkg.SubagentRef{
		ID:              "proxy:cc:200:t200",
		Type:            "cc",
		SourcePID:       200,
		SourceStartTime: "t200",
		IsProxy:         true,
	}
	afterStart := updateSubagents(list, "SubagentStart", proxy)
	if len(afterStart) != 2 {
		t.Fatalf("proxy SubagentStart with same ID as native should append cross-kind; got %d refs, want 2: %+v", len(afterStart), afterStart)
	}

	// A native SubagentStop with the same ID must remove only the native
	// ref, not evict the proxy.
	afterStop := updateSubagents(afterStart, "SubagentStop", native)
	if len(afterStop) != 1 {
		t.Fatalf("native SubagentStop removed wrong count; got %d, want 1: %+v", len(afterStop), afterStop)
	}
	if !afterStop[0].IsProxy {
		t.Fatalf("native Stop evicted proxy ref (kind-isolation broken); got %+v", afterStop[0])
	}
}

// U7 — #632 extended: native SubagentStart path also uses the atomic
// mutate-with-retry helper, so two concurrent hook-driven Task tool
// dispatches that attach different agent_ids to the same cc frame both
// land. Same race shape as PR17 but through the SubagentStart event path
// rather than the proxy fast-path.
func TestSubagentStart_ConcurrentNativeStartsBothLand(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	// Start #1: native SubagentStart with agent_id "s1".
	req1 := EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5",
		EventName: "SubagentStart", AgentType: "cc",
		SenderPID: 100, SenderStartTime: "t100",
	}
	result1 := agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning, Detail: map[string]any{"agent_id": "s1"}}
	_, meta1, err := m.applyFrameEvent(req1, result1, 100)
	if err != nil {
		t.Fatalf("start #1: %v", err)
	}
	if meta1.Reason != "subagent_membership_changed" {
		t.Fatalf("start #1 reason = %q, want subagent_membership_changed", meta1.Reason)
	}

	// Simulate a concurrent racer write (e.g. a probe-driven LastSeenAt
	// bump or another SubagentStart that landed first but hasn't been
	// observed by request #2's read yet).
	racer := agentpkg.SubagentRef{ID: "racer", Type: "cc", StartedAt: 150}
	cur, err := m.frames.GetByIdentity("%5", 100, "t100")
	if err != nil {
		t.Fatalf("racer baseline read: %v", err)
	}
	cur.Subagents = append(cur.Subagents, racer)
	cur.LastSeenAt = 180
	if _, err := m.frames.Upsert(*cur); err != nil {
		t.Fatalf("racer Upsert: %v", err)
	}

	// Start #2: native SubagentStart with agent_id "s2". applyFrameEvent's
	// earlier GetByIdentity read still sees the pre-racer baseline; the
	// atomic retry loop must reload, observe racer + s1, append s2, persist.
	req2 := EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5",
		EventName: "SubagentStart", AgentType: "cc",
		SenderPID: 100, SenderStartTime: "t100",
	}
	result2 := agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning, Detail: map[string]any{"agent_id": "s2"}}
	_, meta2, err := m.applyFrameEvent(req2, result2, 200)
	if err != nil {
		t.Fatalf("start #2: %v", err)
	}
	if meta2.Reason != "subagent_membership_changed" {
		t.Fatalf("start #2 reason = %q, want subagent_membership_changed", meta2.Reason)
	}

	// All three refs should coexist on the parent: s1 from start #1,
	// racer from the injected concurrent write, s2 from start #2.
	final, err := m.frames.GetByIdentity("%5", 100, "t100")
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	if final == nil {
		t.Fatal("parent vanished")
	}
	ids := make(map[string]bool)
	for _, ref := range final.Subagents {
		ids[ref.ID] = true
	}
	for _, want := range []string{"s1", "s2", "racer"} {
		if !ids[want] {
			t.Fatalf("Subagents missing ID=%q; parent=%+v", want, final.Subagents)
		}
	}
	if len(final.Subagents) != 3 {
		t.Fatalf("Subagents len = %d, want 3; parent=%+v", len(final.Subagents), final.Subagents)
	}
	_ = parent
}

// HookRace1 — #632 R8 regression: applyFrameEvent's existing-frame status
// update path (non-SessionEnd / non-SubagentStart / non-SubagentStop hook
// events like UserPromptSubmit) uses narrow UpdateHookPath that does not
// touch subagents_json. A concurrent proxy attach in flight does not lose
// its ref to the hook handler's stale baseline.
func TestHookStatusUpdate_DoesNotClobberConcurrentSubagents(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	// First, attach a proxy ref to the cc parent (via applyFrameEvent's
	// proxy fast-path so we exercise the real attachProxyRefWithRetry).
	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 100}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "t100", nil }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	req1 := EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5",
		EventName: "SessionStart", AgentType: "codex",
		SenderPID: 200, SenderStartTime: "t200",
	}
	if _, _, err := m.applyFrameEvent(req1, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("proxy attach: %v", err)
	}

	// Confirm cc parent now owns 1 proxy ref.
	mid, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if len(mid.Subagents) != 1 || !mid.Subagents[0].IsProxy {
		t.Fatalf("setup: expected 1 proxy ref on cc parent; got %+v", mid.Subagents)
	}

	// Now fire a general-hook status transition on cc itself (not SessionStart
	// / not SubagentStart-Stop / not SessionEnd). The request carries cc's
	// own identity so applyFrameEvent's frame != nil branch triggers on the
	// stale cc row that does NOT have the proxy ref.
	req2 := EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5",
		EventName: "UserPromptSubmit", AgentType: "cc",
		SenderPID: 100, SenderStartTime: "t100",
	}
	if _, _, err := m.applyFrameEvent(req2, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}, 200); err != nil {
		t.Fatalf("hook status update: %v", err)
	}

	// The proxy ref attached above must still be on cc (UpdateHookPath did
	// not touch subagents_json). Status and LastSeenAt should have moved.
	final, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if len(final.Subagents) != 1 || !final.Subagents[0].IsProxy {
		t.Fatalf("proxy ref clobbered by hook status update; got %+v (#632 R8 regression)", final.Subagents)
	}
	if final.Status != agentpkg.StatusRunning {
		t.Fatalf("Status = %q, want Running", final.Status)
	}
	_ = parent
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
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 100}, nil
	}
	// R4 fix: force the same-type ancestor to be live + identity-verified so
	// the hard-stop gate is exercised on its actual rule (live same-type
	// ancestor → re-session) rather than incidentally passing through
	// dead-skip+walk-up. Without these stubs the candidate's PID 100 would
	// be dead under the real isPidAliveFn and the test would mask regressions
	// in the hard-stop ordering.
	isPidAliveFn = func(pid int) bool { return pid == 100 }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t100", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

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
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
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
	// R4 fix: force the same-type codex ancestor (PID 200) to be live +
	// identity-verified so the hard-stop gate triggers on its real rule
	// (live same-type → re-session) rather than walking past on a dead
	// candidate. Without these stubs PR14 would incidentally pass by
	// walking 200 (dead) → 100 (dead) → exhausted, which does not actually
	// verify the same-type hard-stop.
	isPidAliveFn = func(pid int) bool { return pid == 200 || pid == 100 }
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 200:
			return "t200", nil
		case 100:
			return "t100", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 400, SenderStartTime: "t400"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason == "proxy_subagent_attached" {
		t.Fatalf("walk should halt at live same-type codex ancestor, not reach cc; meta=%+v", meta)
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

// PR16 — R3 regression: a stale same-type ancestor (dead process, or PID
// reused with mismatched start_time) must NOT hard-stop the walk. The walk
// must continue upward to locate a live cross-type parent. Before R3 fix,
// the same-type check fired before the liveness/identity gate, erroneously
// aborting the walk for leftover data that sweep had not yet cleared.
func TestProxySubagent_StaleSameTypeAncestorDoesNotBlockWalk(t *testing.T) {
	m := newProxyTestModule(t)
	// Live cc frame at PID 100.
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	// Stale codex frame at PID 200 — row still in DB but process is dead.
	seedFrame(t, m, "%5", "codex", 200, "t200_old", 5)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 400:
			return agentpkg.ProcessInfo{PID: 400, PPID: 200}, nil
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		case 100:
			return agentpkg.ProcessInfo{PID: 100, PPID: 1}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(pid int) bool {
		// Stale codex PID 200 dead; live cc PID 100 alive.
		return pid == 100
	}
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t100", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	req := EventRequest{
		TmuxSession:     "work",
		TmuxPaneID:      "%5",
		EventName:       "SessionStart",
		AgentType:       "codex",
		SenderPID:       400,
		SenderStartTime: "t400",
	}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "proxy_subagent_attached" {
		t.Fatalf("walk should skip dead stale codex and proxy-attach to live cc; got reason=%q meta=%+v", meta.Reason, meta)
	}
}

// PR17 — #632 regression: two concurrent proxy SessionStarts that target the
// same parent frame must both land. The first attach wins the initial
// UpsertIfUnchanged; the second observes the freshened last_seen_at, reloads,
// re-merges its ref through updateSubagents, and persists — yielding a
// parent with both proxy refs attached.
//
// We drive the race by intercepting the store's UpsertIfUnchanged on the
// FIRST attempt of attach #2: before attempt #2 issues its UPDATE, a
// simulated concurrent writer bumps the parent's LastSeenAt. That write
// invalidates attempt #2's baseline and triggers the retry-with-reload
// branch; attempt #3 merges and succeeds.
func TestProxySubagent_ConcurrentAttachesBothLand(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 100}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "t100", nil }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	// Attach #1: codex PID 200, start t200.
	req1 := EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5",
		EventName: "SessionStart", AgentType: "codex",
		SenderPID: 200, SenderStartTime: "t200",
	}
	_, meta1, err := m.applyFrameEvent(req1, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("attach #1 applyFrameEvent: %v", err)
	}
	if meta1.Reason != "proxy_subagent_attached" {
		t.Fatalf("attach #1 reason = %q, want proxy_subagent_attached", meta1.Reason)
	}

	// Concurrency race injection: before attach #2's UpsertIfUnchanged sees
	// the DB, a third writer (e.g. another proxy attach in-flight, or a
	// probe-driven LastSeenAt refresh) lands. We simulate this by directly
	// poking the DB between attach #2's read and write — the read happens in
	// applyFrameEvent's frame lookup / findProxyParent, and we install a
	// hook via UpsertIfUnchanged timing using race-injector: mutate the row
	// in a goroutine between request #2's findProxyParent and its write.
	//
	// Simpler approach: directly bump the DB row via a synthetic Upsert
	// before calling applyFrameEvent #2. Then verify attach #2 retries
	// (visible via post-state: parent has BOTH s1 from attach #1 AND the
	// injected racer's ref AND attach #2's ref; all three survive).
	racer := agentpkg.SubagentRef{
		ID: "racer:codex:999:t999", Type: "codex", StartedAt: 150,
		SourcePID: 999, SourceStartTime: "t999", IsProxy: true,
	}
	cur, err := m.frames.GetByIdentity("%5", 100, "t100")
	if err != nil {
		t.Fatalf("racer baseline read: %v", err)
	}
	cur.Subagents = append(cur.Subagents, racer)
	cur.LastSeenAt = 180
	if _, err := m.frames.Upsert(*cur); err != nil {
		t.Fatalf("racer Upsert: %v", err)
	}

	// Attach #2: different codex, PID 300, start t300. This request's
	// findProxyParent returned the parent with attach #1's ref BEFORE the
	// racer write; the retry branch reloads and merges without losing the
	// racer ref.
	req2 := EventRequest{
		TmuxSession: "work", TmuxPaneID: "%5",
		EventName: "SessionStart", AgentType: "codex",
		SenderPID: 300, SenderStartTime: "t300",
	}
	_, meta2, err := m.applyFrameEvent(req2, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200)
	if err != nil {
		t.Fatalf("attach #2 applyFrameEvent: %v", err)
	}
	if meta2.Reason != "proxy_subagent_attached" {
		t.Fatalf("attach #2 reason = %q, want proxy_subagent_attached", meta2.Reason)
	}

	// All three refs survived the read-modify-write cycle.
	final, err := m.frames.GetByIdentity("%5", 100, "t100")
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	if final == nil {
		t.Fatal("parent vanished")
	}
	have := make(map[int]bool)
	for _, ref := range final.Subagents {
		have[ref.SourcePID] = true
	}
	for _, want := range []int{200, 300, 999} {
		if !have[want] {
			t.Fatalf("Subagents missing SourcePID=%d; parent=%+v", want, final.Subagents)
		}
	}
	if len(final.Subagents) != 3 {
		t.Fatalf("Subagents len = %d, want 3 (both attaches + racer); parent=%+v", len(final.Subagents), final.Subagents)
	}

	_ = parent // suppress unused warning
}

// ---------------------------------------------------------------------------
// SessionEnd proxy cleanup (Phase 2 PR-2b, plan §1.5 + §2.6)
// ---------------------------------------------------------------------------

// SE1 — codex SessionEnd with no frame of its own removes the proxy ref from
// its cc parent and emits a proxy_subagent_detached trace.
func TestSessionEnd_RemovesProxyRefFromParent(t *testing.T) {
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

	// First: proxy attach.
	startReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	if _, meta, err := m.applyFrameEvent(startReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("attach: %v", err)
	} else if meta.Reason != "proxy_subagent_attached" {
		t.Fatalf("attach reason = %q", meta.Reason)
	}

	// Then: SessionEnd with same identity — should remove the ref.
	endReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionEnd", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(endReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusClear}, 200)
	if err != nil {
		t.Fatalf("applyFrameEvent SessionEnd: %v", err)
	}
	if meta.Decision != "updated_frame" || meta.Reason != "proxy_subagent_detached" {
		t.Fatalf("trace meta = %q/%q, want updated_frame/proxy_subagent_detached", meta.Decision, meta.Reason)
	}
	if meta.FrameID != parent.FrameID {
		t.Fatalf("FrameID = %q, want parent %q", meta.FrameID, parent.FrameID)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
	if len(frames[0].Subagents) != 0 {
		t.Fatalf("Subagents = %+v, want empty", frames[0].Subagents)
	}
}

// SE2 — orphan SessionEnd (no matching ref) falls back to legacy skip path.
func TestSessionEnd_OrphanFallsBackToExistingSkip(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	// No proxy ref attached.

	endReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionEnd", AgentType: "codex", SenderPID: 999, SenderStartTime: "t999"}
	_, meta, err := m.applyFrameEvent(endReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusClear}, 200)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "session_end_without_frame" {
		t.Fatalf("reason = %q, want session_end_without_frame (orphan fallback)", meta.Reason)
	}
}

// SE3 — SessionEnd on a frame that owns its own identity deletes the frame
// (the owning frame delete removes any proxy refs it was carrying, which is
// fine as a side effect).
func TestSessionEnd_OwnFrameDeletePreservesOtherProxyRefs(t *testing.T) {
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

	startReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	if _, _, err := m.applyFrameEvent(startReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("proxy attach: %v", err)
	}

	// Now SessionEnd on cc itself (the owning frame).
	endReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionEnd", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	_, meta, err := m.applyFrameEvent(endReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusClear}, 300)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Decision != "deleted_frame" || meta.Reason != "session_end" {
		t.Fatalf("trace meta = %q/%q, want deleted_frame/session_end", meta.Decision, meta.Reason)
	}
	if meta.FrameID != parent.FrameID {
		t.Fatalf("FrameID = %q, want parent %q", meta.FrameID, parent.FrameID)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 0 {
		t.Fatalf("frame count = %d, want 0 (cc frame deleted)", len(frames))
	}
}

// --- tryRebuildFromProcessTree tests (Phase 3 Commit 2) ---
//
// The helper delegates to Prober.FirstAliveAgentInTree via the
// firstAliveAgentInTreeFn seam (sibling of readProcessInfoFn / isPidAliveFn /
// processStartTimeFn in verify.go). Tests override the seam directly, so they
// don't need m.prober to be wired up — same pattern as every other frame_ops
// test that stubs a probe-layer dependency.

func TestTryRebuildFromProcessTree_Hit(t *testing.T) {
	m := newTestModule(t)

	origSeam := firstAliveAgentInTreeFn
	var gotTarget string
	firstAliveAgentInTreeFn = func(_ *Module, target string) (string, int, error) {
		gotTarget = target
		return "cc", 200, nil
	}
	t.Cleanup(func() { firstAliveAgentInTreeFn = origSeam })

	req := EventRequest{TmuxPaneID: "%5", SenderPID: 200, SenderStartTime: "t200"}
	agentType, ok, err := m.tryRebuildFromProcessTree(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if agentType != "cc" {
		t.Fatalf("agentType = %q, want cc", agentType)
	}
	if gotTarget != "%5" {
		t.Fatalf("seam target = %q, want %%5 (pane id)", gotTarget)
	}
}

func TestTryRebuildFromProcessTree_Miss(t *testing.T) {
	m := newTestModule(t)

	origSeam := firstAliveAgentInTreeFn
	firstAliveAgentInTreeFn = func(_ *Module, _ string) (string, int, error) {
		return "", 0, nil
	}
	t.Cleanup(func() { firstAliveAgentInTreeFn = origSeam })

	req := EventRequest{TmuxPaneID: "%5", SenderPID: 200, SenderStartTime: "t200"}
	agentType, ok, err := m.tryRebuildFromProcessTree(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("ok = true, want false (no match)")
	}
	if agentType != "" {
		t.Fatalf("agentType = %q, want empty", agentType)
	}
}

func TestTryRebuildFromProcessTree_Error(t *testing.T) {
	m := newTestModule(t)

	origSeam := firstAliveAgentInTreeFn
	wantErr := errProbeFailure
	firstAliveAgentInTreeFn = func(_ *Module, _ string) (string, int, error) {
		return "", 0, wantErr
	}
	t.Cleanup(func() { firstAliveAgentInTreeFn = origSeam })

	req := EventRequest{TmuxPaneID: "%5", SenderPID: 200, SenderStartTime: "t200"}
	agentType, ok, err := m.tryRebuildFromProcessTree(req)
	if err == nil {
		t.Fatalf("err = nil, want non-nil")
	}
	if err != wantErr {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if ok {
		t.Fatalf("ok = true, want false on error")
	}
	if agentType != "" {
		t.Fatalf("agentType = %q, want empty on error", agentType)
	}
}

// errProbeFailure is a sentinel used by TestTryRebuildFromProcessTree_Error to
// verify that the helper propagates the probe's error verbatim (caller
// fail-soft).
var errProbeFailure = errorString("probe tree query failed")

type errorString string

// ---------------------------------------------------------------------------
// Rebuild wiring into applyFrameEvent (Phase 3 Commit 3, plan §1.2.4 / §1.4)
// ---------------------------------------------------------------------------
//
// Tests override firstAliveAgentInTreeFn directly (same pattern as the
// helper-level tests above) and arrange the standard lookup chain to miss so
// the rebuild fallback is the only path that can satisfy the event.

// R5 — rebuild命中、既有 lookup chain 全 miss → trace reason daemon_restart_recovery.
func TestApplyFrameEvent_RebuildHit_TraceReason(t *testing.T) {
	m := newProxyTestModule(t)

	// Arrange proc info so readProcessInfoFn returns a PPID that doesn't
	// resolve to any seeded frame (FindByPanePID miss). No frames seeded →
	// GetByIdentity miss + findProxyParent miss (no ancestor frame).
	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	origSeam := firstAliveAgentInTreeFn
	firstAliveAgentInTreeFn = func(_ *Module, _ string) (string, int, error) {
		return "cc", 200, nil
	}
	t.Cleanup(func() { firstAliveAgentInTreeFn = origSeam })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 500)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "daemon_restart_recovery" {
		t.Fatalf("reason = %q, want daemon_restart_recovery (meta=%+v)", meta.Reason, meta)
	}
	if meta.Decision != "created_frame" {
		t.Fatalf("decision = %q, want created_frame", meta.Decision)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (new frame created post-rebuild)", len(frames))
	}
	if frames[0].AgentType != "cc" {
		t.Fatalf("AgentType = %q, want cc (req.AgentType is SOT)", frames[0].AgentType)
	}
	if len(frames[0].Subagents) != 0 {
		t.Fatalf("Subagents = %+v, want empty (rebuild does not restore refs)", frames[0].Subagents)
	}
}

// R9 — rebuild 命中時 meta.MatchedAgentType 設為 prober 回的 agent type
// （等於 req.AgentType 的同家情境）。PR #638 codex review round 2 #3 fix
// 的 unit-level guard：previously matchedType 被丟棄；現在保留進 trace
// payload 給 Inspector / Phase 5 reparent 用。
func TestApplyFrameEvent_RebuildHit_MetaIncludesMatchedAgentType(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	origSeam := firstAliveAgentInTreeFn
	firstAliveAgentInTreeFn = func(_ *Module, _ string) (string, int, error) {
		return "cc", 200, nil
	}
	t.Cleanup(func() { firstAliveAgentInTreeFn = origSeam })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 500)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.MatchedAgentType != "cc" {
		t.Fatalf("MatchedAgentType = %q, want cc (rebuild matched cc, same family as hook)", meta.MatchedAgentType)
	}
	if meta.Reason != "daemon_restart_recovery" {
		t.Fatalf("reason = %q, want daemon_restart_recovery", meta.Reason)
	}
}

// R10 — rebuild 命中時 matched type 與 hook event AgentType 不同（mismatch
// 場景，e.g. cc pane 跑 codex hook 但只 cc alive）。frame.AgentType 仍取
// req.AgentType（hook event 是 SOT），但 meta.MatchedAgentType 帶 rebuild
// 看到的真實 type — 給 Inspector / Phase 5 reparent loop 觸發 proxy collapse
// 補正用。PR #638 codex review round 2 #3 fix 的 mismatch 路徑 guard。
func TestApplyFrameEvent_RebuildHit_MismatchedTypePreservedInMeta(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	origSeam := firstAliveAgentInTreeFn
	firstAliveAgentInTreeFn = func(_ *Module, _ string) (string, int, error) {
		// rebuild 看到 pane 內 cc alive，但 hook event 是 codex（典型 mismatch）
		return "cc", 200, nil
	}
	t.Cleanup(func() { firstAliveAgentInTreeFn = origSeam })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 300, SenderStartTime: "t300"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 500)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.MatchedAgentType != "cc" {
		t.Fatalf("MatchedAgentType = %q, want cc (preserved diagnostic, even when ≠ req.AgentType)", meta.MatchedAgentType)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "codex" {
		t.Fatalf("frame.AgentType = %v, want codex (req.AgentType remains SOT despite mismatch)", frames)
	}
}

// R6 — rebuild 命中 → 接著 SubagentStart 仍正確累積 ref（驗證 native path 不壞）.
func TestApplyFrameEvent_RebuildHit_ThenSubagentStart(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	origSeam := firstAliveAgentInTreeFn
	firstAliveAgentInTreeFn = func(_ *Module, _ string) (string, int, error) {
		return "cc", 200, nil
	}
	t.Cleanup(func() { firstAliveAgentInTreeFn = origSeam })

	startReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
	if _, meta, err := m.applyFrameEvent(startReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 500); err != nil {
		t.Fatalf("rebuild SessionStart: %v", err)
	} else if meta.Reason != "daemon_restart_recovery" {
		t.Fatalf("rebuild SessionStart reason = %q, want daemon_restart_recovery", meta.Reason)
	}

	// After rebuild frame exists with subagents=[]; SubagentStart should
	// append a native ref via mutateSubagentsWithRetry (happy path).
	subReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SubagentStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(subReq, agentpkg.DeriveResult{Valid: true, Detail: map[string]any{"agent_id": "sub-1"}}, 600)
	if err != nil {
		t.Fatalf("SubagentStart: %v", err)
	}
	if meta.Decision != "updated_frame" || meta.Reason != "subagent_membership_changed" {
		t.Fatalf("SubagentStart meta = %q/%q, want updated_frame/subagent_membership_changed", meta.Decision, meta.Reason)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1", len(frames))
	}
	if len(frames[0].Subagents) != 1 {
		t.Fatalf("Subagents len = %d, want 1 (native ref accumulated after rebuild)", len(frames[0].Subagents))
	}
	if frames[0].Subagents[0].ID != "sub-1" {
		t.Fatalf("Subagents[0].ID = %q, want sub-1", frames[0].Subagents[0].ID)
	}
}

// R7 — SessionStart 走 findProxyParent 命中 → rebuild 不被呼叫（spy guard）.
func TestApplyFrameEvent_ProxyHit_SkipsRebuild(t *testing.T) {
	m := newProxyTestModule(t)
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

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

	origSeam := firstAliveAgentInTreeFn
	rebuildCalls := 0
	firstAliveAgentInTreeFn = func(_ *Module, _ string) (string, int, error) {
		rebuildCalls++
		return "cc", 200, nil
	}
	t.Cleanup(func() { firstAliveAgentInTreeFn = origSeam })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "proxy_subagent_attached" {
		t.Fatalf("reason = %q, want proxy_subagent_attached (PR-2b path)", meta.Reason)
	}
	if rebuildCalls != 0 {
		t.Fatalf("rebuild seam called %d times, want 0 (proxy hit should short-circuit)", rebuildCalls)
	}
}

// R8 — rebuild err → fail-soft → falls through to no_parent_fallback reason.
// (Commit 4 renamed the terminal fallback reason to make the降階 explicit;
// see plan §1.4 for the three-state contract.)
func TestApplyFrameEvent_RebuildErrorFailsSoft(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	origSeam := firstAliveAgentInTreeFn
	firstAliveAgentInTreeFn = func(_ *Module, _ string) (string, int, error) {
		return "", 0, errProbeFailure
	}
	t.Cleanup(func() { firstAliveAgentInTreeFn = origSeam })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 500)
	if err != nil {
		t.Fatalf("applyFrameEvent err = %v, want nil (fail-soft)", err)
	}
	if meta.Reason != "no_parent_fallback" {
		t.Fatalf("reason = %q, want no_parent_fallback (rebuild err → fail-soft)", meta.Reason)
	}
	if meta.Decision != "created_frame" {
		t.Fatalf("decision = %q, want created_frame", meta.Decision)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (fallback path still creates frame)", len(frames))
	}
}

// N1 (Commit 4) — rebuild returns silent miss ("", 0, nil): all lookup and
// rebuild paths miss without error → trace reason = no_parent_fallback.
// Distinguishes the "probe succeeded but found no alive agent" path from the
// "probe errored out" path (R8). Both end in the same降階 reason, but via
// different code branches — N1 is the dominant production path when a daemon
// restart hits a pane whose agent has genuinely exited before the hook fires.
func TestApplyFrameEvent_NoParentFallback_TraceReason(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	origSeam := firstAliveAgentInTreeFn
	firstAliveAgentInTreeFn = func(_ *Module, _ string) (string, int, error) {
		return "", 0, nil
	}
	t.Cleanup(func() { firstAliveAgentInTreeFn = origSeam })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 500)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "no_parent_fallback" {
		t.Fatalf("reason = %q, want no_parent_fallback (silent rebuild miss)", meta.Reason)
	}
	if meta.Decision != "created_frame" {
		t.Fatalf("decision = %q, want created_frame", meta.Decision)
	}
	if meta.ParentFrameID != "" {
		t.Fatalf("ParentFrameID = %q, want empty (降階 path has no parent)", meta.ParentFrameID)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (fallback still creates frame)", len(frames))
	}
	if frames[0].AgentType != "cc" {
		t.Fatalf("AgentType = %q, want cc (req.AgentType is SOT)", frames[0].AgentType)
	}
}

// N3 — parent 命中時 rebuild helper 不被呼叫（spy guard for FindByPanePID hit）.
func TestApplyFrameEvent_RebuildSkipped_WhenParentFound(t *testing.T) {
	m := newProxyTestModule(t)
	// Seed a cc parent frame with PID 100.
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	// readProcessInfo returns PPID=100 so FindByPanePID(%5, 100) hits the
	// seeded frame. Liveness stubs don't matter for FindByPanePID path.
	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		// sender pid 300 maps up to cc parent via PPID chain.
		if pid == 300 {
			return agentpkg.ProcessInfo{PID: 300, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	// processStartTime/isPidAlive not overridden because we want findProxyParent
	// to miss on liveness (parent.PID 100 is not alive under default stubs in
	// newTestModule: isPidAliveFn=true but processStartTimeFn returns the
	// default "Sun Apr 20 01:30:00 2026" which != seed "t100"). So proxy path
	// bails, and we land in the non-proxy FindByPanePID lookup at line 221-228.

	origSeam := firstAliveAgentInTreeFn
	rebuildCalls := 0
	firstAliveAgentInTreeFn = func(_ *Module, _ string) (string, int, error) {
		rebuildCalls++
		return "", 0, nil
	}
	t.Cleanup(func() { firstAliveAgentInTreeFn = origSeam })

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", EventName: "SessionStart", AgentType: "cc", SenderPID: 300, SenderStartTime: "t300"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 500)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "parent_frame_found" {
		t.Fatalf("reason = %q, want parent_frame_found (FindByPanePID hit)", meta.Reason)
	}
	if rebuildCalls != 0 {
		t.Fatalf("rebuild seam called %d times, want 0 (parent found short-circuits)", rebuildCalls)
	}
}

func (e errorString) Error() string { return string(e) }
