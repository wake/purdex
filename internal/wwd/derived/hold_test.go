package derived

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// WWD-8 8b-3: a hold with `allow` (spec §13.4) keeps the headroom at 0 for every outside reader — the reasons say exception:<name> — but the
// worker may still take a job the hold lists. HeavyRoomForAllowed is the room the worker has when the selective holds are set aside.

func selectiveFacts(in Input, selective ...string) wire.Facts {
	f := factsOf(in, true)
	for i := range f.Exception {
		for _, n := range selective {
			if f.Exception[i].Name == n {
				f.Exception[i].Allow = &wire.HoldAllow{Classes: []wire.JobClass{wire.ClassRelease}, Repos: []string{}}
			}
		}
	}
	return f
}

func TestHeavyRoomForAllowed(t *testing.T) {
	open := Input{Pressure: 1, PressureKnown: true, Slots: wire.Slots{Heavy: 2, Light: 2}}
	with := func(in Input, names ...string) Input {
		for _, n := range names {
			in.Exceptions = append(in.Exceptions, Hold{Name: n})
		}
		return in
	}
	cases := []struct {
		name string
		f    wire.Facts
		want int
	}{
		{"a selective hold alone: the slots that are free", selectiveFacts(with(open, "rel"), "rel"), 2},
		{"a selective hold and one job running", selectiveFacts(with(Input{Pressure: 1, PressureKnown: true, Slots: wire.Slots{Heavy: 2, Light: 2}, Running: wire.Slots{Heavy: 1}}, "rel"), "rel"), 1},
		{"every heavy slot taken", selectiveFacts(with(Input{Pressure: 1, PressureKnown: true, Slots: wire.Slots{Heavy: 1, Light: 1}, Running: wire.Slots{Heavy: 1}}, "rel"), "rel"), 0},
		{"two selective holds", selectiveFacts(with(open, "a", "b"), "a", "b"), 2},
		{"a plain exception holds everything as before", factsOf(with(open, "plain"), true), 0},
		{"a selective hold beside a plain one", selectiveFacts(with(open, "rel", "plain"), "rel"), 0},
		{"no hold at all: nothing to set aside (the headroom is the answer)", factsOf(open, true), 0},
		{"the zone is a reason of its own", selectiveFacts(with(Input{Pressure: 2, PressureKnown: true, Slots: wire.Slots{Heavy: 2, Light: 2}}, "rel"), "rel"), 0},
		{"the owner is a reason of its own", selectiveFacts(with(Input{Pressure: 1, PressureKnown: true, Slots: wire.Slots{Heavy: 2, Light: 2}, OwnerActive: true}, "rel"), "rel"), 0},
	}
	for _, c := range cases {
		if got := HeavyRoomForAllowed(c.f); got != c.want {
			t.Errorf("%s: room %d, want %d (reasons %v)", c.name, got, c.want, Why(c.f).Heavy)
		}
	}
	// the worker being off is the worker's own flag, which the loop checks itself
	off := selectiveFacts(with(open, "rel"), "rel")
	off.Worker.Enabled = false
	if got := HeavyRoomForAllowed(off); got != 2 {
		t.Errorf("worker_off must not count against the room: %d", got)
	}
}

func TestAdmitsJob(t *testing.T) {
	hold := func(classes []wire.JobClass, repos []string) wire.FactExc {
		return wire.FactExc{Name: "h", Allow: &wire.HoldAllow{Classes: classes, Repos: repos}}
	}
	plain := wire.FactExc{Name: "plain"}
	cases := []struct {
		name string
		excs []wire.FactExc
		job  wire.JobClass
		repo string
		want bool
	}{
		{"no exception", nil, wire.ClassDev, "purdex", true},
		{"class listed", []wire.FactExc{hold([]wire.JobClass{wire.ClassRelease}, nil)}, wire.ClassRelease, "other", true},
		{"repo listed", []wire.FactExc{hold(nil, []string{"purdex"})}, wire.ClassDev, "purdex", true},
		{"neither listed", []wire.FactExc{hold([]wire.JobClass{wire.ClassRelease}, []string{"purdex"})}, wire.ClassDev, "other", false},
		{"both lists empty: none", []wire.FactExc{hold([]wire.JobClass{}, []string{})}, wire.ClassRelease, "purdex", false},
		{"both lists left out: none", []wire.FactExc{hold(nil, nil)}, wire.ClassRelease, "purdex", false},
		{"every hold must admit it", []wire.FactExc{hold([]wire.JobClass{wire.ClassRelease}, nil), hold([]wire.JobClass{wire.ClassMerge}, nil)}, wire.ClassRelease, "purdex", false},
		{"two holds that both admit it", []wire.FactExc{hold([]wire.JobClass{wire.ClassRelease}, nil), hold(nil, []string{"purdex"})}, wire.ClassRelease, "purdex", true},
		{"a plain exception says nothing here (the headroom holds for it)", []wire.FactExc{plain}, wire.ClassDev, "purdex", true},
	}
	for _, c := range cases {
		if got := AdmitsJob(c.excs, c.job, c.repo); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// HasHeavyRoom is what the worker's loop and the queue rules ask of a record: the headroom, or the room the selective holds leave.
func TestHasHeavyRoom(t *testing.T) {
	open := Input{Pressure: 1, PressureKnown: true, Slots: wire.Slots{Heavy: 1, Light: 1}}
	if !HasHeavyRoom(factsOf(open, true)) {
		t.Error("a free heavy slot is room")
	}
	full := Input{Pressure: 1, PressureKnown: true, Slots: wire.Slots{Heavy: 1, Light: 1}, Running: wire.Slots{Heavy: 1}}
	if HasHeavyRoom(factsOf(full, true)) {
		t.Error("every heavy slot taken is no room")
	}
	held := open
	held.Exceptions = append(held.Exceptions, Hold{Name: "rel"})
	if HasHeavyRoom(factsOf(held, true)) {
		t.Error("a plain hold leaves no room")
	}
	if !HasHeavyRoom(selectiveFacts(held, "rel")) {
		t.Error("a selective hold leaves the room for the jobs it lists")
	}
}
