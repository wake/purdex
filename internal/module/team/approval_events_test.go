package teammod

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// waitOps waits until the collector has n ops (the hub delivers on its own goroutine).
func waitOps(t *testing.T, c *collector, n int) []feedOp {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.got(); len(got) >= n {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("got %d ops, want %d", len(c.got()), n)
	return nil
}

func TestSubscribeApprovals_OpenedAndClosedReachTheSubscriber(t *testing.T) {
	f := newFixture(t)
	c := &collector{}
	open, unsub := f.m.SubscribeApprovals(c.fn)
	defer unsub()
	if len(open) != 0 {
		t.Fatalf("open = %+v", open)
	}
	a := f.createFrom(uid(1), "/tmp/10.sock")
	f.do(http.MethodDelete, "/api/team/approvals/"+a.ID, nil)
	ops := waitOps(t, c, 2)
	if ops[0].op != "opened" || ops[0].a.ID != a.ID || ops[1].op != "closed" || ops[1].a.ID != a.ID {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestSubscribeApprovals_SnapshotHasEveryOpenApprovalOfEverySession(t *testing.T) {
	f := newFixture(t)
	a := f.createFrom(uid(1), "/tmp/10.sock")
	b := f.createFrom(uid(2), "/tmp/20.sock")
	open, unsub := f.m.SubscribeApprovals((&collector{}).fn)
	defer unsub()
	ids := map[string]bool{}
	for _, x := range open {
		ids[x.ID] = true
	}
	if len(open) != 2 || !ids[a.ID] || !ids[b.ID] {
		t.Fatalf("open = %+v", open)
	}
}

// A callback that never returns must not slow the approval stream: broadcast only does a non-blocking send.
func TestSubscribeApprovals_ABlockedCallbackDoesNotSlowBroadcast(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	defer close(release)
	var started sync.Once
	entered := make(chan struct{})
	_, unsub := f.m.SubscribeApprovals(func(string, team.Approval) {
		started.Do(func() { close(entered) })
		<-release // never returns during the test
	})
	defer unsub()
	first := f.createFrom(uid(1), "/tmp/10.sock")
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the callback never ran")
	}
	f.do(http.MethodDelete, "/api/team/approvals/"+first.ID, nil) // a session may have one open request at a time
	// the callback is stuck on the first op; many more approvals must still open and close quickly
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 2; i < 2+approvalSubQueue+20; i++ {
			a := f.createFrom(uid(i), "/tmp/10.sock")
			f.do(http.MethodDelete, "/api/team/approvals/"+a.ID, nil)
		}
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("a stuck subscriber slowed the approval stream")
	}
	if f.m.ApprovalEventDrops() == 0 {
		t.Fatal("overflow past the bound must be counted as drops")
	}
}

// open snapshot + subscription are one step under eventMu: an approval opened or closed concurrently with the call is in
// the list in its final state, or its op follows - never lost, never listed open without the closed to follow.
func TestSubscribeApprovals_NoGapBetweenSnapshotAndSubscription(t *testing.T) {
	f := newFixture(t)
	a := f.createFrom(uid(1), "/tmp/10.sock")
	c := &collector{}
	var wg sync.WaitGroup
	f.m.afterSnapshotRead = func() { // between the read of the open set and the arming
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.do(http.MethodDelete, "/api/team/approvals/"+a.ID, nil) // blocks on eventMu until the call returns
		}()
		time.Sleep(50 * time.Millisecond)
	}
	open, unsub := f.m.SubscribeApprovals(c.fn)
	defer unsub()
	f.m.afterSnapshotRead = nil
	wg.Wait()
	listed := len(open) == 1 && open[0].ID == a.ID
	if !listed {
		t.Fatalf("open = %+v, want the approval listed open (the close was held behind the subscription)", open)
	}
	ops := waitOps(t, c, 1)
	if ops[0].op != "closed" || ops[0].a.ID != a.ID {
		t.Fatalf("ops = %+v, want the closed op to follow", ops)
	}
}

func TestSubscribeApprovals_UnsubscribeStopsDeliveryAndIsIdempotent(t *testing.T) {
	f := newFixture(t)
	c := &collector{}
	_, unsub := f.m.SubscribeApprovals(c.fn)
	unsub()
	unsub()
	f.createFrom(uid(1), "/tmp/10.sock")
	time.Sleep(100 * time.Millisecond)
	if got := c.got(); len(got) != 0 {
		t.Fatalf("delivered after unsubscribe: %+v", got)
	}
}

// A callback may unsubscribe itself (it runs on the subscriber's goroutine, not under eventMu).
func TestSubscribeApprovals_ACallbackMayUnsubscribe(t *testing.T) {
	f := newFixture(t)
	var unsub func()
	done := make(chan struct{})
	_, unsub = f.m.SubscribeApprovals(func(op string, a team.Approval) {
		unsub()
		close(done)
	})
	f.createFrom(uid(1), "/tmp/10.sock")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("unsubscribing from the callback deadlocked")
	}
}

func TestSubscribeApprovals_APanickingCallbackDoesNotStopTheStream(t *testing.T) {
	f := newFixture(t)
	c := &collector{}
	n := 0
	_, unsub := f.m.SubscribeApprovals(func(op string, a team.Approval) {
		n++
		if n == 1 {
			panic("boom")
		}
		c.fn(op, a)
	})
	defer unsub()
	f.createFrom(uid(1), "/tmp/10.sock")
	f.createFrom(uid(2), "/tmp/20.sock")
	if ops := waitOps(t, c, 1); ops[0].a.Origin.SessionID != "sid-2" {
		t.Fatalf("ops = %+v", ops)
	}
}
