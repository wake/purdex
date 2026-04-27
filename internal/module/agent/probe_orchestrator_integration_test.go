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

// CC2 — manageActivityWatch routes through the orchestrator. Uses the
// recording fake from probe_orchestrator_test.go: when manageActivityWatch
// hands a watch-eligible status it must call orchestrator.startWatch (which
// invokes recordingProber.Watch with target="sess:"); when it hands an
// off-style status the recording prober must observe StopWatch("sess:").
func TestModule_ManageActivityWatch_RoutesThroughOrchestrator(t *testing.T) {
	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec
	provider := &fakeAgentProvider{typeName: "cc"}
	m.registry.Register(provider)

	// waiting → start watch
	m.manageActivityWatch("sess", "cc", agentpkg.StatusWaiting)

	rec.mu.Lock()
	if _, ok := rec.watchOpts["sess:"]; !ok {
		rec.mu.Unlock()
		t.Fatalf("expected Watch on target %q after StatusWaiting, got %v", "sess:", rec.watchOpts)
	}
	rec.mu.Unlock()

	// off (anything not in shouldWatchActivity, e.g. StatusError) → stop watch
	m.manageActivityWatch("sess", "cc", agentpkg.StatusError)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	stopped := false
	for _, target := range rec.stops {
		if target == "sess:" {
			stopped = true
			break
		}
	}
	if !stopped {
		t.Fatalf("expected StopWatch on target %q after off-status, got %v", "sess:", rec.stops)
	}
}

// CC2b — G6 watcher-leak gate (R15 fix #2). End-to-end integration test
// using a REAL *probe.Prober (no recordingProber seam) so failures surface
// the actual map state. Walks: waiting → assert activeWatchers and prober
// HasWatcher are populated → off → assert both are cleared.
func TestModule_ManageActivityWatch_OffClearsActiveWatchers(t *testing.T) {
	m := newTestModule(t)
	// Real prober wired against a fake tmux executor. The watcher goroutine
	// ticks at watchPollInterval (default 500ms) but this test only inspects
	// map state immediately after Watch / StopWatch — no need to override
	// the cadence.
	fake := tmux.NewFakeExecutor()
	fake.SetPaneContent("sess:", "irrelevant")
	m.tmux = fake
	m.prober = probe.New(fake)
	t.Cleanup(func() { m.prober.StopAllWatches() })

	// fakeAgentProvider does not implement ProbeProfileProvider — orchestrator
	// will use defaultProbeProfile (BottomLines=10), which is fine for this
	// gate; we only care about lifecycle, not capture mode.
	provider := &fakeAgentProvider{typeName: "cc"}
	m.registry.Register(provider)

	// waiting → watcher live + activeWatchers populated.
	m.manageActivityWatch("sess", "cc", agentpkg.StatusWaiting)

	m.mu.Lock()
	if got, ok := m.activeWatchers["sess"]; !ok || got != "cc" {
		m.mu.Unlock()
		t.Fatalf("activeWatchers[sess] = (%q, %v), want (\"cc\", true)", got, ok)
	}
	m.mu.Unlock()
	if !m.prober.HasWatcher("sess:") {
		t.Fatalf("prober.HasWatcher(\"sess:\") = false, want true after StatusWaiting")
	}

	// off (StatusError → not in shouldWatchActivity) → watcher torn down +
	// activeWatchers entry gone.
	m.manageActivityWatch("sess", "cc", agentpkg.StatusError)

	m.mu.Lock()
	if _, ok := m.activeWatchers["sess"]; ok {
		m.mu.Unlock()
		t.Fatalf("activeWatchers[sess] still present after StatusOff (G6 watcher leak)")
	}
	m.mu.Unlock()
	if m.prober.HasWatcher("sess:") {
		t.Fatalf("prober.HasWatcher(\"sess:\") = true, want false after StatusOff (G6 watcher leak)")
	}
}

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
	body := `{"tmux_session":"work","tmux_pane_id":"%5","sender_pid":200,"sender_start_time":"Sun Apr 20 01:30:00 2026","event_name":"UserPromptSubmit","raw_event":{},"agent_type":"cc"}`
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

// CC4 — E2E ScreenChanged → broadcast Running. cc registered with its real
// ProbeProfile (TopLines=12). manageActivityWatch starts the watcher, then
// we feed a ScreenChanged event through the orchestrator's callback and
// assert the WS broadcast carries StatusRunning.
func TestCC_E2E_ScreenChangedToRunning(t *testing.T) {
	m, _, _ := orchTestModule(t) // installs recordingProber + cc registry
	seedOrchFrame(t, m, agentpkg.StatusWaiting, 280)

	// manageActivityWatch starts the watcher (recording fake observes it).
	m.manageActivityWatch("work", "cc", agentpkg.StatusWaiting)

	m.mu.Lock()
	if got, ok := m.activeWatchers["work"]; !ok || got != "cc" {
		m.mu.Unlock()
		t.Fatalf("activeWatchers[work] = (%q, %v) after manageActivityWatch", got, ok)
	}
	m.currentStatus["work"] = agentpkg.StatusWaiting
	m.mu.Unlock()

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

// CC6 — RenameSession routes through orchestrator (R2 fix #1). The watcher
// for `oldname` must be torn down and a new watcher started for `newname`,
// with the activeWatchers map atomically transferred. The whole rename runs
// under m.mu (caller contract); the orchestrator deliberately does NOT
// touch m.mu, so the call must complete promptly. We enforce a 100ms
// fail-loud guard so a regression that re-enters m.mu fails the test
// (R3 deadlock-freedom regression).
func TestCC_RenameSession_RestartsWatchViaOrchestrator(t *testing.T) {
	m := newTestModule(t)
	rec := newRecordingProber()
	m.probeOrch.watcher = rec
	provider := &fakeAgentProvider{typeName: "cc"}
	m.registry.Register(provider)

	// Pre-state: oldname is being watched by cc.
	m.mu.Lock()
	m.activeWatchers["oldname"] = "cc"
	m.mu.Unlock()
	// Pre-record a Watch entry so the rec fake's pre-state matches a real
	// watcher (orchestrator doesn't read this; we assert post-state instead).

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

	// Post-state: activeWatchers transferred.
	m.mu.Lock()
	if _, ok := m.activeWatchers["oldname"]; ok {
		m.mu.Unlock()
		t.Fatalf("activeWatchers[oldname] still present after rename")
	}
	if got, ok := m.activeWatchers["newname"]; !ok || got != "cc" {
		m.mu.Unlock()
		t.Fatalf("activeWatchers[newname] = (%q, %v), want (\"cc\", true)", got, ok)
	}
	m.mu.Unlock()

	// Recording fake observed exactly one StopWatch + one Watch with the new
	// target.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.stops) != 1 || rec.stops[0] != "oldname:" {
		t.Fatalf("recordingProber stops = %v, want [oldname:]", rec.stops)
	}
	if _, ok := rec.watchOpts["newname:"]; !ok {
		t.Fatalf("recordingProber watchOpts missing newname:; got %v", rec.watchOpts)
	}
	if _, ok := rec.watchOpts["oldname:"]; ok {
		t.Fatalf("recordingProber watchOpts unexpectedly contains oldname:; got %v", rec.watchOpts)
	}
}
