package promptq

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// U3-0b: the prompt queue (plan D7, rev 5): one owner stream per session, at most once by client_msg_id.

type owners struct {
	mu sync.Mutex
	m  map[string]string
}

func (o *owners) OwnerOf(sid string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, ok := o.m[sid]
	return s, ok
}

func (o *owners) set(sid, stream string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if stream == "" {
		delete(o.m, sid)
		return
	}
	o.m[sid] = stream
}

func newQ(t *testing.T) (*Queue, *owners) {
	t.Helper()
	o := &owners{m: map[string]string{"s1": "mod1"}}
	q := New(o)
	q.Wait, q.HandTimeout = 400*time.Millisecond, 150*time.Millisecond
	return q, o
}

type answer struct {
	r   Result
	err error
}

func submitAsync(q *Queue, sid, id, text string) chan answer {
	ch := make(chan answer, 1)
	go func() {
		r, err := q.Submit(context.Background(), sid, id, text)
		ch <- answer{r, err}
	}()
	return ch
}

func take(t *testing.T, ch chan answer) Result {
	t.Helper()
	select {
	case a := <-ch:
		if a.err != nil {
			t.Fatal(a.err)
		}
		return a.r
	case <-time.After(3 * time.Second):
		t.Fatal("no answer")
	}
	return Result{}
}

func next(t *testing.T, q *Queue, stream, sid string) Job {
	t.Helper()
	j, ok := q.Next(context.Background(), stream, sid, time.Second)
	if !ok {
		t.Fatalf("no job for %s on %s", sid, stream)
	}
	return j
}

func TestSubmit_AcceptedThroughTheOwner(t *testing.T) {
	q, _ := newQ(t)
	ch := submitAsync(q, "s1", "c1", "hello")
	j := next(t, q, "mod1", "s1")
	if j.Kind != KindSubmit || j.SessionID != "s1" || j.Text != "hello" || j.ID == "" {
		t.Fatalf("job = %+v", j)
	}
	if err := q.Result("mod1", j.ID, Outcome{Status: Accepted}); err != nil {
		t.Fatal(err)
	}
	if r := take(t, ch); r.Status != Accepted {
		t.Fatalf("result = %+v", r)
	}
}

func TestSubmit_NoModWhenNoOwner(t *testing.T) {
	q, o := newQ(t)
	o.set("s1", "")
	if r, err := q.Submit(context.Background(), "s1", "c1", "x"); err != nil || r.Status != NoMod {
		t.Fatalf("%+v %v", r, err)
	}
	if q.HasOwner("s1") || !(func() bool { o.set("s1", "m"); return q.HasOwner("s1") })() {
		t.Fatal("HasOwner does not follow the owner")
	}
}

// Only the owner is handed anything; a stream that is not the owner gets no job and holds nothing.
// Mutation gate: hand out to any stream → red.
func TestNext_OnlyTheOwnerStream(t *testing.T) {
	q, _ := newQ(t)
	ch := submitAsync(q, "s1", "c1", "x")
	if _, ok := q.Next(context.Background(), "other", "s1", 80*time.Millisecond); ok {
		t.Fatal("a stream that is not the owner got a job")
	}
	j := next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	take(t, ch)
}

func TestSubmit_DroppedAndBusyAreReported(t *testing.T) {
	q, _ := newQ(t)
	ch := submitAsync(q, "s1", "c1", "x")
	j := next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Dropped, Reason: "session_changed"})
	if r := take(t, ch); r.Status != Dropped || r.Reason != "session_changed" {
		t.Fatalf("dropped: %+v", r)
	}
	ch = submitAsync(q, "s1", "c2", "y")
	j = next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Busy})
	if r := take(t, ch); r.Status != Busy {
		t.Fatalf("busy: %+v", r)
	}
}

