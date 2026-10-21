package loop

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ---- Waiting (#3941): what the worker sees queued for it, told in facts so that the fast lane gives way ----

func qjobLabelled(id string, age time.Duration, labels ...string) wire.Job {
	j := qjob(id, wire.ClassDev, age)
	j.Labels = labels
	return j
}

// With no room (the slot is taken, here by a fast-lane run) the worker still reads the queues and tells what waits.
func TestWaiting_ReadWhileThereIsNoRoom(t *testing.T) {
	c := newConn("mlab", qjob("young", wire.ClassDev, time.Minute), qjob("old", wire.ClassDev, 7*time.Minute), qjob("mid", wire.ClassDev, 3*time.Minute))
	r := newRig(t, c)
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 })
	if r.loop.Waiting() != nil {
		t.Fatal("nothing is known before the first round")
	}
	r.round()
	w := r.loop.Waiting()
	if w == nil || w.Count != 3 || w.OldestOrigin != "mlab" {
		t.Fatalf("waiting %+v", w)
	}
	// seen for the first time now, whatever the origin's clock says of the submissions
	if !w.OldestSince.Equal(w.ObservedAt) {
		t.Errorf("oldest since %v, observed %v", w.OldestSince, w.ObservedAt)
	}
	if time.Since(w.ObservedAt) > 5*time.Second {
		t.Errorf("observed at %v", w.ObservedAt)
	}
	if claims, _, _ := c.seen(); len(claims) != 0 {
		t.Errorf("no room, yet claims %v", claims)
	}
}

// The read of a worker that has no room is not made every round (a busy host would ask every peer every two seconds), but at WaitingEvery.
func TestWaiting_NoRoomReadsOnlyEveryWaitingEvery(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, time.Minute))
	var reads atomic.Int32
	c.onQueued = func() { reads.Add(1) }
	r := newRig(t, c)
	r.loop.cfg.WaitingEvery = 150 * time.Millisecond
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { st.Slots.Heavy = 0 })
	r.round()
	r.round()
	r.round()
	if n := reads.Load(); n != 1 {
		t.Fatalf("%d reads in three quick rounds", n)
	}
	time.Sleep(200 * time.Millisecond)
	r.round()
	if n := reads.Load(); n != 2 {
		t.Errorf("%d reads after the pause", n)
	}
}

// A clock of the worker's own: the rig's rounds run on it.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// The time a job has waited is counted from when this worker first saw it queued, not from the submission time of the origin, whose clock
// may be minutes behind or ahead of this host's (codex R2 on #3950): a slow clock would make every new job look old (and past the cap), a fast
// one would put the giving way off.
func TestWaiting_CountsFromTheFirstLookNotTheOriginsClock(t *testing.T) {
	slow := qjob("slow", wire.ClassDev, 3*time.Hour) // submitted "three hours ago" by an origin whose clock is behind
	fast := qjob("fast", wire.ClassDev, -time.Hour)  // submitted "in an hour"
	c := newConn("mlab", slow)
	r := newRig(t, c)
	clk := &fakeClock{t: time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)}
	r.loop.cfg.Now = clk.now
	r.loop.cfg.WaitingEvery = time.Nanosecond
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 })
	r.round()
	first := clk.t
	if w := r.loop.Waiting(); w == nil || w.OldestID != "slow" || !w.OldestSince.Equal(first) {
		t.Fatalf("first look: %+v", w)
	}
	// six minutes later a second job comes, whose origin clock is ahead: the first is still the oldest, since the first look
	clk.t = clk.t.Add(6 * time.Minute)
	c.mu.Lock()
	c.jobs["fast"] = fast
	c.mu.Unlock()
	r.round()
	w := r.loop.Waiting()
	if w == nil || w.Count != 2 || w.OldestID != "slow" || !w.OldestSince.Equal(first) {
		t.Fatalf("second look: %+v", w)
	}
	// the first goes (claimed elsewhere): the second is the oldest, from when it was first seen
	c.mu.Lock()
	delete(c.jobs, "slow")
	c.mu.Unlock()
	clk.t = clk.t.Add(time.Minute)
	r.round()
	if w := r.loop.Waiting(); w == nil || w.Count != 1 || w.OldestID != "fast" || !w.OldestSince.Equal(first.Add(6*time.Minute)) {
		t.Fatalf("third look: %+v", w)
	}
}

// A job that dropped out of what the worker would take (a hold, a lock) and came back has not been waiting meanwhile: its time starts again.
func TestWaiting_AJobThatLeftAndCameBackStartsAgain(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, time.Minute))
	r := newRig(t, c)
	clk := &fakeClock{t: time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)}
	r.loop.cfg.Now = clk.now
	r.loop.cfg.WaitingEvery = time.Nanosecond
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 })
	r.round()
	c.mu.Lock()
	j := c.jobs["a"]
	j.Manual = wire.PositionHold
	c.jobs["a"] = j
	c.mu.Unlock()
	clk.t = clk.t.Add(4 * time.Minute)
	r.round()
	if w := r.loop.Waiting(); w != nil {
		t.Fatalf("on hold: %+v", w)
	}
	c.mu.Lock()
	j.Manual = ""
	c.jobs["a"] = j
	c.mu.Unlock()
	clk.t = clk.t.Add(time.Minute)
	r.round()
	if w := r.loop.Waiting(); w == nil || !w.OldestSince.Equal(clk.t) {
		t.Fatalf("released: %+v (now %v)", w, clk.t)
	}
}

