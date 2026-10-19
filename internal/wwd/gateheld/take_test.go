package gateheld

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/gate"
)

func heldBy(t *testing.T, dir string) []gate.Holder {
	t.Helper()
	p := &gate.Pool{Dir: dir, Class: gate.ClassHeavy, Size: 8, Now: time.Now}
	return p.Held()
}

// A job takes one of the gate's heavy slots for as long as it runs: the gate sees the slot held, with a record that says whose it is, and
// the gate is full once its slots are all taken. Giving the slot back frees it; giving it back twice is harmless.
func TestTakeHeavyHoldsASlotTheGateCanSee(t *testing.T) {
	cfg, dir := host(t, 2, "")
	r := New(cfg, nil)
	a, st := r.TakeHeavy()
	if st != Taken || a == nil {
		t.Fatalf("first: %v %v", a, st)
	}
	a.Describe([]string{"wwd", "job", "j_1", "go-pkg"}, "")
	h := heldBy(t, dir)
	if len(h) != 1 || h[0].PID != os.Getpid() || len(h[0].Argv) != 4 || h[0].Argv[0] != "wwd" || h[0].Argv[2] != "j_1" || h[0].Tag != "wwd" || h[0].Orphaned {
		t.Fatalf("the gate sees %+v", h)
	}
	if r.HeavyFull() {
		t.Fatal("one of two taken is full")
	}
	b, st := r.TakeHeavy()
	if st != Taken || b == nil {
		t.Fatalf("second: %v %v", b, st)
	}
	if !r.HeavyFull() {
		t.Fatal("both taken is not full: the gate would let a third heavy command in")
	}
	c, st := r.TakeHeavy()
	if st != Full || c != nil {
		t.Fatalf("a third: %v %v, want Full", c, st)
	}
	a.Release()
	a.Release() // again: nothing happens
	if r.HeavyFull() || len(heldBy(t, dir)) != 1 {
		t.Fatalf("after a release: held %+v", heldBy(t, dir))
	}
	if c, st = r.TakeHeavy(); st != Taken || c == nil {
		t.Fatalf("the freed slot is takeable: %v %v", c, st)
	}
	b.Release()
	c.Release()
	if len(heldBy(t, dir)) != 0 {
		t.Fatalf("all given back: %+v", heldBy(t, dir))
	}
}

// A command that is running already (a quarantined job's) takes a slot whatever the gate says: within the count while there is room,
// beyond it when the gate is full (the host is over its size by as many as are held, as the gate's own late holders are). No gate: none.
func TestTakeHeavyLateCountsACommandThatRunsAlready(t *testing.T) {
	cfg, dir := host(t, 1, "")
	r := New(cfg, nil)
	a, st := r.TakeHeavy()
	if st != Taken {
		t.Fatalf("%v", st)
	}
	late, st := r.TakeHeavyLate()
	if st != Taken || late == nil {
		t.Fatalf("a running command's slot is taken even when the gate is full: %v %v", late, st)
	}
	late.Describe([]string{"wwd", "job", "j_q", "(quarantined)"}, "")
	if h := heldBy(t, dir); len(h) != 2 {
		t.Fatalf("both are held: %+v", h)
	}
	if !r.HeavyFull() {
		t.Fatal("full")
	}
	if s, st := r.TakeHeavy(); st != Full || s != nil {
		t.Fatalf("a new job waits: %v %v", s, st)
	}
	late.Release()
	a.Release()
	if h := heldBy(t, dir); len(h) != 0 {
		t.Fatalf("given back: %+v", h)
	}
	paused, _ := host(t, 0, "") // heavy slots 0: a paused class takes no claim, late or over
	if s, st := New(paused, nil).TakeHeavyLate(); s != nil || st != Full {
		t.Fatalf("a paused heavy class has no slot to give: %v %v", s, st)
	}
	none := New(filepath.Join(t.TempDir(), "none.toml"), nil)
	t.Setenv("PDX_GATE_DIR", filepath.Join(t.TempDir(), "absent"))
	if s, st := none.TakeHeavyLate(); s != nil || st != NoGate {
		t.Fatalf("a host without a gate has nothing to hold: %v %v", s, st)
	}
	// a gate that cannot be read is not "no gate": the caller tries again
	cfg2, dir2 := host(t, 1, "")
	if err := os.RemoveAll(dir2); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir2, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, st := New(cfg2, nil).TakeHeavyLate(); s != nil || st != Unreadable {
		t.Fatalf("a broken gate: %v %v", s, st)
	}
}

