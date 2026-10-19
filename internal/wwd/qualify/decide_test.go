package qualify

import (
	"errors"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

var now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

var tsc = Task{Repo: "purdex", Name: "tsc", Reference: "air26", Args: []string{}, Needs: []string{"os:darwin", "tool:node24"}, DefHash: "d-tsc"}
var spa = Task{Repo: "purdex", Name: "spa-related", Reference: "air26", Args: []string{"a.test.ts"}, Needs: []string{"tool:node24", "tool:pnpm10"}, DefHash: "d-spa"}

func mserv() Host {
	return Host{Alias: "mserv", WorkerOn: true, Labels: []string{"arch:amd64", "os:linux", "repo:purdex", "tool:node24", "tool:pnpm10"}, Fingerprint: map[string]string{"node": "24.14.0"}}
}

func input(tasks ...Task) Input {
	return Input{Now: now, Host: mserv(), Tasks: tasks, Earned: map[string]string{}, InFlight: map[string]bool{}, Last: map[string]Attempt{}}
}

func labelsOf(ts []Task) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Label())
	}
	return out
}

func TestDecideStartsATaskTheHostLacksAndCanRun(t *testing.T) {
	got := labelsOf(Decide(input(tsc, spa)))
	if len(got) != 2 || got[0] != "task:purdex/tsc" || got[1] != "task:purdex/spa-related" {
		t.Fatalf("%v", got)
	}
}

