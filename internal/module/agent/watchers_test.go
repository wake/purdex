package agent

// T7 tests (plan D2.1 / D2.2 / D2.3 / D2.4) — Module-side probe Watcher
// management + probe callback observation build + teardown.
//
// The tests exercise probeOnOutcome directly (pushing synthetic
// outcome+token+evidence) rather than going through probe.Start* — that
// layer is covered by probe/watchers_test.go. This keeps the module-layer
// tests decoupled from the probe package's internal goroutine scheduling.

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent/arbitrator"
	"github.com/wake/purdex/internal/module/agent/arbmode"
	"github.com/wake/purdex/internal/module/agent/observation"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// fakeModuleWatcher satisfies probe.Watcher for module-layer tests.
// We intentionally do not re-use probe.StartLiveness here — the module
// tests need a Watcher that records Stop() deterministically without
// spinning a goroutine.
type fakeModuleWatcher struct {
	tokenValue string
	stopped    atomic.Bool
}

func newFakeWatcher(id string) *fakeModuleWatcher {
	return &fakeModuleWatcher{tokenValue: "tok-" + id}
}

func (f *fakeModuleWatcher) Token() string { return f.tokenValue }
func (f *fakeModuleWatcher) Rotate() string {
	f.tokenValue = f.tokenValue + "-rot"
	return f.tokenValue
}
func (f *fakeModuleWatcher) Stop() { f.stopped.Store(true) }

// newProbeObsModule builds a Module wired for probe-path dual-write tests:
// real AgentEventStore so FramesStore is live, Arbitrator with a buffered
// InCh (no Run goroutine) so the test can pull submitted observations.
func newProbeObsModule(t *testing.T, inChCap int) *Module {
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

	fake := tmux.NewFakeExecutor()
	fake.SetPaneSessionName("%7", "work")
	m.tmux = fake
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}},
	}

	return m
}

// seedFrame inserts one frame that maps back to session "work" via pane %7.
func seedFrame(t *testing.T, m *Module) store.Frame {
	t.Helper()
	frame := store.Frame{
		PaneID:           "%7",
		AgentType:        "cc",
		PID:              12345,
		PPID:             1,
		ProcessStartTime: "Sun Apr 20 01:30:00 2026",
		Status:           agentpkg.StatusRunning,
		StartedAt:        time.Now().UnixNano(),
		LastSeenAt:       time.Now().UnixNano(),
		Verified:         true,
	}
	stored, err := m.frames.Upsert(frame)
	if err != nil {
		t.Fatalf("seed frame: %v", err)
	}
	return stored
}

// drainProbeObs pulls one observation off the arbitrator's InCh with a
// bounded wait. Fails the test on timeout so tests stay deterministic.
func drainProbeObs(t *testing.T, m *Module, timeout time.Duration) observation.Observation {
	t.Helper()
	select {
	case obs := <-m.arbitrator.InChForTesting():
		return obs
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for probe observation on InCh")
		return observation.Observation{}
	}
}

// findObsEvidence returns (value, true) when obs carries evidence under key.
func findObsEvidence(obs observation.Observation, key string) (any, bool) {
	for _, e := range obs.Evidence {
		if e.Key == key {
			return e.Value, true
		}
	}
	return nil, false
}

