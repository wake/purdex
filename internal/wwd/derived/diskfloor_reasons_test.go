package derived

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// A work volume under its floor holds heavy and only heavy: headroom.heavy is 0 with disk_low, light is untouched, and the record says so
// (derived.disk_low) so that Why does not take the missing heavy room for a full gate.
func TestDiskLowHoldsHeavyOnly(t *testing.T) {
	in := Input{Pressure: 1, PressureKnown: true, Slots: wire.Slots{Heavy: 2, Light: 3}, DiskLow: true}
	var l ZoneLatch
	d, r := ComputeWithReasons(&l, in)
	if d.Headroom != (wire.Slots{Heavy: 0, Light: 3}) || !d.DiskLow {
		t.Fatalf("derived %+v", d)
	}
	codes(t, r.Heavy, []string{ReasonDiskLow})
	codes(t, r.Light, []string{})
	f := factsOf(in, true)
	if !f.Derived.DiskLow {
		t.Fatal("the record does not carry disk_low")
	}
	w := Why(f)
	codes(t, w.Heavy, []string{ReasonDiskLow})
	codes(t, w.Light, []string{})
}

// The order is spec §3.2: worker_off, the zone, the exceptions, owner_active, disk_low, slots_full.
func TestDiskLowComesAfterTheOwnerAndBeforeTheSlots(t *testing.T) {
	in := Input{Pressure: 2, PressureKnown: true, Slots: wire.Slots{Heavy: 1, Light: 1}, Running: wire.Slots{Heavy: 1},
		OwnerActive: true, DiskLow: true, Exceptions: []Hold{{Name: "dev"}}}
	var l ZoneLatch
	_, r := ComputeWithReasons(&l, in)
	codes(t, r.Heavy, []string{ReasonZoneWarning, ReasonExceptionP + "dev", ReasonOwnerActive, ReasonDiskLow, ReasonSlotsFull})
}

func TestNoDiskLowLeavesTheRecordAsItWas(t *testing.T) {
	in := Input{Pressure: 1, PressureKnown: true, Slots: wire.Slots{Heavy: 2, Light: 3}}
	var l ZoneLatch
	if d := Compute(&l, in); d.DiskLow || d.Headroom != (wire.Slots{Heavy: 2, Light: 3}) {
		t.Fatalf("derived %+v", d)
	}
}

// A hold that lets some jobs through (allow) does not let them past a full disk.
func TestAnAllowHoldDoesNotOpenAFullDisk(t *testing.T) {
	in := Input{Pressure: 1, PressureKnown: true, Slots: wire.Slots{Heavy: 2, Light: 3}, DiskLow: true, Exceptions: []Hold{{Name: "rel"}}}
	f := factsOf(in, true)
	f.Exception[0].Allow = &wire.HoldAllow{Classes: []wire.JobClass{wire.ClassRelease}}
	if got := HeavyRoomForAllowed(f); got != 0 {
		t.Fatalf("room for the allowed jobs on a full disk: %d, want 0", got)
	}
}

// A fast-lane run: heavy (adhoc) is refused with disk_low, light (a declared task) is not.
func TestAdmitLocalRefusesHeavyOnALowDisk(t *testing.T) {
	f := admitFacts(wire.ZoneNormal)
	f.Derived.DiskLow = true
	codes(t, AdmitLocal(f, true, wire.Slots{}), []string{ReasonDiskLow})
	if got := AdmitLocal(f, false, wire.Slots{}); len(got) != 0 {
		t.Fatalf("a declared task on a low disk: %v", got)
	}
	f.Derived.DiskLow = false
	if got := AdmitLocal(f, true, wire.Slots{}); len(got) != 0 {
		t.Fatalf("a disk with room: %v", got)
	}
}
