package agent

import (
	"context"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// A probe detector is bound to one pane. Its transition lands on that pane's
// own frame, is guarded by that pane's own state, and the session's light is
// then re-aggregated across all panes (U1-2b-1: the representative pane is
// the highest-priority one, which a probe write can itself change).

// paneProbeRig is a two-pane session "work" (code-work): pane A is the
// representative (the larger frame id at equal rank and start), pane B the
// other one. Both start waiting.
type paneProbeRig struct {
	*workerRig
	a, b  store.Frame
	drops []string
}

func newPaneProbeRig(t *testing.T) *paneProbeRig {
	t.Helper()
	r := newWorkerRig(t)
	f1 := seedRankPane(t, r.m, "%5", 801, agentpkg.StatusWaiting, 10, "")
	f2 := seedRankPane(t, r.m, "%7", 802, agentpkg.StatusWaiting, 10, "")
	rig := &paneProbeRig{workerRig: r, a: f1, b: f2}
	if f2.FrameID > f1.FrameID {
		rig.a, rig.b = f2, f1
	}
	if rep := rig.rep(t); rep.PaneID != rig.a.PaneID {
		t.Fatalf("setup: representative is %s, want %s", rep.PaneID, rig.a.PaneID)
	}
	rig.syncSession(t)
	return rig
}

func (r *paneProbeRig) rep(t *testing.T) *SessionProjection {
	t.Helper()
	p, err := r.m.projectionForSession("work")
	if err != nil || p == nil {
		t.Fatalf("projectionForSession: %v %v", p, err)
	}
	return p
}

// syncSession mirrors what a hook would have left in the in-memory view.
func (r *paneProbeRig) syncSession(t *testing.T) {
	t.Helper()
	p := r.rep(t)
	r.m.mu.Lock()
	syncProjectionState(r.m.currentStatus, r.m.subagents, "work", p)
	r.m.mu.Unlock()
}

func (r *paneProbeRig) setFrame(t *testing.T, f store.Frame, s agentpkg.Status) {
	t.Helper()
	if err := r.m.frames.UpdateStatusAndLastSeen(f.FrameID, s, 20); err != nil {
		t.Fatal(err)
	}
	r.syncSession(t)
}

func (r *paneProbeRig) status(t *testing.T, f store.Frame) agentpkg.Status {
	t.Helper()
	got, err := r.m.frames.GetByIdentity(f.PaneID, f.PID, f.ProcessStartTime)
	if err != nil || got == nil {
		t.Fatalf("frame %s: %v", f.PaneID, err)
	}
	return got.Status
}

// probe applies a transition to want for the detector bound to pane.
func (r *paneProbeRig) probe(pane string, want agentpkg.Status) bool {
	applied, _ := applyProbeGuards(r.m, probeGuardArgs{
		Session:    "work",
		PaneID:     pane,
		AgentType:  "codex",
		Reason:     "probe-intent:test",
		Mapping:    mappingTo(want),
		StaleCheck: staleAlways,
		OnDrop:     func(reason string) { r.drops = append(r.drops, reason) },
	})
	return applied
}

func (r *paneProbeRig) sessionStatus() agentpkg.Status {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	return r.m.currentStatus["work"]
}

// TestProbe_WritesArmedPaneNotRepresentative: A (the representative) and B
// are both waiting and the detector is bound to B. B's idle lands on B's
// frame; A stays waiting and the session still shows waiting (A is highest).
func TestProbe_WritesArmedPaneNotRepresentative(t *testing.T) {
	r := newPaneProbeRig(t)

	if !r.probe(r.b.PaneID, agentpkg.StatusIdle) {
		t.Fatalf("the probe was refused (drops %v)", r.drops)
	}
	if got := r.status(t, r.b); got != agentpkg.StatusIdle {
		t.Fatalf("armed pane B = %q, want idle", got)
	}
	if got := r.status(t, r.a); got != agentpkg.StatusWaiting {
		t.Fatalf("representative A = %q, want waiting (not the probe's pane)", got)
	}
	wantOneEmit(t, "probe", r.drain(t), "code-work", "waiting", "hook")
	if got := r.sessionStatus(); got != agentpkg.StatusWaiting {
		t.Fatalf("session status = %q, want waiting", got)
	}
}

// TestProbe_CrashOnDemotedPaneDoesNotErrorTheNewRepresentative: A's detector
// writes A running, which ranks it below the still-waiting B; A's process
// then dies (error). The error lands on A, not on B, and the session shows
// A's error (the highest).
func TestProbe_CrashOnDemotedPaneDoesNotErrorTheNewRepresentative(t *testing.T) {
	r := newPaneProbeRig(t)

	if !r.probe(r.a.PaneID, agentpkg.StatusRunning) {
		t.Fatalf("running was refused (drops %v)", r.drops)
	}
	if rep := r.rep(t); rep.PaneID != r.b.PaneID {
		t.Fatalf("setup: representative is %s, want B %s after A dropped to running", rep.PaneID, r.b.PaneID)
	}
	r.drain(t)

	if !r.probe(r.a.PaneID, agentpkg.StatusError) {
		t.Fatalf("A's crash was refused (drops %v)", r.drops)
	}
	if got := r.status(t, r.b); got != agentpkg.StatusWaiting {
		t.Fatalf("B = %q, want waiting (A's crash must not land on it)", got)
	}
	if got := r.status(t, r.a); got != agentpkg.StatusError {
		t.Fatalf("A = %q, want error", got)
	}
	wantOneEmit(t, "crash", r.drain(t), "code-work", "error", "hook")
	if got := r.sessionStatus(); got != agentpkg.StatusError {
		t.Fatalf("session status = %q, want error", got)
	}
}

// TestProbe_ErrorGuardIsPerPane: B is in error, so the session shows error;
// A's probe transition is not blocked by it. A's own error still blocks A,
// and a transition to the pane's current status is still a no-op.
func TestProbe_ErrorGuardIsPerPane(t *testing.T) {
	r := newPaneProbeRig(t)
	r.setFrame(t, r.b, agentpkg.StatusError)
	if got := r.sessionStatus(); got != agentpkg.StatusError {
		t.Fatalf("setup: session status = %q, want error", got)
	}

	if !r.probe(r.a.PaneID, agentpkg.StatusRunning) {
		t.Fatalf("A's probe was blocked by B's error (drops %v)", r.drops)
	}
	if got := r.status(t, r.a); got != agentpkg.StatusRunning {
		t.Fatalf("A = %q, want running", got)
	}
	if got := r.status(t, r.b); got != agentpkg.StatusError {
		t.Fatalf("B = %q, want error (untouched)", got)
	}
	r.drain(t)

	r.drops = nil
	if r.probe(r.b.PaneID, agentpkg.StatusIdle) {
		t.Fatal("B's own error was overwritten by the probe")
	}
	if r.probe(r.a.PaneID, agentpkg.StatusRunning) {
		t.Fatal("a transition to the pane's current status was applied")
	}
	if len(r.drops) != 2 || r.drops[0] != "error-guard" || r.drops[1] != "transition-gate" {
		t.Fatalf("drops = %v, want [error-guard transition-gate]", r.drops)
	}
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("a dropped probe emitted: %+v", got)
	}
}

