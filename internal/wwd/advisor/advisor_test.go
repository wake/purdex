package advisor

import (
	"errors"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// fakeBook records what the evaluator raised and cleared.
type fakeBook struct {
	active    map[string]wire.Alert
	raised    int
	cleared   int
	err       error
	activeErr error
}

func (b *fakeBook) Active() (map[string]bool, error) {
	if b.activeErr != nil {
		return nil, b.activeErr
	}
	out := map[string]bool{}
	for k := range b.active {
		out[k] = true
	}
	return out, nil
}

func newBook() *fakeBook { return &fakeBook{active: map[string]wire.Alert{}} }

func (b *fakeBook) Raise(a wire.Alert) error {
	if b.err != nil {
		return b.err
	}
	b.active[a.Key] = a
	b.raised++
	return nil
}

func (b *fakeBook) Clear(key string) error {
	if b.err != nil {
		return b.err
	}
	if _, ok := b.active[key]; ok {
		b.cleared++
	}
	delete(b.active, key)
	return nil
}

// openEvaluator is an evaluator whose proto 2 gate is open.
func openEvaluator(b Book, in func() (Input, bool), logf func(string, ...any), rules ...Rule) *Evaluator {
	ev := NewEvaluator(b, in, logf, rules...)
	ev.SetGate(func() bool { return true })
	return ev
}

// While the proto 2 gate is shut (a peer older, unreachable, or rolled back) nothing is judged, raised or cleared; when it opens, it is
// judged at once.
func TestEvaluator_ShutGateWritesNothing(t *testing.T) {
	b := newBook()
	b.active["old"] = wire.Alert{Key: "old"}
	r := &stubRule{kind: wire.AlertDiskLow, v: Verdict{Holds: true, Key: "disk_low", Level: wire.AlertPush}}
	open := false
	ev := NewEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, nil, r)
	ev.Pass() // no gate at all: shut
	ev.SetGate(func() bool { return open })
	ev.Pass()
	if b.raised != 0 || b.cleared != 0 || len(b.active) != 1 {
		t.Fatalf("written with the gate shut: raised %d cleared %d %+v", b.raised, b.cleared, b.active)
	}
	open = true
	ev.Pass()
	if _, ok := b.active["disk_low"]; !ok {
		t.Fatal("not raised once the gate opened")
	}
	open = false
	r.v = Verdict{Key: "disk_low"}
	ev.Pass() // rolled back: the rule stopped holding but nothing is cleared while shut
	if _, ok := b.active["disk_low"]; !ok {
		t.Fatal("cleared with the gate shut")
	}
}

// stubRule returns whatever the test sets.
type stubRule struct {
	kind wire.AlertKind
	v    Verdict
	err  error
}

func (r *stubRule) Kind() wire.AlertKind        { return r.kind }
func (r *stubRule) Eval(Input) (Verdict, error) { return r.v, r.err }

func TestEvaluator_RaisesWhileTheRuleHoldsAndClearsWhenItStops(t *testing.T) {
	b := newBook()
	r := &stubRule{kind: wire.AlertDiskLow, v: Verdict{Holds: true, Key: "disk_low", Level: wire.AlertPush, Detail: "d"}}
	ev := openEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, nil, r)
	ev.Pass()
	if a, ok := b.active["disk_low"]; !ok || a.Kind != wire.AlertDiskLow || a.Level != wire.AlertPush || a.Detail != "d" {
		t.Fatalf("active = %+v", b.active)
	}
	r.v = Verdict{Key: "disk_low"}
	ev.Pass()
	if len(b.active) != 0 || b.cleared != 1 {
		t.Fatalf("active = %+v cleared %d", b.active, b.cleared)
	}
}

// A rule that cannot be judged (the volume is not in the facts, the history is unreadable) neither raises nor clears: a quiet host is
// not the same as a host nobody could read.
func TestEvaluator_ARuleThatErrorsLeavesTheAlertAsItWas(t *testing.T) {
	b := newBook()
	r := &stubRule{kind: wire.AlertDiskLow, v: Verdict{Holds: true, Key: "disk_low", Level: wire.AlertPush}}
	var logs []string
	ev := openEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, func(f string, a ...any) { logs = append(logs, f) }, r)
	ev.Pass()
	r.v, r.err = Verdict{Key: "disk_low"}, errors.New("no volume")
	ev.Pass()
	if _, ok := b.active["disk_low"]; !ok || b.cleared != 0 {
		t.Fatalf("an unreadable rule cleared the alert: %+v", b.active)
	}
	ev.Pass()
	ev.Pass()
	if len(logs) != 1 {
		t.Fatalf("the same failure was logged %d times", len(logs))
	}
}

// What is raised is read from the table at every pass: an alert that was active before a restart keeps its clearing margin (12.2 GB is
// still "low" for a disk disk_low raised on), and one somebody else cleared is not raised again by a stale belief.
func TestEvaluator_HysteresisFollowsTheTable(t *testing.T) {
	b := newBook()
	in := inputWith(12.2, nil)
	ev := openEvaluator(b, func() (Input, bool) { return in, true }, nil, DiskLow{})
	ev.Pass()
	if len(b.active) != 0 {
		t.Fatalf("12.2 GB raised afresh: %+v", b.active)
	}
	b.active["disk_low"] = wire.Alert{Key: "disk_low"} // active in the table, as after a restart
	ev.Pass()
	if _, held := b.active["disk_low"]; !held {
		t.Fatal("12.2 GB cleared an alert that is active")
	}
	delete(b.active, "disk_low") // cleared by someone else
	ev.Pass()
	if len(b.active) != 0 {
		t.Fatalf("raised again from a stale belief: %+v", b.active)
	}
}

// When the table cannot be read nothing is judged (no raise, no clear), and the failure is logged once.
func TestEvaluator_AnUnreadableTableJudgesNothing(t *testing.T) {
	b := newBook()
	b.active["disk_low"] = wire.Alert{Key: "disk_low"}
	b.activeErr = errors.New("locked")
	r := &stubRule{kind: wire.AlertDiskLow, v: Verdict{Key: "disk_low"}}
	n := 0
	ev := openEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, func(string, ...any) { n++ }, r)
	ev.Pass()
	ev.Pass()
	if _, held := b.active["disk_low"]; !held || n != 1 {
		t.Fatalf("held=%v logged=%d", held, n)
	}
}

// No facts yet (the first sample has not been taken): nothing is judged.
func TestEvaluator_NoFactsNoVerdict(t *testing.T) {
	b := newBook()
	r := &stubRule{kind: wire.AlertDiskLow, v: Verdict{Holds: true, Key: "disk_low", Level: wire.AlertPush}}
	openEvaluator(b, func() (Input, bool) { return Input{}, false }, nil, r).Pass()
	if len(b.active) != 0 {
		t.Fatalf("active = %+v", b.active)
	}
}

// A book that cannot write is logged and does not stop the other rules.
func TestEvaluator_ABookErrorDoesNotStopTheOtherRules(t *testing.T) {
	b := newBook()
	b.err = errors.New("db locked")
	r1 := &stubRule{kind: wire.AlertDiskLow, v: Verdict{Holds: true, Key: "a", Level: wire.AlertPush}}
	r2 := &stubRule{kind: wire.AlertWorkerCrash, v: Verdict{Holds: true, Key: "b", Level: wire.AlertPush}}
	n := 0
	ev := openEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, func(string, ...any) { n++ }, r1, r2)
	ev.Pass()
	if n != 2 {
		t.Fatalf("logged %d failures, want one per rule", n)
	}
}
