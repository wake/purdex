// internal/module/agent/probe_intent_dispatcher_screen_change_integration_test.go
//
// W6-6 P3-T1: end-to-end integration tests for the codex ScreenChange
// ProbeIntent path. Each test wires a Module with a real
// EventsBroadcaster, swaps probeIntentDisp.startDetector to invoke
// codex.StartScreenChangeDetector with a per-test fakeScreenWatcher +
// scripted isCodexAlive, and exercises the dispatcher pipeline
// (lifecycle, applyProbeGuards, consumeSignals) end-to-end.
//
// Eight cases (per spec §6.3 P3-T1 / plan §3 P3-T1 a-h):
//
//	a Case1Happy — waiting → ScreenStable → ScreenChanged + alive=true
//	  → emit → running + ScreenChange entry teardown
//	b LongDialogNaturalCover — waiting → connecting ScreenChanged
//	  drops (armed=false) → eventually ScreenStable → much later
//	  ScreenChanged → emit (detector has no time judgment, so the
//	  "9s virtual time" is illustrative; v5 contract relies on
//	  ScreenStable to demarcate Phase A, NOT on a grace gate)
//	c Case2FastWithOutput — waiting → ScreenChanged multiple times
//	  (no ScreenStable) → no emit + status stays waiting; later
//	  applyStatus(Idle) covers via PdxStop hook
//	d WaitingToIdle_TeardownNoEmit — waiting → arm → idle → teardown
//	  without emit (case 3 lifecycle)
//	e CrossProviderSwitchReconcileTeardown — codex waiting → switch
//	  to provider with no ScreenChange intent → reconcile teardown
//	f Case1IdentityFalse — fake isCodexAlive=false throughout → drop
//	g Case1IdentityRace — outer alive=true / inner alive=false → no
//	  emit; emitted stays false
//	h RetryAfterTransientFalse — alive=false drops first → alive
//	  recovers → second ScreenChanged emits (round 4 F1 fix)
package agent

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// setupScreenChangeIntegration wires a dispatcher test module with:
//   - codex provider declaring both ProcessDead + ScreenChange
//   - fakeScreenWatcher driving ScreenChange detector
//   - real EventsBroadcaster + a session subscriber so callers can
//     assert broadcast envelopes when desired
//   - ProcessDead detector configured to never emit (always-alive +
//     50ms poll) so it doesn't race the ScreenChange path
//
// The supplied isCodexAlive closure is invoked by the in-detector
// gate; tests pass a per-test scripted closure (always true / always
// false / first-true-then-false / etc) to exercise different facets.
func setupScreenChangeIntegration(
	t *testing.T,
	isCodexAlive func() bool,
) (*Module, *fakeScreenWatcher, *core.EventSubscriber) {
	t.Helper()
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{
		sessions: []session.SessionInfo{{Code: "code-work", Name: "work"}},
	}

	// Replace the registry so the codex provider declares ONLY
	// ScreenChange. Skipping ProcessDead avoids a goroutine leak
	// (its consumeSignals teardown writes to traceSink, and the test
	// cleanup ordering closes the sink before the polling goroutine
	// drains, causing send-on-closed-channel panic). For W6-6 path
	// coverage ProcessDead is irrelevant — its dispatcher routing is
	// already covered by W6-3 wire/integration tests.
	m.registry = agentpkg.NewRegistry()
	m.registry.Register(&fakeProbeIntentAgentProvider{
		fakeAgentProvider: fakeAgentProvider{typeName: "codex"},
		intents:           []agentpkg.ProbeIntent{screenChangeIntent()},
	})

	if fake, ok := getFakeTmux(t, m); ok {
		fake.SetPanes([]string{"%5"})
	}

	fw := &fakeScreenWatcher{}
	installScreenChangeWiring(t, m, fw, isCodexAlive)

	// Real broadcaster + subscriber so tests CAN assert envelopes.
	fakeTmux, ok := m.tmux.(*tmux.FakeExecutor)
	if !ok {
		t.Fatalf("expected fake tmux executor, got %T", m.tmux)
	}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fakeTmux}
	sub := m.core.Events.AddTestSubscriber()
	t.Cleanup(func() { m.core.Events.RemoveTestSubscriber(sub) })

	// Drain consumeSignals goroutine before traceSink.Close() runs in
	// newTestModule's cleanup. waitFor predicates in tests key off the
	// active intent map, which empties at probe_intent_dispatcher.go
	// stopActiveIntentInLock — but consumeSignals continues for a few
	// more lines (emitStopObservability → traceSink.AppendProbeIntent
	// → for-range exit). Without this drain, traceSink.Close() can
	// race the still-running goroutine and panic with send-on-closed.
	//
	// LIFO cleanup order: this registers AFTER newTestModule's
	// traceSink.Close cleanup but FIRST in execution; so this drain
	// runs before Close.
	t.Cleanup(func() { time.Sleep(50 * time.Millisecond) })

	return m, fw, sub
}

