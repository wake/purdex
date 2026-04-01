package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/tmux-box/internal/bridge"
	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/detect"
	"github.com/wake/tmux-box/internal/module/session"
	"github.com/wake/tmux-box/internal/tmux"
)

// --- Fake CCOperator ---

type fakeCCOperator struct {
	mu            sync.Mutex
	interruptErr  error
	exitErr       error
	getStatusErr  error
	launchErr     error
	statusInfo    *detect.StatusInfo
	interruptCalls int
	exitCalls      int
	getStatusCalls int
	launchCalls    []string // commands passed to Launch
}

func (f *fakeCCOperator) Interrupt(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interruptCalls++
	return f.interruptErr
}

func (f *fakeCCOperator) Exit(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exitCalls++
	return f.exitErr
}

func (f *fakeCCOperator) GetStatus(_ context.Context, _ string) (*detect.StatusInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getStatusCalls++
	if f.getStatusErr != nil {
		return nil, f.getStatusErr
	}
	return f.statusInfo, nil
}

func (f *fakeCCOperator) Launch(_ context.Context, _ string, cmd string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launchCalls = append(f.launchCalls, cmd)
	return f.launchErr
}

// --- Fake CCDetector ---

type fakeCCDetector struct {
	mu       sync.Mutex
	statuses []detect.Status // returns statuses in sequence; last one repeats
	callIdx  int
}

func (f *fakeCCDetector) Detect(_ string) detect.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.statuses) == 0 {
		return detect.StatusNormal
	}
	idx := f.callIdx
	if idx >= len(f.statuses) {
		idx = len(f.statuses) - 1
	}
	f.callIdx++
	return f.statuses[idx]
}

// --- Test helpers ---

// setupHandoffModule creates a StreamModule wired with all fakes for orchestrator testing.
func setupHandoffModule(t *testing.T, opts handoffTestOpts) *handoffTestEnv {
	t.Helper()

	fp := &fakeSessionProvider{sessions: opts.sessions}
	fakeOps := opts.ccOps
	if fakeOps == nil {
		fakeOps = &fakeCCOperator{}
	}
	fakeDet := opts.ccDetect
	if fakeDet == nil {
		fakeDet = &fakeCCDetector{}
	}
	fakeTx := opts.tmux
	if fakeTx == nil {
		fakeTx = tmux.NewFakeExecutor()
	}

	cfg := opts.cfg
	if cfg == nil {
		cfg = &config.Config{
			Bind:  "127.0.0.1",
			Port:  7860,
			Token: "test-token",
			Stream: config.StreamConfig{
				Presets: []config.Preset{{Name: "cc", Command: "claude -p --verbose --input-format stream-json --output-format stream-json"}},
			},
		}
	}

	c := &core.Core{
		Cfg:    cfg,
		Tmux:   fakeTx,
		Events: core.NewEventsBroadcaster(),
	}

	m := &StreamModule{
		core:     c,
		bridge:   bridge.New(),
		sessions: fp,
		ccOps:    fakeOps,
		ccDetect: fakeDet,
		locks:    newHandoffLocks(),
	}

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &handoffTestEnv{
		module:   m,
		provider: fp,
		ccOps:    fakeOps,
		ccDetect: fakeDet,
		tmux:     fakeTx,
		srv:      srv,
		core:     c,
	}
}

type handoffTestOpts struct {
	sessions map[string]*session.SessionInfo
	ccOps    *fakeCCOperator
	ccDetect *fakeCCDetector
	tmux     *tmux.FakeExecutor
	cfg      *config.Config
}

type handoffTestEnv struct {
	module   *StreamModule
	provider *fakeSessionProvider
	ccOps    *fakeCCOperator
	ccDetect *fakeCCDetector
	tmux     *tmux.FakeExecutor
	srv      *httptest.Server
	core     *core.Core
}

func postHandoff(t *testing.T, srv *httptest.Server, code, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(srv.URL+"/api/sessions/"+code+"/handoff", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	return resp
}

// drainEvents collects events from a test subscriber until timeout.
func drainEvents(sub *core.EventSubscriber, timeout time.Duration) []core.SessionEvent {
	var events []core.SessionEvent
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-sub.SendCh():
			var evt core.SessionEvent
			if json.Unmarshal(msg, &evt) == nil {
				events = append(events, evt)
			}
		case <-deadline:
			return events
		}
	}
}

