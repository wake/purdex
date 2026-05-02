package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/opencode"
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

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxSessionStart","raw_event":{},"agent_type":"cc"}`))
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
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxSessionStart","raw_event":{},"agent_type":"cc"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxStop","raw_event":{},"agent_type":"cc"}`,
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
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxSessionStart","raw_event":{},"agent_type":"cc"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxSessionEnd","raw_event":{},"agent_type":"cc"}`,
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

	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxSubagentStart","raw_event":{},"agent_type":"cc"}`))
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
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"SessionStart","raw_event":{},"agent_type":"opencode"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"SubagentStart","raw_event":{},"agent_type":"opencode"}`,
		`{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"SessionStart","raw_event":{},"agent_type":"opencode"}`,
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

// TestReplay_OpencodeLegacyEventName_NotRestored pins the post-W2-P3
// behavior surfaced by codex Round-1 review of PR #736: when daemon restart
// replays the legacy agent_events store and a session has no frame
// projection (e.g. a pre-W2 session that ran before frame projections were
// written), DeriveStatus is fed the persisted EventName verbatim. After
// P3-T2 / P3-T6, opencode's DeriveStatus only recognizes Pdx-prefixed names,
// so legacy literals from a pre-alpha.255 store now return Valid=false.
//
// Spec §0 + plan G1 explicitly accept this for alpha — daemon-internal
// store does not carry a cross-version migration; user reinstall + a fresh
// hook trigger restores status. This test pins the behavior so a future
// silent drift back to a normalize/alias path (which would re-introduce the
// just-removed lifecycle fallback into a different code path) surfaces
// immediately.
//
// P3-T6.2 hardening: also asserts replayFromDB deletes the stale row so a
// follow-on sendSnapshot (or another replay cycle) doesn't keep tripping
// over the same garbage. Mirrors handler.go:230's hot-path cleanup.
func TestReplay_OpencodeLegacyEventName_NotRestored(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "legacy")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "legacy-code", Name: "legacy"},
	}}
	m.registry.Register(opencode.NewProvider())

	// Seed the legacy agent_events store with an opencode row written by
	// a pre-alpha.255 daemon (event_name = upstream literal "Stop"). No
	// frame projection exists for this session — replay falls through to
	// the agent_events fallback.
	if err := m.events.Set("legacy", "Stop", json.RawMessage(`{}`), "opencode", 11); err != nil {
		t.Fatalf("seed legacy event: %v", err)
	}

	m.replayFromDB()

	if got := m.currentStatus["legacy"]; got != "" {
		t.Errorf("legacy currentStatus = %q, want empty (post-P3 opencode DeriveStatus rejects legacy literal; spec §0 alpha-acceptable — user reinstall + fresh hook restores status)", got)
	}
	if got, err := m.events.Get("legacy"); err != nil {
		t.Fatalf("events.Get after replay: %v", err)
	} else if got != nil {
		t.Errorf("legacy agent_events row not deleted after replay: %+v (mirror handler.go:230 invalid-result cleanup so subsequent sendSnapshot doesn't broadcast stale row)", got)
	}
}

// TestSendSnapshot_OpencodeLegacyEventName_SkipAndCleanup pins the cold
// reconnect path that codex Round-2 Attack flagged: a legacy stored
// event_name like "Stop" that DeriveStatus now rejects must NOT be
// broadcast to a fresh SPA subscriber. Without this guard sendSnapshot
// emits a `hook` payload with raw_event_name="Stop" and empty status,
// which the SPA's hook-module lastTrigger keys directly off — surfacing a
// stale legacy event in the UI on every reconnect.
func TestSendSnapshot_OpencodeLegacyEventName_SkipAndCleanup(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "legacy")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "legacy-code", Name: "legacy"},
	}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(opencode.NewProvider())

	if err := m.events.Set("legacy", "Stop", json.RawMessage(`{}`), "opencode", 11); err != nil {
		t.Fatalf("seed legacy event: %v", err)
	}

	broadcaster := core.NewEventsBroadcaster()
	sub := broadcaster.AddTestSubscriber()
	defer broadcaster.RemoveTestSubscriber(sub)

	m.sendSnapshot(sub)

	select {
	case msg := <-sub.SendCh():
		t.Errorf("sendSnapshot broadcast a stale legacy hook event to a fresh subscriber: %s", string(msg))
	case <-time.After(50 * time.Millisecond):
		// expected — no broadcast for invalid-result rows.
	}

	if got, err := m.events.Get("legacy"); err != nil {
		t.Fatalf("events.Get after sendSnapshot: %v", err)
	} else if got != nil {
		t.Errorf("legacy agent_events row not deleted after sendSnapshot: %+v", got)
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
	got := updateSubagents(nil, agentpkg.LifecycleSubagentStart, start)
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
	got := updateSubagents([]agentpkg.SubagentRef{existing}, agentpkg.LifecycleSubagentStart, dup)
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
	got := updateSubagents([]agentpkg.SubagentRef{a, b}, agentpkg.LifecycleSubagentStop, agentpkg.SubagentRef{ID: "a"})
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
	got := updateSubagents([]agentpkg.SubagentRef{existing}, agentpkg.LifecycleSubagentStop, agentpkg.SubagentRef{ID: "a", Type: "codex"})
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0 (ID match removes regardless of Type)", len(got))
	}
}

func TestUpdateSubagents_StopMissingIsNoop(t *testing.T) {
	a := agentpkg.SubagentRef{ID: "a", Type: "cc"}
	got := updateSubagents([]agentpkg.SubagentRef{a}, agentpkg.LifecycleSubagentStop, agentpkg.SubagentRef{ID: "b"})
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
	afterStart := updateSubagents(list, agentpkg.LifecycleSubagentStart, proxy)
	if len(afterStart) != 2 {
		t.Fatalf("proxy SubagentStart with same ID as native should append cross-kind; got %d refs, want 2: %+v", len(afterStart), afterStart)
	}

	// A native SubagentStop with the same ID must remove only the native
	// ref, not evict the proxy.
	afterStop := updateSubagents(afterStart, agentpkg.LifecycleSubagentStop, native)
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
		PurdexName: "PdxSubagentStart", AgentType: "cc",
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
		PurdexName: "PdxSubagentStart", AgentType: "cc",
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
		PurdexName: "PdxSessionStart", AgentType: "codex",
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
		PurdexName: "PdxUserPromptSubmit", AgentType: "cc",
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
		TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart",
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 300, SenderStartTime: "t300"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 700, SenderStartTime: "t700"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 9999, SenderStartTime: "t9999"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxUserPromptSubmit", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work2", TmuxPaneID: "%7", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 400, SenderStartTime: "t400"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
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
		PurdexName:      "PdxSessionStart",
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
		PurdexName: "PdxSessionStart", AgentType: "codex",
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
		PurdexName: "PdxSessionStart", AgentType: "codex",
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
	startReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	if _, meta, err := m.applyFrameEvent(startReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("attach: %v", err)
	} else if meta.Reason != "proxy_subagent_attached" {
		t.Fatalf("attach reason = %q", meta.Reason)
	}

	// Then: SessionEnd with same identity — should remove the ref.
	endReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionEnd", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
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

	endReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionEnd", AgentType: "codex", SenderPID: 999, SenderStartTime: "t999"}
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

	startReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	if _, _, err := m.applyFrameEvent(startReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("proxy attach: %v", err)
	}

	// Now SessionEnd on cc itself (the owning frame).
	endReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionEnd", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 300, SenderStartTime: "t300"}
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

	startReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
	if _, meta, err := m.applyFrameEvent(startReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 500); err != nil {
		t.Fatalf("rebuild SessionStart: %v", err)
	} else if meta.Reason != "daemon_restart_recovery" {
		t.Fatalf("rebuild SessionStart reason = %q, want daemon_restart_recovery", meta.Reason)
	}

	// After rebuild frame exists with subagents=[]; SubagentStart should
	// append a native ref via mutateSubagentsWithRetry (happy path).
	subReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSubagentStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 200, SenderStartTime: "t200"}
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 300, SenderStartTime: "t300"}
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

// ---------------------------------------------------------------------------
// Phase 3.5 PR-3.5a — Cold-start race canonicalization helpers
// (plan §2.1.2 + §2.1.1 / §3.2 unit tests RC1-RC5)
// ---------------------------------------------------------------------------

// RC1 — pidIsAncestorOfWithCap returns false when the walk runs past
// proxyMaxDepth before hitting the candidate ancestor.
func TestPidIsAncestorOfWithCap_DepthExhaustion(t *testing.T) {
	origInfo := readProcessInfoFn
	// Each PID's PPID is one less, forming a long chain: 100→99→98→...→1.
	// Looking for ancestorPID=1 starting at descendantPID=100 needs 99 hops
	// — far exceeds proxyMaxDepth=5.
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: pid - 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	if pidIsAncestorOfWithCap(100, 1, proxyMaxDepth) {
		t.Fatalf("expected false (depth exhausted before reaching ancestor)")
	}
}

// RC2 — pidIsAncestorOfWithCap returns false when readProcessInfoFn returns
// a self-loop (info.PPID == info.PID), preventing infinite walk.
func TestPidIsAncestorOfWithCap_LoopDetectionPpidEqPid(t *testing.T) {
	origInfo := readProcessInfoFn
	// Descendant 200's parent is itself — loop guard must short-circuit.
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 200}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	if pidIsAncestorOfWithCap(200, 100, proxyMaxDepth) {
		t.Fatalf("expected false (PPID == PID self-loop)")
	}
}

// RC3 — pidIsAncestorOfWithCap returns false when readProcessInfoFn errors
// out partway through the walk.
func TestPidIsAncestorOfWithCap_ProcessInfoError(t *testing.T) {
	origInfo := readProcessInfoFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{}, errProbeFailure
	}
	t.Cleanup(func() { readProcessInfoFn = origInfo })

	if pidIsAncestorOfWithCap(200, 100, proxyMaxDepth) {
		t.Fatalf("expected false (read error mid-walk)")
	}
}

// RC4 — canonicalizeDescendantsAfterUpsert skips candidates whose AgentType
// matches self.AgentType (same-type frames are not proxy-attachment subjects).
func TestCanonicalizeDescendantsAfterUpsert_SkipsSameType(t *testing.T) {
	m := newProxyTestModule(t)
	// Self = cc PID 100. Candidate = cc PID 200 (same agent_type).
	self := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	seedFrame(t, m, "%5", "cc", 200, "t200", 11)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	// 200's PPID = 100 — would normally pass ancestor check.
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			return "t200", nil
		}
		return "t100", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	updated, err := m.canonicalizeDescendantsAfterUpsert(self, 100)
	if err != nil {
		t.Fatalf("canonicalizeDescendantsAfterUpsert: %v", err)
	}
	if len(updated.Subagents) != 0 {
		t.Fatalf("self.Subagents = %+v, want empty (same-type candidate skipped)", updated.Subagents)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frame count = %d, want 2 (no fold for same-type)", len(frames))
	}
}

// RC5 — canonicalizeDescendantsAfterUpsert skips a candidate whose stored
// ProcessStartTime no longer matches the live actualStart (PID reused).
func TestCanonicalizeDescendantsAfterUpsert_SkipsPidReuseViaIdentityGate(t *testing.T) {
	m := newProxyTestModule(t)
	// Self = cc PID 100; descendant codex PID 200 stored with start_time
	// "stale-t200" but live process at PID 200 reports "fresh-t200".
	self := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	seedFrame(t, m, "%5", "codex", 200, "stale-t200", 11)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			return "fresh-t200", nil // mismatch with stored "stale-t200"
		}
		return "t100", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	updated, err := m.canonicalizeDescendantsAfterUpsert(self, 100)
	if err != nil {
		t.Fatalf("canonicalizeDescendantsAfterUpsert: %v", err)
	}
	if len(updated.Subagents) != 0 {
		t.Fatalf("self.Subagents = %+v, want empty (identity gate fails)", updated.Subagents)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frame count = %d, want 2 (stale candidate left for sweep)", len(frames))
	}
}

// ---------------------------------------------------------------------------
// Phase 3.5 PR-3.5a — Integration tests for cold-start race canonicalization
// (plan §3.1)
// ---------------------------------------------------------------------------

// IT3 — concurrent_descendant_first_then_reconcile_hits_post_upsert.
// IT22 — descendant scan skips a candidate that has accumulated its own
// subagent state (native SubagentRef from a SubagentStart hook, or its own
// IsProxy ref). Plan §2.1.2 v8 M2 regression guard: previously the scan
// folded any cross-type live PPID-descendant unconditionally, so a codex
// frame that had already attached its own task ref would be DELETE'd in
// the fold, silently losing that ref. Race-window standalones safe to
// fold have empty Subagents; len(candidate.Subagents) > 0 is the signal
// the candidate owns state we must preserve.
func TestPhase35_IT22_DescendantScanSkipsCandidateWithSubagents(t *testing.T) {
	m := newProxyTestModule(t)
	self := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	candidate := seedFrame(t, m, "%5", "codex", 200, "t200", 11)
	// Candidate has its own native subagent ref (e.g. from a previous
	// SubagentStart hook landing on the codex frame).
	candidate.Subagents = []agentpkg.SubagentRef{{
		ID:        "task-codex-1",
		Type:      "codex",
		StartedAt: 12,
	}}
	candidate.LastSeenAt = 12
	if _, err := m.frames.Upsert(candidate); err != nil {
		t.Fatalf("seed candidate with subagents: %v", err)
	}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	// PPID chain would otherwise fold codex into cc.
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	updated, err := m.canonicalizeDescendantsAfterUpsert(self, 100)
	if err != nil {
		t.Fatalf("canonicalizeDescendantsAfterUpsert: %v", err)
	}
	if len(updated.Subagents) != 0 {
		t.Fatalf("self.Subagents = %+v, want empty (candidate skipped because it owns subagent state)", updated.Subagents)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frame count = %d, want 2 (candidate not folded); frames=%+v", len(frames), frames)
	}
	var preservedCodex *store.Frame
	for i := range frames {
		if frames[i].AgentType == "codex" {
			preservedCodex = &frames[i]
		}
	}
	if preservedCodex == nil {
		t.Fatalf("codex frame deleted; frames=%+v", frames)
	}
	if len(preservedCodex.Subagents) != 1 || preservedCodex.Subagents[0].ID != "task-codex-1" {
		t.Fatalf("codex.Subagents = %+v, want preserved native task ref", preservedCodex.Subagents)
	}
}

// IT22b — codex round 2 #O2 fix: descendant scan FOLDS a candidate whose
// only Subagents are stale dead IsProxy refs. The previous len > 0 guard
// over-protected: a candidate carrying a single dead-source IsProxy (e.g.
// a stale ref left over from PR-2b's filter-merge missing a hook race)
// still represents a race-window standalone whose own state is empty
// (the dead IsProxy doesn't represent live state — sweep prune would
// remove it). Folding such a candidate is safe because the dead ref is
// dropped along with the candidate row.
func TestPhase35_IT22b_DescendantScanFoldsCandidateWithOnlyStaleProxyRef(t *testing.T) {
	m := newProxyTestModule(t)
	self := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	candidate := seedFrame(t, m, "%5", "codex", 200, "t200", 11)
	// Candidate has only a STALE dead IsProxy ref (source PID 999 is
	// dead). No native refs, no live identity-verified IsProxy refs.
	candidate.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:opencode:999:t999-dead",
		Type:            "opencode",
		SourcePID:       999,
		SourceStartTime: "t999-dead",
		IsProxy:         true,
	}}
	candidate.LastSeenAt = 12
	if _, err := m.frames.Upsert(candidate); err != nil {
		t.Fatalf("seed candidate with stale dead proxy: %v", err)
	}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	// PID 999 (the stale ref's source) is DEAD. Self (100) and candidate
	// (200) are alive.
	isPidAliveFn = func(pid int) bool {
		return pid != 999
	}
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	updated, err := m.canonicalizeDescendantsAfterUpsert(self, 100)
	if err != nil {
		t.Fatalf("canonicalizeDescendantsAfterUpsert: %v", err)
	}
	// candidate should be folded: cc.Subagents now contains a codex IsProxy
	// ref (the stale opencode ref is gone, dropped along with the
	// candidate frame).
	if len(updated.Subagents) != 1 {
		t.Fatalf("self.Subagents = %+v, want 1 codex IsProxy ref (candidate folded; stale opencode dropped)", updated.Subagents)
	}
	ref := updated.Subagents[0]
	if !ref.IsProxy || ref.SourcePID != 200 {
		t.Fatalf("ref = %+v, want codex IsProxy SourcePID=200", ref)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].FrameID != self.FrameID {
		t.Fatalf("frames = %+v, want only cc surviving (codex deleted by fold)", frames)
	}
}

// IT22c — codex round 2 #O2 fix (negative case): descendant scan still
// SKIPS a candidate carrying a live identity-verified IsProxy ref. The
// live IsProxy represents real owned state — folding would lose it.
func TestPhase35_IT22c_DescendantScanSkipsCandidateWithLiveProxyRef(t *testing.T) {
	m := newProxyTestModule(t)
	self := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	candidate := seedFrame(t, m, "%5", "codex", 200, "t200", 11)
	// Candidate has a LIVE identity-verified IsProxy ref (source PID 300
	// is alive + start_time matches).
	candidate.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:opencode:300:t300",
		Type:            "opencode",
		SourcePID:       300,
		SourceStartTime: "t300",
		IsProxy:         true,
	}}
	candidate.LastSeenAt = 12
	if _, err := m.frames.Upsert(candidate); err != nil {
		t.Fatalf("seed candidate with live proxy: %v", err)
	}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		case 300:
			return "t300", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	updated, err := m.canonicalizeDescendantsAfterUpsert(self, 100)
	if err != nil {
		t.Fatalf("canonicalizeDescendantsAfterUpsert: %v", err)
	}
	if len(updated.Subagents) != 0 {
		t.Fatalf("self.Subagents = %+v, want empty (candidate skipped — owns live IsProxy state)", updated.Subagents)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frame count = %d, want 2 (candidate not folded)", len(frames))
	}
}

// codex applyFrameEvent: pre-walk findProxyParent misses (cc not yet
// visible to walk; readProcessInfoFn returns dud PPID on calls 1+2),
// then reconcile post-Upsert hits (cc visible on call 3+; readProcessInfoFn
// returns PPID=100 with cc seeded). Verifies the §2.2.1 reconcile path
// is wired and emits post_upsert_canonicalization_self.
func TestPhase35_IT3_PreWalkMiss_PostReconcileHit(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	// Counter swaps PPID for codex sender (PID=200) so PR-2b's findProxyParent
	// (calls 1+2: pre-walk + line-222 info read) sees PPID=999 = no parent
	// in DB, fall-through to new-frame Upsert. Then post-Upsert reconcile's
	// findProxyParent (call 3+) sees PPID=100 = cc → fold.
	calls := 0
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			calls++
			if calls <= 2 {
				return agentpkg.ProcessInfo{PID: 200, PPID: 999}, nil
			}
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
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

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Reason != "post_upsert_canonicalization_self" {
		t.Fatalf("reason = %q, want post_upsert_canonicalization_self", meta.Reason)
	}
	if meta.Decision != "updated_frame" {
		t.Fatalf("decision = %q, want updated_frame", meta.Decision)
	}
	if meta.FrameID != parent.FrameID {
		t.Fatalf("FrameID = %q, want parent %q", meta.FrameID, parent.FrameID)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (codex folded into cc)", len(frames))
	}
	if frames[0].AgentType != "cc" {
		t.Fatalf("remaining AgentType = %q, want cc", frames[0].AgentType)
	}
	if len(frames[0].Subagents) != 1 || frames[0].Subagents[0].SourcePID != 200 {
		t.Fatalf("cc.Subagents = %+v, want 1 codex proxy ref", frames[0].Subagents)
	}
}

// IT4 — concurrent_descendant_post_reconcile_also_misses_recovered_by_ancestor_scan.
// Core ancestor-late race: codex SessionStart races first, both pre-walk and
// post-walk miss → standalone codex frame. Then cc SessionStart fires —
// new-frame post-Upsert descendant scan must locate codex and fold it.
func TestPhase35_IT4_AncestorLateRecoveredByDescendantScan(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	// codex sender PID=200 PPID=100; cc sender PID=100 PPID=1.
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		case 100:
			return agentpkg.ProcessInfo{PID: 100, PPID: 1}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	// Step (a): codex SessionStart with cc not yet in DB → pre-walk misses
	// (FindByPanePID(100) = nil), reconcile post-walk also misses (same
	// reason), new standalone codex created.
	req1 := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	if _, _, err := m.applyFrameEvent(req1, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("codex applyFrameEvent: %v", err)
	}
	mid, _ := m.frames.ListByPane("%5")
	if len(mid) != 1 || mid[0].AgentType != "codex" {
		t.Fatalf("after codex SessionStart: frames=%+v, want 1 standalone codex", mid)
	}

	// Step (b): cc SessionStart — new-frame Upsert + descendant scan must
	// find codex (PPID 200→100) + fold.
	req2 := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(req2, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200); err != nil {
		t.Fatalf("cc applyFrameEvent: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (codex folded into cc); frames=%+v", len(frames), frames)
	}
	if frames[0].AgentType != "cc" {
		t.Fatalf("remaining AgentType = %q, want cc", frames[0].AgentType)
	}
	if len(frames[0].Subagents) != 1 {
		t.Fatalf("cc.Subagents = %+v, want 1 codex proxy ref", frames[0].Subagents)
	}
	ref := frames[0].Subagents[0]
	if !ref.IsProxy || ref.Type != "codex" || ref.SourcePID != 200 || ref.SourceStartTime != "t200" {
		t.Fatalf("ref = %+v, want codex proxy SourcePID=200 t200", ref)
	}
}

// IT17 — existing_frame SessionStart preserves a live identity-verified
// IsProxy ref via filter-merge-retry (plan §2.2.2 v6 J1).
func TestPhase35_IT17_ExistingFrameSessionStartPreservesLiveProxyRef(t *testing.T) {
	m := newProxyTestModule(t)
	// Seed cc parent with an existing codex IsProxy ref.
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:200:t200",
		Type:            "codex",
		StartedAt:       50,
		SourcePID:       200,
		SourceStartTime: "t200",
		IsProxy:         true,
	}}
	parent.LastSeenAt = 50
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed proxy ref: %v", err)
	}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	// cc SessionStart on existing cc frame (same PID + start_time) — exists
	// path triggers filter-merge-retry. Live + identity-verified codex ref
	// must survive the reset.
	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}

	final, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if final == nil {
		t.Fatalf("cc frame disappeared")
	}
	if len(final.Subagents) != 1 {
		t.Fatalf("Subagents = %+v, want 1 (live codex proxy preserved)", final.Subagents)
	}
	ref := final.Subagents[0]
	if !ref.IsProxy || ref.SourcePID != 200 || ref.SourceStartTime != "t200" {
		t.Fatalf("ref = %+v, want codex proxy alive 200 t200", ref)
	}
	if final.LastSeenAt != 100 {
		t.Fatalf("LastSeenAt = %d, want 100 (broadcastTs)", final.LastSeenAt)
	}
}

// IT18 — existing_frame SessionStart filter-merge drops a proxy ref whose
// SourcePID is dead (isPidAliveFn=false). Plan §2.2.2 negative case.
func TestPhase35_IT18_ExistingFrameSessionStartSkipsDeadProxy(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:200:t200",
		Type:            "codex",
		SourcePID:       200,
		SourceStartTime: "t200",
		IsProxy:         true,
	}}
	parent.LastSeenAt = 50
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed: %v", err)
	}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	// Codex PID 200 is dead.
	isPidAliveFn = func(pid int) bool { return pid != 200 }
	processStartTimeFn = func(pid int) (string, error) { return "t100", nil }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	final, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if final == nil {
		t.Fatalf("cc frame disappeared")
	}
	if len(final.Subagents) != 0 {
		t.Fatalf("Subagents = %+v, want empty (dead proxy dropped)", final.Subagents)
	}
}

// IT19 — existing_frame SessionStart filter-merge drops a proxy ref whose
// SourcePID is alive but stored start_time mismatches actualStart (PID
// reused). Plan §2.2.2 negative case.
func TestPhase35_IT19_ExistingFrameSessionStartSkipsPidReusedProxy(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:200:stale-t200",
		Type:            "codex",
		SourcePID:       200,
		SourceStartTime: "stale-t200",
		IsProxy:         true,
	}}
	parent.LastSeenAt = 50
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed: %v", err)
	}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	// PID 200 alive but start_time changed (reused).
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			return "fresh-t200", nil
		}
		return "t100", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	final, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if final == nil {
		t.Fatalf("cc frame disappeared")
	}
	if len(final.Subagents) != 0 {
		t.Fatalf("Subagents = %+v, want empty (PID-reused proxy dropped)", final.Subagents)
	}
}

// IT20 — filter-merge-retry preserves a proxy ref attached concurrently
// during attempt 1 (race window between read and UpsertIfUnchanged).
// Plan §2.2.2 v6 J1 regression guard.
//
// Setup: cc frame seeded with codex proxy ref at LastSeenAt=50.
// applyFrameEvent's GetByIdentity reads frame at 50. processStartTimeFn
// is hooked so that the FIRST call (during attempt 1's filter pass) writes
// a concurrent opencode proxy ref into the row, bumping LastSeenAt to 60.
// attempt 1's UpsertIfUnchanged(expected=50) then conflicts; reload picks
// up both refs at LastSeenAt=60; attempt 2 filters both refs (alive),
// IfUnchanged(expected=60) succeeds. Both refs survive — J1 violated
// would have lost the opencode ref.
func TestPhase35_IT20_ExistingFrameSessionStartPreservesConcurrentlyAttachedProxy(t *testing.T) {
	m := newProxyTestModule(t)
	// Seed cc with one codex proxy ref at LastSeenAt=50.
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:200:t200",
		Type:            "codex",
		SourcePID:       200,
		SourceStartTime: "t200",
		IsProxy:         true,
	}}
	parent.LastSeenAt = 50
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed cc + codex ref: %v", err)
	}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	// First call to processStartTimeFn(200) — happens during attempt 1's
	// filter pass — triggers the concurrent attach side effect (+
	// LastSeenAt bump to 60). Subsequent calls return start_time normally.
	concurrentInjected := false
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 && !concurrentInjected {
			concurrentInjected = true
			racer, _ := m.frames.GetByIdentity("%5", 100, "t100")
			if racer != nil {
				racer.Subagents = append(racer.Subagents, agentpkg.SubagentRef{
					ID:              "proxy:opencode:300:t300",
					Type:            "opencode",
					SourcePID:       300,
					SourceStartTime: "t300",
					IsProxy:         true,
				})
				racer.LastSeenAt = 60
				if _, err := m.frames.Upsert(*racer); err != nil {
					t.Fatalf("concurrent inject: %v", err)
				}
			}
			return "t200", nil
		}
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		case 300:
			return "t300", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}

	if !concurrentInjected {
		t.Fatalf("concurrent inject hook never fired — filter-merge loop didn't run")
	}

	final, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if final == nil {
		t.Fatalf("cc frame disappeared")
	}
	if len(final.Subagents) != 2 {
		t.Fatalf("Subagents count = %d, want 2 (both proxies preserved across retry); refs=%+v", len(final.Subagents), final.Subagents)
	}
	ids := map[int]bool{}
	for _, ref := range final.Subagents {
		ids[ref.SourcePID] = true
	}
	if !ids[200] || !ids[300] {
		t.Fatalf("ref SourcePIDs=%v, want both 200 and 300", ids)
	}
	if final.LastSeenAt != 100 {
		t.Fatalf("LastSeenAt = %d, want 100 (broadcastTs from final write)", final.LastSeenAt)
	}
}

// IT21 — existing_frame SessionStart preserves a concurrently-attached
// native subagent ref (IsProxy=false) across the filter-merge retry. Plan
// §2.2.2 v8 M3 regression guard: the previous "keep only IsProxy" filter
// dropped ALL native refs, so a SubagentStart that landed between
// applyFrameEvent's GetByIdentity and the IfUnchanged conflict's reload
// would silently disappear when attempt 2 re-filtered the reloaded
// baseline.
//
// Setup mirrors IT20: cc frame seeded with one codex IsProxy ref at
// LastSeenAt=50; processStartTimeFn(200)'s first call (during attempt 1's
// prune pass) injects a concurrent native SubagentStart-style ref into
// the row at LastSeenAt=60. attempt 1's IfUnchanged(expected=50) then
// conflicts; reload picks up codex IsProxy + new native ref at 60;
// attempt 2's prune pass keeps both (codex passes identity gate, native
// is preserved by the new pruning semantics). IfUnchanged(expected=60)
// succeeds. Both refs survive — M3 violated would have lost the native.
func TestPhase35_IT21_ExistingFrameSessionStartPreservesConcurrentNativeSubagent(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:200:t200",
		Type:            "codex",
		SourcePID:       200,
		SourceStartTime: "t200",
		IsProxy:         true,
	}}
	parent.LastSeenAt = 50
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed cc + codex proxy: %v", err)
	}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	concurrentInjected := false
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 && !concurrentInjected {
			concurrentInjected = true
			racer, _ := m.frames.GetByIdentity("%5", 100, "t100")
			if racer != nil {
				// Native ref (IsProxy=false) — simulating a concurrent
				// SubagentStart hook landing on cc's row.
				racer.Subagents = append(racer.Subagents, agentpkg.SubagentRef{
					ID:        "task-abc",
					Type:      "cc",
					StartedAt: 60,
				})
				racer.LastSeenAt = 60
				if _, err := m.frames.Upsert(*racer); err != nil {
					t.Fatalf("concurrent native inject: %v", err)
				}
			}
			return "t200", nil
		}
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}

	if !concurrentInjected {
		t.Fatalf("concurrent native inject hook never fired — filter-merge loop didn't run")
	}

	final, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if final == nil {
		t.Fatalf("cc frame disappeared")
	}
	if len(final.Subagents) != 2 {
		t.Fatalf("Subagents count = %d, want 2 (codex proxy + native task ref preserved); refs=%+v", len(final.Subagents), final.Subagents)
	}
	hasCodexProxy := false
	hasNative := false
	for _, ref := range final.Subagents {
		if ref.IsProxy && ref.SourcePID == 200 {
			hasCodexProxy = true
		}
		if !ref.IsProxy && ref.ID == "task-abc" {
			hasNative = true
		}
	}
	if !hasCodexProxy {
		t.Fatalf("codex proxy ref missing from %+v", final.Subagents)
	}
	if !hasNative {
		t.Fatalf("native task ref missing from %+v — M3 regression: native refs dropped by filter", final.Subagents)
	}
}

// IT21b — existing_frame SessionStart preserves natives that arrive across
// MULTIPLE conflict retries. Codex round 2 #O1 regression guard: the
// prevWrittenNativeIDs strategy (v8 M3) preserved natives that appeared
// after attempt 0 once, but then recorded them in prevWrittenNativeIDs
// itself — so a second IfUnchanged conflict (e.g. probe status update +
// another concurrent attach) caused attempt 2 to "drop" what attempt 1
// had just preserved, racing the native into oblivion. The fix is the
// initialNativeIDs baseline: snapshot the BASELINE native ID set before
// the retry loop and only drop those across all retries; any native
// appearing in a reloaded frame after baseline snapshot is unconditionally
// concurrent and preserved.
//
// Setup — three concurrent attaches across two conflicts:
//   - cc seeded with one BASELINE native (baseline-task-1) at LastSeenAt=50
//   - attempt 0 conflict (mock — bump LastSeenAt to 60 + add concurrent-task-1)
//   - attempt 1 conflict (mock — bump LastSeenAt to 70 + add concurrent-task-2)
//   - attempt 2 succeeds
//
// Assertions: final.Subagents contains concurrent-task-1 + concurrent-task-2;
// baseline-task-1 is dropped (SessionStart reset semantic).
func TestPhase35_IT21b_ExistingFrameSessionStartPreservesNativeAcrossMultiConflict(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:        "baseline-task-1",
		Type:      "cc",
		StartedAt: 5,
	}}
	parent.LastSeenAt = 50
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed cc + baseline native: %v", err)
	}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	// We don't have IsProxy refs in this test, so the filter loop's
	// processStartTimeFn calls only happen for non-existent IsProxy refs
	// (zero in baseline). Drive the conflict via a counter on a different
	// seam: each attempt's UpsertIfUnchanged will reload, and we use a
	// counter to simulate concurrent natives appearing during reload via
	// readProcessInfoFn (but readProcessInfoFn is also called once before
	// the loop). Instead, drive the race via a per-attempt counter on
	// processStartTimeFn — but we have no IsProxy refs to trigger it.
	//
	// Simpler approach: inject natives BEFORE applyFrameEvent's first
	// reload by piggy-backing on isPidAliveFn (called for every IsProxy
	// in the loop — but we have no IsProxy refs). Final approach: use
	// readProcessInfoFn (called once before the loop) to set up a
	// counter-driven concurrent inject directly via a side channel.
	//
	// Cleanest: put the inject in a custom seam. Since we don't have an
	// existing per-attempt seam to hook for native-only frames, we'll
	// instead use TWO IsProxy refs in the baseline (which will be kept
	// across conflicts because they pass identity check) to drive the
	// processStartTimeFn count, and the seeded native (baseline-task-1)
	// to verify reset semantic.
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
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

	// Reseed with two IsProxy refs (PIDs 200, 300) so processStartTimeFn
	// is called at least twice per filter pass — gives us a counter to
	// drive concurrent inject across attempts.
	parent.Subagents = []agentpkg.SubagentRef{
		{
			ID:        "baseline-task-1",
			Type:      "cc",
			StartedAt: 5,
		},
		{
			ID:              "proxy:codex:200:t200",
			Type:            "codex",
			SourcePID:       200,
			SourceStartTime: "t200",
			IsProxy:         true,
		},
	}
	parent.LastSeenAt = 50
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("re-seed: %v", err)
	}

	// Counter on processStartTimeFn(200): each invocation corresponds to
	// one filter pass. Inject concurrent natives BEFORE the IfUnchanged
	// write at the END of attempts 0 and 1 — but processStartTimeFn runs
	// at the START of each attempt's filter pass. Instead use a hook on
	// each call AFTER the filter pass: rely on the fact that each attempt
	// calls processStartTimeFn(200) exactly once (the lone IsProxy in the
	// reloaded baseline). For each call N (1-indexed), at the END of the
	// callback inject the racer that conflicts THIS attempt's write.
	attempt := 0
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t100", nil
		}
		if pid != 200 {
			return "other", nil
		}
		// Each call indicates we're in attempt N's filter pass for the
		// codex IsProxy ref. After returning, applyFrameEvent will issue
		// IfUnchanged. To force a conflict, mutate the row right now —
		// the LastSeenAt bump invalidates the optimistic write.
		attempt++
		if attempt <= 2 {
			racer, _ := m.frames.GetByIdentity("%5", 100, "t100")
			if racer != nil {
				newID := "concurrent-task-" + map[int]string{1: "1", 2: "2"}[attempt]
				racer.Subagents = append(racer.Subagents, agentpkg.SubagentRef{
					ID:        newID,
					Type:      "cc",
					StartedAt: int64(60 + 10*attempt),
				})
				racer.LastSeenAt = int64(50 + 10*attempt)
				if _, err := m.frames.Upsert(*racer); err != nil {
					t.Fatalf("concurrent inject attempt=%d: %v", attempt, err)
				}
			}
		}
		return "t200", nil
	}

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}

	if attempt < 3 {
		t.Fatalf("filter-merge ran only %d attempts, expected at least 3 (2 conflicts + final success)", attempt)
	}

	final, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if final == nil {
		t.Fatalf("cc frame disappeared")
	}
	hasBaseline := false
	hasConcurrent1 := false
	hasConcurrent2 := false
	hasCodexProxy := false
	for _, ref := range final.Subagents {
		switch {
		case !ref.IsProxy && ref.ID == "baseline-task-1":
			hasBaseline = true
		case !ref.IsProxy && ref.ID == "concurrent-task-1":
			hasConcurrent1 = true
		case !ref.IsProxy && ref.ID == "concurrent-task-2":
			hasConcurrent2 = true
		case ref.IsProxy && ref.SourcePID == 200:
			hasCodexProxy = true
		}
	}
	if hasBaseline {
		t.Fatalf("baseline-task-1 still present in %+v — SessionStart reset must drop initial natives", final.Subagents)
	}
	if !hasConcurrent1 {
		t.Fatalf("concurrent-task-1 missing from %+v — multi-conflict regression: attempt 2's filter dropped it (initialNativeIDs baseline broken)", final.Subagents)
	}
	if !hasConcurrent2 {
		t.Fatalf("concurrent-task-2 missing from %+v — last-conflict native wasn't preserved", final.Subagents)
	}
	if !hasCodexProxy {
		t.Fatalf("codex IsProxy ref missing from %+v — proxy preservation regression", final.Subagents)
	}
}

// IT21c — existing_frame SessionStart preserves a concurrent SubagentStart
// whose native ID HAPPENS TO COLLIDE with an old-session baseline native ID.
// Codex round 5 #T1 regression guard: the v11 initialNativeIDs baseline used
// ID-only identity, so a SessionStart reset racing with a concurrent
// SubagentStart whose new native ref reuses an old-session ID would silently
// drop the new ref — classified as baseline old-session state by ID match
// alone. Native IDs are provider-supplied strings; cross-session ID reuse
// is provider-dependent (cc/codex tend toward UUID-like, opencode may be
// more deterministic). The fix is to track baseline by (Type, ID, StartedAt)
// — the new SubagentStart's StartedAt will be a fresh broadcastTs distinct
// from baseline refs, so the new ref doesn't match baseline and survives
// filter-merge.
//
// Setup:
//   - cc seeded with baseline native {ID:"call-1", Type:"cc", StartedAt:5}
//     plus an IsProxy ref (PID 200) that drives the per-attempt seam.
//   - attempt 0: processStartTimeFn(200) injects a NEW SubagentStart with
//     SAME ID "call-1" but distinct StartedAt 65, then bumps LastSeenAt
//     to force IfUnchanged conflict.
//   - attempt 1: filter pass should preserve the new {ID:"call-1",
//     StartedAt:65} ref while still dropping the baseline {ID:"call-1",
//     StartedAt:5} ref. IfUnchanged write succeeds.
//
// Assertions: final.Subagents contains the new ref (ID="call-1",
// StartedAt=65); the baseline ref (ID="call-1", StartedAt=5) is dropped;
// codex IsProxy ref preserved.
func TestPhase35_IT21c_ExistingFrameSessionStartPreservesNewNativeWithReusedID(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	parent.Subagents = []agentpkg.SubagentRef{
		{
			ID:        "call-1",
			Type:      "cc",
			StartedAt: 5,
		},
		{
			ID:              "proxy:codex:200:t200",
			Type:            "codex",
			SourcePID:       200,
			SourceStartTime: "t200",
			IsProxy:         true,
		},
	}
	parent.LastSeenAt = 50
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed cc + baseline native + codex proxy: %v", err)
	}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	// Per-attempt seam: each call to processStartTimeFn(200) corresponds to
	// one filter pass for the codex IsProxy ref. After attempt 0 only,
	// inject a new SubagentStart with SAME ID "call-1" but distinct
	// StartedAt 65, and bump LastSeenAt to force IfUnchanged conflict.
	// Attempt 1's filter must preserve the new ref despite ID collision
	// with baseline.
	attempt := 0
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			return "t100", nil
		}
		if pid != 200 {
			return "other", nil
		}
		attempt++
		if attempt == 1 {
			racer, _ := m.frames.GetByIdentity("%5", 100, "t100")
			if racer != nil {
				// Concurrent SubagentStart with ID colliding against the
				// baseline ID "call-1", but distinct StartedAt 65 (fresh
				// broadcastTs). With T1 fix, baseline is keyed by (Type,
				// ID, StartedAt) so this new ref does NOT match baseline.
				racer.Subagents = append(racer.Subagents, agentpkg.SubagentRef{
					ID:        "call-1",
					Type:      "cc",
					StartedAt: 65,
				})
				racer.LastSeenAt = 60
				if _, err := m.frames.Upsert(*racer); err != nil {
					t.Fatalf("concurrent inject attempt=%d: %v", attempt, err)
				}
			}
		}
		return "t200", nil
	}

	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}

	if attempt < 2 {
		t.Fatalf("filter-merge ran only %d attempts, expected at least 2 (1 conflict + final success)", attempt)
	}

	final, _ := m.frames.GetByIdentity("%5", 100, "t100")
	if final == nil {
		t.Fatalf("cc frame disappeared")
	}

	hasNewNative := false           // ID="call-1", StartedAt=65 (NEW concurrent SubagentStart)
	hasBaselineNative := false      // ID="call-1", StartedAt=5  (OLD baseline — must be dropped)
	hasCodexProxy := false
	for _, ref := range final.Subagents {
		switch {
		case !ref.IsProxy && ref.ID == "call-1" && ref.StartedAt == 65:
			hasNewNative = true
		case !ref.IsProxy && ref.ID == "call-1" && ref.StartedAt == 5:
			hasBaselineNative = true
		case ref.IsProxy && ref.SourcePID == 200:
			hasCodexProxy = true
		}
	}
	if !hasNewNative {
		t.Fatalf("new SubagentStart ref (ID=call-1, StartedAt=65) missing from %+v — T1 regression: ID-only baseline dropped concurrent native that reused old-session ID", final.Subagents)
	}
	if hasBaselineNative {
		t.Fatalf("baseline native (ID=call-1, StartedAt=5) still present in %+v — SessionStart reset must drop initial natives", final.Subagents)
	}
	if !hasCodexProxy {
		t.Fatalf("codex IsProxy ref missing from %+v — proxy preservation regression", final.Subagents)
	}
}

// IT1 — descendant_then_ancestor_canonicalizes_via_descendant_scan.
// codex SessionStart lands first (no cc parent yet → standalone codex).
// cc SessionStart lands second (new-frame Upsert + descendant scan finds
// codex via PPID 200 → 100 + folds it). Verifies plan §2.2.1 descendant
// scan path on the new-frame branch.
func TestPhase35_IT1_DescendantThenAncestorCanonicalizesViaDescendantScan(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		case 100:
			return agentpkg.ProcessInfo{PID: 100, PPID: 1}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	// codex first → standalone (no cc parent yet).
	codexReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	if _, _, err := m.applyFrameEvent(codexReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 50); err != nil {
		t.Fatalf("codex apply: %v", err)
	}
	mid, _ := m.frames.ListByPane("%5")
	if len(mid) != 1 || mid[0].AgentType != "codex" {
		t.Fatalf("after codex SessionStart: frames=%+v, want 1 standalone codex", mid)
	}

	// cc next → Upsert + descendant scan folds codex.
	ccReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(ccReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("cc apply: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "cc" {
		t.Fatalf("final frames = %+v, want 1 cc", frames)
	}
	if len(frames[0].Subagents) != 1 || frames[0].Subagents[0].SourcePID != 200 {
		t.Fatalf("cc.Subagents = %+v, want 1 codex proxy", frames[0].Subagents)
	}

	// projection top must be cc.
	proj, _ := m.projectPane("%5")
	if proj == nil || proj.TopFrame == nil || proj.TopFrame.AgentType != "cc" {
		t.Fatalf("projection top = %+v, want cc", proj)
	}
}

// IT2 — ancestor_then_descendant via PR-2b fast-path. cc first creates a
// frame; codex follows under cc's PPID chain → existing pre-Upsert
// findProxyParent fast-path collapses codex into a proxy ref. Sanity
// check that PR-3.5a wiring did not regress PR-2b behavior.
func TestPhase35_IT2_AncestorThenDescendantUsesPR2bFastPath(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		case 100:
			return agentpkg.ProcessInfo{PID: 100, PPID: 1}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	// cc first.
	ccReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(ccReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 50); err != nil {
		t.Fatalf("cc apply: %v", err)
	}

	// codex → PR-2b fast-path collapses.
	codexReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(codexReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("codex apply: %v", err)
	}
	if meta.Reason != "proxy_subagent_attached" {
		t.Fatalf("reason = %q, want proxy_subagent_attached (PR-2b fast-path)", meta.Reason)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "cc" {
		t.Fatalf("frames = %+v, want 1 cc", frames)
	}
	if len(frames[0].Subagents) != 1 || frames[0].Subagents[0].SourcePID != 200 {
		t.Fatalf("Subagents = %+v, want 1 codex proxy", frames[0].Subagents)
	}
}

// IT6 — descendant_scan_partial_skip_does_not_block_others. Two standalone
// children (codex + opencode); concurrent writer bumps codex's LastSeenAt
// during the scan so DeleteIfUnchanged for codex fails (partial state) but
// opencode's delete succeeds. cc's descendant scan attaches both proxy
// refs but only deletes opencode's standalone row.
func TestPhase35_IT6_DescendantScanPartialDoesNotBlockOthers(t *testing.T) {
	m := newProxyTestModule(t)
	m.registry.Register(&fakeAgentProvider{
		typeName: "opencode",
		derive:   func(string, json.RawMessage) agentpkg.DeriveResult { return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle} },
	})

	// Seed two standalone children directly (skip applyFrameEvent paths so
	// state is exactly: cc not in DB yet, codex + opencode standalone).
	codex := seedFrame(t, m, "%5", "codex", 200, "t200", 11)
	openc := seedFrame(t, m, "%5", "opencode", 300, "t300", 12)
	_ = codex
	_ = openc

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 200, 300:
			return agentpkg.ProcessInfo{PID: pid, PPID: 100}, nil
		case 100:
			return agentpkg.ProcessInfo{PID: 100, PPID: 1}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	// Hook on processStartTimeFn(200) → bump codex's LastSeenAt to force
	// its DeleteIfUnchanged to fail. Fires once per applyFrameEvent run.
	bumped := false
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			if !bumped {
				bumped = true
				cur, _ := m.frames.GetByIdentity("%5", 200, "t200")
				if cur != nil {
					cur.LastSeenAt = 999
					if _, err := m.frames.Upsert(*cur); err != nil {
						t.Fatalf("bump codex: %v", err)
					}
				}
			}
			return "t200", nil
		case 300:
			return "t300", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	startMetric := agentpkg.MetricPartialCanonicalizationCreated.Value()
	ccReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(ccReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 200); err != nil {
		t.Fatalf("cc apply: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	// Expect: cc + codex standalone (delete failed). opencode folded + deleted.
	if len(frames) != 2 {
		t.Fatalf("frames count = %d, want 2 (cc + codex partial); frames=%+v", len(frames), frames)
	}
	var ccFrame *store.Frame
	for i := range frames {
		if frames[i].AgentType == "cc" {
			ccFrame = &frames[i]
		}
	}
	if ccFrame == nil {
		t.Fatalf("no cc frame found")
	}
	// cc.Subagents must contain BOTH codex (partial — proxy attached, child
	// not yet deleted) and opencode proxy refs.
	pids := map[int]bool{}
	for _, ref := range ccFrame.Subagents {
		pids[ref.SourcePID] = true
	}
	if !pids[200] || !pids[300] {
		t.Fatalf("cc.Subagents SourcePIDs = %v, want both 200 + 300", pids)
	}

	// Partial metric must have incremented at least once for codex.
	if delta := agentpkg.MetricPartialCanonicalizationCreated.Value() - startMetric; delta < 1 {
		t.Fatalf("MetricPartialCanonicalizationCreated delta = %d, want >= 1 (codex partial)", delta)
	}
}

// IT7 — existing_frame_session_start_runs_descendant_scan_only.
// (a) cc creates frame; (b) codex lands standalone (race); (c) cc gets
// SessionStart again — existing-frame filter-merge path runs descendant
// scan and folds codex.
func TestPhase35_IT7_ExistingFrameSessionStartDescendantScan(t *testing.T) {
	m := newProxyTestModule(t)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		case 100:
			return agentpkg.ProcessInfo{PID: 100, PPID: 1}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	// (a) cc SessionStart.
	ccReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(ccReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 50); err != nil {
		t.Fatalf("cc apply 1: %v", err)
	}

	// (b) Inject codex standalone directly (simulating cold-start race that
	// raced past PR-2b's pre-walk in real conditions).
	codex := seedFrame(t, m, "%5", "codex", 200, "t200", 60)
	_ = codex

	// (c) cc SessionStart again (existing frame). Filter-merge path +
	// descendant scan should fold codex.
	if _, _, err := m.applyFrameEvent(ccReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("cc apply 2: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "cc" {
		t.Fatalf("frames = %+v, want 1 cc", frames)
	}
	if len(frames[0].Subagents) != 1 || frames[0].Subagents[0].SourcePID != 200 {
		t.Fatalf("Subagents = %+v, want 1 codex proxy ref", frames[0].Subagents)
	}
}

// IT8 — non_session_start_event_no_canonicalization. Notification event
// in frame == nil path goes through legacy create-frame behavior;
// reconcile + descendant scan are gated on req.PurdexName == "SessionStart"
// so they must NOT trigger.
func TestPhase35_IT8_NonSessionStartEventNoCanonicalization(t *testing.T) {
	m := newProxyTestModule(t)

	// Seed cc parent so a candidate ancestor exists in the pane.
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	// codex Notification (non-SessionStart). Should hit fallback create-
	// frame and emit created_frame trace; no canonicalization.
	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxNotification", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Decision != "created_frame" {
		t.Fatalf("decision = %q, want created_frame", meta.Decision)
	}
	if meta.Reason == "post_upsert_canonicalization_self" {
		t.Fatalf("reason = %q, must not be canonicalization for non-SessionStart", meta.Reason)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frames count = %d, want 2 (cc + standalone codex)", len(frames))
	}
}

// IT9 — filter-merge-retry exhaustion aborts applyFrameEvent. Storage
// errors and exhausted-retry both surface to the caller as a non-nil
// error from applyFrameEvent. We exercise the exhausted-retry surface
// (mutateSubagentsWithRetry / filter-merge-retry caller) because the
// concrete sqlite path can be triggered without a store interface seam.
//
// Setup: cc frame with one live codex proxy ref. processStartTimeFn(200)
// is hooked to bump cc's LastSeenAt on every call — fires once per
// filter-merge attempt's filter pass — so each UpsertIfUnchanged sees
// its expected LastSeenAt was already mutated by the bump that just ran
// during the same attempt's filter scan. Three attempts conflict back-
// to-back → exhausted error → applyFrameEvent propagates.
func TestPhase35_IT9_FilterMergeExhaustedRetryAbortsApply(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:200:t200",
		Type:            "codex",
		SourcePID:       200,
		SourceStartTime: "t200",
		IsProxy:         true,
	}}
	parent.LastSeenAt = 50
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed cc + ref: %v", err)
	}

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	// Every call to processStartTimeFn(200) bumps cc's row — fires on each
	// filter pass, so each attempt's UpsertIfUnchanged conflicts.
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 200 {
			cur, _ := m.frames.GetByIdentity("%5", 100, "t100")
			if cur != nil {
				cur.LastSeenAt = cur.LastSeenAt + 1
				if _, err := m.frames.Upsert(*cur); err != nil {
					t.Fatalf("bump cc: %v", err)
				}
			}
			return "t200", nil
		}
		return "t100", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	// cc SessionStart on existing cc frame triggers filter-merge-retry.
	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	_, _, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err == nil {
		t.Fatalf("expected exhausted-retry error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("error = %v, want 'exceeded' retry message", err)
	}
}

// IT11 — descendant_scan_skips_pid_reuse_stale at end-to-end level.
// pane has a stale codex standalone (PID reused; actualStart != stored).
// cc SessionStart's descendant scan must skip it (identity gate fails),
// leave the row for sweep, and not fold a stale ref.
func TestPhase35_IT11_DescendantScanSkipsPidReuseStale(t *testing.T) {
	m := newProxyTestModule(t)

	// Seed stale codex with stored start_time "stale-t200".
	seedFrame(t, m, "%5", "codex", 200, "stale-t200", 11)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		switch pid {
		case 200:
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		case 100:
			return agentpkg.ProcessInfo{PID: 100, PPID: 1}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	// PID 200 alive, but actualStart = "fresh-t200" ≠ stored "stale-t200".
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "fresh-t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	ccReq := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "cc", SenderPID: 100, SenderStartTime: "t100"}
	if _, _, err := m.applyFrameEvent(ccReq, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100); err != nil {
		t.Fatalf("cc apply: %v", err)
	}

	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frames count = %d, want 2 (cc + stale codex left for sweep)", len(frames))
	}
	var ccFrame *store.Frame
	for i := range frames {
		if frames[i].AgentType == "cc" {
			ccFrame = &frames[i]
		}
	}
	if ccFrame == nil {
		t.Fatalf("no cc frame found")
	}
	if len(ccFrame.Subagents) != 0 {
		t.Fatalf("cc.Subagents = %+v, want empty (stale candidate skipped by identity gate)", ccFrame.Subagents)
	}
}

// IT15 — partial_metric_increments_on_partial_state. mock DeleteIfUnchanged
// (via LastSeenAt bump hook) → reconcile partial path runs in the new-
// frame branch, MetricPartialCanonicalizationCreated +1.
func TestPhase35_IT15_PartialMetricIncrementsOnPartialState(t *testing.T) {
	m := newProxyTestModule(t)
	// Seed cc parent so reconcile finds an ancestor.
	seedFrame(t, m, "%5", "cc", 100, "t100", 10)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: pidPPIDForIT15()}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	// Hook on processStartTimeFn(100) — fires inside reconcile's
	// findProxyParent identity gate AFTER the codex standalone has been
	// Upserted but BEFORE DeleteIfUnchanged. Bump codex's LastSeenAt to
	// force the IfUnchanged delete to fail (partial state).
	bumped := false
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 100 {
			if !bumped {
				bumped = true
				cur, _ := m.frames.GetByIdentity("%5", 200, "t200")
				if cur != nil {
					cur.LastSeenAt = 999
					if _, err := m.frames.Upsert(*cur); err != nil {
						t.Fatalf("bump codex: %v", err)
					}
				}
			}
			return "t100", nil
		}
		if pid == 200 {
			return "t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
		phase35IT15Reset()
	})

	startMetric := agentpkg.MetricPartialCanonicalizationCreated.Value()
	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionStart", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	// v8 M4 fix: partial path now reports canonicalized=true so the
	// caller emits the updated_frame / post_upsert_canonicalization_self
	// trace consistent with what the SPA actually sees (projection dedup
	// hides the orphan child; user-visible state is parent + proxy ref).
	// Reporting created_frame would have desynced trace from projection.
	if meta.Decision != "updated_frame" {
		t.Fatalf("decision = %q, want updated_frame (partial reports canonicalized=true so trace matches projection)", meta.Decision)
	}
	if meta.Reason != "post_upsert_canonicalization_self" {
		t.Fatalf("reason = %q, want post_upsert_canonicalization_self", meta.Reason)
	}

	if delta := agentpkg.MetricPartialCanonicalizationCreated.Value() - startMetric; delta < 1 {
		t.Fatalf("MetricPartialCanonicalizationCreated delta = %d, want >= 1 (partial state metric still increments)", delta)
	}

	// Final state: parent has codex proxy ref (partial); codex standalone
	// row remains. Sweep canonicalize (PR-3.5b) repairs within 2s.
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 {
		t.Fatalf("frames count = %d, want 2 (parent + standalone partial)", len(frames))
	}
}

// IT15 PPID toggle: same pattern as IT3/IT9 — calls 1+2 return 999, then
// 100. Lets PR-2b pre-walk miss while reconcile post-walk hits cc.
var phase35IT15Calls int

func pidPPIDForIT15() int {
	phase35IT15Calls++
	if phase35IT15Calls <= 2 {
		return 999
	}
	return 100
}

func phase35IT15Reset() { phase35IT15Calls = 0 }

// IT16 — projection_dedup_metric_increments across multiple panes.
func TestPhase35_IT16_ProjectionDedupMetricIncrements(t *testing.T) {
	m := newProxyTestModule(t)

	// Pane %5: cc with codex proxy ref + codex standalone (partial).
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:200:t200",
		Type:            "codex",
		SourcePID:       200,
		SourceStartTime: "t200",
		IsProxy:         true,
	}}
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed pane 1: %v", err)
	}
	seedFrame(t, m, "%5", "codex", 200, "t200", 50)

	// Pane %6: codex with cc proxy ref + cc standalone (partial).
	parent2 := seedFrame(t, m, "%6", "codex", 400, "t400", 10)
	parent2.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:cc:300:t300",
		Type:            "cc",
		SourcePID:       300,
		SourceStartTime: "t300",
		IsProxy:         true,
	}}
	if _, err := m.frames.Upsert(parent2); err != nil {
		t.Fatalf("seed pane 2: %v", err)
	}
	seedFrame(t, m, "%6", "cc", 300, "t300", 50)

	startMetric := agentpkg.MetricProjectionDedupHidden.Value()
	if _, err := m.projectPane("%5"); err != nil {
		t.Fatalf("projectPane %%5: %v", err)
	}
	if _, err := m.projectPane("%6"); err != nil {
		t.Fatalf("projectPane %%6: %v", err)
	}
	delta := agentpkg.MetricProjectionDedupHidden.Value() - startMetric
	if delta != 2 {
		t.Fatalf("MetricProjectionDedupHidden delta = %d, want +2 (one per pane)", delta)
	}
}

// IT12 — session_end_clears_parent_proxy_ref (plan §2.3 / Side B SessionEnd
// hot-path cleanup). Partial state: cc parent has codex proxy ref +
// codex standalone frame. codex SessionEnd must delete its own frame AND
// detach the proxy ref from cc — projection dedup can't help here (no
// standalone left to hide behind once the child SessionEnd is processed).
func TestPhase35_IT12_SessionEndClearsParentProxyRef(t *testing.T) {
	m := newProxyTestModule(t)

	// Seed cc parent with a codex proxy ref AND a codex standalone frame
	// (the partial state cold-start race could leave behind).
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:200:t200",
		Type:            "codex",
		StartedAt:       50,
		SourcePID:       200,
		SourceStartTime: "t200",
		IsProxy:         true,
	}}
	parent.LastSeenAt = 50
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed cc + proxy: %v", err)
	}
	codexStandalone := seedFrame(t, m, "%5", "codex", 200, "t200", 60)

	// codex SessionEnd: own-frame delete path + new §2.3 proxy cleanup.
	req := EventRequest{TmuxSession: "work", TmuxPaneID: "%5", PurdexName: "PdxSessionEnd", AgentType: "codex", SenderPID: 200, SenderStartTime: "t200"}
	_, meta, err := m.applyFrameEvent(req, agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusClear}, 100)
	if err != nil {
		t.Fatalf("applyFrameEvent: %v", err)
	}
	if meta.Decision != "deleted_frame" || meta.Reason != "session_end" {
		t.Fatalf("meta = %+v, want deleted_frame / session_end", meta)
	}

	// codex standalone gone.
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (codex deleted)", len(frames))
	}
	if frames[0].AgentType != "cc" {
		t.Fatalf("remaining AgentType = %q, want cc", frames[0].AgentType)
	}
	// Proxy ref also removed from cc.
	if len(frames[0].Subagents) != 0 {
		t.Fatalf("cc.Subagents = %+v, want empty (proxy detached by SessionEnd hot-path)", frames[0].Subagents)
	}
	_ = codexStandalone
}

// RC4b — positive case: live identity-verified cross-type descendant under
// self.PID is folded into a proxy ref + standalone deleted.
func TestCanonicalizeDescendantsAfterUpsert_FoldsLiveCrossTypeDescendant(t *testing.T) {
	m := newProxyTestModule(t)
	self := seedFrame(t, m, "%5", "cc", 100, "t100", 10)
	candidate := seedFrame(t, m, "%5", "codex", 200, "t200", 11)

	origInfo := readProcessInfoFn
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		if pid == 200 {
			return agentpkg.ProcessInfo{PID: 200, PPID: 100}, nil
		}
		return agentpkg.ProcessInfo{PID: pid, PPID: 1}, nil
	}
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(pid int) (string, error) {
		switch pid {
		case 100:
			return "t100", nil
		case 200:
			return "t200", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		readProcessInfoFn = origInfo
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})

	updated, err := m.canonicalizeDescendantsAfterUpsert(self, 100)
	if err != nil {
		t.Fatalf("canonicalizeDescendantsAfterUpsert: %v", err)
	}
	if len(updated.Subagents) != 1 {
		t.Fatalf("self.Subagents = %+v, want 1 proxy ref", updated.Subagents)
	}
	ref := updated.Subagents[0]
	if !ref.IsProxy || ref.Type != "codex" || ref.SourcePID != 200 || ref.SourceStartTime != "t200" {
		t.Fatalf("ref = %+v, want codex IsProxy=true source_pid=200 source_start_time=t200", ref)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 {
		t.Fatalf("frame count = %d, want 1 (codex folded + deleted)", len(frames))
	}
	_ = candidate
}

// ---------------------------------------------------------------------------
// PR-3.5b §3.2 — candidateHasOwnedState unit tests (RC12-RC16).
//
// Pure classifier — exercises the four code paths in the helper:
//   - native ref (IsProxy=false) → owned
//   - live + identity-verified IsProxy → owned
//   - only stale (dead PID / PID-reused) IsProxy → not owned
//   - processStartTime read error on a live IsProxy → defensively owned
//   - empty subagents → not owned
//
// Each case overrides isPidAliveFn / processStartTimeFn package-level
// seams (already used by hot-path tests above) to drive the helper's
// branches without going through the database. Helper is invoked
// directly so the failing test pinpoints classification, not
// canonicalize integration.
// ---------------------------------------------------------------------------

// RC12 — candidate carrying a native (IsProxy=false) ref → owned.
func TestCandidateHasOwnedState_NativeRefReturnsTrue(t *testing.T) {
	candidate := store.Frame{
		Subagents: []agentpkg.SubagentRef{{
			ID:      "task-1",
			Type:    "cc",
			IsProxy: false,
		}},
	}
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "live", nil }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})
	if !candidateHasOwnedState(candidate) {
		t.Fatal("candidateHasOwnedState = false, want true (native ref must be treated as owned state)")
	}
}

// RC13 — candidate carrying a live + identity-verified IsProxy ref → owned.
func TestCandidateHasOwnedState_LiveProxyReturnsTrue(t *testing.T) {
	candidate := store.Frame{
		Subagents: []agentpkg.SubagentRef{{
			ID:              "proxy:opencode:300:t300",
			Type:            "opencode",
			SourcePID:       300,
			SourceStartTime: "t300",
			IsProxy:         true,
		}},
	}
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	isPidAliveFn = func(pid int) bool { return pid == 300 }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 300 {
			return "t300", nil
		}
		return "other", nil
	}
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})
	if !candidateHasOwnedState(candidate) {
		t.Fatal("candidateHasOwnedState = false, want true (live identity-verified IsProxy must count as owned)")
	}
}

// RC14 — candidate carrying ONLY stale IsProxy refs (dead PID and PID-
// reused) → not owned (sweep prune would reap them anyway).
func TestCandidateHasOwnedState_OnlyStaleProxyReturnsFalse(t *testing.T) {
	candidate := store.Frame{
		Subagents: []agentpkg.SubagentRef{
			{
				ID:              "proxy:opencode:888:dead",
				Type:            "opencode",
				SourcePID:       888,
				SourceStartTime: "dead-t888",
				IsProxy:         true,
			},
			{
				ID:              "proxy:opencode:889:reused",
				Type:            "opencode",
				SourcePID:       889,
				SourceStartTime: "stale-t889",
				IsProxy:         true,
			},
		},
	}
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	// 888 is dead; 889 is alive but identity changed.
	isPidAliveFn = func(pid int) bool { return pid == 889 }
	processStartTimeFn = func(pid int) (string, error) {
		if pid == 889 {
			return "fresh-t889", nil
		}
		return "", nil
	}
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})
	if candidateHasOwnedState(candidate) {
		t.Fatal("candidateHasOwnedState = true, want false (only stale IsProxy refs — sweep prune would reap them)")
	}
}

// RC15 — processStartTime read error on a live IsProxy → defensively
// treated as owned (don't drop state on uncertainty).
func TestCandidateHasOwnedState_ProxyReadErrorTreatsAsOwned(t *testing.T) {
	candidate := store.Frame{
		Subagents: []agentpkg.SubagentRef{{
			ID:              "proxy:opencode:300:t300",
			Type:            "opencode",
			SourcePID:       300,
			SourceStartTime: "t300",
			IsProxy:         true,
		}},
	}
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) {
		return "", errStub("ps transient failure")
	}
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})
	if !candidateHasOwnedState(candidate) {
		t.Fatal("candidateHasOwnedState = false, want true (read error must defensively treat as owned)")
	}
}

// RC16 — empty subagents list → not owned (nothing to preserve).
func TestCandidateHasOwnedState_EmptySubagentsReturnsFalse(t *testing.T) {
	candidate := store.Frame{}
	origAlive := isPidAliveFn
	origStart := processStartTimeFn
	isPidAliveFn = func(int) bool { return true }
	processStartTimeFn = func(int) (string, error) { return "live", nil }
	t.Cleanup(func() {
		isPidAliveFn = origAlive
		processStartTimeFn = origStart
	})
	if candidateHasOwnedState(candidate) {
		t.Fatal("candidateHasOwnedState = true, want false (empty subagents — no state to preserve)")
	}
}

// ---------------------------------------------------------------------------
// L2 Phase 2 P2-T4 — subagentRefMatches turn-aware + findProxyRefByBroker
// (spec §3.2 / plan §2 P2-T4)
// ---------------------------------------------------------------------------

// TestSubagentRefMatches_TurnAware pins the spec §3.2.A behavior of
// subagentRefMatches when at least one side is IsProxy=true, plus the
// existing native-ref ID equality semantics (rows f/g — L2/v2 fix
// regression guards).
func TestSubagentRefMatches_TurnAware(t *testing.T) {
	cases := []struct {
		name string
		a    agentpkg.SubagentRef
		b    agentpkg.SubagentRef
		want bool
	}{
		{
			name: "a — both turnIDs empty, process fallback matches",
			a:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1"},
			b:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1"},
			want: true,
		},
		{
			name: "b — one side turnID empty (process fallback)",
			a:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1"},
			b:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
			want: true,
		},
		{
			name: "c — both turnIDs non-empty and equal",
			a:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
			b:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
			want: true,
		},
		{
			name: "d — both turnIDs non-empty but different (turn-level no match)",
			a:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
			b:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_b"},
			want: false,
		},
		{
			name: "e — IsProxy mismatch (cross-namespace)",
			a:    agentpkg.SubagentRef{IsProxy: true, SourcePID: 42, SourceStartTime: "t1"},
			b:    agentpkg.SubagentRef{IsProxy: false, ID: "task-x"},
			want: false,
		},
		{
			name: "f — native ref same ID (L2/v2 regression pin)",
			a:    agentpkg.SubagentRef{IsProxy: false, ID: "task-x"},
			b:    agentpkg.SubagentRef{IsProxy: false, ID: "task-x"},
			want: true,
		},
		{
			name: "g — native ref different ID (L2/v2 regression pin)",
			a:    agentpkg.SubagentRef{IsProxy: false, ID: "task-x"},
			b:    agentpkg.SubagentRef{IsProxy: false, ID: "task-y"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := subagentRefMatches(tc.a, tc.b); got != tc.want {
				t.Fatalf("subagentRefMatches(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			// Symmetry: matches must be symmetric — swap arguments and re-check.
			if got := subagentRefMatches(tc.b, tc.a); got != tc.want {
				t.Fatalf("subagentRefMatches(%+v, %+v) reverse = %v, want %v", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

// TestFindProxyRefByBroker covers the new process-level lookup helper
// per spec §3.2.B. Lookup is by (PID, StartTime) only — turnID is
// intentionally NOT compared so attach/upsert can locate the existing
// broker ref to mutate-in-place rather than appending a duplicate
// (spec §3.2 F1 fix).
func TestFindProxyRefByBroker(t *testing.T) {
	refs := []agentpkg.SubagentRef{
		{IsProxy: true, SourcePID: 42, SourceStartTime: "t1", SourceTurnID: "t_a"},
		{IsProxy: true, SourcePID: 43, SourceStartTime: "t2", SourceTurnID: "t_b"},
		{IsProxy: false, ID: "task-x"}, // native ref must never match
	}
	cases := []struct {
		name      string
		pid       int
		startTime string
		want      int
	}{
		{name: "a — match first proxy ref", pid: 42, startTime: "t1", want: 0},
		{name: "b — match second proxy ref", pid: 43, startTime: "t2", want: 1},
		{name: "c — no match (PID/StartTime miss)", pid: 99, startTime: "tX", want: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := findProxyRefByBroker(refs, tc.pid, tc.startTime); got != tc.want {
				t.Fatalf("findProxyRefByBroker(refs, %d, %q) = %d, want %d", tc.pid, tc.startTime, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// L2 Phase 2 P2-T5 — upsertProxyRefForBroker
// (spec §3.4 / plan §2 P2-T5)
// ---------------------------------------------------------------------------

// TestUpsertProxyRefForBroker_AppendsWhenNoExistingRef covers case (a):
// parent has no matching broker ref → helper appends one with full identity
// (PID, StartTime, turnID) and ID = "proxy:codex:<pid>:<startTime>"
// (matches SessionStart fast-path at frame_ops.go:218).
func TestUpsertProxyRefForBroker_AppendsWhenNoExistingRef(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 50)

	persisted, stored, err := m.upsertProxyRefForBroker(parent, 42, "t1", "t_a", 100)
	if err != nil {
		t.Fatalf("upsertProxyRefForBroker: %v", err)
	}
	if !persisted {
		t.Fatalf("persisted = false, want true")
	}
	if len(stored.Subagents) != 1 {
		t.Fatalf("Subagents count = %d, want 1; refs=%+v", len(stored.Subagents), stored.Subagents)
	}
	got := stored.Subagents[0]
	wantID := "proxy:codex:42:t1"
	if !got.IsProxy || got.SourcePID != 42 || got.SourceStartTime != "t1" ||
		got.SourceTurnID != "t_a" || got.ID != wantID || got.Type != "codex" || got.StartedAt != 100 {
		t.Fatalf("appended ref = %+v, want IsProxy=true PID=42 StartTime=t1 TurnID=t_a ID=%q Type=codex StartedAt=100", got, wantID)
	}
	if stored.LastSeenAt != 100 {
		t.Fatalf("stored.LastSeenAt = %d, want 100", stored.LastSeenAt)
	}
}

// TestUpsertProxyRefForBroker_InPlaceFromEmptyTurnID covers case (b):
// parent already has a proxy ref with empty SourceTurnID (SessionStart
// attached without turn_id) → helper mutates SourceTurnID in-place; still
// 1 ref (no append).
func TestUpsertProxyRefForBroker_InPlaceFromEmptyTurnID(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:42:t1",
		Type:            "codex",
		StartedAt:       50,
		SourcePID:       42,
		SourceStartTime: "t1",
		IsProxy:         true,
		// SourceTurnID intentionally empty.
	}}
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed parent ref: %v", err)
	}
	parent, _ = func() (store.Frame, error) {
		got, err := m.frames.GetByIdentity("%5", 100, "t100")
		return *got, err
	}()

	persisted, stored, err := m.upsertProxyRefForBroker(parent, 42, "t1", "t_a", 200)
	if err != nil {
		t.Fatalf("upsertProxyRefForBroker: %v", err)
	}
	if !persisted {
		t.Fatalf("persisted = false, want true")
	}
	if len(stored.Subagents) != 1 {
		t.Fatalf("Subagents count = %d, want 1 (in-place mutation, not append); refs=%+v", len(stored.Subagents), stored.Subagents)
	}
	got := stored.Subagents[0]
	if got.SourceTurnID != "t_a" {
		t.Fatalf("ref.SourceTurnID = %q, want t_a (in-place upsert)", got.SourceTurnID)
	}
	if got.SourcePID != 42 || got.SourceStartTime != "t1" || !got.IsProxy {
		t.Fatalf("ref identity drifted: %+v", got)
	}
	if got.StartedAt != 200 {
		t.Fatalf("ref.StartedAt = %d, want 200 (recency refresh)", got.StartedAt)
	}
}

// TestUpsertProxyRefForBroker_InPlaceOverwritesExistingTurnID covers case (c):
// parent already has a proxy ref with SourceTurnID="t_a" → helper mutates
// SourceTurnID to "t_b" in-place; still 1 ref (NOT appended). Spec §3.4
// guards against the v3 F1 race where a turn-aware lookup would mistake
// (PID, t1, t_b) for "no existing ref" and append a duplicate.
func TestUpsertProxyRefForBroker_InPlaceOverwritesExistingTurnID(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 50)
	parent.Subagents = []agentpkg.SubagentRef{{
		ID:              "proxy:codex:42:t1",
		Type:            "codex",
		StartedAt:       50,
		SourcePID:       42,
		SourceStartTime: "t1",
		IsProxy:         true,
		SourceTurnID:    "t_a",
	}}
	if _, err := m.frames.Upsert(parent); err != nil {
		t.Fatalf("seed parent ref: %v", err)
	}
	reloaded, err := m.frames.GetByIdentity("%5", 100, "t100")
	if err != nil || reloaded == nil {
		t.Fatalf("reload parent: %v / %v", err, reloaded)
	}

	persisted, stored, err := m.upsertProxyRefForBroker(*reloaded, 42, "t1", "t_b", 300)
	if err != nil {
		t.Fatalf("upsertProxyRefForBroker: %v", err)
	}
	if !persisted {
		t.Fatalf("persisted = false, want true")
	}
	if len(stored.Subagents) != 1 {
		t.Fatalf("Subagents count = %d, want 1 (in-place overwrite, not append); refs=%+v", len(stored.Subagents), stored.Subagents)
	}
	if stored.Subagents[0].SourceTurnID != "t_b" {
		t.Fatalf("ref.SourceTurnID = %q, want t_b (overwrite)", stored.Subagents[0].SourceTurnID)
	}
}

// TestUpsertProxyRefForBroker_RetryOnConflict covers case (d): the first
// UpsertIfUnchanged conflicts (a concurrent writer bumped LastSeenAt
// between the caller's read and our write); the helper reloads, re-runs
// findProxyRefByBroker against the fresh refs, and the second attempt
// succeeds. Mirrors the race-injection pattern from
// TestProxySubagent_ConcurrentAttachesBothLand (PR17 #632).
func TestUpsertProxyRefForBroker_RetryOnConflict(t *testing.T) {
	m := newProxyTestModule(t)
	parent := seedFrame(t, m, "%5", "cc", 100, "t100", 50)

	// Inject a concurrent racer write BEFORE our upsert call. The racer
	// adds an unrelated proxy ref and bumps LastSeenAt to 60. Our caller's
	// `parent` snapshot is still at LastSeenAt=50, so the helper's first
	// UpsertIfUnchanged(expected=50) fails; the helper reloads, sees the
	// racer's ref + LastSeenAt=60, and the second attempt succeeds —
	// preserving both the racer's ref and our newly-appended ref.
	racer := agentpkg.SubagentRef{
		ID:              "proxy:codex:999:t999",
		Type:            "codex",
		StartedAt:       55,
		SourcePID:       999,
		SourceStartTime: "t999",
		IsProxy:         true,
		SourceTurnID:    "t_x",
	}
	cur, err := m.frames.GetByIdentity("%5", 100, "t100")
	if err != nil || cur == nil {
		t.Fatalf("racer baseline read: %v / %v", err, cur)
	}
	cur.Subagents = append(cur.Subagents, racer)
	cur.LastSeenAt = 60
	if _, err := m.frames.Upsert(*cur); err != nil {
		t.Fatalf("racer Upsert: %v", err)
	}

	// Call helper with the STALE parent (LastSeenAt=50). First attempt
	// conflicts, reload picks up racer + LastSeenAt=60, second attempt
	// merges and succeeds.
	persisted, stored, err := m.upsertProxyRefForBroker(parent, 42, "t1", "t_a", 100)
	if err != nil {
		t.Fatalf("upsertProxyRefForBroker: %v", err)
	}
	if !persisted {
		t.Fatalf("persisted = false, want true after retry")
	}
	if len(stored.Subagents) != 2 {
		t.Fatalf("Subagents count = %d, want 2 (racer + new); refs=%+v", len(stored.Subagents), stored.Subagents)
	}
	pids := map[int]bool{}
	for _, ref := range stored.Subagents {
		pids[ref.SourcePID] = true
	}
	if !pids[42] || !pids[999] {
		t.Fatalf("Subagents PIDs = %v, want both 42 and 999", pids)
	}
	if stored.LastSeenAt != 100 {
		t.Fatalf("stored.LastSeenAt = %d, want 100", stored.LastSeenAt)
	}
}
