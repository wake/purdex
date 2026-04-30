// internal/module/agent/probe_intent_dispatcher_observability_test.go
//
// W6-3 P2-T6: observability surfaces for the ProbeIntent dispatcher —
// dev-mode log lines, TraceStore [probe-intent] step persistence, and
// expvar counters. Tests use the recording / emit-once detector helpers
// from probe_intent_dispatcher_test.go.
package agent

import (
	"strings"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// -----------------------------------------------------------------------------
// (a) dev log
// -----------------------------------------------------------------------------

// TestProbeIntent_DevLog_StartLine pins the [probe-intent] start log line
// emitted from applyIntentLifecycle when a fresh detector arms.
func TestProbeIntent_DevLog_StartLine(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	buf := captureDevLog(t)

	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	<-rec.started

	out := buf.String()
	line := findProbeIntentLogLine(t, out, "[probe-intent] start")
	for _, want := range []string{
		"session=work",
		"agent=codex",
		"kind=process_dead",
		"pane=%5",
		"pid=4242",
		"generation=",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("[probe-intent] start missing %q in: %s", want, line)
		}
	}
}

// TestProbeIntent_DevLog_SignalLine pins the [probe-intent] signal log line
// emitted from consumeSignals after applyProbeGuards returns applied=true.
func TestProbeIntent_DevLog_SignalLine(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	buf := captureDevLog(t)

	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}
	installEmitOnceDetector(t, m, agentpkg.Signal{
		Kind:      agentpkg.ProbeIntentKindProcessDead,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 4242,
	})
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)

	waitFor(t, time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return !ok
	}, "active entry torn down after applied=true")

	out := buf.String()
	line := findProbeIntentLogLine(t, out, "[probe-intent] signal")
	for _, want := range []string{
		"session=work",
		"kind=process_dead",
		"PaneAlive=true",
		"newStatus=error",
		"applied=true",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("[probe-intent] signal missing %q in: %s", want, line)
		}
	}
}

// TestProbeIntent_DevLog_DropReason_StaleCallback verifies the drop log
// line shape when applyProbeGuards rejects a signal at the stale-callback
// guard (active map empty / generation mismatch).
//
// Direct-call pattern: we exercise applyProbeGuards with StaleCheck → false
// while wired with the dispatcher's session-aware OnDrop callback. This
// pins the log-line + counter contract without needing a second detector
// emit (which would conflict with the spec §5.4 case-3 ctx-cancel teardown
// in consumeSignals).
func TestProbeIntent_DevLog_DropReason_StaleCallback(t *testing.T) {
	t.Setenv("PDX_DEV_MODE", "1")
	buf := captureDevLog(t)

	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}

	applyProbeGuards(m, probeGuardArgs{
		Session:   "work",
		AgentType: "codex",
		Reason:    "probe-intent:process_dead",
		Signal: agentpkg.Signal{
			Kind:      agentpkg.ProbeIntentKindProcessDead,
			PaneAlive: true,
			PaneID:    "%5",
			SenderPID: 4242,
		},
		Mapping:    func(agentpkg.Signal) agentpkg.Status { return agentpkg.StatusError },
		StaleCheck: func(*Module) bool { return false },
		OnDrop:     probeIntentOnDropForSession("work", agentpkg.ProbeIntentKindProcessDead),
	})

	out := buf.String()
	line := findProbeIntentLogLine(t, out, "[probe-intent] drop")
	for _, want := range []string{
		"session=work",
		"kind=process_dead",
		"reason=stale-callback",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("[probe-intent] drop missing %q in: %s", want, line)
		}
	}
}

// findProbeIntentLogLine is a helper that returns the first log line
// containing the given pattern, or fails the test if not found.
func findProbeIntentLogLine(t *testing.T, out, pattern string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, pattern) {
			return line
		}
	}
	t.Fatalf("missing log line %q in:\n%s", pattern, out)
	return ""
}

// -----------------------------------------------------------------------------
// (b) TraceStore step
// -----------------------------------------------------------------------------

