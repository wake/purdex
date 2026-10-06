package teammod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
