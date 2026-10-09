package teammod

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

type feedOp struct {
	op string
	a  team.Approval
}

// collector is an fn that only enqueues, as the interface demands.
type collector struct {
	mu  sync.Mutex
	ops []feedOp
}

func (c *collector) fn(op string, a team.Approval) {
	c.mu.Lock()
	c.ops = append(c.ops, feedOp{op, a})
	c.mu.Unlock()
}

func (c *collector) got() []feedOp {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]feedOp(nil), c.ops...)
}

// createFrom opens a lead request from the given inbox (sid-1 = /tmp/10.sock, sid-2 = /tmp/20.sock).
func (f *fixture) createFrom(id, inbox string) team.Approval {
	f.t.Helper()
	f.createReqEdit = func(r *team.CreateApprovalRequest) { r.OriginInbox = inbox }
	defer func() { f.createReqEdit = nil }()
	return f.create(id)
}

func TestSubscribeSession_ListsOnlyThisSessionsOpenApprovals(t *testing.T) {
	f := newFixture(t)
	mine := f.createFrom(uid(1), "/tmp/10.sock")
	f.createFrom(uid(2), "/tmp/20.sock")
	open, cancel, err := f.m.SubscribeSession("sid-1", (&collector{}).fn)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if len(open) != 1 || open[0].ID != mine.ID {
		t.Fatalf("open = %+v, want only %s", open, mine.ID)
	}
	if open2, cancel2, _ := f.m.SubscribeSession("nobody", (&collector{}).fn); len(open2) != 0 || open2 == nil {
		t.Fatalf("a session with none must get an empty list, got %#v", open2)
	} else {
		cancel2()
	}
}

func TestSubscribeSession_OpsForThisSessionOnlyNotTheLineage(t *testing.T) {
	f := newFixture(t)
	c := &collector{}
	_, cancel, err := f.m.SubscribeSession("sid-1", c.fn)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	other := f.createFrom(uid(2), "/tmp/20.sock") // another session id (e.g. the relayed successor)
	mine := f.createFrom(uid(1), "/tmp/10.sock")
	f.do(http.MethodDelete, "/api/team/approvals/"+other.ID, nil)
	f.do(http.MethodDelete, "/api/team/approvals/"+mine.ID, nil)
	ops := c.got()
	if len(ops) != 2 || ops[0].op != "opened" || ops[0].a.ID != mine.ID || ops[1].op != "closed" || ops[1].a.ID != mine.ID {
		t.Fatalf("ops = %+v, want opened then closed of %s only", ops, mine.ID)
	}
}

// An approval closed while the subscription is being made is never left open on the client: either it is not in the
// list, or it is and its closed op follows. (Same guarantee as the host-events snapshot; mutation: list and arm
// without eventMu → red.)
func TestSubscribeSession_ClosedDuringConnectIsNeverShownOpen(t *testing.T) {
	f := newFixture(t)
	a := f.createFrom(uid(1), "/tmp/10.sock")
	c := &collector{}
	var wg sync.WaitGroup
	f.m.afterSnapshotRead = func() {
		wg.Add(1)
		go func() { // the close lands in the window between the list and the arming
			defer wg.Done()
			f.do(http.MethodDelete, "/api/team/approvals/"+a.ID, nil)
		}()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) { // its store write happens before its broadcast, which waits for eventMu
			if got, ok, _ := f.m.store.Get(a.ID); ok && got.State != team.StateOpen {
				break
			}
			time.Sleep(time.Millisecond)
		}
		time.Sleep(100 * time.Millisecond)
	}
	open, cancel, err := f.m.SubscribeSession("sid-1", c.fn)
	f.m.afterSnapshotRead = nil
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	wg.Wait()
	listedOpen := len(open) == 1
	closedFollowed := false
	for _, o := range c.got() {
		if o.op == "closed" && o.a.ID == a.ID {
			closedFollowed = true
		}
	}
	if listedOpen && !closedFollowed {
		t.Fatalf("listed open (%+v) with no closed op to follow: the client would show a closed approval", open)
	}
}

func TestSubscribeSession_CancelStopsDeliveryAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	c := &collector{}
	_, cancel, err := f.m.SubscribeSession("sid-1", c.fn)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	cancel()
	f.createFrom(uid(1), "/tmp/10.sock")
	if ops := c.got(); len(ops) != 0 {
		t.Fatalf("ops after cancel: %+v", ops)
	}
	f.m.eventMu.Lock() // cancel never leaves a subscription behind
	n := f.m.sessionSubN + len(f.m.sessionSubs)
	f.m.eventMu.Unlock()
	if n != 0 {
		t.Fatalf("%d subscriptions left", n)
	}
}

func TestSubscribeSession_StoreFailureIsAnError(t *testing.T) {
	f := newFixture(t)
	if err := f.m.store.Close(); err != nil {
		t.Fatal(err)
	}
	open, cancel, err := f.m.SubscribeSession("sid-1", (&collector{}).fn)
	if err == nil || open != nil || cancel != nil {
		t.Fatalf("open %v cancel %v err %v: a failed read must be an error, never an empty set", open, cancel != nil, err)
	}
}

