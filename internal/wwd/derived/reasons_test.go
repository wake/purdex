package derived

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// factsOf is the record the sampler would write for in: what Why reads.
func factsOf(in Input, enabled bool) wire.Facts {
	var l ZoneLatch
	f := wire.Facts{}
	f.Worker = wire.FactsWorker{Enabled: enabled, Slots: in.Slots, Running: in.Running}
	f.Derived = Compute(&l, in)
	f.Exception = []wire.FactExc{}
	for _, h := range in.Exceptions {
		f.Exception = append(f.Exception, wire.FactExc{Name: h.Name})
	}
	if in.OwnerActive {
		f.Owner = &wire.OwnerState{Active: true}
		f.Worker.OwnerRule = "hid<5m" // the reading counts only with a rule in force
	}
	return f
}

func codes(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reasons = %#v, want %#v", got, want)
	}
}

// The codes and their order are spec §3.2: worker_off, the zone, the exceptions, owner_active, slots_full, gate_full.
func TestWhyTable(t *testing.T) {
	open := Input{Pressure: 1, PressureKnown: true, Slots: wire.Slots{Heavy: 2, Light: 3}}
	t.Run("room: no reason", func(t *testing.T) {
		r := Why(factsOf(open, true))
		codes(t, r.Heavy, []string{})
		codes(t, r.Light, []string{})
	})
	t.Run("worker off is first, and the headroom is not touched", func(t *testing.T) {
		f := factsOf(open, false)
		r := Why(f)
		codes(t, r.Heavy, []string{"worker_off"})
		codes(t, r.Light, []string{"worker_off"})
		if f.Derived.Headroom != (wire.Slots{Heavy: 2, Light: 3}) {
			t.Fatalf("headroom = %+v", f.Derived.Headroom)
		}
	})
	t.Run("warning holds heavy only", func(t *testing.T) {
		in := open
		in.Pressure = 2
		r := Why(factsOf(in, true))
		codes(t, r.Heavy, []string{"zone_warning"})
		codes(t, r.Light, []string{})
	})
	t.Run("danger holds both", func(t *testing.T) {
		in := open
		in.Pressure = 4
		r := Why(factsOf(in, true))
		codes(t, r.Heavy, []string{"zone_danger"})
		codes(t, r.Light, []string{"zone_danger"})
	})
	t.Run("unknown zone holds heavy only", func(t *testing.T) {
		in := open
		in.PressureKnown = false
		r := Why(factsOf(in, true))
		codes(t, r.Heavy, []string{"zone_unknown"})
		codes(t, r.Light, []string{})
	})
	t.Run("an exception holds heavy by name; light only when the record shows it did", func(t *testing.T) {
		in := open
		in.Exceptions = []Hold{{Name: "build"}}
		r := Why(factsOf(in, true))
		codes(t, r.Heavy, []string{"exception:build"})
		codes(t, r.Light, []string{})
		in.Exceptions = []Hold{{Name: "build"}, {Name: "stress", Light: true}}
		r = Why(factsOf(in, true))
		codes(t, r.Heavy, []string{"exception:build", "exception:stress"})
		codes(t, r.Light, []string{"exception:build", "exception:stress"}) // the record cannot say which one: all are named
	})
	t.Run("owner", func(t *testing.T) {
		in := open
		in.OwnerActive = true
		r := Why(factsOf(in, true))
		codes(t, r.Heavy, []string{"owner_active"})
		codes(t, r.Light, []string{"owner_active"})
	})
	t.Run("owner reading without a rule", func(t *testing.T) {
		f := factsOf(open, true)
		f.Owner = &wire.OwnerState{Active: true} // shown, not counted: headroom has room, so reasons are empty
		r := Why(f)
		codes(t, r.Heavy, []string{})
		codes(t, r.Light, []string{})
	})
	t.Run("slots", func(t *testing.T) {
		in := open
		in.Running = wire.Slots{Heavy: 2, Light: 1}
		r := Why(factsOf(in, true))
		codes(t, r.Heavy, []string{"slots_full"})
		codes(t, r.Light, []string{})
		in.Slots = wire.Slots{} // no slots configured is full too
		r = Why(factsOf(in, true))
		codes(t, r.Light, []string{"slots_full"})
	})
	t.Run("a heavy side at 0 that nothing explains is unknown, never gate_full", func(t *testing.T) {
		// WWD-9 W3: an older peer's record can state heavy 0 for a gate this build does not know; it must not read as room
		f := factsOf(open, true)
		f.Derived.Headroom.Heavy = 0
		codes(t, Why(f).Heavy, []string{"unknown"})
	})
	t.Run("the order", func(t *testing.T) {
		in := open
		in.Pressure, in.OwnerActive = 2, true
		in.Exceptions = []Hold{{Name: "a"}}
		in.Running = wire.Slots{Heavy: 2}
		r := Why(factsOf(in, false))
		codes(t, r.Heavy, []string{"worker_off", "zone_warning", "exception:a", "owner_active", "slots_full"})
	})
	t.Run("a side at 0 that nothing explains is unknown, never an empty list", func(t *testing.T) {
		f := factsOf(open, true)
		f.Derived.Headroom.Light = 0 // normal zone, no exception, no owner, light slots free: a record that cannot come from Compute
		r := Why(f)
		codes(t, r.Light, []string{"unknown"})
		codes(t, r.Heavy, []string{})
	})
	t.Run("a full slot count is slots_full", func(t *testing.T) {
		in := open
		in.Running = wire.Slots{Heavy: 2}
		codes(t, Why(factsOf(in, true)).Heavy, []string{"slots_full"})
	})
}

