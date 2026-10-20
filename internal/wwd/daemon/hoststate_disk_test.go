package daemon

import (
	"math"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func gb(v float64) *float64 { return &v }

func diskOf(t *testing.T, ws wire.WorkerSettings) float64 {
	t.Helper()
	if ws.DiskMinGB == nil {
		t.Fatalf("settings without a disk floor: %+v", ws)
	}
	return *ws.DiskMinGB
}

// The floor is always there to read, whatever the settings were started from.
func TestTheDiskFloorStartsAtTheDefaultAndIsNeverAbsent(t *testing.T) {
	r := newRig(t)
	if got := diskOf(t, r.st.Worker()); got != wire.DefaultDiskMinGB {
		t.Fatalf("started from a config with none: %v, want %v", got, wire.DefaultDiskMinGB)
	}
}

func TestTheDiskFloorFollowsTheConfigUntilTheFirstPut(t *testing.T) {
	r := newRig(t)
	r.st.opts.Initial.DiskMinGB = gb(25)
	r.st.worker = r.st.sane(r.st.opts.Initial, "config")
	if got := diskOf(t, r.st.Worker()); got != 25 {
		t.Fatalf("from the config: %v", got)
	}
}

// A PUT that does not carry the floor (a client that does not know the field) leaves it as it is; one that carries it sets it,
// persists it and announces it. 0 is a floor of its own (none).
func TestAPutWithoutTheFloorKeepsItAndOneWithItSetsIt(t *testing.T) {
	r := newRig(t)
	base := wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}}

	withFloor := base
	withFloor.DiskMinGB = gb(30)
	if changed, err := r.st.SetWorker(withFloor); err != nil || !changed {
		t.Fatalf("set 30: changed=%v err=%v", changed, err)
	}
	if got := diskOf(t, r.st.Worker()); got != 30 {
		t.Fatalf("floor %v, want 30", got)
	}

	slots := base
	slots.Slots.Heavy = 2 // no floor in the body
	if changed, err := r.st.SetWorker(slots); err != nil || !changed {
		t.Fatalf("slots only: changed=%v err=%v", changed, err)
	}
	if got := diskOf(t, r.st.Worker()); got != 30 {
		t.Fatalf("a PUT without the floor changed it to %v", got)
	}

	off := slots
	off.DiskMinGB = gb(0)
	if changed, err := r.st.SetWorker(off); err != nil || !changed {
		t.Fatalf("set 0: changed=%v err=%v", changed, err)
	}
	if got := diskOf(t, r.st.Worker()); got != 0 {
		t.Fatalf("floor %v, want 0 (none)", got)
	}

	r.reopen(t) // it is in wwd.db
	if got := diskOf(t, r.st.Worker()); got != 0 {
		t.Fatalf("after a restart: %v, want 0", got)
	}
}

func TestTheSameFloorIsNoChange(t *testing.T) {
	r := newRig(t)
	same := wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, DiskMinGB: gb(wire.DefaultDiskMinGB)}
	if changed, err := r.st.SetWorker(same); err != nil || changed {
		t.Fatalf("the settings as they are: changed=%v err=%v", changed, err)
	}
	same.DiskMinGB = gb(12)
	if changed, err := r.st.SetWorker(same); err != nil || !changed {
		t.Fatalf("a new floor: changed=%v err=%v", changed, err)
	}
	if changed, err := r.st.SetWorker(same); err != nil || changed {
		t.Fatalf("the same new floor again: changed=%v err=%v", changed, err)
	}
}

func TestTheDiskFloorIsAnnouncedWithTheWorkerEvent(t *testing.T) {
	r := newRig(t)
	if _, err := r.st.SetWorker(wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, DiskMinGB: gb(40)}); err != nil {
		t.Fatal(err)
	}
	ev := <-r.ch
	we, err := ev.DecodeData()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := we.(wire.WorkerEvent)
	if !ok || got.Worker.DiskMinGB == nil || *got.Worker.DiskMinGB != 40 {
		t.Fatalf("event %+v", we)
	}
}

func TestADiskFloorThatCannotBeOneIsRefused(t *testing.T) {
	r := newRig(t)
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), 2e6} {
		_, err := r.st.SetWorker(wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, DiskMinGB: gb(v)})
		if err == nil {
			t.Errorf("floor %v accepted", v)
		}
	}
	if got := diskOf(t, r.st.Worker()); got != wire.DefaultDiskMinGB {
		t.Fatalf("a refused PUT changed the floor to %v", got)
	}
}
