package dialogue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// T8 review fixes: retries run only in the reaper and one at a time, they are bounded, the kind contract (apply, after,
// finish), sessions that cannot take jobs, usage over attempts, and Stop against work in flight.

var errDisk = errors.New("disk full")

// storeFailure leaves one answer pending in the rig (the first apply failed with errDisk).
func storeFailure(t *testing.T, r *jcRig) {
	t.Helper()
	r.kind.applyErr = []error{errDisk}
	r.enq(jcSID, kindRecap, 1)
	j, _ := r.next(jcSID)
	if _, err := r.ch.Result("streamA", j.ID, result{Answered: true, Text: "answer"}); err != nil {
		t.Fatal(err)
	}
}

func TestRetry_OnlyOneApplyAtATimePerAnswer(t *testing.T) {
	r := newJCRig(t, 0)
	storeFailure(t, r)
	entered, gate := make(chan struct{}), make(chan struct{})
	var first int32
	r.kind.onApply = func() {
		if atomic.AddInt32(&first, 1) == 1 {
			close(entered)
			<-gate
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); r.ch.retryPending() }()
	<-entered
	r.ch.retryPending() // a second trigger while the first apply is still running
	close(gate)
	wg.Wait()
	r.ch.retryPending()
	r.kind.mu.Lock()
	n := len(r.kind.applies)
	r.kind.mu.Unlock()
	if n != 2 { // the original apply and exactly one retry
		t.Fatalf("applies %d, want 2", n)
	}
	if r.kind.afterCount() != 1 {
		t.Fatalf("after ran %d times", r.kind.afterCount())
	}
}

func TestRetry_NextAndResultDoNotRetry(t *testing.T) {
	r := newJCRig(t, 0)
	storeFailure(t, r)
	r.enq(jcSID, kindRecap, 2)
	j, _ := r.next(jcSID)
	_, _ = r.ch.Result("streamA", j.ID, result{Answered: true, Text: "two"})
	r.kind.mu.Lock()
	n := len(r.kind.applies)
	r.kind.mu.Unlock()
	if n != 2 { // answer 1 once, answer 2 once: no retry of answer 1 from Next or Result
		t.Fatalf("applies %d, want 2", n)
	}
}

func TestRetry_GivesUpAfterTheLimit(t *testing.T) {
	r := newJCRig(t, 0)
	r.kind.applyAll = errDisk
	storeFailure(t, r)
	for i := 1; i < maxApplyRetries; i++ {
		r.ch.reap()
		if r.kind.count() != 0 {
			t.Fatalf("ended after %d retries", i)
		}
	}
	r.ch.reap()
	if r.kind.count() != 1 {
		t.Fatalf("finishes %+v after %d retries", r.kind.finishes, maxApplyRetries)
	}
	if f := r.kind.last(); f.state != stateFailed || f.reason != reasonStore {
		t.Fatalf("%+v", f)
	}
	r.ch.reap()
	r.ch.reap()
	if r.kind.count() != 1 || len(r.kind.applies) != 1+maxApplyRetries {
		t.Fatalf("finishes %d applies %d", r.kind.count(), len(r.kind.applies))
	}
	if r.kind.afterCount() != 0 {
		t.Fatal("after ran for an answer that was never stored")
	}
}

func TestRetry_GoneEndsAtOnce(t *testing.T) {
	r := newJCRig(t, 0)
	storeFailure(t, r)
	r.kind.applyErr = []error{errGone}
	r.ch.reap()
	if f := r.kind.last(); r.kind.count() != 1 || f.state != stateFailed || f.reason != reasonStore {
		t.Fatalf("%+v", r.kind.finishes)
	}
	r.ch.reap()
	if r.kind.count() != 1 || len(r.kind.applies) != 2 || r.kind.afterCount() != 0 {
		t.Fatalf("finishes %d applies %d after %d", r.kind.count(), len(r.kind.applies), r.kind.afterCount())
	}
}

func TestAfter_OnceOnDirectSuccess(t *testing.T) {
	r := newJCRig(t, 0)
	r.enq(jcSID, kindRecap, 1)
	j, _ := r.next(jcSID)
	_, _ = r.ch.Result("streamA", j.ID, result{Answered: true, Text: "x"})
	r.ch.reap()
	if r.kind.afterCount() != 1 || r.kind.count() != 0 {
		t.Fatalf("after %d finishes %d", r.kind.afterCount(), r.kind.count())
	}
}

