package teammod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/team"
)

// readSnapshot connects a real WS subscriber to the fixture's broadcaster
// and returns the first approval.request frame it receives: OnSubscribe
// callbacks only run through HandleHostEvents, never for a test subscriber.
func readSnapshot(t *testing.T, f *fixture) team.EventValue {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.core.Events.HandleHostEvents))
	defer srv.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		var ev core.HostEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type != team.EventType {
			continue
		}
		var v team.EventValue
		if err := json.Unmarshal([]byte(ev.Value), &v); err != nil {
			t.Fatal(err)
		}
		if v.Op == "snapshot" && !strings.Contains(ev.Value, `"approvals":[`) {
			t.Fatalf("snapshot must carry an array: %s", ev.Value)
		}
		return v
	}
}

// Review Focus 3 / spec §9.2: on boot every open request's lease becomes
// max(lease_until, boot + 30 s), and each new subscriber gets one snapshot
// of the open set ([] when none).
func TestStart_ExtendsLeasesAndSnapshotsOpenRequests(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1)) // lease 1_030_000
	f.events()
	f.clock.Add(25_000) // boot at 1_025_000: grace → 1_055_000 > 1_030_000
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, _, _ := f.m.store.Get(uid(1))
	if a.LeaseUntil != 1_055_000 {
		t.Fatalf("lease after boot = %d, want 1055000 (boot + 30 s)", a.LeaseUntil)
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events broadcast by Start (the snapshot goes to new subscribers only)", n)
	}
	snap := readSnapshot(t, f)
	if snap.Op != "snapshot" || len(snap.Approvals) != 1 || snap.Approvals[0].ID != uid(1) || snap.Approvals[0].LeaseUntil != 1_055_000 {
		t.Fatalf("snapshot = %+v", snap)
	}
	f.do(http.MethodDelete, "/api/team/approvals/"+uid(1), nil)
	if snap := readSnapshot(t, f); snap.Op != "snapshot" || len(snap.Approvals) != 0 || snap.Approvals == nil {
		t.Fatalf("empty snapshot = %+v (approvals must be [] not null)", snap)
	}
	if err := f.m.Stop(context.Background()); err != nil { // joins the sweeper
		t.Fatal(err)
	}
}

// A lease already further out than the boot grace is left alone: the
// grace is max(lease_until, boot + 30 s), never a shortening.
func TestStart_DoesNotShortenALongerLease(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1)) // lease 1_030_000
	f.clock.Add(1)
	second := f.createReq(uid(2))
	second.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatal("second create")
	}
	if err := f.m.store.RenewLease(uid(2), 1_090_000); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(9_999) // boot at 1_010_000: grace 1_040_000
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := f.m.store.Get(uid(1)); a.LeaseUntil != 1_040_000 {
		t.Fatalf("short lease after boot = %d, want 1040000", a.LeaseUntil)
	}
	if a, _, _ := f.m.store.Get(uid(2)); a.LeaseUntil != 1_090_000 {
		t.Fatalf("long lease after boot = %d, want 1090000 (unchanged)", a.LeaseUntil)
	}
}

