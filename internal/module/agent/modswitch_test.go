package agent

import (
	"context"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/modevents"
	modeventsmod "github.com/wake/purdex/internal/module/modevents"
)

// The overlay switch (plan, "The overlay switch"): on exactly while the
// re-emit worker runs. Without the worker a mod change after a hook emit is
// never re-sent: the Stop hook beats the mod's 150 ms-batched turn.complete
// and the light would stay running.

func modCoreWithRegistry(t *testing.T) (*core.Core, *modevents.Registry) {
	t.Helper()
	reg := modevents.NewRegistry(time.Now)
	c := &core.Core{Registry: core.NewServiceRegistry()}
	c.Registry.Register(modeventsmod.ServiceName, reg)
	return c, reg
}

func applyMod(t *testing.T, reg *modevents.Registry, evs ...modevents.Event) {
	t.Helper()
	for i, ev := range evs {
		ev.Seq = int64(i + 1)
		ev.At = ev.Seq * 1000
		if _, err := reg.Apply(modevents.Batch{V: 1, Stream: modStrm, Agent: "cc", Events: []modevents.Event{ev}}); err != nil {
			t.Fatal(err)
		}
	}
}

// TestModOverlay_OnWhileWorkerRuns: the overlay is off before Start, on once
// Start has the worker in its loop, and off again with the worker gone after
// Stop (which is safe to call twice).
func TestModOverlay_OnWhileWorkerRuns(t *testing.T) {
	m := newTestModule(t) // not overlayModule: the switch is what is under test
	useModClock(m)
	c, reg := modCoreWithRegistry(t)
	m.initModLights(c)
	if m.modOverlayOn.Load() {
		t.Fatal("the overlay is on before Start")
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	if !m.modOverlayOn.Load() {
		t.Fatal("the overlay is off while the worker runs")
	}
	done := m.modWorkerDone
	if done == nil {
		t.Fatal("Start did not start the worker")
	}
	select {
	case <-done:
		t.Fatal("the worker exited right after Start")
	default:
	}

	// The running worker consumes what the subscriber marks dirty.
	seedIdentityFrame(t, m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	applyMod(t, reg, modStart, modTurnStart)
	wantLight(t, "overlay on", *paneProjection(t, m, "%5"), agentpkg.StatusRunning, "mod")
	deadline := time.Now().Add(2 * time.Second)
	for len(modDirtySIDs(m)) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the worker did not consume the dirty sid")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if m.modOverlayOn.Load() {
		t.Fatal("the overlay is still on after Stop")
	}
	select {
	case <-done:
	default:
		t.Fatal("Stop returned before the worker ended")
	}
	wantLight(t, "overlay off after Stop", *paneProjection(t, m, "%5"), agentpkg.StatusIdle, "hook")
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// TestModOverlay_OffWithoutRegistry: with no mod event registry there is
// nothing to overlay and no worker to start.
func TestModOverlay_OffWithoutRegistry(t *testing.T) {
	m := newTestModule(t)
	m.initModLights(&core.Core{Registry: core.NewServiceRegistry()})
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	if m.modOverlayOn.Load() || m.modWorkerDone != nil {
		t.Fatalf("overlay %v, worker %v; want both off without a registry", m.modOverlayOn.Load(), m.modWorkerDone != nil)
	}
}

// TestModOverlay_OnlyAfterWorkerRuns: Start holds the overlay off until the
// worker goroutine has reported ready. The seam holds the worker back; the
// overlay must still be off and Start still waiting, and both move on when
// the worker is released.
func TestModOverlay_OnlyAfterWorkerRuns(t *testing.T) {
	m := newTestModule(t)
	c, _ := modCoreWithRegistry(t)
	m.initModLights(c)
	entered := make(chan struct{})
	release := make(chan struct{})
	m.modWorkerStartHook = func() { close(entered); <-release }

	started := make(chan error, 1)
	go func() { started <- m.Start(context.Background()) }()
	released := false
	releaseOnce := func() {
		if !released {
			released = true
			close(release)
		}
	}
	t.Cleanup(func() {
		releaseOnce()
		select { // Start's result may already have been read below
		case <-started:
		case <-time.After(50 * time.Millisecond):
		}
		_ = m.Stop(context.Background())
	})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker never started")
	}
	if m.modOverlayOn.Load() {
		t.Fatal("the overlay is on before the worker is running")
	}
	select {
	case err := <-started:
		t.Fatalf("Start returned (%v) before the worker was running", err)
	default:
	}
	releaseOnce()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return once the worker ran")
	}
	if !m.modOverlayOn.Load() {
		t.Fatal("the overlay is off although the worker runs")
	}
}