// The gate's own admission comes first: while it would hold a new heavy command (pressure or page rate over its lines, free disk under
// its floor) the job gets no slot and is not claimed, however many slots are free; running work is not touched. It is asked with the
// gate's settings as they are.
func TestTakeHeavyAsksTheGatesAdmissionFirst(t *testing.T) {
	cfg, dir := host(t, 2, "")
	var l logs
	r := New(cfg, l.logf)
	var asked []gate.Config
	hold := gate.AdmissionState{Held: true, Reason: "page rate 20/s >= 10/s"}
	r.admission = func(c gate.Config) gate.AdmissionState { asked = append(asked, c); return hold }
	for i := 0; i < 3; i++ {
		if s, st := r.TakeHeavy(); st != HeldByHost || s != nil {
			t.Fatalf("held by the host: %v %v", s, st)
		}
	}
	if len(heldBy(t, dir)) != 0 {
		t.Fatalf("nothing was taken: %+v", heldBy(t, dir))
	}
	if len(asked) != 3 || asked[0].Dir != dir || asked[0].Slots.Size(gate.ClassHeavy) != 2 {
		t.Fatalf("asked with %+v", asked)
	}
	if l.n() != 1 {
		t.Fatalf("the hold is logged once per change, not per look: %d lines", l.n())
	}
	hold = gate.AdmissionState{} // the host is calm again
	s, st := r.TakeHeavy()
	if st != Taken || s == nil {
		t.Fatalf("after the hold: %v %v", s, st)
	}
	s.Release()
	hold = gate.AdmissionState{Held: true, Reason: "page rate 20/s >= 10/s"} // the same reason after a calm spell is news again
	if _, st := r.TakeHeavy(); st != HeldByHost || l.n() != 2 {
		t.Fatalf("a hold again is logged again: %v, %d lines", st, l.n())
	}
	// a slot for a command that is running already does not ask: it is not new work
	if s, st := r.TakeHeavyLate(); st != Taken || s == nil {
		t.Fatalf("late while held: %v %v", s, st)
	} else {
		s.Release()
	}
}

// The default admission is the gate's own, with a config that disables it: nothing holds (the tests above put theirs in; this keeps the
// real call honest).
func TestTakeHeavyDefaultAdmissionIsTheGatesOwn(t *testing.T) {
	cfg, _ := host(t, 1, "")
	r := New(cfg, nil)
	if s, st := r.TakeHeavy(); st != Taken || s == nil {
		t.Fatalf("admission off in the config: %v %v", s, st)
	} else {
		s.Release()
	}
}

// A taker does not jump the gate's queue: with a command waiting for the slot that is free in the meantime, the older ticket is served
// first, so the job is not given the slot (it would be one more heavy thing than the gate wanted).
func TestTakeHeavyDoesNotPassAWaitingCommand(t *testing.T) {
	cfg, dir := host(t, 1, "")
	r := New(cfg, nil)
	held := take(t, dir, 1) // the gate's only slot is a command's
	p := &gate.Pool{Dir: dir, Class: gate.ClassHeavy, Size: 1, Now: time.Now}
	tk, err := p.Enqueue() // another command waits behind it
	if err != nil {
		t.Fatal(err)
	}
	defer tk.Drop()
	held[0].Release(nil) // the slot frees, the waiter has not looked yet
	if s, st := r.TakeHeavy(); st != Full || s != nil {
		t.Fatalf("a waiting command comes first: %v %v", s, st)
	}
	if s, _, err := p.TryTake(tk); err != nil || s == nil {
		t.Fatalf("the waiter still gets the slot: %v %v", s, err)
	} else {
		s.Release(nil)
	}
}

// A host without a gate is not held back: no slot, and nothing to give back. One whose gate cannot be read, or whose heavy class is
// paused, gives nothing (the worker claims nothing then), the same as HeavyFull says full.
func TestTakeHeavyWithoutAGateOrWithABrokenOne(t *testing.T) {
	var l logs
	r := New(filepath.Join(t.TempDir(), "none.toml"), l.logf)
	t.Setenv("PDX_GATE_DIR", filepath.Join(t.TempDir(), "absent"))
	if s, st := r.TakeHeavy(); st != NoGate || s != nil {
		t.Fatalf("no gate: %v %v", s, st)
	}
	var nilHeld *Held
	nilHeld.Describe([]string{"x"}, "") // no slot: harmless
	nilHeld.Release()

	cfg, dir := host(t, 2, "")
	r = New(cfg, l.logf)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("x"), 0o644); err != nil { // the gate directory is a file
		t.Fatal(err)
	}
	if s, st := r.TakeHeavy(); st != Unreadable || s != nil {
		t.Fatalf("a broken gate: %v %v", s, st)
	}
	cfg, _ = host(t, 0, "") // heavy slots 0: paused
	r = New(cfg, l.logf)
	if s, st := r.TakeHeavy(); st != Full || s != nil {
		t.Fatalf("a paused heavy class: %v %v", s, st)
	}
}