// The sweeper runs once Start ran and stops with Stop: a lease that ran
// out is abandoned without any test calling tick.
func TestStart_RunsTheSweeperUntilStop(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.events()
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(600_000) // past the deadline
	deadline := time.Now().Add(5 * time.Second)
	for {
		if a, _, _ := f.m.store.Get(uid(1)); a.State == team.StateTimeout {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sweeper did not close the overdue request within 5 s of Start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want 1", n)
	}
	stopped := make(chan struct{})
	go func() {
		_ = f.m.Stop(context.Background())
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not join the sweeper")
	}
}

// #1970: opened and closed are never dropped silently. A plain subscriber
// (opted into nothing) whose buffer is full is closed by the broadcast
// (Done, deregistered), so its client reconnects for the approval snapshot
// instead of running without the dialog's open or close; a subscriber with
// room still gets the frame. Mutation gate: broadcast with BroadcastEvent
// (best-effort) in Module.broadcast → red, for both ops.
func TestApprovalEvents_AreStrictForASubscriberThatCannotTakeThem(t *testing.T) {
	captureEventsLog(t) // the hub logs the drop it is asked to make: expected output, not a failure (#2210)
	for _, op := range []string{"opened", "closed"} {
		t.Run(op, func(t *testing.T) {
			f := newFixture(t)
			a := f.create(uid(1))
			f.events() // drain the fixture subscriber: it must have room
			full := f.core.Events.AddTestSubscriber()
			t.Cleanup(func() { f.core.Events.RemoveTestSubscriber(full) })
			for full.TrySend([]byte(`{"type":"fill"}`)) {
			}
			f.m.broadcast(op, &a)
			select {
			case <-full.Done():
			default:
				t.Fatalf("a subscriber that could not take %s was kept without it", op)
			}
			evs := f.events()
			if len(evs) != 1 || evs[0].Op != op {
				t.Fatalf("events on the subscriber with room = %+v, want one %s", evs, op)
			}
		})
	}
}

func TestSendSnapshot_FullBufferClosesSubscriber(t *testing.T) {
	f := newFixture(t)
	sub := f.core.Events.AddTestSubscriber()
	for i := 0; i < 64; i++ { // the send buffer is 64 deep
		sub.TrySend([]byte("x"))
	}
	f.m.sendSnapshot(sub)
	select {
	case <-sub.Done():
	default:
		t.Fatal("a subscriber that cannot take the snapshot must be removed so the client reconnects")
	}
}

// A subscriber whose snapshot read fails is removed (spec §6.2: every new
// subscriber gets the open set); keeping it would leave a client that never
// sees the requests open before it connected. A healthy store sends the
// snapshot and keeps the subscriber.
func TestSnapshot_ListOpenFailureDropsTheSubscriber(t *testing.T) {
	t.Run("store failing", func(t *testing.T) {
		f := newFixture(t)
		if err := f.m.store.Close(); err != nil {
			t.Fatal(err)
		}
		sub := f.core.Events.AddTestSubscriber()
		f.m.sendSnapshot(sub)
		select {
		case <-sub.Done():
		default:
			t.Fatal("a subscriber whose snapshot read failed must be removed so the client reconnects")
		}
		select {
		case data, ok := <-sub.SendCh():
			if ok {
				t.Fatalf("a frame was queued to a subscriber whose snapshot read failed: %s", data)
			}
		default:
		}
	})
	t.Run("store healthy", func(t *testing.T) {
		f := newFixture(t)
		sub := f.core.Events.AddTestSubscriber()
		defer f.core.Events.RemoveTestSubscriber(sub)
		f.m.sendSnapshot(sub)
		select {
		case <-sub.Done():
			t.Fatal("a subscriber that received its snapshot must be kept")
		default:
		}
		select {
		case data := <-sub.SendCh():
			if !strings.Contains(string(data), `\"op\":\"snapshot\"`) {
				t.Fatalf("first frame is not the snapshot: %s", data)
			}
		default:
			t.Fatal("no snapshot frame was queued")
		}
	})
}

// Close releases team.db (core.Closer, PD6): a store call afterwards fails.
func TestClose_ReleasesTheStore(t *testing.T) {
	f := newFixture(t)
	if err := f.m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.store.ListOpen(); err == nil {
		t.Fatal("ListOpen after Close must fail: the database is closed")
	}
	if err := f.m.Close(); err == nil {
		// database/sql reports a second Close as an error; either way it must not panic.
		t.Log("second Close returned nil")
	}
}

// dialEvents connects a real WS subscriber (OnSubscribe callbacks run for
// those only) and returns a reader of its approval.request frames, in
// order, plus a closer for the connection.
func dialEvents(t *testing.T, f *fixture) (read func() team.EventValue, closeConn func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.core.Events.HandleHostEvents))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		srv.Close()
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	read = func() team.EventValue {
		t.Helper()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("read event: %v", err)
			}
			var ev core.HostEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatal(err)
			}
			if ev.Type != team.EventType {
				continue
			}
			var v team.EventValue
			if err := json.Unmarshal([]byte(ev.Value), &v); err != nil {
				t.Fatal(err)
			}
			return v
		}
	}
	closeConn = func() {
		conn.Close()
		srv.Close()
	}
	return read, closeConn
}