// TestProbeIntent_TraceStore_StartStep verifies that arming a detector
// records a [probe-intent] step into the trace sink when traces are wired.
func TestProbeIntent_TraceStore_StartStep(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	if m.traceSink == nil {
		t.Skip("traceSink not wired in this fixture")
	}

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	<-rec.started

	// Flush before reading.
	m.traceSink.FlushForTest()

	page, err := m.traces.ListChains(store.TraceListFilter{})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	var found bool
	for _, ch := range page.Chains {
		if ch.LatestStepKind != TraceStepProbeIntent {
			continue
		}
		rec, err := m.traces.GetChainRecord(ch.ChainID)
		if err != nil {
			t.Fatalf("GetChainRecord: %v", err)
		}
		if rec == nil || len(rec.Steps) == 0 {
			t.Fatalf("chain %s has no steps", ch.ChainID)
		}
		step := rec.Steps[0]
		if step.Kind != TraceStepProbeIntent {
			t.Errorf("step.Kind = %q, want %q", step.Kind, TraceStepProbeIntent)
			continue
		}
		if step.Decision != "start" {
			continue
		}
		found = true
		if step.TmuxSession != "work" {
			t.Errorf("step.TmuxSession = %q, want work", step.TmuxSession)
		}
		if step.AgentType != "codex" {
			t.Errorf("step.AgentType = %q, want codex", step.AgentType)
		}
		if step.PaneID != "%5" {
			t.Errorf("step.PaneID = %q, want %%5", step.PaneID)
		}
	}
	if !found {
		t.Fatalf("no chain found with [probe-intent] start step")
	}
}

// TestProbeIntent_TraceStore_SignalStep verifies that an applied signal
// records a [probe-intent] step with decision=signal.
func TestProbeIntent_TraceStore_SignalStep(t *testing.T) {
	m := newDispatcherTestModule(t)
	m.sessions = &fakeSessionProvider{}
	installEmitOnceDetector(t, m, agentpkg.Signal{
		Kind:      agentpkg.ProbeIntentKindProcessDead,
		PaneAlive: true,
		PaneID:    "%5",
		SenderPID: 4242,
	})
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	if m.traceSink == nil {
		t.Skip("traceSink not wired in this fixture")
	}

	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	waitFor(t, time.Second, func() bool {
		_, ok := readActiveIntent(m, "work", agentpkg.ProbeIntentKindProcessDead)
		return !ok
	}, "active entry torn down")

	m.traceSink.FlushForTest()

	page, err := m.traces.ListChains(store.TraceListFilter{})
	if err != nil {
		t.Fatalf("ListChains: %v", err)
	}
	var foundSignal bool
	for _, ch := range page.Chains {
		rec, err := m.traces.GetChainRecord(ch.ChainID)
		if err != nil {
			t.Fatalf("GetChainRecord: %v", err)
		}
		if rec == nil {
			continue
		}
		for _, st := range rec.Steps {
			if st.Kind == TraceStepProbeIntent && st.Decision == "signal" {
				foundSignal = true
				if st.TmuxSession != "work" {
					t.Errorf("signal step session = %q, want work", st.TmuxSession)
				}
			}
		}
	}
	if !foundSignal {
		t.Fatalf("no [probe-intent] step found with decision=signal")
	}
}

// -----------------------------------------------------------------------------
// (c) expvar metrics
// -----------------------------------------------------------------------------

// probeIntentMetricSnapshot reads the eight ProbeIntent counters for delta
// assertions. expvar globals persist across tests — only deltas are stable.
type probeIntentMetricSnapshot struct {
	started               int64
	stopped               int64
	signalEmitted         int64
	applied               int64
	droppedStale          int64
	droppedGrace          int64
	droppedErrorGuard     int64
	droppedTransitionGate int64
	unsupportedKind       int64
}