// waitForEvent waits for a specific handoff event value.
func waitForEvent(t *testing.T, sub *core.EventSubscriber, wantValue string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-sub.SendCh():
			var evt core.SessionEvent
			if json.Unmarshal(msg, &evt) == nil && evt.Type == "handoff" && evt.Value == wantValue {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for handoff event %q", wantValue)
		}
	}
}

// --- handleHandoff HTTP entry tests ---

func TestHandoff_SessionNotFound_Returns404(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{},
	})

	resp := postHandoff(t, env.srv, "nope00", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandoff_InvalidMode_Returns400(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
	})

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"invalid","preset":"cc"}`)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandoff_PresetNotFound_Returns400(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
	})

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"nonexistent"}`)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandoff_TermMode_NoPresetNeeded(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "stream", CCSessionID: "sess-uuid"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{detect.StatusNormal}},
	})

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"terminal"}`)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.NotEmpty(t, body["handoff_id"])
}

func TestHandoff_ConcurrentLock_Returns409(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
		// Slow ccDetect — keeps handoff running while we try second request
		ccDetect: &fakeCCDetector{statuses: []detect.Status{detect.StatusCCRunning}},
		ccOps: &fakeCCOperator{
			interruptErr: fmt.Errorf("context deadline exceeded"), // will stall
		},
	})

	// First handoff: acquire lock
	resp1 := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp1.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp1.StatusCode)

	// Give goroutine time to start
	time.Sleep(50 * time.Millisecond)

	// Second handoff: should get 409
	resp2 := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusConflict, resp2.StatusCode)
}

func TestHandoff_Returns202WithHandoffID(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{detect.StatusCCIdle}},
		ccOps: &fakeCCOperator{
			statusInfo: &detect.StatusInfo{SessionID: "uuid-1234", Cwd: "/home/user"},
		},
	})

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusAccepted, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Len(t, body["handoff_id"], 16, "handoff_id should be 16 hex chars")
}

func TestHandoff_InvalidBody_Returns400(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
	})

	resp := postHandoff(t, env.srv, "abc123", `{invalid`)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// --- runHandoff orchestration tests ---

func TestRunHandoff_FullSequence(t *testing.T) {
	// CC is running (not idle) → interrupt → getStatus → exit → launch relay → relay connects → update meta → broadcast connected
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusCCRunning, // initial detect: CC is running
		}},
		ccOps: &fakeCCOperator{
			statusInfo: &detect.StatusInfo{SessionID: "uuid-1234", Cwd: "/home/user/project"},
		},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// The orchestrator will: detect → interrupt → getStatus → exit → launch relay → wait relay connect
	// Since no real relay connects, it will eventually broadcast "failed:relay did not connect within 15s"
	// We need to simulate relay connection. Let's register a relay on the bridge to simulate relay connect.
	go func() {
		// Wait a bit for the orchestrator to reach "wait for relay" step
		time.Sleep(200 * time.Millisecond)
		// Simulate relay connecting
		env.module.bridge.RegisterRelay("abc123")
	}()

	// Wait for "connected" broadcast
	waitForEvent(t, eventSub, "connected", 5*time.Second)

	// Verify CCOperator was called correctly
	env.ccOps.mu.Lock()
	assert.Equal(t, 1, env.ccOps.interruptCalls, "should have interrupted CC")
	assert.Equal(t, 1, env.ccOps.getStatusCalls, "should have called GetStatus")
	assert.Equal(t, 1, env.ccOps.exitCalls, "should have called Exit")
	env.ccOps.mu.Unlock()

	// Verify meta was updated with mode + cc_session_id + cwd
	updates := env.provider.getUpdates()
	var modeUpdated, sessionIDUpdated, cwdUpdated bool
	for _, u := range updates {
		if u.Code == "abc123" {
			if u.Update.Mode != nil && *u.Update.Mode == "stream" {
				modeUpdated = true
			}
			if u.Update.CCSessionID != nil && *u.Update.CCSessionID == "uuid-1234" {
				sessionIDUpdated = true
			}
			if u.Update.Cwd != nil && *u.Update.Cwd == "/home/user/project" {
				cwdUpdated = true
			}
		}
	}
	assert.True(t, modeUpdated, "mode should be updated to stream")
	assert.True(t, sessionIDUpdated, "cc_session_id should be updated")
	assert.True(t, cwdUpdated, "cwd should be updated")

	// Verify SendKeys was called with relay command
	sent := env.tmux.KeysSent()
	require.NotEmpty(t, sent, "should have sent relay command")
	lastCmd := sent[len(sent)-1].Keys
	assert.Contains(t, lastCmd, "tbox relay")
	assert.Contains(t, lastCmd, "--session abc123")
	assert.Contains(t, lastCmd, "--resume uuid-1234")

	// Clean up the simulated relay
	env.module.bridge.UnregisterRelay("abc123")
}

func TestRunHandoff_CCIdle_SkipsInterrupt(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusCCIdle, // already idle
		}},
		ccOps: &fakeCCOperator{
			statusInfo: &detect.StatusInfo{SessionID: "uuid-5678", Cwd: "/tmp"},
		},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// Simulate relay connect
	go func() {
		time.Sleep(200 * time.Millisecond)
		env.module.bridge.RegisterRelay("abc123")
	}()

	waitForEvent(t, eventSub, "connected", 5*time.Second)

	// Interrupt should NOT have been called since CC was already idle
	env.ccOps.mu.Lock()
	assert.Equal(t, 0, env.ccOps.interruptCalls, "should not interrupt when already idle")
	assert.Equal(t, 1, env.ccOps.getStatusCalls)
	assert.Equal(t, 1, env.ccOps.exitCalls)
	env.ccOps.mu.Unlock()

	env.module.bridge.UnregisterRelay("abc123")
}

func TestRunHandoff_NoCCRunning_BroadcastsFailed(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusNormal, // no CC running
		}},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	waitForEvent(t, eventSub, "failed:no CC running", 3*time.Second)
}

func TestRunHandoff_InterruptFails_BroadcastsFailed(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusCCRunning, // CC busy
		}},
		ccOps: &fakeCCOperator{
			interruptErr: fmt.Errorf("context deadline exceeded"),
		},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	waitForEvent(t, eventSub, "failed:interrupt CC: context deadline exceeded", 3*time.Second)
}

func TestRunHandoff_GetStatusFails_BroadcastsFailed(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusCCIdle,
		}},
		ccOps: &fakeCCOperator{
			getStatusErr: fmt.Errorf("could not extract session ID"),
		},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	waitForEvent(t, eventSub, "failed:get status: could not extract session ID", 3*time.Second)
}

func TestRunHandoff_ExitFails_BroadcastsFailed(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusCCIdle,
		}},
		ccOps: &fakeCCOperator{
			statusInfo: &detect.StatusInfo{SessionID: "uuid-1234", Cwd: "/tmp"},
			exitErr:    fmt.Errorf("CC did not exit"),
		},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	waitForEvent(t, eventSub, "failed:exit CC: CC did not exit", 3*time.Second)
}

// --- runHandoffToTerm orchestration tests ---

func TestRunHandoffToTerm_FullSequence(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "stream", CCSessionID: "sess-uuid"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusNormal,  // shell wait loop: first check
			detect.StatusNormal,  // shell wait: final check after loop
			detect.StatusCCIdle,  // CC verify: detected CC started
			detect.StatusCCIdle,  // (extra for safety)
		}},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"terminal"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	waitForEvent(t, eventSub, "connected", 5*time.Second)

	// Verify SendKeys was called with claude --resume
	sent := env.tmux.KeysSent()
	require.NotEmpty(t, sent)
	lastCmd := sent[len(sent)-1].Keys
	assert.Contains(t, lastCmd, "claude --resume sess-uuid")

	// Verify cc_session_id was cleared
	updates := env.provider.getUpdates()
	var sessionIDCleared bool
	for _, u := range updates {
		if u.Code == "abc123" && u.Update.CCSessionID != nil && *u.Update.CCSessionID == "" {
			sessionIDCleared = true
		}
	}
	assert.True(t, sessionIDCleared, "cc_session_id should be cleared")
}

func TestRunHandoffToTerm_NoCCSessionID_Fails(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "stream", CCSessionID: ""},
		},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"terminal"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	waitForEvent(t, eventSub, "failed:no CC session ID stored", 3*time.Second)
}

func TestRunHandoffToTerm_ShellNotRecovered_RollsBack(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "stream", CCSessionID: "sess-uuid"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusNotInCC, // shell doesn't recover — keeps returning not-in-cc
		}},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"terminal"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	waitForEvent(t, eventSub, "failed:shell did not recover", 20*time.Second)

	// Verify mode was rolled back to original ("stream")
	updates := env.provider.getUpdates()
	var modeRolledBack bool
	for _, u := range updates {
		if u.Code == "abc123" && u.Update.Mode != nil && *u.Update.Mode == "stream" {
			modeRolledBack = true
		}
	}
	assert.True(t, modeRolledBack, "mode should be rolled back to stream on failure")
}

func TestRunHandoffToTerm_CCDidNotStart_RollsBack(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "stream", CCSessionID: "sess-uuid"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusNormal, // shell wait: passes
			detect.StatusNormal, // shell final check: passes
			detect.StatusNormal, // CC verify: stays normal — CC never starts
		}},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"terminal"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// Should fail because CC never starts (detector stays at StatusNormal)
	waitForEvent(t, eventSub, "failed:CC did not start", 20*time.Second)

	// Verify mode was rolled back to original ("stream")
	updates := env.provider.getUpdates()
	var modeRolledBack bool
	for _, u := range updates {
		if u.Code == "abc123" && u.Update.Mode != nil && *u.Update.Mode == "stream" {
			modeRolledBack = true
		}
	}
	assert.True(t, modeRolledBack, "mode should be rolled back to stream on failure")
}

func TestRunHandoffToTerm_PreUpdatesMode(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "stream", CCSessionID: "sess-uuid"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusNormal,  // shell wait loop
			detect.StatusNormal,  // shell wait final check
			detect.StatusCCIdle,  // CC verify
			detect.StatusCCIdle,  // extra
		}},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"terminal"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	waitForEvent(t, eventSub, "connected", 5*time.Second)

	// Verify mode was pre-updated to "terminal" (should be the first update)
	updates := env.provider.getUpdates()
	require.NotEmpty(t, updates)
	first := updates[0]
	assert.Equal(t, "abc123", first.Code)
	require.NotNil(t, first.Update.Mode)
	assert.Equal(t, "terminal", *first.Update.Mode)
}

// --- Handoff with existing relay tests ---

func TestRunHandoff_DisconnectsExistingRelay(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "stream"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusCCIdle,
		}},
		ccOps: &fakeCCOperator{
			statusInfo: &detect.StatusInfo{SessionID: "uuid-1234", Cwd: "/tmp"},
		},
	})

	// Pre-register a relay
	relayCh, err := env.module.bridge.RegisterRelay("abc123")
	require.NoError(t, err)

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// The orchestrator sends shutdown to relay. Simulate relay reading it and disconnecting.
	go func() {
		select {
		case msg := <-relayCh:
			_ = msg // shutdown message received
			// Simulate relay disconnecting
			env.module.bridge.UnregisterRelay("abc123")
		case <-time.After(5 * time.Second):
		}
	}()

	// After old relay disconnects and orchestrator proceeds through pane prep + detect +
	// getStatus + exit + launch, simulate new relay connecting.
	// Wait for "launching" event to know when to connect the new relay.
	go func() {
		deadline := time.After(15 * time.Second)
		for {
			select {
			case <-deadline:
				return
			default:
				// Poll for relay slot to be available (old relay unregistered,
				// and orchestrator has proceeded past the disconnect step)
				time.Sleep(100 * time.Millisecond)
				if !env.module.bridge.HasRelay("abc123") {
					// Wait a bit more for the orchestrator to launch the relay command
					time.Sleep(3 * time.Second)
					if !env.module.bridge.HasRelay("abc123") {
						env.module.bridge.RegisterRelay("abc123")
					}
					return
				}
			}
		}
	}()

	waitForEvent(t, eventSub, "connected", 15*time.Second)

	// Clean up
	env.module.bridge.UnregisterRelay("abc123")
}

// --- Broadcast uses session CODE test ---

func TestRunHandoff_BroadcastsUseSessionCode(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusNormal, // no CC → triggers "failed:no CC running"
		}},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// Collect events — all should use session code "abc123" (not name "test-sess")
	// Wait long enough for pane prep (1.5s) + detect + broadcast
	events := drainEvents(eventSub, 5*time.Second)
	for _, evt := range events {
		if evt.Type == "handoff" {
			assert.Equal(t, "abc123", evt.Session, "broadcast should use session code, not name")
		}
	}
}

// --- Tmux target format test ---

func TestRunHandoff_UsesCorrectTmuxTarget(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "my-app", Mode: "terminal"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusCCIdle,
		}},
		ccOps: &fakeCCOperator{
			statusInfo: &detect.StatusInfo{SessionID: "uuid-9999", Cwd: "/tmp"},
		},
	})

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// Give goroutine time to run
	time.Sleep(500 * time.Millisecond)

	// Verify tmux target uses "Name:0" format
	sent := env.tmux.KeysSent()
	for _, k := range sent {
		assert.Equal(t, "my-app:0", k.Target, "tmux target should be sess.Name:0")
	}
}

// --- Lock released after handoff ---

func TestRunHandoff_LockReleasedAfterCompletion(t *testing.T) {
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusNormal, // no CC → fails after pane prep (~1.5s)
		}},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// Wait for the handoff to broadcast failure (includes 1.5s pane prep)
	waitForEvent(t, eventSub, "failed:no CC running", 5*time.Second)

	// Give goroutine a moment to fully return and release lock
	time.Sleep(100 * time.Millisecond)

	// Lock should be released — TryLock should succeed
	assert.True(t, env.module.locks.TryLock("abc123"), "lock should be released after handoff completes")
	env.module.locks.Unlock("abc123")
}

// --- Timeout failure tests ---

func TestRunHandoff_RelayConnectTimeout(t *testing.T) {
	// CC idle → getStatus → exit → launch relay → relay never connects → timeout
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "terminal"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusCCIdle,
		}},
		ccOps: &fakeCCOperator{
			statusInfo: &detect.StatusInfo{SessionID: "uuid-1234", Cwd: "/tmp"},
		},
	})

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"stream","preset":"cc"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// No relay registers on the bridge — the orchestrator waits 15s then times out.
	waitForEvent(t, eventSub, "failed:relay did not connect within 15s", 20*time.Second)
}

func TestRunHandoffToTerm_RelayShutdownTimeout(t *testing.T) {
	// Pre-register a relay that never reads the shutdown message → timeout
	env := setupHandoffModule(t, handoffTestOpts{
		sessions: map[string]*session.SessionInfo{
			"abc123": {Code: "abc123", Name: "test-sess", Mode: "stream", CCSessionID: "sess-uuid"},
		},
		ccDetect: &fakeCCDetector{statuses: []detect.Status{
			detect.StatusNormal,
		}},
	})

	// Register a relay but never read from its channel — shutdown message is dropped
	_, err := env.module.bridge.RegisterRelay("abc123")
	require.NoError(t, err)

	eventSub := env.core.Events.AddTestSubscriber()
	defer env.core.Events.RemoveTestSubscriber(eventSub)

	resp := postHandoff(t, env.srv, "abc123", `{"mode":"terminal"}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	// Relay never disconnects → 5s timeout → broadcasts failure
	waitForEvent(t, eventSub, "failed:relay did not disconnect", 10*time.Second)

	// Verify mode was rolled back to original ("stream")
	updates := env.provider.getUpdates()
	var modeRolledBack bool
	for _, u := range updates {
		if u.Code == "abc123" && u.Update.Mode != nil && *u.Update.Mode == "stream" {
			modeRolledBack = true
		}
	}
	assert.True(t, modeRolledBack, "mode should be rolled back to stream on relay shutdown timeout")

	// Clean up
	env.module.bridge.UnregisterRelay("abc123")
}
