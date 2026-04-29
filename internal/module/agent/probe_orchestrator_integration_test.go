package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// CC3 — hook handler calls recordHookAt. Verifies the wiring put in place
// in Commit 4 (handler.go calls m.probeOrch.recordHookAt before
// manageActivityWatch). Confirms lastHookAt[session] is populated within the
// test horizon.
func TestModule_HookHandler_CallsRecordHookAt(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusWaiting}
		},
	})
	rec := newRecordingProber()
	m.probeOrch.watcher = rec

	before := time.Now()
	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxUserPromptSubmit","raw_event":{},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	m.probeOrch.graceMu.Lock()
	stamp, ok := m.probeOrch.lastHookAt["work"]
	m.probeOrch.graceMu.Unlock()
	if !ok {
		t.Fatalf("lastHookAt[work] missing — handler did not call recordHookAt")
	}
	if stamp.Before(before) {
		t.Fatalf("lastHookAt[work] = %v, want >= %v", stamp, before)
	}
}

// FX2 — handler calls recordHookAt BEFORE the m.mu critical section that
// writes currentStatus. Regression for codex finding #3 (R2 attack HIGH):
// the previous order let an in-flight probe callback observe the new
// currentStatus while lastHookAt was still empty — the graceWindow check
// passed and the probe overwrote authoritative hook status.
//
// Order-witness: install recordHookAtHook (test-only seam) that captures
// the value of currentStatus[session] at the moment recordHookAt fires.
// If recordHookAt runs first (correct order), the captured value is the
// pre-hook empty/legacy state; if currentStatus is written first (broken
// order), the captured value is the new result.Status.
func TestHandler_RecordHookAtBeforeCurrentStatus(t *testing.T) {
	m := newTestModule(t)
	fakeTmux := tmux.NewFakeExecutor()
	fakeTmux.SetPaneSessionName("%5", "work")
	m.tmux = fakeTmux
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusWaiting}
		},
	})
	rec := newRecordingProber()
	m.probeOrch.watcher = rec

	var captured agentpkg.Status
	var capturedOK bool
	origHook := recordHookAtHook
	recordHookAtHook = func(session string) {
		m.mu.Lock()
		captured, capturedOK = m.currentStatus[session]
		m.mu.Unlock()
	}
	t.Cleanup(func() { recordHookAtHook = origHook })

	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","purdex_name":"PdxUserPromptSubmit","raw_event":{},"agent_type":"cc"}`
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	// Correct order: recordHookAt fires BEFORE currentStatus write — the
	// captured currentStatus is empty (pre-hook). If the broken order shipped,
	// the captured value would be StatusWaiting.
	if capturedOK && captured == agentpkg.StatusWaiting {
		t.Fatalf("recordHookAt observed currentStatus[work] = %q (the new hook status) — recordHookAt was called AFTER the currentStatus write (broken order)", captured)
	}
	// And after the handler finishes, currentStatus must be the new status.
	m.mu.Lock()
	final := m.currentStatus["work"]
	m.mu.Unlock()
	if final != agentpkg.StatusWaiting {
		t.Fatalf("post-handler currentStatus[work] = %q, want StatusWaiting", final)
	}
}

// CC4 — E2E ScreenChanged → broadcast Running. W3 撤回: manageActivityWatch
// is now stop-only, so the test seeds activeWatchers + invokes startWatch
// directly with WatchOptions{TopLines: 12} (mirroring the future W6
// ProbeIntent caller for cc). The downstream broadcast assertion is
// unchanged: feed a ScreenChanged event through the orchestrator's callback
// and confirm the WS broadcast carries StatusRunning.
func TestCC_E2E_ScreenChangedToRunning(t *testing.T) {
	m, _, _ := orchTestModule(t) // installs recordingProber + cc registry
	seedOrchFrame(t, m, agentpkg.StatusWaiting, 280)

	// Seed the active-watcher entry under m.mu (the orchestrator's stale-
	// callback guard reads it under the same lock) and start the watcher
	// explicitly — the production W6 caller will do the same.
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusWaiting
	m.mu.Unlock()
	if !m.probeOrch.startWatch("work", "cc", probe.WatchOptions{TopLines: 12}) {
		t.Fatalf("startWatch returned false; expected true for valid TopLines opts")
	}

	// Subscribe BEFORE firing the event so the broadcast is observed.
	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	cb := m.probeOrch.makeCallback("work", "cc")
	cb(probe.ScreenChangeEvent{Kind: probe.ScreenChanged, Target: "work:", OccurredAt: time.Now()})

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
		if env.Session != "s1" {
			t.Fatalf("broadcast session = %q, want s1", env.Session)
		}
		if !strings.Contains(env.Value, `"status":"running"`) {
			t.Fatalf("broadcast value missing status=running: %s", env.Value)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for ScreenChanged → Running broadcast")
	}

	m.mu.Lock()
	got := m.currentStatus["work"]
	m.mu.Unlock()
	if got != agentpkg.StatusRunning {
		t.Fatalf("currentStatus[work] = %q, want StatusRunning", got)
	}
}