// A request nobody fetched in time answers timeout and is withdrawn: nothing is handed out later, and the same
// client_msg_id may be sent again (it never ran).
func TestSubmit_NeverHandedOutTimesOutAndMayBeResent(t *testing.T) {
	q, _ := newQ(t)
	if r := take(t, submitAsync(q, "s1", "c1", "x")); r.Status != Timeout {
		t.Fatalf("result = %+v", r)
	}
	if _, ok := q.Next(context.Background(), "mod1", "s1", 0); ok {
		t.Fatal("a withdrawn request was handed out")
	}
	ch := submitAsync(q, "s1", "c1", "x") // same id again
	j := next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	if r := take(t, ch); r.Status != Accepted {
		t.Fatalf("resend: %+v", r)
	}
}

// Handed out and unanswered: unknown, and a repeat of the same id never queues it again (at most once). Mutation gate:
// re-queue a handed request on a repeat → red.
func TestSubmit_HandedOutWithoutResultIsUnknownAndNeverRequeued(t *testing.T) {
	q, _ := newQ(t)
	ch := submitAsync(q, "s1", "c1", "x")
	next(t, q, "mod1", "s1") // handed, never answered
	if r := take(t, ch); r.Status != Unknown {
		t.Fatalf("result = %+v", r)
	}
	r, err := q.Submit(context.Background(), "s1", "c1", "x")
	if err != nil || r.Status != Unknown {
		t.Fatalf("repeat = %+v %v", r, err)
	}
	if _, ok := q.Next(context.Background(), "mod1", "s1", 0); ok {
		t.Fatal("an unknown request was handed out again")
	}
}

// A repeat while the first is still open joins its wait: one job, both callers get its result.
func TestSubmit_RepeatJoinsTheOpenRequest(t *testing.T) {
	q, _ := newQ(t)
	a := submitAsync(q, "s1", "c1", "x")
	b := submitAsync(q, "s1", "c1", "x")
	time.Sleep(30 * time.Millisecond)
	j := next(t, q, "mod1", "s1")
	if _, ok := q.Next(context.Background(), "mod1", "s1", 30*time.Millisecond); ok {
		t.Fatal("two jobs for one client_msg_id")
	}
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	if take(t, a).Status != Accepted || take(t, b).Status != Accepted {
		t.Fatal("a caller did not get the shared result")
	}
}

// busy never ran: the same client_msg_id may be sent again.
func TestSubmit_BusyMayBeSentAgain(t *testing.T) {
	q, _ := newQ(t)
	ch := submitAsync(q, "s1", "c1", "x")
	j := next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Busy})
	take(t, ch)
	ch = submitAsync(q, "s1", "c1", "x")
	j2 := next(t, q, "mod1", "s1")
	if j2.ID == j.ID {
		t.Fatal("same job id")
	}
	q.Result("mod1", j2.ID, Outcome{Status: Accepted})
	if r := take(t, ch); r.Status != Accepted {
		t.Fatalf("second attempt: %+v", r)
	}
}

func TestSubmit_ClientMsgIDReusedForADifferentRequest(t *testing.T) {
	q, _ := newQ(t)
	ch := submitAsync(q, "s1", "c1", "first")
	j := next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	take(t, ch)
	r, _ := q.Submit(context.Background(), "s1", "c1", "something else")
	if r.Status != Dropped || r.Reason != "client_msg_id_reused" {
		t.Fatalf("result = %+v", r)
	}
}

// The owner changes after hand-out (a reloaded mod): the old stream's result is refused, the request is unknown, and
// the new owner is not handed it. Mutation gate: skip the owner re-check at result → red.
func TestResult_FromAFormerOwnerIsRefused(t *testing.T) {
	q, o := newQ(t)
	ch := submitAsync(q, "s1", "c1", "x")
	j := next(t, q, "mod1", "s1")
	o.set("s1", "mod2")
	if err := q.Result("mod1", j.ID, Outcome{Status: Accepted}); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("err = %v", err)
	}
	if r := take(t, ch); r.Status != Unknown {
		t.Fatalf("result = %+v", r)
	}
	if _, ok := q.Next(context.Background(), "mod2", "s1", 0); ok {
		t.Fatal("the new owner was handed the former owner's request")
	}
	if err := q.Result("mod1", j.ID, Outcome{Status: Accepted}); !errors.Is(err, ErrNotLeased) {
		t.Fatalf("second result err = %v", err)
	}
}