// drainBroadcasts pulls every broadcast envelope until the sub channel
// times out. Used by tests that need to assert NO broadcast within a
// window after performing the action.
func drainBroadcasts(sub *core.EventSubscriber, dur time.Duration) []agentpkg.NormalizedEvent {
	var got []agentpkg.NormalizedEvent
	deadline := time.After(dur)
	for {
		select {
		case msg := <-sub.SendCh():
			var env struct {
				Type    string `json:"type"`
				Session string `json:"session"`
				Value   string `json:"value"`
			}
			if json.Unmarshal(msg, &env) != nil || env.Type != "hook" {
				continue
			}
			var n agentpkg.NormalizedEvent
			if json.Unmarshal([]byte(env.Value), &n) != nil {
				continue
			}
			got = append(got, n)
		case <-deadline:
			return got
		}
	}
}

// waitStatus polls until currentStatus[session] equals want or timeout.
func waitStatus(t *testing.T, m *Module, session string, want agentpkg.Status, dur time.Duration, msg string) {
	t.Helper()
	waitFor(t, dur, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.currentStatus[session] == want
	}, msg)
}

// (a) Case1Happy — canonical happy path.
func TestIntegration_ScreenChange_Case1Happy(t *testing.T) {
	m, fw, sub := setupScreenChangeIntegration(t, func() bool { return true })

	seedWaitingFrame(t, m, "work", "%5", "codex", 4242)
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusWaiting)

	waitFor(t, time.Second, fw.hasCallback, "screen watcher callback registered")

	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	waitStatus(t, m, "work", agentpkg.StatusRunning, 2*time.Second, "currentStatus → Running after emit")

	// ScreenChange entry torn down on transition out of Waiting.
	waitFor(t, 2*time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindScreenChange)
		return !ok
	}, "ScreenChange entry torn down after Running")

	// Broadcast envelope carries Status=running.
	got := drainBroadcasts(sub, 100*time.Millisecond)
	hasRunning := false
	for _, n := range got {
		if n.Status == string(agentpkg.StatusRunning) && n.AgentType == "codex" {
			hasRunning = true
			break
		}
	}
	if !hasRunning {
		t.Errorf("expected at least one broadcast with Status=running, got %d envelopes", len(got))
	}
}

// (b) LongDialogNaturalCover — connecting ScreenChanged drops while
// armed=false; arrival of ScreenStable arms; later ScreenChanged
// emits. Detector has no time judgment so the gap between ScreenStable
// and the emitting ScreenChanged is bounded only by Phase B (any
// future ScreenChanged emits).
func TestIntegration_ScreenChange_LongDialogNaturalCover(t *testing.T) {
	m, fw, _ := setupScreenChangeIntegration(t, func() bool { return true })

	seedWaitingFrame(t, m, "work", "%5", "codex", 4242)
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusWaiting)
	waitFor(t, time.Second, fw.hasCallback, "screen watcher callback registered")

	// Pre-stable burst of ScreenChanged events from dialog rendering.
	for i := 0; i < 5; i++ {
		fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})
	}
	// No emit yet — armed=false drops everything.
	if got := readCurrentStatus(m, "work"); got != agentpkg.StatusWaiting {
		t.Fatalf("currentStatus = %q during armed=false, want Waiting", got)
	}

	// Eventually pane stabilises.
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})

	// Simulate "long dialog" — wait an arbitrary window before the
	// next ScreenChanged. v5 detector has no grace gate so no time
	// judgment is applied; any future ScreenChanged emits.
	time.Sleep(50 * time.Millisecond)
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	waitStatus(t, m, "work", agentpkg.StatusRunning, 2*time.Second, "currentStatus → Running after long-dialog stable+changed")
}