// The reference host holds the label without a pair.
func TestTheReferenceHostIsNeverAskedToQualify(t *testing.T) {
	in := input(tsc)
	in.Host.Alias = "air26"
	if err := Check(in, tsc); !errors.Is(err, ErrReference) {
		t.Fatalf("%v", err)
	}
	if got := Decide(in); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestDecideNeedsTheWorkerOnTheRepoAndTheTools(t *testing.T) {
	in := input(tsc)
	in.Host.WorkerOn = false
	if err := Check(in, tsc); !errors.Is(err, ErrWorkerOff) {
		t.Fatalf("worker off: %v", err)
	}
	in = input(tsc)
	in.Host.Labels = []string{"os:linux", "tool:node24"} // no repo:purdex
	var me *MissingError
	if err := Check(in, tsc); !errors.As(err, &me) || len(me.Labels) != 1 || me.Labels[0] != "repo:purdex" {
		t.Fatalf("no repo: %v", err)
	}
	in = input(spa)
	in.Host.Labels = []string{"os:linux", "repo:purdex", "tool:node24"} // no tool:pnpm10
	if err := Check(in, spa); !errors.As(err, &me) || len(me.Labels) != 1 || me.Labels[0] != "tool:pnpm10" {
		t.Fatalf("no tool: %v", err)
	}
	// os: and arch: needs are not asked of the candidate: the pair's candidate half runs without them
	in = input(tsc)
	if err := Check(in, tsc); err != nil {
		t.Fatalf("os:darwin is in the task's needs and the host is linux: %v", err)
	}
}

// A task with no qualify or no reference is not asked for.
func TestATaskWithoutQualifyIsNotAsked(t *testing.T) {
	bare := Task{Repo: "purdex", Name: "lint"}
	if err := Check(input(bare), bare); !errors.Is(err, ErrNoQualify) {
		t.Fatalf("%v", err)
	}
}

// A task without the manifest's definition hash is never asked for, by the trigger or by hand: without the hash the check that the task
// is still the one that was run would compare an empty string with itself (fail closed, and 5b-2c must fill it).
func TestATaskWithoutADefinitionHashIsNotAsked(t *testing.T) {
	bare := tsc
	bare.DefHash = ""
	in := input(bare)
	if err := Check(in, bare); !errors.Is(err, ErrNoDefinition) {
		t.Fatalf("%v", err)
	}
	if got := Decide(in); len(got) != 0 {
		t.Fatalf("the trigger asked for it: %v", labelsOf(got))
	}
	if _, err := Manual(in, "purdex/tsc"); !errors.Is(err, ErrNoDefinition) {
		t.Fatalf("by hand: %v", err)
	}
}

// At most one unfinished pair per (host, task): the same task offered twice in one call, or while a pair is in flight, starts one.
func TestAtMostOneUnfinishedPairPerTask(t *testing.T) {
	in := input(tsc, tsc)
	if got := Decide(in); len(got) != 1 {
		t.Fatalf("the same task twice in one call: %v", labelsOf(got))
	}
	in.InFlight[tsc.Label()] = true
	if got := Decide(in); len(got) != 0 {
		t.Fatalf("a pair in flight: %v", labelsOf(got))
	}
	if err := Check(in, tsc); !errors.Is(err, ErrInFlight) {
		t.Fatalf("%v", err)
	}
	// calling the trigger again before anything changed gives the same answer (it is a decision, not a side effect)
	in = input(tsc)
	if a, b := labelsOf(Decide(in)), labelsOf(Decide(in)); len(a) != 1 || len(b) != 1 {
		t.Fatalf("%v %v", a, b)
	}
}

func TestAnEarnedLabelIsNotQualifiedAgainButAQuarantinedOneIs(t *testing.T) {
	in := input(tsc)
	in.Earned[tsc.Label()] = wire.EarnedLabelEarned
	if err := Check(in, tsc); !errors.Is(err, ErrAlreadyHas) {
		t.Fatalf("%v", err)
	}
	in.Earned[tsc.Label()] = wire.EarnedLabelQuarantined
	if got := Decide(in); len(got) != 1 {
		t.Fatalf("a quarantined label is re-qualified: %v", labelsOf(got))
	}
}

// After a mismatch or a void pair the automatic retry waits 24 h: 23:59:59 no, 24:00:00 yes.
func TestTheRetryWaits24HoursToTheSecond(t *testing.T) {
	for _, o := range []Outcome{OutcomeMismatch, OutcomeVoid} {
		in := input(tsc)
		in.Last[tsc.Label()] = Attempt{At: now.Add(-RetryAfter + time.Second), Outcome: o, Fingerprint: mserv().Fingerprint}
		if got := Decide(in); len(got) != 0 {
			t.Fatalf("%s at 23:59:59 started a pair", o)
		}
		in.Last[tsc.Label()] = Attempt{At: now.Add(-RetryAfter), Outcome: o, Fingerprint: mserv().Fingerprint}
		if got := Decide(in); len(got) != 1 {
			t.Fatalf("%s at 24:00:00 did not", o)
		}
	}
}

// A change in the host's fingerprint ends the wait at once; an unknown one on either side does too (nothing shows it is unchanged).
func TestAFingerprintChangeEndsTheWait(t *testing.T) {
	in := input(tsc)
	in.Last[tsc.Label()] = Attempt{At: now.Add(-time.Minute), Outcome: OutcomeMismatch, Fingerprint: map[string]string{"node": "24.13.0"}}
	if got := Decide(in); len(got) != 1 {
		t.Fatalf("a changed fingerprint did not end the wait")
	}
	in.Last[tsc.Label()] = Attempt{At: now.Add(-time.Minute), Outcome: OutcomeMismatch, Fingerprint: mserv().Fingerprint}
	if got := Decide(in); len(got) != 0 {
		t.Fatal("the same fingerprint ended the wait")
	}
	in.Last[tsc.Label()] = Attempt{At: now.Add(-time.Minute), Outcome: OutcomeMismatch}
	if got := Decide(in); len(got) != 1 {
		t.Fatal("an attempt with no fingerprint kept the wait")
	}
}

// pdx wwd qualify does not wait for the retry, but it still obeys the rest.
func TestManualDoesNotWaitButStillChecks(t *testing.T) {
	in := input(tsc)
	in.Last[tsc.Label()] = Attempt{At: now.Add(-time.Second), Outcome: OutcomeMismatch, Fingerprint: mserv().Fingerprint}
	if got := Decide(in); len(got) != 0 {
		t.Fatal("the automatic trigger did not wait")
	}
	got, err := Manual(in, "purdex/tsc")
	if err != nil || got.Label() != tsc.Label() {
		t.Fatalf("manual: %v %v", got, err)
	}
	in.InFlight[tsc.Label()] = true
	if _, err := Manual(in, "purdex/tsc"); !errors.Is(err, ErrInFlight) {
		t.Fatalf("manual with a pair in flight: %v", err)
	}
	if _, err := Manual(input(tsc), "purdex/nope"); err == nil {
		t.Fatal("an unknown task")
	}
}