func TestResult_WrongStreamOrUnknownJobIsNotLeased(t *testing.T) {
	q, _ := newQ(t)
	ch := submitAsync(q, "s1", "c1", "x")
	j := next(t, q, "mod1", "s1")
	if err := q.Result("mod9", j.ID, Outcome{Status: Accepted}); !errors.Is(err, ErrNotLeased) {
		t.Fatalf("wrong stream: %v", err)
	}
	if err := q.Result("mod1", "pj-nope", Outcome{Status: Accepted}); !errors.Is(err, ErrNotLeased) {
		t.Fatalf("unknown job: %v", err)
	}
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	take(t, ch)
	if err := q.Result("mod1", j.ID, Outcome{Status: Accepted}); !errors.Is(err, ErrNotLeased) {
		t.Fatalf("twice: %v", err)
	}
}

// Two Apps submit to one session: one request out at a time, in the order they were made.
func TestSubmit_OneAtATimeInOrder(t *testing.T) {
	q, _ := newQ(t)
	q.Wait, q.HandTimeout = 2*time.Second, time.Second
	a := submitAsync(q, "s1", "ca", "first")
	time.Sleep(20 * time.Millisecond)
	b := submitAsync(q, "s1", "cb", "second")
	j1 := next(t, q, "mod1", "s1")
	if j1.Text != "first" {
		t.Fatalf("first job = %q", j1.Text)
	}
	if _, ok := q.Next(context.Background(), "mod1", "s1", 50*time.Millisecond); ok {
		t.Fatal("a second job went out before the first had a result")
	}
	q.Result("mod1", j1.ID, Outcome{Status: Accepted})
	j2 := next(t, q, "mod1", "s1")
	if j2.Text != "second" {
		t.Fatalf("second job = %q", j2.Text)
	}
	q.Result("mod1", j2.ID, Outcome{Status: Accepted})
	take(t, a)
	take(t, b)
}

func TestInterrupt_GoesThroughTheMod(t *testing.T) {
	q, _ := newQ(t)
	ch := make(chan answer, 1)
	go func() {
		r, err := q.Interrupt(context.Background(), "s1")
		ch <- answer{r, err}
	}()
	j := next(t, q, "mod1", "s1")
	if j.Kind != KindInterrupt || j.Text != "" {
		t.Fatalf("job = %+v", j)
	}
	q.Result("mod1", j.ID, Outcome{Status: Dropped, Reason: "not_running"})
	if r := take(t, ch); r.Status != Dropped || r.Reason != "not_running" {
		t.Fatalf("result = %+v", r)
	}
	q.owners.(*owners).set("s1", "")
	if r, _ := q.Interrupt(context.Background(), "s1"); r.Status != NoMod {
		t.Fatalf("no mod: %+v", r)
	}
}

func TestSubmit_TooManyWaiting(t *testing.T) {
	q, _ := newQ(t)
	q.Wait = 2 * time.Second
	for i := 0; i < maxQueued; i++ {
		submitAsync(q, "s1", "", "x")
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := q.Submit(context.Background(), "s1", "", "overflow"); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v", err)
	}
}

// A request whose caller went away and that nobody fetched within the wait is not handed out later: a prompt is never
// typed minutes after it was asked for. Mutation gate: drop the expiry in Next → red.
func TestNext_ExpiredRequestIsNotHandedOut(t *testing.T) {
	q, _ := newQ(t)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan Result, 1)
	go func() { r, _ := q.Submit(ctx, "s1", "c1", "stale"); got <- r }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-got
	time.Sleep(q.Wait + 50*time.Millisecond)
	if j, ok := q.Next(context.Background(), "mod1", "s1", 0); ok {
		t.Fatalf("a stale request was handed out: %+v", j)
	}
	// and its id may be used again: it never ran
	ch := submitAsync(q, "s1", "c1", "stale")
	j := next(t, q, "mod1", "s1")
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	take(t, ch)
}