// A held responder alone makes a terminal-only ask create its row, exactly as a connected /ws/host-events client does,
// and releasing (any number of times) takes it away again.
func TestHoldResponder_AloneLetsATerminalAskCreateARow(t *testing.T) {
	f := newFixture(t)
	f.core.Events.RemoveTestSubscriber(f.sub) // no host-events client
	defer func() { f.sub = f.core.Events.AddTestSubscriber() }()

	code, _ := f.do(http.MethodPost, "/api/ask/begin", askBeginBody("sid-1", "toolu_a"))
	if code != http.StatusConflict {
		t.Fatalf("no responder: %d, want 409", code)
	}
	release := f.m.HoldResponder()
	code, body := f.do(http.MethodPost, "/api/ask/begin", askBeginBody("sid-1", "toolu_b"))
	if code != http.StatusCreated {
		t.Fatalf("with a held responder: %d %s, want a row", code, body)
	}
	release()
	release() // idempotent
	code, _ = f.do(http.MethodPost, "/api/ask/begin", askBeginBody("sid-1", "toolu_c"))
	if code != http.StatusConflict {
		t.Fatalf("after release: %d, want 409 again", code)
	}
}

// Two holders: one release (twice) must not drop the other's hold, and the count returns to zero when both are gone.
func TestHoldResponder_CountsAndNeverGoesNegative(t *testing.T) {
	f := newFixture(t)
	r1, r2 := f.m.HoldResponder(), f.m.HoldResponder()
	r1()
	r1()
	if got := f.m.responderHolds.Load(); got != 1 {
		t.Fatalf("holds = %d after a double release of one of two", got)
	}
	r2()
	r2()
	if got := f.m.responderHolds.Load(); got != 0 {
		t.Fatalf("holds = %d, want 0", got)
	}
}

// Session ids are opaque and case-sensitive: an id that differs only by case is another session.
func TestSubscribeSession_SessionIdsAreCaseSensitive(t *testing.T) {
	f := newFixture(t)
	f.createFrom(uid(1), "/tmp/10.sock") // sid-1
	c := &collector{}
	open, cancel, err := f.m.SubscribeSession("SID-1", c.fn)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	f.do(http.MethodDelete, "/api/team/approvals/"+uid(1), nil) // a closed op of sid-1
	if len(open) != 0 || len(c.got()) != 0 {
		t.Fatalf("SID-1 received sid-1's approvals: open %+v ops %+v", open, c.got())
	}
}

// A callback that panics is dropped and the others still get every op; the host's approval stream goes on.
func TestSubscribeSession_APanickingCallbackDoesNotStopTheOthers(t *testing.T) {
	f := newFixture(t)
	bad := func(string, team.Approval) { panic("consumer bug") }
	good := &collector{}
	for _, fn := range []func(string, team.Approval){bad, good.fn, bad} {
		if _, _, err := f.m.SubscribeSession("sid-1", fn); err != nil {
			t.Fatal(err)
		}
	}
	a := f.createFrom(uid(1), "/tmp/10.sock")
	f.do(http.MethodDelete, "/api/team/approvals/"+a.ID, nil)
	if ops := good.got(); len(ops) != 2 {
		t.Fatalf("the healthy subscription saw %d ops, want opened and closed", len(ops))
	}
	f.m.eventMu.Lock()
	n := f.m.sessionSubN
	f.m.eventMu.Unlock()
	if n != 1 {
		t.Fatalf("%d subscriptions left, want only the healthy one", n)
	}
}

func TestSubscribeSession_IsBoundedAndStopClearsIt(t *testing.T) {
	f := newFixture(t)
	var cancels []func()
	for i := 0; i < maxSessionSubs; i++ {
		_, cancel, err := f.m.SubscribeSession("sid-1", (&collector{}).fn)
		if err != nil {
			t.Fatalf("subscription %d: %v", i, err)
		}
		cancels = append(cancels, cancel)
	}
	if _, _, err := f.m.SubscribeSession("sid-1", (&collector{}).fn); !errors.Is(err, ErrTooManySubscriptions) {
		t.Fatalf("over the bound: %v", err)
	}
	cancels[0]()
	if _, cancel, err := f.m.SubscribeSession("sid-1", (&collector{}).fn); err != nil {
		t.Fatalf("after a cancel there is room again: %v", err)
	} else {
		cancel()
	}
	if err := f.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.m.eventMu.Lock()
	n := f.m.sessionSubN
	f.m.eventMu.Unlock()
	if n != 0 {
		t.Fatalf("%d subscriptions survive Stop", n)
	}
	for _, c := range cancels { // late cancels after Stop are harmless
		c()
	}
	f.m.eventMu.Lock()
	n = f.m.sessionSubN
	f.m.eventMu.Unlock()
	if n != 0 {
		t.Fatalf("count %d after late cancels", n)
	}
}
