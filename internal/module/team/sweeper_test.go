package teammod

import (
	"net/http"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

func TestTick_DeadlineBecomesTimeout(t *testing.T) {
	f := newFixture(t)
	req := f.createReq(uid(1))
	req.WaitS = 60
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", req); code != 201 {
		t.Fatal("create")
	}
	f.events()
	f.clock.Add(59_999)
	f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=0", nil) // the CLI's poll renews the 30 s lease
	f.m.tick()
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("closed before the deadline: %s", a.State)
	}
	f.clock.Add(1)
	f.m.tick()
	a, _, _ := f.m.store.Get(uid(1))
	if a.State != team.StateTimeout || a.DecidedAt != 1_060_000 || a.DecidedBy != nil || a.Grant != nil {
		t.Fatalf("after deadline: %+v", a)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d", n)
	}
	f.m.tick() // nothing open: no second event
	if n := f.countOps("closed"); n != 0 {
		t.Fatalf("extra closed events = %d", n)
	}
}

func TestTick_LeaseExpiryBecomesAbandoned(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1)) // lease now+30 s, deadline now+540 s
	f.events()
	f.clock.Add(29_999)
	f.m.tick()
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("closed before the lease ran out: %s", a.State)
	}
	f.clock.Add(1)
	f.m.tick()
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateAbandoned || a.DecidedAt != 1_030_000 || a.DecidedBy != nil {
		t.Fatalf("after lease expiry: %+v", a)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d", n)
	}
}

// TestTick_TimeoutWinsOverLeaseWhenBothPassed: a row whose deadline and
// lease both passed closes as timeout (U7: the user did not answer), not
// abandoned; the sweeper checks the deadline first.
func TestTick_TimeoutWinsOverLeaseWhenBothPassed(t *testing.T) {
	f := newFixture(t)
	req := f.createReq(uid(1))
	req.WaitS = 60
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", req); code != 201 {
		t.Fatal("create")
	}
	f.events()
	f.clock.Add(120_000)
	f.m.tick()
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateTimeout {
		t.Fatalf("both passed: %+v, want timeout", a)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d", n)
	}
}

func TestTick_OriginGoneIsCheckedEveryTenthTick(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.events()
	f.origins.markDead("sid-1")
	for i := 1; i <= 9; i++ {
		f.m.tick()
		if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
			t.Fatalf("tick %d: liveness must only run on the 10th tick, got %s", i, a.State)
		}
	}
	f.m.tick()
	a, _, _ := f.m.store.Get(uid(1))
	if a.State != team.StateAbandoned || a.DecidedAt != 1_000_000 || a.DecidedBy != nil {
		t.Fatalf("10th tick: %+v", a)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d", n)
	}
}

// TestTick_LiveOriginIsKeptOnTheTenthTick: the liveness check abandons
// only what the resolver reports dead; a live session (and, by the
// resolver's contract, a registry it could not read) is left open.
func TestTick_LiveOriginIsKeptOnTheTenthTick(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	second := f.createReq(uid(2))
	second.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatal("second create")
	}
	f.events()
	f.origins.markDead("sid-2")
	for i := 0; i < 10; i++ {
		f.m.tick()
	}
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("live origin's row = %s, want open", a.State)
	}
	if a, _, _ := f.m.store.Get(uid(2)); a.State != team.StateAbandoned {
		t.Fatalf("dead origin's row = %s, want abandoned", a.State)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want 1", n)
	}
}