// Compute and Why are one judgement: over random inputs, a side has no reason exactly when it has room, and the only thing a record
// never states gate_full.
func TestWhyAgreesWithCompute(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 20000; i++ {
		in := Input{
			Pressure: []int{0, 1, 1, 2, 4}[rng.Intn(5)], PageRate: float64(rng.Intn(90)), RateLimit: []float64{0, PageRateLimit}[rng.Intn(2)],
			Slots:       wire.Slots{Heavy: rng.Intn(4), Light: rng.Intn(5)},
			OwnerActive: rng.Intn(4) == 0, AvailableMB: rng.Intn(8000),
		}
		in.PressureKnown = in.Pressure > 0
		in.Running = wire.Slots{Heavy: rng.Intn(in.Slots.Heavy + 2), Light: rng.Intn(in.Slots.Light + 2)}
		for n := rng.Intn(3); n > 0; n-- {
			in.Exceptions = append(in.Exceptions, Hold{Name: string(rune('a' + n)), Light: rng.Intn(2) == 0})
		}
		f := factsOf(in, true)
		r := Why(f)
		if (f.Derived.Headroom.Heavy > 0) != (len(r.Heavy) == 0) {
			t.Fatalf("%d: heavy headroom %d but reasons %v (input %+v)", i, f.Derived.Headroom.Heavy, r.Heavy, in)
		}
		if (f.Derived.Headroom.Light > 0) != (len(r.Light) == 0) {
			t.Fatalf("%d: light headroom %d but reasons %v (input %+v)", i, f.Derived.Headroom.Light, r.Light, in)
		}
	}
}

// Compute's own reasons are exact, and gate_full is never among them (WWD-9 W3).
func TestComputeNeverStatesGateFull(t *testing.T) {
	in := Input{Pressure: 1, PressureKnown: true, Slots: wire.Slots{Heavy: 2, Light: 2}}
	var l ZoneLatch
	d, r := ComputeWithReasons(&l, in)
	if d.Headroom.Heavy != 2 || d.Headroom.Light != 2 {
		t.Fatalf("headroom = %+v", d.Headroom)
	}
	codes(t, r.Heavy, []string{})
	codes(t, r.Light, []string{})
}