// TestProbe_ModGateIsPerPane: A's light comes from a live mod stream, B (the
// representative, waiting by hook) has none. A probe bound to A is dropped
// (its stream is the better observer) although the representative is hook.
func TestProbe_ModGateIsPerPane(t *testing.T) {
	r := newWorkerRig(t)
	a := seedRankPane(t, r.m, "%5", 811, agentpkg.StatusIdle, 10, modSID1)
	seedRankPane(t, r.m, "%7", 812, agentpkg.StatusWaiting, 20, "")
	feedMod(r.m, modStrm, modStart, modTurnStart)
	var drops []string
	applied, _ := applyProbeGuards(r.m, probeGuardArgs{
		Session: "work", PaneID: "%5", AgentType: "codex", Reason: "probe-intent:test",
		Mapping: mappingTo(agentpkg.StatusError), StaleCheck: staleAlways,
		OnDrop: func(reason string) { drops = append(drops, reason) },
	})
	if applied {
		t.Fatal("the probe applied a transition on a pane whose mod stream is live")
	}
	if len(drops) != 1 || drops[0] != "mod-live" {
		t.Fatalf("drops = %v, want [mod-live]", drops)
	}
	got, err := r.m.frames.GetByIdentity(a.PaneID, a.PID, a.ProcessStartTime)
	if err != nil || got == nil || got.Status != agentpkg.StatusIdle {
		t.Fatalf("frame A = %+v %v, want idle (no write)", got, err)
	}
}

// crashIntentProvider is a codex provider whose one probe intent is armed
// while the pane waits and maps its signal to error.
type crashIntentProvider struct{ *fakeAgentProvider }

func (crashIntentProvider) ProbeIntents() []agentpkg.ProbeIntent {
	return []agentpkg.ProbeIntent{{
		Kind:          agentpkg.ProbeIntentKindScreenChange,
		OnEntryStatus: []agentpkg.Status{agentpkg.StatusWaiting},
		OnSignal:      func(agentpkg.Signal) agentpkg.Status { return agentpkg.StatusError },
	}}
}

// TestProbeIntent_DetectorSignalWritesItsArmedPane: the dispatcher binds the
// detector to the representative pane at arm time and hands that pane to the
// guards, so a signal that fires after the ranking flipped still lands on
// the armed pane.
func TestProbeIntent_DetectorSignalWritesItsArmedPane(t *testing.T) {
	r := newPaneProbeRig(t)
	r.m.registry.Register(crashIntentProvider{&fakeAgentProvider{typeName: "codex"}})
	fire := make(chan struct{})
	r.m.probeIntentDisp.startDetector = func(ctx context.Context, _ *Module, kind agentpkg.ProbeIntentKind, paneID string, _ int, out chan<- agentpkg.Signal) {
		select {
		case <-fire:
			out <- agentpkg.Signal{Kind: kind, PaneID: paneID, PaneAlive: true}
		case <-ctx.Done():
		}
	}
	t.Cleanup(r.m.probeIntentDisp.stopAll)

	r.m.probeIntentDisp.applyStatus("work", "codex", agentpkg.StatusWaiting) // arms on A
	r.setFrame(t, r.a, agentpkg.StatusRunning)                               // the ranking flips to B
	if rep := r.rep(t); rep.PaneID != r.b.PaneID {
		t.Fatalf("setup: representative is %s, want B", rep.PaneID)
	}
	close(fire)

	deadline := time.Now().Add(3 * time.Second)
	for r.status(t, r.a) != agentpkg.StatusError && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := r.status(t, r.a); got != agentpkg.StatusError {
		t.Fatalf("armed pane A = %q, want error", got)
	}
	if got := r.status(t, r.b); got != agentpkg.StatusWaiting {
		t.Fatalf("B = %q, want waiting", got)
	}
}