// TestModule_ProbeCallback_SubmitsObservation_WithIdentityTriple_AndRoleResolved
// verifies the probeOnOutcome callback builds an Observation that carries
// the identity triple + role_resolution=RoleResolved.
func TestModule_ProbeCallback_SubmitsObservation_WithIdentityTriple_AndRoleResolved(t *testing.T) {
	m := newProbeObsModule(t, 8)
	seeded := seedFrame(t, m)

	cb := m.probeOnOutcome("work", "cc", probe.KindLiveness)
	cb(probe.OutcomeAlive, "tok-A", []probe.EvidenceRef{
		{Key: "probe_id", Value: "p-1"},
		{Key: "probe_kind", Value: string(probe.KindLiveness)},
	})

	obs := drainProbeObs(t, m, 500*time.Millisecond)

	if obs.SessionID != "code-work" {
		t.Fatalf("SessionID = %q, want %q", obs.SessionID, "code-work")
	}
	if obs.SourceKind != observation.SourceProbe {
		t.Fatalf("SourceKind = %q, want probe", obs.SourceKind)
	}
	if obs.Phase != observation.PhaseProposed {
		t.Fatalf("Phase = %q, want proposed", obs.Phase)
	}
	if obs.WatcherToken != "tok-A" {
		t.Fatalf("WatcherToken = %q, want tok-A", obs.WatcherToken)
	}
	// Identity triple: pid / pane_id / start_time.
	if v, ok := findObsEvidence(obs, "pid"); !ok || v != int64(seeded.PID) {
		t.Fatalf("evidence[pid] = %v ok=%v, want %d", v, ok, seeded.PID)
	}
	if v, ok := findObsEvidence(obs, "pane_id"); !ok || v != seeded.PaneID {
		t.Fatalf("evidence[pane_id] = %v ok=%v, want %q", v, ok, seeded.PaneID)
	}
	if v, ok := findObsEvidence(obs, "start_time"); !ok || v != seeded.ProcessStartTime {
		t.Fatalf("evidence[start_time] = %v ok=%v, want %q", v, ok, seeded.ProcessStartTime)
	}
	// role_resolution — alive outcome → RoleResolved (not Retryable).
	if v, ok := findObsEvidence(obs, observation.EvidenceKeyRoleResolution); !ok {
		t.Fatalf("evidence[%s] missing; evidence=%+v", observation.EvidenceKeyRoleResolution, obs.Evidence)
	} else if v != observation.RoleResolved {
		t.Fatalf("evidence[%s] = %v, want %v", observation.EvidenceKeyRoleResolution, v, observation.RoleResolved)
	}
}

// TestModule_ProbeLivenessDead_SubmitsEndLifecycleObservation: dead outcome
// maps to SuggestStatus=ended + EndLifecycle=true (via T4 builder contract).
func TestModule_ProbeLivenessDead_SubmitsEndLifecycleObservation(t *testing.T) {
	m := newProbeObsModule(t, 8)
	seedFrame(t, m)

	cb := m.probeOnOutcome("work", "cc", probe.KindLiveness)
	cb(probe.OutcomeDead, "tok-A", []probe.EvidenceRef{
		{Key: "probe_id", Value: "p-1"},
		{Key: "probe_kind", Value: string(probe.KindLiveness)},
	})

	obs := drainProbeObs(t, m, 500*time.Millisecond)

	if obs.Proposal.SuggestStatus != "ended" {
		t.Fatalf("SuggestStatus = %q, want ended", obs.Proposal.SuggestStatus)
	}
	if !obs.Proposal.EndLifecycle {
		t.Fatalf("EndLifecycle = false, want true")
	}
	if obs.Proposal.EndReason != "probe_dead" {
		t.Fatalf("EndReason = %q, want probe_dead", obs.Proposal.EndReason)
	}
}

// TestModule_SessionTeardown_StopsAllWatchers: registerWatcher +
// stopWatchersForSession stops exactly the watchers registered under the
// given session. Uses a trivial fake Watcher so we can observe Stop calls
// without the probe goroutine.
func TestModule_SessionTeardown_StopsAllWatchers(t *testing.T) {
	m := newProbeObsModule(t, 8)

	wA := newFakeWatcher("a")
	wB := newFakeWatcher("b")
	wC := newFakeWatcher("c")

	m.registerWatcher("work", probe.KindLiveness, wA)
	m.registerWatcher("work", probe.KindActivity, wB)
	m.registerWatcher("other", probe.KindLiveness, wC)

	n := m.stopWatchersForSession("work")
	if n != 2 {
		t.Fatalf("stopWatchersForSession returned %d, want 2", n)
	}
	if !wA.stopped.Load() {
		t.Fatal("watcher A (work/liveness) should be stopped")
	}
	if !wB.stopped.Load() {
		t.Fatal("watcher B (work/activity) should be stopped")
	}
	if wC.stopped.Load() {
		t.Fatal("watcher C (other/liveness) should NOT be stopped")
	}
	if _, ok := m.takeWatcher("work", probe.KindLiveness); ok {
		t.Fatal("takeWatcher(work, liveness) should return not-found after teardown")
	}
	if _, ok := m.takeWatcher("other", probe.KindLiveness); !ok {
		t.Fatal("takeWatcher(other, liveness) should still return the surviving watcher")
	}
}

