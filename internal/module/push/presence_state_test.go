package push

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
)

// WK-3b (watcher wake settings spec 6.5): the state is three-valued. known is whether any unexpired v2 report says anything at all, so that
// "nobody is reporting" is not read as "the person is away". (v2, put and newClockPresence are the helpers of presence_v2_test.go.)

func TestPresenceState_AtAwayAndNotKnown(t *testing.T) {
	p, c := newClockPresence()
	if at, known := p.PresenceState(); at || known {
		t.Fatalf("nothing reported: at %v known %v, want false false", at, known)
	}
	p.Put(v2("mac-1", 1000, 300000))
	if at, known := p.PresenceState(); !at || !known {
		t.Fatalf("a Mac at its machine: at %v known %v", at, known)
	}
	p.Put(v2("mac-1", 400000, 300000))
	if at, known := p.PresenceState(); at || !known {
		t.Fatalf("a Mac that says away: at %v known %v, want false true", at, known)
	}
	// the reports expire (ttl 60 s): nobody reporting again is not known
	c.advance(61 * time.Second)
	if at, known := p.PresenceState(); at || known {
		t.Fatalf("the report expired: at %v known %v, want false false", at, known)
	}
}

// A legacy (v1) report says nothing about the machine: it is not a reading of presence.
func TestPresenceState_AV1ReportIsNotAReading(t *testing.T) {
	p, _ := newClockPresence()
	p.Put(put("mac-1", true, 60000))
	if at, known := p.PresenceState(); at || known {
		t.Fatalf("a v1 report: at %v known %v, want false false", at, known)
	}
}

// One Mac away and another at its machine: the person is at a computer (AtComputer's rule), and it is known.
func TestPresenceState_AgreesWithAtComputerWhenKnown(t *testing.T) {
	p, _ := newClockPresence()
	p.Put(v2("mac-1", 400000, 300000))
	p.Put(v2("mac-2", 1000, 300000))
	at, known := p.PresenceState()
	if !known || at != p.AtComputer() || !at {
		t.Fatalf("at %v known %v AtComputer %v", at, known, p.AtComputer())
	}
}

// The value the module publishes under AtComputerKey is the one that has the state (the load watcher asks it with a type assertion).
func TestPresence_PublishesTheStateFaceUnderAtComputerKey(t *testing.T) {
	var v any = NewPresence(nil)
	if _, ok := v.(push.AtComputerReader); !ok {
		t.Fatal("not an AtComputerReader")
	}
	if _, ok := v.(push.PresenceStateReader); !ok {
		t.Fatal("not a PresenceStateReader")
	}
}