// (c) Case2FastWithOutput — armed=false throughout; multiple
// ScreenChanged events drop; later applyStatus(Idle) (PdxStop hook
// cover) takes the session to Idle WITHOUT a Running intermediate.
// By contract: lights waiting → idle, skipping running phase.
func TestIntegration_ScreenChange_Case2FastWithOutput(t *testing.T) {
	m, fw, _ := setupScreenChangeIntegration(t, func() bool { return true })

	seedWaitingFrame(t, m, "work", "%5", "codex", 4242)
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusWaiting)
	waitFor(t, time.Second, fw.hasCallback, "screen watcher callback registered")

	// Many ScreenChanged events with NO ScreenStable — armed stays false.
	for i := 0; i < 8; i++ {
		fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})
	}

	// Sleep briefly — no emit must arrive.
	time.Sleep(80 * time.Millisecond)
	if got := readCurrentStatus(m, "work"); got != agentpkg.StatusWaiting {
		t.Fatalf("currentStatus = %q during case-2 armed=false, want Waiting", got)
	}

	// PdxStop hook simulated: applyStatus(Idle).
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusIdle)

	// ScreenChange entry torn down (case 3 — Idle ∉ {Waiting}).
	waitFor(t, 2*time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindScreenChange)
		return !ok
	}, "ScreenChange entry torn down on Idle")

	// Status stayed at Idle (no spurious Running flip).
	if got := readCurrentStatus(m, "work"); got != agentpkg.StatusIdle {
		t.Errorf("currentStatus = %q after PdxStop, want Idle (case 2 by-contract)", got)
	}
}

// (d) WaitingToIdle_TeardownNoEmit — arm ScreenChange, then transition
// directly to Idle without ScreenStable. ScreenChange entry must tear
// down (case 3 lifecycle); no Signal emitted.
func TestIntegration_ScreenChange_WaitingToIdle_TeardownNoEmit(t *testing.T) {
	m, fw, _ := setupScreenChangeIntegration(t, func() bool { return true })

	seedWaitingFrame(t, m, "work", "%5", "codex", 4242)
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusWaiting)
	waitFor(t, time.Second, fw.hasCallback, "screen watcher callback registered")

	// Sanity: ScreenChange entry exists.
	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindScreenChange); !ok {
		t.Fatalf("ScreenChange entry not present after applyStatus(Waiting)")
	}

	// Idle transition (PdxStop) — no ScreenChange events fired.
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusIdle)

	waitFor(t, 2*time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindScreenChange)
		return !ok
	}, "ScreenChange entry torn down after Idle without emit")

	// StopWatch must have been called.
	waitFor(t, 2*time.Second, func() bool {
		return fw.stopCallCount() >= 1
	}, "fakeScreenWatcher.StopWatch called on teardown")
}

// (e) CrossProviderSwitchReconcileTeardown — codex waiting →
// applyStatus("cc-noprobes", Waiting) triggers reconcile that drops
// the codex ScreenChange entry.
func TestIntegration_ScreenChange_CrossProviderSwitchReconcileTeardown(t *testing.T) {
	m, fw, _ := setupScreenChangeIntegration(t, func() bool { return true })

	seedWaitingFrame(t, m, "work", "%5", "codex", 4242)
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusWaiting)
	waitFor(t, time.Second, fw.hasCallback, "screen watcher callback registered")

	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindScreenChange); !ok {
		t.Fatalf("codex ScreenChange entry not present after applyStatus(Waiting)")
	}

	// Switch to a cc provider with no ProbeIntents.
	m.registry.Register(&fakeAgentProvider{typeName: "cc-noprobes"})
	m.probeIntentDisp.applyStatus("work", "cc-noprobes", agentpkg.StatusWaiting)

	waitFor(t, 2*time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindScreenChange)
		return !ok
	}, "codex ScreenChange entry torn down after cross-provider switch")

	waitFor(t, 2*time.Second, func() bool {
		return fw.stopCallCount() >= 1
	}, "fakeScreenWatcher.StopWatch called on reconcile")
}

