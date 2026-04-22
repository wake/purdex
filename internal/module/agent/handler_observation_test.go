package agent

// T6 tests (plan D1.1 / D1.2 / D1.3 / D1.4 / D9) — hook path dual-write wiring.
//
// Each test uses a Module wired with a real Arbitrator (InCh only, no Run
// goroutine) so the test can pull observations off InCh and assert shape. The
// Module also keeps the legacy frame / broadcast / trace path so we can assert
// that dual-write does not regress existing behavior.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent/arbitrator"
	"github.com/wake/purdex/internal/module/agent/arbmode"
	"github.com/wake/purdex/internal/module/agent/observation"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// newHookObsModule returns a Module wired for hook-path dual-write tests:
// real AgentEventStore + Arbitrator (InCh buffered, no Run goroutine), verify
// stubbed to accept by default, session provider + tmux wired so the legacy
// broadcast path runs through.
func newHookObsModule(t *testing.T, inChCap int) *Module {
	t.Helper()
	arbitrator.ResetForTesting()

	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		t.Fatalf("open agent event store: %v", err)
	}
	t.Cleanup(func() { events.Close() })

	m := New(events)
	m.registry = agentpkg.NewRegistry()
	m.registry.Register(&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusRunning}
		},
	})

	// Default stubs (mirrors newTestModule).
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

	// Wire Arbitrator (no Run) + shared trace-id registry.
	minter, lookup := observation.NewTraceIDRegistry()
	m.traceMinter = minter
	m.traceLookup = lookup
	m.arbmodeMgr = arbmode.NewManager("", "")
	m.arbitrator = arbitrator.NewArbitrator(arbitrator.Options{
		Minter:      minter,
		Lookup:      lookup,
		Arbmode:     m.arbmodeMgr,
		TraceWriter: &nopTraceSubmitter{},
		InChCap:     inChCap,
	})

	// Session + tmux + core wiring so the legacy broadcast path is fully alive.
	fake := tmux.NewFakeExecutor()
	fake.SetPaneSessionName("%7", "work")
	m.tmux = fake
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}},
	}

	return m
}

func drainObservation(t *testing.T, m *Module, timeout time.Duration) observation.Observation {
	t.Helper()
	select {
	case obs := <-m.arbitrator.InChForTesting():
		return obs
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for observation on InCh")
		return observation.Observation{}
	}
}

func assertEvidence(t *testing.T, obs observation.Observation, key string, want any) {
	t.Helper()
	for _, ev := range obs.Evidence {
		if ev.Key == key {
			if ev.Value != want {
				t.Fatalf("evidence[%s] = %v (%T), want %v (%T)", key, ev.Value, ev.Value, want, want)
			}
			return
		}
	}
	t.Fatalf("evidence[%s] missing; evidence=%+v", key, obs.Evidence)
}

func postHookEvent(t *testing.T, m *Module, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/agent/event", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	m.handleEvent(w, req)
	return w
}

