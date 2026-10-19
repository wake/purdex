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
	active  map[string]wire.Alert
	raised  int
	cleared int
	err     error
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
	ev := NewEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, nil, r)
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
	ev := NewEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, func(f string, a ...any) { logs = append(logs, f) }, r)
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

// No facts yet (the first sample has not been taken): nothing is judged.
func TestEvaluator_NoFactsNoVerdict(t *testing.T) {
	b := newBook()
	r := &stubRule{kind: wire.AlertDiskLow, v: Verdict{Holds: true, Key: "disk_low", Level: wire.AlertPush}}
	NewEvaluator(b, func() (Input, bool) { return Input{}, false }, nil, r).Pass()
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
	ev := NewEvaluator(b, func() (Input, bool) { return Input{Now: t0}, true }, func(string, ...any) { n++ }, r1, r2)
	ev.Pass()
	if n != 2 {
		t.Fatalf("logged %d failures, want one per rule", n)
	}
}
