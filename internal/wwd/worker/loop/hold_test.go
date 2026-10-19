package loop

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// WWD-8 8b-3 (spec §13.4): while a hold with `allow` is on, the facts say headroom 0 with the exception as the reason — every outside reader
// waits — and the worker takes only a job the hold lists.

// holdFacts are the facts of a host with these exceptions on and nothing else in the way: the headroom is 0 because of them.
func (r *rig) holdFacts(excs ...wire.FactExc) {
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) {
		f.Worker = wire.FactsWorker{Enabled: true, Slots: wire.Slots{Heavy: 2, Light: 2}}
		f.Exception = excs
		f.Derived.Headroom = wire.Slots{}
	})
}

func allowing(classes []wire.JobClass, repos []string) wire.FactExc {
	return wire.FactExc{Name: "hold", Allow: &wire.HoldAllow{Classes: classes, Repos: repos}}
}

func claimsAfterARound(t *testing.T, r *rig, c *fakeConn) []string {
	t.Helper()
	r.round()
	time.Sleep(10 * unit) // a refusal leaves nothing to wait for; give a claim the time it needs
	claims, _, _ := c.seen()
	r.loop.stopAll(t.Context(), "test")
	return claims
}

func TestAHoldAdmitsTheJobsItListsAndOnlyThose(t *testing.T) {
	cases := []struct {
		name string
		hold wire.FactExc
		want []string
	}{
		{"a class", allowing([]wire.JobClass{wire.ClassRelease}, nil), []string{"rel"}},
		{"a repo", allowing(nil, []string{"other"}), []string{"oth"}},
		{"a class or a repo", allowing([]wire.JobClass{wire.ClassRelease}, []string{"other"}), nil}, // best of the two listed: release first
		{"neither", allowing([]wire.JobClass{wire.ClassMerge}, []string{"nowhere"}), []string{}},
		{"both lists empty: none", allowing([]wire.JobClass{}, []string{}), []string{}},
	}
	for _, c := range cases {
		conn := newConn("mlab", qjob("dev", wire.ClassDev, time.Hour), qjob("rel", wire.ClassRelease, 0), func() wire.Job {
			j := qjob("oth", wire.ClassDev, 0)
			j.Repo.Name = "other"
			return j
		}())
		r := newRig(t, conn)
		r.ex.run = blockUntilDone
		r.holdFacts(c.hold)
		got := claimsAfterARound(t, r, conn)
		if c.want == nil { // class and repo both listed: either listed job may go, the unlisted one never
			if len(got) != 1 || got[0] == "dev" {
				t.Errorf("%s: claimed %v", c.name, got)
			}
			continue
		}
		if len(got) != len(c.want) || (len(got) == 1 && got[0] != c.want[0]) {
			t.Errorf("%s: claimed %v, want %v", c.name, got, c.want)
		}
	}
}

// An exception without `allow` holds as before: the headroom is 0 and the worker takes nothing, whatever it could have taken.
func TestAPlainExceptionStillHoldsEverything(t *testing.T) {
	conn := newConn("mlab", qjob("rel", wire.ClassRelease, 0))
	r := newRig(t, conn)
	r.holdFacts(wire.FactExc{Name: "dev-server"})
	if got := claimsAfterARound(t, r, conn); len(got) != 0 {
		t.Fatalf("claimed %v", got)
	}
	// ...and beside a selective hold it still does
	r2 := newRig(t, conn)
	r2.holdFacts(allowing([]wire.JobClass{wire.ClassRelease}, nil), wire.FactExc{Name: "dev-server"})
	if got := claimsAfterARound(t, r2, conn); len(got) != 0 {
		t.Fatalf("with a plain exception beside the hold: claimed %v", got)
	}
}

// Every hold must admit the job.
func TestEveryHoldMustAdmitTheJob(t *testing.T) {
	conn := newConn("mlab", qjob("rel", wire.ClassRelease, 0))
	r := newRig(t, conn)
	a := allowing([]wire.JobClass{wire.ClassRelease}, nil)
	b := allowing([]wire.JobClass{wire.ClassMerge}, nil)
	b.Name = "hold-b"
	r.holdFacts(a, b)
	if got := claimsAfterARound(t, r, conn); len(got) != 0 {
		t.Fatalf("claimed %v: the second hold does not list release", got)
	}
}

// A hold does not make room that is not there: the other reasons still count (a full slot, the zone, the owner) and so does the worker's
// own switch.
func TestAHoldDoesNotOpenWhatElseIsClosed(t *testing.T) {
	cases := map[string]func(st *wire.WorkerSettings, f *wire.Facts){
		"slots running": func(st *wire.WorkerSettings, f *wire.Facts) { f.Worker.Running = wire.Slots{Heavy: 2} },
		"warning zone":  func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Zone = wire.ZoneWarning },
		"worker off":    func(st *wire.WorkerSettings, f *wire.Facts) { st.Enabled = false },
	}
	for name, mod := range cases {
		conn := newConn("mlab", qjob("rel", wire.ClassRelease, 0))
		r := newRig(t, conn)
		r.holdFacts(allowing([]wire.JobClass{wire.ClassRelease}, nil))
		r.set(mod)
		if got := claimsAfterARound(t, r, conn); len(got) != 0 {
			t.Errorf("%s: claimed %v", name, got)
		}
	}
}

// A sample taken while the round was reading the queues may carry a hold made since the round began: the facts are read again before the
// try, so the job is not started. (A hold made after the newest sample is not seen until the next one: spec §13.4.)
func TestAHoldThatAppearsWhileTheQueuesAreReadStopsTheTry(t *testing.T) {
	plain := func(r *rig) {
		r.set(func(st *wire.WorkerSettings, f *wire.Facts) {
			f.Exception = []wire.FactExc{{Name: "dev-server"}}
			f.Derived.Headroom = wire.Slots{}
		})
	}
	second := func(r *rig) {
		r.set(func(st *wire.WorkerSettings, f *wire.Facts) {
			b := allowing([]wire.JobClass{wire.ClassMerge}, nil)
			b.Name = "hold-b"
			f.Exception = append(f.Exception, b)
		})
	}
	cases := map[string]struct {
		start func(r *rig)
		later func(r *rig)
	}{
		"a plain exception":                   {start: func(r *rig) {}, later: plain},
		"a second hold that does not list it": {start: func(r *rig) { r.holdFacts(allowing([]wire.JobClass{wire.ClassRelease}, nil)) }, later: second},
	}
	for name, c := range cases {
		conn := newConn("mlab", qjob("rel", wire.ClassRelease, 0))
		r := newRig(t, conn)
		c.start(r)
		conn.onQueued = func() { c.later(r) }
		if got := claimsAfterARound(t, r, conn); len(got) != 0 {
			t.Errorf("%s: claimed %v", name, got)
		}
	}
}

// ...and the same round with nothing changed still claims (the second reading must not be a second way to refuse).
func TestTheSecondReadingOfTheFactsRefusesNothingByItself(t *testing.T) {
	conn := newConn("mlab", qjob("rel", wire.ClassRelease, 0))
	r := newRig(t, conn)
	r.holdFacts(allowing([]wire.JobClass{wire.ClassRelease}, nil))
	conn.onQueued = func() {}
	if got := claimsAfterARound(t, r, conn); len(got) != 1 || got[0] != "rel" {
		t.Fatalf("claimed %v", got)
	}
}
