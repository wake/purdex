package wwdalerts

import (
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A watch-level alert wakes the watcher and is neither pushed nor gathered for the digest; a level of a newer wwd is told to nobody and
// does not upset the wake.
func TestAWatchLevelAlertWakesAndIsNotPushed(t *testing.T) {
	e, w, _ := wakeEnv(t)
	e.baseline(t)
	e.src.set("mlab",
		alertOf("pressure_watch", wire.AlertKind("pressure_watch"), wire.AlertWatch, t0, "壓力持續"),
		alertOf("future", wire.AlertKind("future_kind"), wire.AlertLevel("future"), t0, "新"))
	e.pass(t)
	e.settle(t)
	all := strings.Join(w.got(), "\n")
	if !strings.Contains(all, wakeKindName(wire.AlertPressureWatch)+"｜壓力持續") || wakeKindName(wire.AlertPressureWatch) == "pressure_watch" {
		t.Fatalf("the watch-level alert did not wake the watcher: %q", all)
	}
	if e.ntf.count() != 0 {
		t.Fatalf("pushes = %d, want 0", e.ntf.count())
	}
	if n, _ := e.m.store.digestCount(); n != 0 {
		t.Fatalf("digest rows = %d, want 0", n)
	}
}