// A flood of distinct ids forgets the OLDEST settled rows first and never an open one: a request that may still run is
// not sent a second time. Mutation gate: drop any settled row (map order), or an open one → red (codex attack).
func TestLedger_FloodForgetsTheOldestSettledAndNeverAnOpenRow(t *testing.T) {
	q, _ := newQ(t)
	q.Wait, q.HandTimeout = time.Minute, time.Minute
	now := time.Unix(1_700_000_000, 0)
	q.Now = func() time.Time { return now }
	open := submitAsync(q, "s1", "open", "x")
	time.Sleep(20 * time.Millisecond)
	j := next(t, q, "mod1", "s1") // handed out, still open
	_ = j
	q.mu.Lock()
	for i := 0; i < maxLedger+10; i++ {
		now = now.Add(time.Second)
		id := "done-" + time.Duration(i).String()
		e := &entry{key: id, state: stDone, finishedAt: now, done: make(chan struct{})}
		q.ledger[id] = e
	}
	q.sweepLedger()
	_, openKept := q.ledger["open"]
	_, oldestKept := q.ledger["done-"+time.Duration(0).String()]
	_, newestKept := q.ledger["done-"+time.Duration(maxLedger+9).String()]
	n := len(q.ledger)
	q.mu.Unlock()
	if !openKept || oldestKept || !newestKept || n > maxLedger {
		t.Fatalf("open kept %v, oldest kept %v, newest kept %v, size %d", openKept, oldestKept, newestKept, n)
	}
	// and a repeat of the open id still answers the open request, not a new one
	r := submitAsync(q, "s1", "open", "x")
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	if take(t, open).Status != Accepted || take(t, r).Status != Accepted {
		t.Fatal("the repeat did not share the open request")
	}
}

// A caller that disconnects after the hand-out must not leave the session blocked: the lease expires on its own and the
// next request is handed out (codex R1). Mutation gate: drop the expiry in Next → red.
func TestNext_HandedRequestOfAGoneCallerExpires(t *testing.T) {
	q, _ := newQ(t)
	q.Wait = 2 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, _ = q.Submit(ctx, "s1", "c1", "first") }()
	j := next(t, q, "mod1", "s1") // handed out, never answered
	cancel()                      // the caller goes away
	ch := submitAsync(q, "s1", "c2", "second")
	if _, ok := q.Next(context.Background(), "mod1", "s1", 50*time.Millisecond); ok {
		t.Fatal("handed out while the first lease was still within its timeout")
	}
	time.Sleep(q.HandTimeout + 50*time.Millisecond)
	j2 := next(t, q, "mod1", "s1")
	if j2.Text != "second" {
		t.Fatalf("second job = %+v", j2)
	}
	q.Result("mod1", j2.ID, Outcome{Status: Accepted})
	take(t, ch)
	if err := q.Result("mod1", j.ID, Outcome{Status: Accepted}); !errors.Is(err, ErrNotLeased) {
		t.Fatalf("the expired job's late result: %v", err)
	}
}

// A caller that goes away does not cancel the request: it has been handed out or will be.
func TestSubmit_CallerGoneKeepsTheRequest(t *testing.T) {
	q, _ := newQ(t)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan Result, 1)
	go func() { r, _ := q.Submit(ctx, "s1", "c1", "x"); got <- r }()
	time.Sleep(30 * time.Millisecond)
	cancel()
	if r := <-got; r.Status != Unknown || r.Reason != "client_gone" {
		t.Fatalf("result = %+v", r)
	}
	j := next(t, q, "mod1", "s1") // still there for the mod
	q.Result("mod1", j.ID, Outcome{Status: Accepted})
	if r, _ := q.Submit(context.Background(), "s1", "c1", "x"); r.Status != Accepted {
		t.Fatalf("a repeat after the mod ran it = %+v", r)
	}
}
