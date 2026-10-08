package agent

import (
	"testing"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// TestProbe_ModTakesOverAfterTheGate: the mod goes live between the probe's
// gate and its frame write. The write cannot be undone, but the frame it
// would broadcast is built from a projection the mod now decides, so nothing
// is sent and no baseline recorded; the in-memory state follows what the
// user sees.
func TestProbe_ModTakesOverAfterTheGate(t *testing.T) {
	r := newProbeModRig(t)
	orig := interruptBeforeFinalLockFn
	interruptBeforeFinalLockFn = func(string) { feedMod(r.m, modStrm, modStart, modTurnStart) }
	t.Cleanup(func() { interruptBeforeFinalLockFn = orig })

	if r.probe(agentpkg.StatusWaiting) {
		t.Fatal("the probe reported a transition although the mod took over")
	}
	if len(r.drops) != 1 || r.drops[0] != "mod-live-late" {
		t.Fatalf("drops = %v, want [mod-live-late]", r.drops)
	}
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("the probe emitted: %+v", got)
	}
	if d, ok := lastEmittedDigest(r.m, "work"); ok {
		t.Fatalf("a baseline was recorded: %+v", d)
	}
	r.m.mu.Lock()
	got := r.m.currentStatus["work"]
	r.m.mu.Unlock()
	if got != agentpkg.StatusRunning {
		t.Fatalf("currentStatus = %q, want the effective running", got)
	}
}