// With the first look equal, the better ranked is named; a job that came later, though it ranks higher (release), is not the oldest.
func TestWaiting_TheOldestIsNotTheBestRanked(t *testing.T) {
	c := newConn("mlab", qjob("dev", wire.ClassDev, 8*time.Minute))
	r := newRig(t, c)
	clk := &fakeClock{t: time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)}
	r.loop.cfg.Now = clk.now
	r.loop.cfg.WaitingEvery = time.Nanosecond
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 })
	r.round()
	clk.t = clk.t.Add(time.Minute)
	c.mu.Lock()
	c.jobs["rel"] = qjob("rel", wire.ClassRelease, 0)
	c.mu.Unlock()
	r.round()
	if w := r.loop.Waiting(); w == nil || w.Count != 2 || w.OldestID != "dev" {
		t.Fatalf("waiting %+v", w)
	}
}

// What the worker would not claim is not waiting for it: by labels, a job on hold.
func TestWaiting_OnlyWhatThisWorkerWouldTake(t *testing.T) {
	held := qjob("held", wire.ClassDev, 10*time.Minute)
	held.Manual = wire.PositionHold
	c := newConn("mlab", qjobLabelled("elsewhere", 20*time.Minute, "os:linux"), held, qjob("mine", wire.ClassDev, 4*time.Minute))
	r := newRig(t, c)
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 })
	r.round()
	w := r.loop.Waiting()
	if w == nil || w.Count != 1 || w.OldestID != "mine" {
		t.Fatalf("waiting %+v", w)
	}
}

func TestWaiting_NoneWhenTheQueueIsEmptyOrNothingIsTakeable(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, time.Minute))
	r := newRig(t, c)
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 })
	r.round()
	if r.loop.Waiting() == nil {
		t.Fatal("one is waiting")
	}
	c.mu.Lock()
	delete(c.jobs, "a")
	c.mu.Unlock()
	r.loop.cfg.WaitingEvery = time.Nanosecond
	r.round()
	if w := r.loop.Waiting(); w != nil {
		t.Errorf("the queue is empty: %+v", w)
	}
}

// A worker that is off, or that the owner has stopped, takes nothing, so nothing waits for it: the fast lane must not give way to it.
func TestWaiting_ClearedWhenTheWorkerTakesNothing(t *testing.T) {
	for name, mod := range map[string]func(st *wire.WorkerSettings, f *wire.Facts){
		"off": func(st *wire.WorkerSettings, f *wire.Facts) { st.Enabled = false },
		"owner is active": func(st *wire.WorkerSettings, f *wire.Facts) {
			st.OwnerRule = "air26"
			f.Owner = &wire.OwnerState{Active: true, Reason: "hid"}
		},
		"no facts yet": func(st *wire.WorkerSettings, f *wire.Facts) {},
	} {
		c := newConn("mlab", qjob("a", wire.ClassDev, time.Minute))
		r := newRig(t, c)
		r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 })
		r.round()
		if r.loop.Waiting() == nil {
			t.Fatalf("%s: one is waiting", name)
		}
		r.set(mod)
		if name == "no facts yet" {
			r.mu.Lock()
			r.facts = nil
			r.mu.Unlock()
		}
		r.round()
		if w := r.loop.Waiting(); w != nil {
			t.Errorf("%s: still %+v", name, w)
		}
	}
}

// A round that has room reads the queues anyway (to claim), so it tells what is left after the claim, and not the job it took.
func TestWaiting_ARoundThatClaimsTellsWhatIsLeft(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 9*time.Minute), qjob("b", wire.ClassDev, 2*time.Minute))
	r := newRig(t, c)
	r.ex.run = blockUntilDone
	r.round()
	claims, _, _ := c.seen()
	if len(claims) != 1 {
		t.Fatalf("claims %v", claims)
	}
	w := r.loop.Waiting()
	if w == nil || w.Count != 1 || w.OldestID == claims[0] {
		t.Errorf("claimed %s, waiting %+v", claims[0], w)
	}
	r.loop.stopAll(t.Context(), "test")
}

// A queue that cannot be read this time leaves no claim on what it held: the worker could not take those jobs now either.
func TestWaiting_ACrashedOriginIsNotWaitedFor(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, time.Minute))
	r := newRig(t, c)
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 })
	r.round()
	c.mu.Lock()
	c.readErr = errors.New("the origin does not answer")
	c.mu.Unlock()
	r.loop.cfg.WaitingEvery = time.Nanosecond
	r.round()
	if w := r.loop.Waiting(); w != nil {
		t.Errorf("%+v", w)
	}
}