func snapshotProbeIntentMetrics() probeIntentMetricSnapshot {
	return probeIntentMetricSnapshot{
		started:               metricInt("purdex_probe_intent_started_total"),
		stopped:               metricInt("purdex_probe_intent_stopped_total"),
		signalEmitted:         metricInt("purdex_probe_intent_signal_emitted_total"),
		applied:               metricInt("purdex_probe_intent_applied_total"),
		droppedStale:          metricInt("purdex_probe_intent_dropped_stale_total"),
		droppedGrace:          metricInt("purdex_probe_intent_dropped_grace_total"),
		droppedErrorGuard:     metricInt("purdex_probe_intent_dropped_error_guard_total"),
		droppedTransitionGate: metricInt("purdex_probe_intent_dropped_transition_gate_total"),
		unsupportedKind:       metricInt("purdex_probe_intent_unsupported_kind_total"),
	}
}

// TestProbeIntent_Expvar_StartedCounterIncrement verifies arming +1's the
// MetricProbeIntentStarted counter exactly once.
func TestProbeIntent_Expvar_StartedCounterIncrement(t *testing.T) {
	m := newDispatcherTestModule(t)
	rec := installRecordingDetector(t, m)
	seedRunningFrame(t, m, "work", "%5", "codex", 4242)

	before := snapshotProbeIntentMetrics()
	m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusRunning)
	<-rec.started
	after := snapshotProbeIntentMetrics()

	if got := after.started - before.started; got != 1 {
		t.Errorf("MetricProbeIntentStarted delta = %d, want 1", got)
	}
}

