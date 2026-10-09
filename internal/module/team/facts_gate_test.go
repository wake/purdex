// internal/module/team/facts_gate_test.go
package teammod

import (
	"errors"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// X4a-3 (spec §3.1 rule 7 for facts): the facts pump sends a fact only when the lead host announces its kind in
// team.fact_kinds. Held is not settled: without the gate a JSON 400 unsupported_kind reads as a permanent refusal and the
// fact is lost.

// A kind the lead host does not announce is held: nothing is sent, the row stays pending (never dropped), it backs off.
func TestFactGate_UnannouncedKindIsHeldNotDropped(t *testing.T) {
	f, fc := factsFixture(t)
	fc.caps["host-L"] = ipeers.TeamCaps{FactKinds: []string{"moved"}} // announces facts, but not `ended`
	fc.script = doneFor("host-L")
	f.queueEnded("mk-1", "sid-1")
	f.m.factPump.drain("host-L")
	if len(fc.sent()) != 0 {
		t.Fatalf("a fact of an unannounced kind was sent: %+v", fc.sent())
	}
	row := f.factState("fact-mk-1")
	if row.State != factPending || row.Attempts != 1 || row.NextAt <= f.clock.Load() {
		t.Fatalf("fact = %+v, want pending, one attempt, backed off", row)
	}
}

// A host without fact_kinds (no facts route) gets nothing either, and the fact waits for it.
func TestFactGate_HostWithoutFactKindsHoldsEverything(t *testing.T) {
	f, fc := factsFixture(t)
	fc.caps["host-L"] = ipeers.TeamCaps{Kinds: []string{CmdAdopt}} // an older daemon: no fact_kinds
	f.queueEnded("mk-1", "sid-1")
	f.m.factPump.drain("host-L")
	if len(fc.sent()) != 0 || f.factState("fact-mk-1").State != factPending {
		t.Fatalf("sent %+v, fact %+v", fc.sent(), f.factState("fact-mk-1"))
	}
}

// After the lead host is upgraded the next round sends it, with its first attempt count kept (no limit on holding).
func TestFactGate_SentOnTheFirstRoundAfterTheHostAnnouncesIt(t *testing.T) {
	f, fc := factsFixture(t)
	fc.caps["host-L"] = ipeers.TeamCaps{}
	fc.script = doneFor("host-L")
	f.queueEnded("mk-1", "sid-1")
	f.m.factPump.drain("host-L")
	if len(fc.sent()) != 0 {
		t.Fatal("sent before the announcement")
	}
	fc.mu.Lock()
	fc.caps["host-L"] = ipeers.TeamCaps{FactKinds: []string{team.FactEnded}}
	fc.mu.Unlock()
	// its backoff and the capabilities' cache both have to pass
	f.clock.Add((pumpBackoff(1) + capsTTL*time.Millisecond).Milliseconds())
	f.m.factPump.drain("host-L")
	if len(fc.sent()) != 1 || f.factState("fact-mk-1").State != factDone {
		t.Fatalf("sent %+v, fact %+v", fc.sent(), f.factState("fact-mk-1"))
	}
}

// A queue asks the host's capabilities once, not once per fact.
func TestFactGate_CapabilitiesAreCached(t *testing.T) {
	f, fc := factsFixture(t)
	fc.script = doneFor("host-L")
	for i, mk := range []string{"mk-1", "mk-2", "mk-3"} {
		f.queueEnded(mk, "sid-"+string(rune('a'+i)))
	}
	f.m.factPump.drain("host-L")
	if len(fc.sent()) != 3 {
		t.Fatalf("sent %d", len(fc.sent()))
	}
	if n := fc.capsAsked(); n != 1 {
		t.Fatalf("capabilities asked %d times for three facts", n)
	}
}

// Capabilities that cannot be read hold the fact like an unannounced kind (transient), nothing is sent or dropped.
func TestFactGate_UnreadableCapabilitiesHoldTheFact(t *testing.T) {
	f, fc := factsFixture(t)
	fc.capsErr = errors.New("timeout")
	f.queueEnded("mk-1", "sid-1")
	f.m.factPump.drain("host-L")
	if len(fc.sent()) != 0 || f.factState("fact-mk-1").State != factPending {
		t.Fatalf("sent %+v, fact %+v", fc.sent(), f.factState("fact-mk-1"))
	}
}

// The commands pump has no gate (its kinds are checked before they are queued): nothing there changed.
func TestFactGate_CommandsAreNotGated(t *testing.T) {
	f, fc, _ := pumpFixture(t)
	fc.caps = nil // announces nothing at all
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "mk1"))
	f.m.cmdPump.drain("hostM")
	if len(fc.sent()) != 1 {
		t.Fatalf("the commands pump held a command: %+v", fc.sent())
	}
}