func TestStop_UnstoredAnswerFinishesStoppedAndNeverRunsAfter(t *testing.T) {
	r := newJCRig(t, 0)
	r.kind.applyAll = errDisk
	storeFailure(t, r)
	r.ch.Stop()
	if f := r.kind.last(); r.kind.count() != 1 || f.state != stateFailed || f.reason != reasonStopped {
		t.Fatalf("%+v", r.kind.finishes)
	}
	if r.kind.afterCount() != 0 {
		t.Fatal("after ran")
	}
}

// A session whose mod stopped announcing the capability: its waiting jobs end as skipped(no_mod) and its queue entry goes.
func TestReap_SweepsSessionsThatCannotTakeJobs(t *testing.T) {
	kind := &jcKind{}
	var mu sync.Mutex
	bad := map[string]bool{}
	ch := newChannel(chanDeps{
		capable:  func(sid string) bool { mu.Lock(); defer mu.Unlock(); return !bad[sid] },
		handlers: map[string]kindHandler{kindTurnSummary: kind, kindRecap: kind},
		logf:     func(string, ...any) {},
	})
	other := "11111111-1b2c-4d3e-8f90-a1b2c3d4e5f6"
	for _, sid := range []string{jcSID, other} {
		if id, why := ch.enqueue(sid, kindRecap, sid); id == "" {
			t.Fatal(why)
		}
	}
	mu.Lock()
	bad[jcSID] = true
	mu.Unlock()
	ch.reap()
	if kind.count() != 1 {
		t.Fatalf("finishes %+v", kind.finishes)
	}
	if f := kind.last(); f.state != stateSkipped || f.reason != reasonNoMod || f.job.sid != jcSID {
		t.Fatalf("%+v", f)
	}
	ch.qmu.Lock()
	_, gone := ch.qs[jcSID]
	q := ch.qs[other]
	ok := q != nil && len(q.waiting) == 1
	ch.qmu.Unlock()
	if gone {
		t.Fatal("the session's queue entry stayed")
	}
	if !ok {
		t.Fatal("a capable session's job was touched")
	}
}

func TestResult_UsageAddsUpOverAttempts(t *testing.T) {
	r := newJCRig(t, 0)
	r.enq(jcSID, kindTurnSummary, 1)
	j, _ := r.next(jcSID)
	_, _ = r.ch.Result("streamA", j.ID, result{Reason: "empty-reply", Usage: usage{In: 1, Out: 2, CacheRead: 3}, LatencyMS: 10})
	j2, ok := r.next(jcSID)
	if !ok {
		t.Fatal("no retry")
	}
	_, _ = r.ch.Result("streamA", j2.ID, result{Answered: true, Text: "x", Usage: usage{In: 10, Out: 20, CacheRead: 30}, LatencyMS: 5})
	if len(r.kind.usages) != 1 || r.kind.usages[0] != (usage{In: 11, Out: 22, CacheRead: 33, LatencyMS: 15}) {
		t.Fatalf("usages %+v", r.kind.usages)
	}
}

// Stop while Next builds a job's input: the job ends exactly once, and nothing is written after Stop returns.
func TestStop_DuringBuildFinishesOnce(t *testing.T) {
	r := newJCRig(t, 0)
	reached, gate := make(chan struct{}), make(chan struct{})
	r.kind.buildErr = errors.New("store down")
	r.kind.onBuild = func() { close(reached); <-gate }
	r.enq(jcSID, kindRecap, 1)
	done := make(chan struct{})
	go func() { r.ch.Next(context.Background(), "streamA", jcSID, 0); close(done) }()
	<-reached
	r.ch.Stop()
	if r.kind.count() != 1 {
		t.Fatalf("finishes after Stop: %+v", r.kind.finishes)
	}
	close(gate)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Next did not return")
	}
	if r.kind.count() != 1 {
		t.Fatalf("a finish after Stop returned: %+v", r.kind.finishes)
	}
	if f := r.kind.last(); f.reason != reasonStopped {
		t.Fatalf("%+v", f)
	}
}