// TestProbeIntent_Expvar_DroppedCounterByReason exercises the four drop
// reason counters by routing each through applyProbeGuards.
func TestProbeIntent_Expvar_DroppedCounterByReason(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		m := newDispatcherTestModule(t)
		m.sessions = &fakeSessionProvider{}
		// active map empty → StaleCheck returns false on first lock.
		before := snapshotProbeIntentMetrics()
		applied, _ := applyProbeGuards(m, probeGuardArgs{
			Session:   "work",
			AgentType: "codex",
			Reason:    "probe-intent:process_dead",
			Signal:    agentpkg.Signal{Kind: agentpkg.ProbeIntentKindProcessDead},
			Mapping:   func(agentpkg.Signal) agentpkg.Status { return agentpkg.StatusError },
			StaleCheck: func(*Module) bool { return false },
			OnDrop:    nil, // dispatcher-only field; pass nil here, set via reason argument below
		})
		// We need a way to actually count "stale" → use the dispatcher route.
		_ = applied
		// Direct route: emit a signal via consumeSignals with a closure
		// that returns false. Wrap the dispatcher to confirm counter delta.
		dispatcherDropStale(t, m)
		after := snapshotProbeIntentMetrics()
		if got := after.droppedStale - before.droppedStale; got < 1 {
			t.Errorf("droppedStale delta = %d, want >= 1", got)
		}
	})

	t.Run("grace", func(t *testing.T) {
		m := newDispatcherTestModule(t)
		m.sessions = &fakeSessionProvider{}
		seedRunningFrame(t, m, "work", "%5", "codex", 4242)

		// Arm so StaleCheck passes.
		m.mu.Lock()
		m.activeProbeIntents["work"] = map[agentpkg.ProbeIntentKind]activeIntent{
			agentpkg.ProbeIntentKindProcessDead: {
				agentType:  "codex",
				paneID:     "%5",
				senderPID:  4242,
				generation: 1,
			},
		}
		m.probeIntentGen = 1
		m.mu.Unlock()
		// Trigger graceWindow: recordHookAt now → next probe within window
		// is suppressed.
		m.probeOrch.recordHookAt("work")

		before := snapshotProbeIntentMetrics()
		applyProbeGuards(m, probeGuardArgs{
			Session:    "work",
			AgentType:  "codex",
			Reason:     "probe-intent:process_dead",
			Signal:     agentpkg.Signal{Kind: agentpkg.ProbeIntentKindProcessDead},
			Mapping:    func(agentpkg.Signal) agentpkg.Status { return agentpkg.StatusError },
			StaleCheck: makeProbeIntentStaleCheck("work", agentpkg.ProbeIntentKindProcessDead, "codex", 1),
			OnDrop:     probeIntentOnDrop,
		})
		after := snapshotProbeIntentMetrics()
		if got := after.droppedGrace - before.droppedGrace; got != 1 {
			t.Errorf("droppedGrace delta = %d, want 1", got)
		}
	})

	t.Run("error_guard", func(t *testing.T) {
		m := newDispatcherTestModule(t)
		m.sessions = &fakeSessionProvider{}
		seedRunningFrame(t, m, "work", "%5", "codex", 4242)

		m.mu.Lock()
		m.activeProbeIntents["work"] = map[agentpkg.ProbeIntentKind]activeIntent{
			agentpkg.ProbeIntentKindProcessDead: {
				agentType:  "codex",
				paneID:     "%5",
				senderPID:  4242,
				generation: 1,
			},
		}
		m.probeIntentGen = 1
		m.currentStatus["work"] = agentpkg.StatusError
		m.mu.Unlock()

		before := snapshotProbeIntentMetrics()
		applyProbeGuards(m, probeGuardArgs{
			Session:    "work",
			AgentType:  "codex",
			Reason:     "probe-intent:process_dead",
			Signal:     agentpkg.Signal{Kind: agentpkg.ProbeIntentKindProcessDead},
			Mapping:    func(agentpkg.Signal) agentpkg.Status { return agentpkg.StatusError },
			StaleCheck: makeProbeIntentStaleCheck("work", agentpkg.ProbeIntentKindProcessDead, "codex", 1),
			OnDrop:     probeIntentOnDrop,
		})
		after := snapshotProbeIntentMetrics()
		if got := after.droppedErrorGuard - before.droppedErrorGuard; got != 1 {
			t.Errorf("droppedErrorGuard delta = %d, want 1", got)
		}
	})

	t.Run("transition_gate", func(t *testing.T) {
		m := newDispatcherTestModule(t)
		m.sessions = &fakeSessionProvider{}
		seedRunningFrame(t, m, "work", "%5", "codex", 4242)

		m.mu.Lock()
		m.activeProbeIntents["work"] = map[agentpkg.ProbeIntentKind]activeIntent{
			agentpkg.ProbeIntentKindProcessDead: {
				agentType:  "codex",
				paneID:     "%5",
				senderPID:  4242,
				generation: 1,
			},
		}
		m.probeIntentGen = 1
		// currentStatus already Running; mapping returns Running → gate trips.
		m.currentStatus["work"] = agentpkg.StatusRunning
		m.mu.Unlock()

		before := snapshotProbeIntentMetrics()
		applyProbeGuards(m, probeGuardArgs{
			Session:    "work",
			AgentType:  "codex",
			Reason:     "probe-intent:process_dead",
			Signal:     agentpkg.Signal{Kind: agentpkg.ProbeIntentKindProcessDead},
			Mapping:    func(agentpkg.Signal) agentpkg.Status { return agentpkg.StatusRunning },
			StaleCheck: makeProbeIntentStaleCheck("work", agentpkg.ProbeIntentKindProcessDead, "codex", 1),
			OnDrop:     probeIntentOnDrop,
		})
		after := snapshotProbeIntentMetrics()
		if got := after.droppedTransitionGate - before.droppedTransitionGate; got != 1 {
			t.Errorf("droppedTransitionGate delta = %d, want 1", got)
		}
	})
}

// dispatcherDropStale exercises the stale-callback drop counter by
// invoking applyProbeGuards via the dispatcher's consumeSignals path.
func dispatcherDropStale(t *testing.T, m *Module) {
	t.Helper()
	// Direct path: applyProbeGuards with a StaleCheck that returns false.
	// The dispatcher uses probeIntentOnDrop callback when wired through
	// consumeSignals; we mirror that here.
	applyProbeGuards(m, probeGuardArgs{
		Session:    "work",
		AgentType:  "codex",
		Reason:     "probe-intent:process_dead",
		Signal:     agentpkg.Signal{Kind: agentpkg.ProbeIntentKindProcessDead},
		Mapping:    func(agentpkg.Signal) agentpkg.Status { return agentpkg.StatusError },
		StaleCheck: func(*Module) bool { return false },
		OnDrop:     probeIntentOnDrop,
	})
}
