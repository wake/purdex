package wwdalerts

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Every kind this build knows has a name in the wake note (S3-3a): the three watcher rules of S3-2 and the four queue rules of S3-3 were
// missing or are new, and a kind without one is shown by its wire spelling, which the person reading the note cannot be expected to know.
func TestEveryKnownKindHasAWakeName(t *testing.T) {
	for _, k := range []wire.AlertKind{
		wire.AlertDiskLow, wire.AlertDiskFalling, wire.AlertPressureHeld, wire.AlertMservService, wire.AlertWorkerCrash, wire.AlertSilence,
		wire.AlertThrash, wire.AlertResidentGrowth, wire.AlertOrphans,
		wire.AlertPressureWatch, wire.AlertPressureDanger, wire.AlertSwapGrowth,
		wire.AlertQueueStalled, wire.AlertJobRequeued, wire.AlertQueueBacklog, wire.AlertQueueUnclaimable,
	} {
		if !k.Valid() {
			t.Errorf("%q is not a kind", k)
		}
		if n := wakeKindName(k); n == string(k) {
			t.Errorf("%q has no name in wakeKindNames", k)
		}
	}
	for k, want := range map[wire.AlertKind]string{
		wire.AlertQueueStalled: "佇列停滯", wire.AlertJobRequeued: "job 反覆被放回", wire.AlertQueueBacklog: "佇列積壓", wire.AlertQueueUnclaimable: "沒有主機接得了",
	} {
		if got := wakeKindName(k); got != want {
			t.Errorf("%s is called %q, want %q", k, got, want)
		}
	}
}
