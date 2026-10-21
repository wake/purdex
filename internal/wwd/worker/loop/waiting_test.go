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
	if w == nil || w.Count != 3 || w.OldestID != "old" || w.OldestOrigin != "mlab" {
		t.Fatalf("waiting %+v", w)
	}
	if got := time.Since(w.OldestSince); got < 7*time.Minute || got > 7*time.Minute+10*time.Second {
		t.Errorf("oldest since %v ago", got)
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

// The oldest is by submission time, not the best ranked: a release job that came a minute ago ranks before a dev job that has waited eight.
func TestWaiting_TheOldestIsNotTheBestRanked(t *testing.T) {
	c := newConn("mlab", qjob("rel", wire.ClassRelease, time.Minute), qjob("dev", wire.ClassDev, 8*time.Minute))
	r := newRig(t, c)
	r.set(func(st *wire.WorkerSettings, f *wire.Facts) { f.Derived.Headroom.Heavy = 0 })
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