// Review Focus 4: a decide, the sweeper past the deadline and the
// requester's DELETE all race for one close. Exactly one wins, the row
// says which, every loser is answered with the winner's row (decided_by
// included when a decide won), and exactly one closed event is broadcast.
// Run with -race.
func TestTick_DecideSweeperAndCancelCloseOnce(t *testing.T) {
	f := newFixture(t)
	req := f.createReq(uid(1))
	req.WaitS = 60
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", req); code != 201 {
		t.Fatal("create")
	}
	f.events()
	f.clock.Add(60_000)
	f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=0", nil) // keep the lease alive: the race is deadline vs decide vs cancel
	client := team.Client{Kind: "app", Label: "Purdex.app @ air26"}
	var wg sync.WaitGroup
	var decideCode, deleteCode int
	var decideBody, deleteBody []byte
	start := make(chan struct{})
	wg.Add(3)
	go func() { defer wg.Done(); <-start; f.m.tick() }()
	go func() {
		defer wg.Done()
		<-start
		decideCode, decideBody = f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide",
			team.DecideRequest{Decision: "approve", Client: client})
	}()
	go func() {
		defer wg.Done()
		<-start
		deleteCode, deleteBody = f.do(http.MethodDelete, "/api/team/approvals/"+uid(1), nil)
	}()
	close(start)
	wg.Wait()

	final, _, _ := f.m.store.Get(uid(1))
	if final.State == team.StateOpen || final.DecidedAt != 1_060_000 {
		t.Fatalf("row after three closes: %+v", final)
	}
	// Each competitor's own answer says whether it won; exactly one may.
	decideWon := decideCode == 200
	sweeperWon := final.State == team.StateTimeout
	if deleteCode != 200 {
		t.Fatalf("delete: %d %s (DELETE answers the row as it is, won or lost)", deleteCode, deleteBody)
	}
	deleted := decodeApproval(t, deleteBody)
	cancelWon := deleted.State == team.StateCancelled
	wins := 0
	for _, w := range []bool{decideWon, sweeperWon, cancelWon} {
		if w {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("winners = %d (decide=%v sweeper=%v cancel=%v), want exactly one; row %s", wins, decideWon, sweeperWon, cancelWon, final.State)
	}
	switch {
	case decideWon && (final.State != team.StateApproved || final.DecidedBy == nil || final.DecidedBy.Label != client.Label):
		t.Fatalf("decide won but row is %+v", final)
	case cancelWon && (final.State != team.StateCancelled || final.DecidedBy != nil):
		t.Fatalf("cancel won but row is %+v", final)
	case sweeperWon && final.DecidedBy != nil:
		t.Fatalf("sweeper won but row carries decided_by: %+v", final)
	}
	// Losers carry the full winner row: the decide loser inside its 409
	// already_decided, the DELETE loser as its 200 body.
	if !decideWon {
		e := decodeErr(t, decideBody)
		if decideCode != 409 || e.Error != team.ErrAlreadyDecided || e.Approval == nil ||
			e.Approval.ID != uid(1) || e.Approval.State != final.State || e.Approval.DecidedAt != final.DecidedAt {
			t.Fatalf("decide lost: %d %s, want 409 already_decided carrying the %s row", decideCode, decideBody, final.State)
		}
	}
	if !cancelWon {
		if deleted.ID != uid(1) || deleted.State != final.State || deleted.DecidedAt != final.DecidedAt {
			t.Fatalf("delete lost: %s, want the %s row", deleteBody, final.State)
		}
		if decideWon && (deleted.DecidedBy == nil || deleted.DecidedBy.Label != client.Label) {
			t.Fatalf("delete lost to a decide but its body has no decided_by: %s", deleteBody)
		}
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want exactly 1 (final state %s)", n, final.State)
	}
}

