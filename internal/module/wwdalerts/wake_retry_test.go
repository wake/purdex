package wwdalerts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func (f *fakeWaker) setErr(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func TestWakeBackoff(t *testing.T) {
	want := []time.Duration{2, 4, 8, 15, 15, 15}
	for i, w := range want {
		if got := wakeBackoff(i + 1); got != w*time.Minute {
			t.Errorf("after failure %d: %v, want %v", i+1, got, w*time.Minute)
		}
	}
}

// A failed call (an error, or a timeout) leaves the episode pending: it is tried again after 2 minutes, then 4, and once it succeeds it is
// not called again.
func TestAFailedWakeIsRetriedWithBackoff(t *testing.T) {
	e, w, _ := wakeEnv(t)
	w.setErr(context.DeadlineExceeded)
	e.baseline(t)
	e.src.set("mlab", alertOf("disk_low:/", wire.AlertDiskLow, wire.AlertPush, t0, "x"))
	step := func(d time.Duration, want int) {
		t.Helper()
		e.now = e.now.Add(d)
		e.pass(t)
		e.settle(t)
		if n := len(w.got()); n != want {
			t.Fatalf("at +%v: calls = %d, want %d", d, n, want)
		}
	}
	step(0, 1)
	step(time.Minute, 1)    // 1 minute after the first failure: not yet
	step(70*time.Second, 2) // 2m10s: retried
	step(3*time.Minute, 2)  // the second failure waits 4 minutes
	step(90*time.Second, 3) // 4m40s after the second
	w.setErr(nil)
	step(10*time.Minute, 4) // succeeds
	step(time.Hour, 4)      // told: never again
}

// A failed call does not start the ten minutes (only a success does): a new episode of the same key right after is still woken for, and
// after a success the ten minutes hold.
func TestOnlyASuccessfulWakeStartsTheTenMinutes(t *testing.T) {
	e, w, _ := wakeEnv(t)
	w.setErr(errors.New("no watcher"))
	e.baseline(t)
	a := alertOf("disk_low:/", wire.AlertDiskLow, wire.AlertPush, t0, "x")
	e.src.set("mlab", a)
	e.pass(t)
	e.settle(t)
	e.src.set("mlab") // cleared before it was told: given up
	e.pass(t)
	if e.logCount("given up") != 1 {
		t.Fatalf("logs = %v", e.logs)
	}
	w.setErr(nil)
	e.now = e.now.Add(time.Minute)
	b := a
	b.Since = e.now
	e.src.set("mlab", b)
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 2 {
		t.Fatalf("calls = %v, want the failed one and the new episode", w.got())
	}
	e.src.set("mlab")
	e.pass(t)
	e.now = e.now.Add(time.Minute)
	c := a
	c.Since = e.now
	e.src.set("mlab", c)
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 2 {
		t.Fatalf("calls = %v", w.got())
	}
}

// A pending episode is given up when the alert is no longer standing, and not called afterwards.
func TestAClearedPendingEpisodeIsGivenUp(t *testing.T) {
	e, w, _ := wakeEnv(t)
	w.setErr(errors.New("no watcher"))
	e.baseline(t)
	e.src.set("mlab", alertOf("k", wire.AlertThrash, wire.AlertDigest, t0, "x"))
	e.pass(t)
	e.settle(t)
	e.src.set("mlab")
	e.pass(t)
	e.now = e.now.Add(time.Hour)
	e.pass(t)
	e.settle(t)
	if n := len(w.got()); n != 1 {
		t.Fatalf("calls = %v", w.got())
	}
}

// A restart keeps the pending episode: it is called again once its backoff is over.
func TestAPendingEpisodeSurvivesARestart(t *testing.T) {
	e, w, _ := wakeEnv(t)
	w.setErr(errors.New("no watcher"))
	e.baseline(t)
	e.src.set("mlab", alertOf("k", wire.AlertThrash, wire.AlertDigest, t0, "x"))
	e.pass(t)
	e.settle(t)
	e.m = e.module(t) // restarted
	w2 := startWakeFor(t, e)
	e.pass(t)
	e.settle(t)
	if len(w2.got()) != 0 {
		t.Fatalf("called before the backoff was over: %v", w2.got())
	}
	e.now = e.now.Add(3 * time.Minute)
	e.pass(t)
	e.settle(t)
	if len(w2.got()) != 1 {
		t.Fatalf("calls after the restart = %v", w2.got())
	}
}

// One episode never has two calls at once, however many passes find it due.
func TestNoParallelWakeOfOneEpisode(t *testing.T) {
	e, w, _ := wakeEnv(t)
	w.block = make(chan struct{})
	w.entered = make(chan struct{}, 8)
	e.baseline(t)
	e.src.set("mlab", alertOf("k", wire.AlertThrash, wire.AlertDigest, t0, "x"))
	e.pass(t)
	<-w.entered
	w.setErr(errors.New("no watcher"))
	for i := 0; i < 3; i++ {
		e.now = e.now.Add(20 * time.Minute)
		e.pass(t)
	}
	if n := len(w.got()); n != 1 {
		t.Fatalf("calls during the first = %d", n)
	}
	close(w.block)
	e.settle(t)
}

// Episodes called together stay together: a failure leaves all of them pending, a success marks all of them told.
func TestAGroupSucceedsOrFailsTogether(t *testing.T) {
	e, w, _ := wakeEnv(t)
	w.block = make(chan struct{})
	w.entered = make(chan struct{}, 8)
	w.setErr(errors.New("no watcher"))
	e.baseline(t)
	e.src.set("mlab", alertOf("first", wire.AlertThrash, wire.AlertDigest, t0, "x"))
	e.pass(t)
	<-w.entered // the worker is inside the first call; the next two are folded while it runs
	e.src.set("mlab",
		alertOf("first", wire.AlertThrash, wire.AlertDigest, t0, "x"),
		alertOf("a", wire.AlertOrphans, wire.AlertDigest, t0, "one"),
		alertOf("b", wire.AlertResidentGrowth, wire.AlertDigest, t0, "two"))
	e.pass(t)
	close(w.block)
	e.settle(t) // the first failed; the other two went as one call and failed too
	calls := w.got()
	if len(calls) != 2 || !strings.HasSuffix(calls[1], "；另 1 則") {
		t.Fatalf("calls = %v", calls)
	}
	w.setErr(nil)
	e.now = e.now.Add(3 * time.Minute)
	e.pass(t)
	e.settle(t)
	// first is retried alone or with the pair, the pair retried together: three episodes told by now
	e.now = e.now.Add(time.Hour)
	e.pass(t)
	e.settle(t)
	n := len(w.got())
	e.now = e.now.Add(time.Hour)
	e.pass(t)
	e.settle(t)
	if len(w.got()) != n {
		t.Fatalf("called again after the success: %v", w.got())
	}
	if n < 3 || n > 4 {
		t.Fatalf("calls = %v", w.got())
	}
}