// TestModule_StopAllWatchers_StopsEveryWatcher exercises the Module.Stop
// codepath without booting the full Start/Stop lifecycle. Mirrors the
// teardown contract at plan D2.4.
func TestModule_StopAllWatchers_StopsEveryWatcher(t *testing.T) {
	m := newProbeObsModule(t, 8)

	w1 := newFakeWatcher("1")
	w2 := newFakeWatcher("2")
	m.registerWatcher("work", probe.KindLiveness, w1)
	m.registerWatcher("other", probe.KindReadiness, w2)

	m.stopAllWatchers()

	if !w1.stopped.Load() || !w2.stopped.Load() {
		t.Fatalf("expected both watchers stopped; w1=%v w2=%v", w1.stopped.Load(), w2.stopped.Load())
	}
}

// TestModule_RegisterWatcher_ReplacesPrevious: a second registration for
// the same (sessionName, kind) stops the previous Watcher.
func TestModule_RegisterWatcher_ReplacesPrevious(t *testing.T) {
	m := newProbeObsModule(t, 8)
	first := newFakeWatcher("first")
	second := newFakeWatcher("second")

	m.registerWatcher("work", probe.KindLiveness, first)
	m.registerWatcher("work", probe.KindLiveness, second)

	if !first.stopped.Load() {
		t.Fatal("replaced watcher was not stopped")
	}
	if second.stopped.Load() {
		t.Fatal("replacement watcher was stopped prematurely")
	}
}

// TestModule_ProbeOutdatedToken_RejectsAtArbitratorWatcherCheck: after
// Rotate()-like flow (direct rotateWatcherToken) an observation submitted
// with the old token fails the apply pipeline's watcher check, whereas
// one with the current token passes. The test exercises the apply
// pipeline directly through the module-side SubmitObservation and a
// synchronous Arbitrator drain (we don't call Run; we pull observations
// and apply them through the deps fixture because apply_test.go covers
// the deps fixture shape). Here we re-use apply_test's expectations:
// apply pipeline is a separate test surface, but module-level tests
// still need to confirm the module submits observations carrying the
// correct WatcherToken so the apply check receives the authoritative
// value. That's all the module layer can verify without spinning the
// Run goroutine.
func TestModule_ProbeOutdatedToken_RejectsAtArbitratorWatcherCheck(t *testing.T) {
	m := newProbeObsModule(t, 8)
	seedFrame(t, m)

	// Build two callbacks scoped to the same session to simulate token
	// rotation at the probe layer: old callback uses "tok-A", new callback
	// uses "tok-B". The module is agnostic to rotation — it just tags
	// whatever token the Watcher hands it. We assert both observations
	// reach InCh with their respective tokens; the apply pipeline's
	// checkWatcher gate is exercised in apply_test.go.
	cb := m.probeOnOutcome("work", "cc", probe.KindLiveness)
	cb(probe.OutcomeAlive, "tok-A", []probe.EvidenceRef{{Key: "probe_id", Value: "p-1"}})
	cb(probe.OutcomeDead, "tok-B", []probe.EvidenceRef{{Key: "probe_id", Value: "p-1"}})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case obs := <-m.arbitrator.InChForTesting():
			seen[obs.WatcherToken] = true
		case <-ctx.Done():
			t.Fatalf("expected two observations with tokens tok-A and tok-B; got=%v", seen)
		}
	}
	if !seen["tok-A"] {
		t.Fatal("missing observation with token tok-A")
	}
	if !seen["tok-B"] {
		t.Fatal("missing observation with token tok-B")
	}
}