// Review F1 / spec §6.2: a new subscriber's snapshot and the live
// opened/closed events are one ordered stream. The afterSnapshotRead hook
// pauses sendSnapshot between its ListOpen and its send; a create (then a
// DELETE) issued in that window must reach the subscriber after the
// snapshot — a client that replaces its set from the snapshot would
// otherwise lose the just-opened request, or revive the just-closed one.
// Without eventMu the event is queued first → red (mutation gate).
func TestSnapshot_IsOrderedWithEvents(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.events()
	var hookMu sync.Mutex
	var hook func()
	f.m.afterSnapshotRead = func() {
		hookMu.Lock()
		h := hook
		hookMu.Unlock()
		if h != nil {
			h()
		}
	}
	f.core.Events.OnSubscribe(f.m.sendSnapshot) // what Start does, without the sweeper

	// inWindow subscribes with act running inside sendSnapshot's window:
	// act is started, its store write is awaited (it precedes act's
	// broadcast), and the broadcast is then given 200 ms to be queued —
	// which the lock must prevent. Returns the first two frames.
	inWindow := func(act func(), landed func() bool) (first, second team.EventValue) {
		t.Helper()
		var wg sync.WaitGroup
		hookMu.Lock()
		hook = func() {
			wg.Add(1)
			go func() { defer wg.Done(); act() }()
			deadline := time.Now().Add(5 * time.Second)
			for !landed() {
				if time.Now().After(deadline) {
					t.Error("the store write inside the snapshot window did not land within 5 s")
					return
				}
				time.Sleep(time.Millisecond)
			}
			time.Sleep(200 * time.Millisecond)
		}
		hookMu.Unlock()
		read, closeConn := dialEvents(t, f)
		first, second = read(), read()
		closeConn()
		wg.Wait()
		return first, second
	}

	// A request opened inside the window: snapshot (without it), then opened.
	second := f.createReq(uid(2))
	second.OriginInbox = "/tmp/20.sock"
	first, next := inWindow(
		func() {
			if code, body := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
				t.Errorf("create in the window: %d %s", code, body)
			}
		},
		func() bool { _, ok, _ := f.m.store.Get(uid(2)); return ok },
	)
	if first.Op != "snapshot" || len(first.Approvals) != 1 || first.Approvals[0].ID != uid(1) {
		t.Fatalf("first frame = %+v, want the snapshot taken before the create (uid(1) only)", first)
	}
	if next.Op != "opened" || next.Approval == nil || next.Approval.ID != uid(2) {
		t.Fatalf("second frame = %+v, want opened uid(2) after the snapshot", next)
	}
	f.events()

	// A request closed inside the window: snapshot (still carrying it open), then closed.
	first, next = inWindow(
		func() {
			if code, body := f.do(http.MethodDelete, "/api/team/approvals/"+uid(1), nil); code != 200 {
				t.Errorf("delete in the window: %d %s", code, body)
			}
		},
		func() bool { a, _, _ := f.m.store.Get(uid(1)); return a.State != team.StateOpen },
	)
	if first.Op != "snapshot" || len(first.Approvals) != 2 || first.Approvals[0].ID != uid(1) || first.Approvals[0].State != team.StateOpen {
		t.Fatalf("first frame = %+v, want the snapshot taken before the delete (uid(1) open, uid(2))", first)
	}
	if next.Op != "closed" || next.Approval == nil || next.Approval.ID != uid(1) || next.Approval.State != team.StateCancelled {
		t.Fatalf("second frame = %+v, want closed uid(1) after the snapshot", next)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want exactly 1", n)
	}
}
