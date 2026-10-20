package wwdalerts

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// S3-2: an alert at level `watch` is for the watcher alone (S3-1 wakes it): this daemon neither pushes it nor puts it in the digest. And a
// level this build does not know is told to nobody, so a newer wwd's level can never reach a phone as a push.

func watchAlert(kind wire.AlertKind, level wire.AlertLevel) wire.Alert {
	return wire.Alert{Key: string(kind), Kind: kind, Level: level, Since: t0, Last: t0, Detail: "壓力"}
}

func TestAWatchLevelAlertIsNeitherPushedNorDigested(t *testing.T) {
	e := newEnv(t)
	e.src.set("mlab", watchAlert(wire.AlertPressureWatch, wire.AlertWatch), watchAlert(wire.AlertSwapGrowth, wire.AlertWatch))
	e.pass(t)
	e.now = e.now.Add(time.Minute)
	e.pass(t)
	if e.ntf.count() != 0 {
		t.Fatalf("a watch-level alert was pushed %d times", e.ntf.count())
	}
	if n, _ := e.m.store.digestCount(); n != 0 {
		t.Fatalf("a watch-level alert went into the digest: %d rows", n)
	}
}

// pressure_danger is a push alert (the phone is told too); the watch level beside it changes nothing about that.
func TestADangerPushStillPushesBesideAWatchAlert(t *testing.T) {
	e := newEnv(t)
	e.src.set("mlab", watchAlert(wire.AlertPressureWatch, wire.AlertWatch), watchAlert(wire.AlertPressureDanger, wire.AlertPush))
	e.pass(t)
	if e.ntf.count() != 1 {
		t.Fatalf("pushes = %d, want the one pressure_danger", e.ntf.count())
	}
}

func TestAnUnknownLevelIsNeverPushedOrDigested(t *testing.T) {
	e := newEnv(t)
	e.m.handle("mlab", []wire.Alert{watchAlert(wire.AlertPressureWatch, wire.AlertLevel("page"))})
	if e.ntf.count() != 0 {
		t.Fatal("an unknown level was pushed")
	}
	if n, _ := e.m.store.digestCount(); n != 0 {
		t.Fatalf("an unknown level went into the digest: %d rows", n)
	}
}