// 1. Accept branch submits observation with Phase=Committed.
func TestHandleEvent_Accept_SubmitsObservation(t *testing.T) {
	m := newHookObsModule(t, 8)

	body := `{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"PostToolUse",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`
	w := postHookEvent(t, m, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	obs := drainObservation(t, m, 100*time.Millisecond)

	if obs.Phase != observation.PhaseCommitted {
		t.Fatalf("Phase = %q, want %q (accepted must commit)", obs.Phase, observation.PhaseCommitted)
	}
	if obs.SourceKind != observation.SourceHook {
		t.Fatalf("SourceKind = %q, want %q", obs.SourceKind, observation.SourceHook)
	}
	if obs.Action != "PostToolUse" {
		t.Fatalf("Action = %q, want PostToolUse", obs.Action)
	}
	if obs.SessionID != "code-work" {
		t.Fatalf("SessionID = %q, want code-work (resolved from tmux name)", obs.SessionID)
	}
	if obs.TraceID == "" {
		t.Fatalf("TraceID must be non-empty (provisional UUID mint)")
	}
	// Identity triple + event_name + role_resolution all present.
	assertEvidence(t, obs, "pid", int64(1234))
	assertEvidence(t, obs, "pane_id", "%7")
	assertEvidence(t, obs, "event_name", "PostToolUse")
	assertEvidence(t, obs, observation.EvidenceKeyRoleResolution, observation.RoleResolved)
}

// 2. Reject branch submits observation with Phase=Proposed and role_resolution.
func TestHandleEvent_Reject_SubmitsObservation(t *testing.T) {
	m := newHookObsModule(t, 8)
	verifyEventFn = func(*Module, EventRequest) verifyDecision {
		return verifyDecision{Accepted: false, Reason: "identify_mismatch"}
	}

	body := `{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"Stop",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`
	w := postHookEvent(t, m, body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (reject branch)", w.Code)
	}

	obs := drainObservation(t, m, 100*time.Millisecond)
	if obs.Phase != observation.PhaseProposed {
		t.Fatalf("Phase = %q, want proposed", obs.Phase)
	}
	if obs.SourceKind != observation.SourceHook {
		t.Fatalf("SourceKind = %q, want hook", obs.SourceKind)
	}
	// identify_mismatch is retryable per builder.
	assertEvidence(t, obs, observation.EvidenceKeyRoleResolution, observation.RoleRetryableUnresolved)
	assertEvidence(t, obs, "verify_reason", "identify_mismatch")
}

// 3. SessionStart bumps generation by +1.
func TestHandleEvent_SessionStart_ObservationObservedGenerationIsCurrentPlusOne(t *testing.T) {
	m := newHookObsModule(t, 8)

	// Arbitrator starts with gen=0 for unknown session.
	if got := m.arbitrator.CurrentGeneration("code-work"); got != 0 {
		t.Fatalf("CurrentGeneration = %d, want 0 for unknown session", got)
	}

	body := `{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"SessionStart",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`
	w := postHookEvent(t, m, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	obs := drainObservation(t, m, 100*time.Millisecond)
	if obs.Action != "SessionStart" {
		t.Fatalf("Action = %q, want SessionStart", obs.Action)
	}
	if obs.ObservedGeneration != 1 {
		t.Fatalf("ObservedGeneration = %d, want 1 (current=0 +1)", obs.ObservedGeneration)
	}
	if obs.Proposal.ActorKey.Generation != 1 {
		t.Fatalf("Proposal.ActorKey.Generation = %d, want 1", obs.Proposal.ActorKey.Generation)
	}
}

// 4. Non-SessionStart uses current generation unchanged.
func TestHandleEvent_NonSessionStart_ObservationObservedGenerationIsCurrent(t *testing.T) {
	m := newHookObsModule(t, 8)

	body := `{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"UserPromptSubmit",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`
	w := postHookEvent(t, m, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	obs := drainObservation(t, m, 100*time.Millisecond)
	if obs.ObservedGeneration != 0 {
		t.Fatalf("ObservedGeneration = %d, want 0 (current generation for unknown session)", obs.ObservedGeneration)
	}
}

// 5. Provisional TraceID is non-empty — bypasses builder.Build validation.
func TestHandleEvent_ProvisionalTraceID_NonEmpty(t *testing.T) {
	m := newHookObsModule(t, 8)

	body := `{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"PostToolUse",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`
	if w := postHookEvent(t, m, body); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	obs := drainObservation(t, m, 100*time.Millisecond)
	if obs.TraceID == "" {
		t.Fatalf("TraceID must never be empty — builder validation would reject")
	}
}

// 6. Identity-triple start_time backfilled from FramesStore when hook req is
// missing it. (SenderUncertain=true makes SenderStartTime optional; our
// resolver should then try FramesStore.FindByPanePID.)
func TestHandleEvent_IdentityTriple_StartTimeBackfilled_WhenReqMissing(t *testing.T) {
	m := newHookObsModule(t, 8)
	// Verify must accept uncertain events for this test so we exercise the
	// backfill branch without verify-layer rejection.
	verifyEventFn = func(*Module, EventRequest) verifyDecision { return verifyDecision{Accepted: true} }

	// Seed a frame with a known start time for (pane %7, pid 1234).
	const seedStart = "Mon Apr 21 03:00:00 2026"
	if _, err := m.frames.Upsert(store.Frame{
		PaneID:           "%7",
		AgentType:        "cc",
		PID:              1234,
		PPID:             100,
		ProcessStartTime: seedStart,
		Status:           agentpkg.StatusRunning,
		StartedAt:        1,
		LastSeenAt:       1,
		Verified:         true,
	}); err != nil {
		t.Fatalf("seed frame: %v", err)
	}

	// sender_uncertain=true + no sender_start_time → backfill should kick in.
	body := `{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"PostToolUse",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":1234,
		"sender_uncertain":true
	}`
	if w := postHookEvent(t, m, body); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	obs := drainObservation(t, m, 100*time.Millisecond)
	assertEvidence(t, obs, "start_time", seedStart)
}

// 7. Identity-triple start_time falls back to "unknown" if FramesStore also has nothing.
func TestHandleEvent_IdentityTriple_StartTimeUnknown_WhenFrameNotFound(t *testing.T) {
	m := newHookObsModule(t, 8)
	verifyEventFn = func(*Module, EventRequest) verifyDecision { return verifyDecision{Accepted: true} }

	// No frame seeded → FindByPanePID returns nil → resolver must yield ""
	// which the builder maps to "unknown".
	body := `{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"PostToolUse",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":9999,
		"sender_uncertain":true
	}`
	arbitrator.ResetForTesting() // clear any prior identity_unknown counter
	if w := postHookEvent(t, m, body); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	obs := drainObservation(t, m, 100*time.Millisecond)
	assertEvidence(t, obs, "start_time", "unknown")

	// Metric fires on fallback path regardless of whether start_time ended up
	// "unknown" or resolved via FramesStore — both take the non-req path.
	if got := arbitrator.Value("lights_hook_identity_unknown", "event=PostToolUse"); got != 1 {
		t.Fatalf("lights_hook_identity_unknown{event=PostToolUse} = %d, want 1", got)
	}
}

// 8. hookTraceCollector uses traceLookup.Get when available.
func TestHookTraceCollector_TraceIDFromLookup_WhenAvailable(t *testing.T) {
	m := newHookObsModule(t, 8)

	// Pre-populate lookup with a trace_id for (code-work, gen=0) — the
	// non-SessionStart hook should pick it up instead of chain_id fallback.
	const wantTID = "11111111-2222-3333-4444-555555555555"
	got := m.traceMinter.AdoptTraceID("code-work", 0, wantTID)
	if got != wantTID {
		t.Fatalf("AdoptTraceID returned %q, want %q (precondition)", got, wantTID)
	}

	body := `{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"UserPromptSubmit",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`
	if w := postHookEvent(t, m, body); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	// drain obs
	_ = drainObservation(t, m, 100*time.Millisecond)

	// Wait for the trace to hit the DB (collector.Finish enqueues async).
	m.traceSink.FlushForTest()

	page, err := m.traces.ListChains(store.TraceListFilter{TmuxSession: "work", Limit: 10})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) != 1 {
		t.Fatalf("len(Chains) = %d, want 1", len(page.Chains))
	}
	record, err := m.traces.GetChainRecord(page.Chains[0].ChainID)
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	for i, step := range record.Steps {
		if step.TraceID != wantTID {
			t.Fatalf("step %d TraceID = %q, want %q (from lookup)", i, step.TraceID, wantTID)
		}
	}
}

// 9. hookTraceCollector falls back to chain_id when lookup misses; metric fires.
func TestHookTraceCollector_TraceIDFallsBackToChainID_WhenMinterNotYetCalled(t *testing.T) {
	m := newHookObsModule(t, 8)

	// No AdoptTraceID call → lookup miss for (code-work, 0).
	arbitrator.ResetForTesting()

	body := `{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"UserPromptSubmit",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`
	if w := postHookEvent(t, m, body); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	_ = drainObservation(t, m, 100*time.Millisecond)
	m.traceSink.FlushForTest()

	page, err := m.traces.ListChains(store.TraceListFilter{TmuxSession: "work", Limit: 10})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) != 1 {
		t.Fatalf("len(Chains) = %d, want 1", len(page.Chains))
	}
	record, err := m.traces.GetChainRecord(page.Chains[0].ChainID)
	if err != nil {
		t.Fatalf("GetChainRecord: %v", err)
	}
	for i, step := range record.Steps {
		if step.TraceID != record.Chain.ChainID {
			t.Fatalf("step %d TraceID = %q, want chain_id %q (fallback)", i, step.TraceID, record.Chain.ChainID)
		}
	}

	if got := arbitrator.Value("lights_hook_trace_id_fallback"); got < 1 {
		t.Fatalf("lights_hook_trace_id_fallback counter = %d, want >= 1", got)
	}
}

// 10. D9: SubmitObservation drop (inCh full) leaves legacy broadcast + trace intact.
func TestHandleEvent_SubmitObservationDrop_LegacyPathStillRuns(t *testing.T) {
	m := newHookObsModule(t, 1)
	// Saturate InCh so the committed observation has to fall through the
	// 100ms blocking retry before dropping.
	fillInCh(t, m, 1)

	sub := m.core.Events.AddTestSubscriber()
	defer m.core.Events.RemoveTestSubscriber(sub)

	body := `{
		"tmux_session":"work",
		"tmux_pane_id":"%7",
		"event_name":"UserPromptSubmit",
		"raw_event":{},
		"agent_type":"cc",
		"sender_pid":1234,
		"sender_start_time":"Sun Apr 20 01:30:00 2026"
	}`
	w := postHookEvent(t, m, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (legacy path unaffected by submit drop)", w.Code)
	}

	// Legacy broadcast must still fire for the session.
	select {
	case msg := <-sub.SendCh():
		var env struct {
			Type    string `json:"type"`
			Session string `json:"session"`
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
	case <-time.After(500 * time.Millisecond):
		t.Fatal("legacy hook broadcast missing after submit drop")
	}

	// Legacy trace chain must still land in the DB.
	m.traceSink.FlushForTest()
	page, err := m.traces.ListChains(store.TraceListFilter{TmuxSession: "work", Limit: 10})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	if len(page.Chains) == 0 {
		t.Fatalf("legacy hook trace missing after submit drop")
	}

	// Metric fires because the committed observation ended up dropped.
	if got := arbitrator.Value("lights_arb_in_dropped", "priority=committed"); got != 1 {
		t.Fatalf("lights_arb_in_dropped{priority=committed} = %d, want 1 (drop expected)", got)
	}
}