// CC5 — E2E ScreenStable → broadcast Idle. cc running, ScreenStable arrives
// with bottom-capture (independent of ev.Content) returning prose (no shell
// prompt) → orchestrator broadcasts Idle.
func TestCC_E2E_ScreenStableToIdle(t *testing.T) {
	m, _, fake := orchTestModule(t)
	seedOrchFrame(t, m, agentpkg.StatusRunning, 281)
	m.mu.Lock()
	m.activeWatchers["work"] = "cc"
	m.currentStatus["work"] = agentpkg.StatusRunning
	m.mu.Unlock()
	// Bottom capture returns text without a shell prompt → routes to Idle.
	fake.setCaptureFn(func(string, int) (string, error) {
		return "user typed prose\nmore prose", nil
	})

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	cb := m.probeOrch.makeCallback("work", "cc")
	cb(probe.ScreenChangeEvent{Kind: probe.ScreenStable, Target: "work:", Content: "irrelevant", OccurredAt: time.Now()})

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
		if env.Session != "s1" {
			t.Fatalf("broadcast session = %q, want s1", env.Session)
		}
		if !strings.Contains(env.Value, `"status":"idle"`) {
			t.Fatalf("broadcast value missing status=idle: %s", env.Value)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for ScreenStable → Idle broadcast")
	}

	m.mu.Lock()
	got := m.currentStatus["work"]
	m.mu.Unlock()
	if got != agentpkg.StatusIdle {
		t.Fatalf("currentStatus[work] = %q, want StatusIdle", got)
	}
}

// FX1 — RenameSession migrates lastHookAt across the rename so a freshly-
// set graceWindow stays in effect for the new session name. Regression for
// codex finding #2 (R1 P2): without this, hook-set status could be
// overwritten by probe events within 2s after rename because lastHookAt
// stayed under the old name (probe targets the new name and observes "no
// graceWindow active").
func TestRenameSession_MigratesLastHookAt(t *testing.T) {
	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec
	provider := &fakeAgentProvider{typeName: "cc"}
	m.registry.Register(provider)

	// Pre-state: oldname is being watched + has a fresh hook timestamp.
	m.mu.Lock()
	m.activeWatchers["oldname"] = "cc"
	m.mu.Unlock()
	m.probeOrch.recordHookAt("oldname")

	m.RenameSession("oldname", "newname")

	// graceWindow must follow the rename: lastHookAt[oldname] gone,
	// lastHookAt[newname] populated and still within probeGraceWindow.
	m.probeOrch.graceMu.Lock()
	if _, ok := m.probeOrch.lastHookAt["oldname"]; ok {
		m.probeOrch.graceMu.Unlock()
		t.Fatalf("lastHookAt[oldname] still present after rename — graceWindow leaked")
	}
	stamp, ok := m.probeOrch.lastHookAt["newname"]
	m.probeOrch.graceMu.Unlock()
	if !ok {
		t.Fatalf("lastHookAt[newname] missing after rename — graceWindow lost")
	}
	if time.Since(stamp) > probeGraceWindow {
		t.Fatalf("lastHookAt[newname] = %v (age %v), want recent enough that graceWindow still active",
			stamp, time.Since(stamp))
	}
}

// CC6 — RenameSession is stop-only after W3 撤回. The watcher for `oldname`
// must be torn down and the activeWatchers map evicted (no new start for
// `newname` — Phase 4a-1 always-on start was reverted; W6 will reintroduce
// starts via ProbeIntent). The whole rename runs under m.mu (caller
// contract); the orchestrator deliberately does NOT touch m.mu, so the call
// must complete promptly. We keep the 100ms fail-loud guard so a regression
// that re-enters m.mu fails the test (R3 deadlock-freedom regression).
func TestCC_RenameSession_StopsWatchViaOrchestrator(t *testing.T) {
	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec
	provider := &fakeAgentProvider{typeName: "cc"}
	m.registry.Register(provider)

	// Pre-state: oldname is being watched by cc.
	m.mu.Lock()
	m.activeWatchers["oldname"] = "cc"
	m.mu.Unlock()

	// Deadlock guard: rename should complete well within 100ms. If a
	// regression re-enters m.mu inside orchestrator calls, the rename hangs
	// and this fires.
	done := make(chan struct{})
	go func() {
		m.RenameSession("oldname", "newname")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("RenameSession hung — likely deadlock re-entering m.mu inside orchestrator")
	}

	// Post-state: activeWatchers EVICTED for both names (stop-only path).
	m.mu.Lock()
	if _, ok := m.activeWatchers["oldname"]; ok {
		m.mu.Unlock()
		t.Fatalf("activeWatchers[oldname] still present after rename")
	}
	if _, ok := m.activeWatchers["newname"]; ok {
		m.mu.Unlock()
		t.Fatalf("activeWatchers[newname] present after rename — W3 reverted always-on start; W6 will reintroduce via ProbeIntent")
	}
	m.mu.Unlock()

	// Recording fake observed exactly one StopWatch (oldname:) and NO Watch
	// calls — the orchestrator no longer auto-starts on rename.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.stops) != 1 || rec.stops[0] != "oldname:" {
		t.Fatalf("recordingProber stops = %v, want [oldname:]", rec.stops)
	}
	if len(rec.watchOpts) != 0 {
		t.Fatalf("recordingProber watchOpts = %v, want empty (no auto-start after rename)", rec.watchOpts)
	}
}
