package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/modevents"
)

// The probe is the recovery path for sessions without a live mod stream
// (plan, "Probe gate"): while the representative pane's light comes from a
// live stream, a probe transition writes no frame and emits nothing.

// probeModRig is an orchTestModule (session "work", pane %5) with a frame on
// modSID1, the overlay on and a subscriber on the events bus.
type probeModRig struct {
	*workerRig
	drops []string
}

func newProbeModRig(t *testing.T) *probeModRig {
	t.Helper()
	m, _, fake := orchTestModule(t)
	fake.SetPaneSessionName("%5", "work")
	clock := useModClock(m)
	m.modOverlayOn.Store(true)
	sub := m.core.Events.AddTestSubscriber()
	t.Cleanup(func() { m.core.Events.RemoveTestSubscriber(sub) })
	seedIdentityFrame(t, m, "%5", "cc", 710, "live", 10, modSID1, "/w")
	m.mu.Lock()
	m.currentStatus["work"] = agentpkg.StatusIdle
	m.mu.Unlock()
	return &probeModRig{workerRig: &workerRig{m: m, clock: clock, sub: sub}}
}

// probe applies a probe transition to want through the shared guards.
func (r *probeModRig) probe(want agentpkg.Status) bool {
	applied, _ := applyProbeGuards(r.m, probeGuardArgs{
		Session:    "work",
		AgentType:  "cc",
		Reason:     "probe:activity",
		Mapping:    mappingTo(want),
		StaleCheck: staleAlways,
		OnDrop:     func(reason string) { r.drops = append(r.drops, reason) },
	})
	return applied
}

func frameStatus(t *testing.T, m *Module) agentpkg.Status {
	t.Helper()
	f, err := m.frames.GetByIdentity("%5", 710, "live")
	if err != nil || f == nil {
		t.Fatalf("frame: %v", err)
	}
	return f.Status
}

// TestProbe_SkippedWhileModLive: the mod says running, the frame (hooks)
// says idle, the probe wants running: nothing is written or sent.
func TestProbe_SkippedWhileModLive(t *testing.T) {
	r := newProbeModRig(t)
	feedMod(r.m, modStrm, modStart, modTurnStart)

	if r.probe(agentpkg.StatusRunning) {
		t.Fatal("the probe applied a transition while the mod is live")
	}
	if len(r.drops) != 1 || r.drops[0] != "mod-live" {
		t.Fatalf("drops = %v, want [mod-live]", r.drops)
	}
	if got := frameStatus(t, r.m); got != agentpkg.StatusIdle {
		t.Fatalf("frame status = %q, want idle (no write)", got)
	}
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("the probe emitted: %+v", got)
	}
}

// TestProbe_ResumesAfterStreamGoesStale: 31 s without a mod event the stream
// no longer drives the pane, and the probe works as before.
func TestProbe_ResumesAfterStreamGoesStale(t *testing.T) {
	r := newProbeModRig(t)
	feedMod(r.m, modStrm, modStart, modTurnStart)
	r.clock.Set(modT0.Add(31 * time.Second))

	if !r.probe(agentpkg.StatusRunning) {
		t.Fatalf("the probe was refused after the stream went stale (drops %v)", r.drops)
	}
	if got := frameStatus(t, r.m); got != agentpkg.StatusRunning {
		t.Fatalf("frame status = %q, want running", got)
	}
	wantOneEmit(t, "probe", r.drain(t), "s1", "running", "hook")
}

// TestProbe_ModErrorNotClearedByProbe: the mod reports an error; a probe
// that sees an idle screen must not move the light off it (currentStatus is
// left idle here so the error guard alone cannot be what stops it).
func TestProbe_ModErrorNotClearedByProbe(t *testing.T) {
	r := newProbeModRig(t)
	failed := modEv(modSID1, modevents.TypeTurnComplete, `{"turn_id":"t1","reason":"error"}`)
	failed.Seq = 3
	feedMod(r.m, modStrm, modStart, modTurnStart, failed)
	wantLight(t, "setup", *paneProjection(t, r.m, "%5"), agentpkg.StatusError, "mod")

	for _, want := range []agentpkg.Status{agentpkg.StatusIdle, agentpkg.StatusRunning} {
		if r.probe(want) {
			t.Fatalf("the probe applied %s over a mod error", want)
		}
	}
	if got := frameStatus(t, r.m); got != agentpkg.StatusIdle {
		t.Fatalf("frame status = %q, want idle (untouched)", got)
	}
	wantLight(t, "after", *paneProjection(t, r.m, "%5"), agentpkg.StatusError, "mod")
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("the probe emitted: %+v", got)
	}
}

// idleIntentProvider is a cc provider (every hook answers idle) that
// declares one probe intent which is armed while the pane is idle.
type idleIntentProvider struct{ *fakeAgentProvider }

func (idleIntentProvider) ProbeIntents() []agentpkg.ProbeIntent {
	return []agentpkg.ProbeIntent{{
		Kind:          agentpkg.ProbeIntentKindScreenChange,
		OnEntryStatus: []agentpkg.Status{agentpkg.StatusIdle},
		OnSignal:      func(agentpkg.Signal) agentpkg.Status { return agentpkg.StatusRunning },
	}}
}

// armedByStopHook posts a Stop hook (the frame goes idle) to a pane with an
// idle-armed probe intent, optionally with a live mod stream saying running,
// and returns the intent kinds the dispatcher armed.
func armedByStopHook(t *testing.T, modRunning bool) []agentpkg.ProbeIntentKind {
	t.Helper()
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, racePane, "cc", 200, raceFrameStart, 10, modSID1, "/w")
	r.m.registry.Register(idleIntentProvider{&fakeAgentProvider{
		typeName: "cc",
		derive: func(string, json.RawMessage) agentpkg.DeriveResult {
			return agentpkg.DeriveResult{Valid: true, Status: agentpkg.StatusIdle}
		},
	}})
	r.m.prober = probe.New(r.m.tmux)
	var armed []agentpkg.ProbeIntentKind
	var mu sync.Mutex
	r.m.probeIntentDisp.startDetector = func(ctx context.Context, _ *Module, kind agentpkg.ProbeIntentKind, _ string, _ int, _ chan<- agentpkg.Signal) {
		mu.Lock()
		armed = append(armed, kind)
		mu.Unlock()
		<-ctx.Done()
	}
	t.Cleanup(r.m.probeIntentDisp.stopAll)
	if modRunning {
		feedMod(r.m, modStrm, modStart, modTurnStart)
	}

	r.hook(t, "PdxStop")
	// Detectors start on their own goroutine: wait for one when one is
	// expected, give a wrongly armed one a moment to show up otherwise.
	deadline := time.Now().Add(50 * time.Millisecond)
	if !modRunning {
		deadline = time.Now().Add(2 * time.Second)
	}
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(armed)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	return append([]agentpkg.ProbeIntentKind(nil), armed...)
}

// TestHandler_WatchStatusUsesEffectiveStatus: the status the handler hands
// to the activity-watch manager is the light the user sees. The frame is
// idle (the hook said so) but the mod says running, so the idle-armed probe
// intent must not be armed; without a stream (the control) it is.
func TestHandler_WatchStatusUsesEffectiveStatus(t *testing.T) {
	if got := armedByStopHook(t, false); len(got) != 1 {
		t.Fatalf("control: armed %v, want the idle intent armed once", got)
	}
	if got := armedByStopHook(t, true); len(got) != 0 {
		t.Fatalf("armed %v although the pane shows running", got)
	}
}