// (f) Case1IdentityFalse — fake isCodexAlive=false throughout; even
// after Phase A → B, ScreenChanged events drop on the outer alive
// check. emitted stays false; no Signal; status stays Waiting.
func TestIntegration_ScreenChange_Case1IdentityFalse(t *testing.T) {
	m, fw, _ := setupScreenChangeIntegration(t, func() bool { return false })

	seedWaitingFrame(t, m, "work", "%5", "codex", 4242)
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusWaiting)
	waitFor(t, time.Second, fw.hasCallback, "screen watcher callback registered")

	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	time.Sleep(80 * time.Millisecond)
	if got := readCurrentStatus(m, "work"); got != agentpkg.StatusWaiting {
		t.Errorf("currentStatus = %q with alive=false, want Waiting", got)
	}

	// ScreenChange entry must remain — no transition triggered.
	if _, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindScreenChange); !ok {
		t.Errorf("ScreenChange entry torn down despite no emit")
	}
}

// (g) Case1IdentityRace — first call (outer check) returns true; second
// call (inner check) returns false. emit is refused inside the mutex;
// emitted stays false; status stays Waiting.
func TestIntegration_ScreenChange_Case1IdentityRace(t *testing.T) {
	var calls atomic.Int32
	aliveFn := func() bool {
		// First invocation true (outer check), all subsequent false
		// (inner check + future ScreenChanged fires that race).
		return calls.Add(1) == 1
	}
	m, fw, _ := setupScreenChangeIntegration(t, aliveFn)

	seedWaitingFrame(t, m, "work", "%5", "codex", 4242)
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusWaiting)
	waitFor(t, time.Second, fw.hasCallback, "screen watcher callback registered")

	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	time.Sleep(80 * time.Millisecond)
	if got := readCurrentStatus(m, "work"); got != agentpkg.StatusWaiting {
		t.Errorf("currentStatus = %q on identity race, want Waiting (in-mutex second check refused emit)", got)
	}
	if got := calls.Load(); got < 2 {
		t.Errorf("isCodexAlive call count = %d, want ≥ 2 (outer + in-mutex second check)", got)
	}
}

// (h) RetryAfterTransientFalse — alive=false initially → first
// ScreenChanged drops; alive recovers true; subsequent ScreenChanged
// emits and status flips to Running. Pins the v5 round 4 F1 fix
// (sync.Once permanent fuse retired).
func TestIntegration_ScreenChange_RetryAfterTransientFalse(t *testing.T) {
	var alive atomic.Bool
	aliveFn := func() bool { return alive.Load() }
	m, fw, _ := setupScreenChangeIntegration(t, aliveFn)

	seedWaitingFrame(t, m, "work", "%5", "codex", 4242)
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusWaiting)
	waitFor(t, time.Second, fw.hasCallback, "screen watcher callback registered")

	// Phase A complete.
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenStable})

	// First ScreenChanged with alive=false → drop (no permanent fuse).
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})
	time.Sleep(80 * time.Millisecond)
	if got := readCurrentStatus(m, "work"); got != agentpkg.StatusWaiting {
		t.Fatalf("currentStatus = %q with alive=false, want Waiting", got)
	}

	// Recover identity; second ScreenChanged should emit.
	alive.Store(true)
	fw.fire(probe.ScreenChangeEvent{Kind: probe.ScreenChanged})

	waitStatus(t, m, "work", agentpkg.StatusRunning, 2*time.Second, "currentStatus → Running after identity recovery + second emit")
}

// readCurrentStatus is a small helper: read currentStatus[session]
// under m.mu. Returns the zero value (empty Status) when the session
// is absent.
func readCurrentStatus(m *Module, session string) agentpkg.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentStatus[session]
}