// The loser shapes, deterministically (the race above shows one ordering
// per run): a DELETE after a decide answers 200 with the approved row,
// decided_by and grant included; a decide after a DELETE answers 409
// already_decided with the cancelled row; the sweeper past both deadlines
// changes neither; and each request broadcast closed exactly once.
func TestClose_LosersCarryTheWinnerRow(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.clock.Add(1)
	second := f.createReq(uid(2))
	second.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatal("second create")
	}
	f.events()
	client := team.Client{Kind: "app", Label: "Purdex.app @ air26"}

	// uid(1): decide wins, DELETE loses.
	if code, _ := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", team.DecideRequest{Decision: "approve", Client: client}); code != 200 {
		t.Fatalf("decide uid(1): %d", code)
	}
	code, body := f.do(http.MethodDelete, "/api/team/approvals/"+uid(1), nil)
	if a := decodeApproval(t, body); code != 200 || a.State != team.StateApproved || a.DecidedBy == nil || a.DecidedBy.Label != client.Label || a.Grant == nil {
		t.Fatalf("delete after decide: %d %s (want the approved row with decided_by and grant)", code, body)
	}

	// uid(2): DELETE wins, decide loses.
	if code, body := f.do(http.MethodDelete, "/api/team/approvals/"+uid(2), nil); code != 200 || decodeApproval(t, body).State != team.StateCancelled {
		t.Fatalf("delete uid(2): %d %s", code, body)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+uid(2)+"/decide", team.DecideRequest{Decision: "deny", Client: client})
	e := decodeErr(t, body)
	if code != 409 || e.Error != team.ErrAlreadyDecided || e.Approval == nil || e.Approval.ID != uid(2) || e.Approval.State != team.StateCancelled || e.Approval.DecidedBy != nil {
		t.Fatalf("decide after delete: %d %s (want 409 already_decided carrying the cancelled row)", code, body)
	}

	// The sweeper past both deadlines (540 s) finds nothing open.
	f.clock.Add(600_000)
	f.m.tick()
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateApproved {
		t.Fatalf("sweeper changed a closed row: %+v", a)
	}
	if a, _, _ := f.m.store.Get(uid(2)); a.State != team.StateCancelled {
		t.Fatalf("sweeper changed a closed row: %+v", a)
	}
	if n := f.countOps("closed"); n != 2 {
		t.Fatalf("closed events = %d, want exactly 2 (one per request)", n)
	}
}

// TestTick_WakesTheLongPoll: a sweeper close releases the requester's poll
// with the closed row, like decide and DELETE do (all through closeAs).
func TestTick_WakesTheLongPoll(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	done := make(chan team.Approval, 1)
	go func() {
		_, body := f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=20", nil)
		done <- decodeApproval(t, body)
	}()
	waitForWaiter(t, f, uid(1))
	f.clock.Add(600_000) // past the deadline
	f.m.tick()
	a := <-done
	if a.State != team.StateTimeout {
		t.Fatalf("long-poll woke with %+v, want timeout", a)
	}
}

// Review F2 / spec §9.2: the sweeper decides on a copy of the row; a poll
// that renews the lease between that read and the close must win. The
// afterListOpen hook renews in that window: the close's CAS requires the
// lease to still be expired, so it loses, the row stays open, nothing is
// broadcast, and the next tick after the renewed lease runs out abandons
// it. With the unconditional CloseIfOpen the renewal is lost → red.
func TestTick_RenewBetweenReadAndCloseKeepsTheRequestOpen(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1)) // lease 1_030_000, deadline 1_540_000
	f.events()
	f.clock.Add(30_000) // the lease has just run out
	renewed := 0
	f.m.afterListOpen = func() {
		renewed++
		// The CLI's poll, landing after the sweeper's read.
		if code, body := f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=0", nil); code != 200 {
			t.Errorf("poll in the window: %d %s", code, body)
		}
	}
	f.m.tick()
	if renewed != 1 {
		t.Fatalf("hook ran %d times, want 1", renewed)
	}
	a, _, _ := f.m.store.Get(uid(1))
	if a.State != team.StateOpen || a.LeaseUntil != 1_060_000 {
		t.Fatalf("after a renewal inside the sweeper's window: %+v, want open with lease 1060000", a)
	}
	if n := f.countOps("closed"); n != 0 {
		t.Fatalf("closed events = %d, want 0 (the request is still open)", n)
	}
	// Still open on the renewed lease; abandoned once that one runs out too.
	f.m.afterListOpen = nil
	f.clock.Add(29_999)
	f.m.tick()
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("closed before the renewed lease ran out: %s", a.State)
	}
	f.clock.Add(1)
	f.m.tick()
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateAbandoned || a.DecidedAt != 1_060_000 {
		t.Fatalf("after the renewed lease ran out: %+v", a)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want 1", n)
	}
}
