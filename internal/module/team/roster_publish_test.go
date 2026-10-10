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

// rosterFlush returns once every rosterChanged signal sent before it has
// been published. With the publisher running (Start ran, Stop did not) it
// asks the publisher goroutine itself, through its barrier; otherwise no
// goroutine exists and it does what the goroutine would: one sync for a
// signal that is pending.
func (m *Module) rosterFlush() {
	if m.bootAt != 0 && !m.stopping() {
		done := make(chan struct{})
		m.rosterBarrier <- done
		<-done
		return
	}
	select {
	case <-m.rosterSig:
		m.rosterSync(true)
	default:
	}
}

// rosterWatch is a subscriber that keeps only the team.roster frames. Every
// read of it first flushes the publisher, so the events a test looks at
// include everything signalled so far.
type rosterWatch struct {
	t   *testing.T
	m   *Module
	sub *core.EventSubscriber
}

func (f *fixture) watchRoster() *rosterWatch {
	f.t.Helper()
	sub := f.core.Events.AddTestSubscriber()
	f.t.Cleanup(func() { f.core.Events.RemoveTestSubscriber(sub) })
	return &rosterWatch{t: f.t, m: f.m, sub: sub}
}

// drain is every team.roster event queued so far, in order.
func (w *rosterWatch) drain() []team.RosterEventValue {
	w.t.Helper()
	w.m.rosterFlush()
	var out []team.RosterEventValue
	for {
		select {
		case raw := <-w.sub.SendCh():
			var ev core.HostEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				w.t.Fatalf("decode HostEvent: %v", err)
			}
			if ev.Type != team.RosterEventType {
				continue
			}
			var v team.RosterEventValue
			if err := json.Unmarshal([]byte(ev.Value), &v); err != nil {
				w.t.Fatalf("decode RosterEventValue: %v", err)
			}
			out = append(out, v)
		default:
			return out
		}
	}
}

// one fails unless exactly one team.roster event was queued and it is a
// changed; it returns it.
func (w *rosterWatch) one(what string) team.RosterEventValue {
	w.t.Helper()
	evs := w.drain()
	if len(evs) != 1 || evs[0].Op != "changed" {
		w.t.Fatalf("%s: roster events = %+v, want exactly one changed", what, evs)
	}
	return evs[0]
}

// none fails if any team.roster event was queued.
func (w *rosterWatch) none(what string) {
	w.t.Helper()
	if evs := w.drain(); len(evs) != 0 {
		w.t.Fatalf("%s: roster events = %+v, want none", what, evs)
	}
}

// Rule 4: every new subscriber gets the snapshot through OnSubscribe — a
// real /ws/host-events connection, twice, each with the same roster.
func TestRoster_SnapshotToEveryNewSubscriber(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	seedMember(t, f.m.store, "op-1", uid(1), "sid-m1", 2000)
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(f.core.Events.HandleHostEvents))
	t.Cleanup(srv.Close)

	for i := 0; i < 2; i++ {
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var snap *team.RosterEventValue
		for snap == nil {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				t.Fatalf("subscriber %d never got a team.roster snapshot: %v", i, err)
			}
			var ev core.HostEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				t.Fatal(err)
			}
			if ev.Type != team.RosterEventType {
				continue
			}
			var v team.RosterEventValue
			if err := json.Unmarshal([]byte(ev.Value), &v); err != nil {
				t.Fatal(err)
			}
			snap = &v
		}
		if snap.Op != "snapshot" || len(snap.Teams) != 1 || snap.Teams[0].ID != uid(1) || len(snap.Teams[0].Members) != 1 {
			t.Fatalf("subscriber %d snapshot = %+v", i, snap)
		}
	}
}

// PL-1f′ rule 3: changed is BroadcastStrict. A subscriber that cannot take
// it is closed so it reconnects for the snapshot; one with room gets it.
// Mutation gate: plain Broadcast → red.
func TestRoster_ChangedIsStrict(t *testing.T) {
	captureEventsLog(t) // the hub logs the drop it is asked to make: expected output, not a failure (#2210)
	f := newFixture(t)
	w := f.watchRoster()
	full := f.core.Events.AddTestSubscriber()
	t.Cleanup(func() { f.core.Events.RemoveTestSubscriber(full) })
	for full.TrySend([]byte(`{"type":"fill"}`)) {
	}
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	f.m.rosterChanged()
	f.m.rosterFlush()
	select {
	case <-full.Done():
	default:
		t.Fatal("a subscriber that could not take the roster was kept without it")
	}
	w.one("the subscriber with room")
}

// A module with no events (a unit test's bare Module) announces nothing and
// does not panic, whether it is only signalled or also synced.
func TestRoster_ChangedWithoutEventsIsANoop(t *testing.T) {
	m := &Module{}
	m.rosterChanged()
	m.rosterSync(true)
}

// A-1: a snapshot that could not be built (the registry is unreadable)
// sends nothing and keeps the subscriber, but it marks the publisher
// "unsent", so the next sync — even of a roster that did not change —
// broadcasts the full roster as a changed. Without that the hash gate would
// keep an unchanged roster from this subscriber for good. Mutation gate:
// drop the unsent marking → red.
func TestRoster_SnapshotBuildFailureIsRepairedByTheNextSync(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	f.m.rosterSync(true) // the roster is sent, as it was before this subscriber
	w := f.watchRoster()

	f.origins.setReadErr(true)
	f.m.sendRosterSnapshot(w.sub)
	select {
	case <-w.sub.Done():
		t.Fatal("a snapshot that could not be built closed the subscriber")
	default:
	}
	w.none("a snapshot that could not be built")

	f.origins.setReadErr(false)
	f.m.rosterChanged() // the roster itself did not change
	ev := w.one("the next sync")
	if len(ev.Teams) != 1 || ev.Teams[0].ID != uid(1) {
		t.Fatalf("repair = %+v, want the full roster", ev.Teams)
	}
}

// blockResolver makes every roster build block inside the registry resolver
// until release is called (cleanup releases it too, so a failed test never
// hangs Stop). entered gets a value as each build gets there.
func (f *fixture) blockResolver() (entered chan struct{}, release func()) {
	f.t.Helper()
	entered = make(chan struct{}, 1000)
	block := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(block) }) }
	f.origins.mu.Lock()
	f.origins.batchHook = func() {
		entered <- struct{}{}
		<-block
	}
	f.origins.mu.Unlock()
	f.t.Cleanup(release)
	return entered, release
}

// returnsPromptly fails the test unless fn returns while the resolver is
// still blocked.
func returnsPromptly(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not return while the roster build was blocked in the registry resolver", what)
	}
}

func waitEntered(t *testing.T, entered chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the roster build never reached the resolver")
	}
}

// A-2: signals are absorbed while a build runs: a burst of any size costs
// at most one more build. Mutation gate: a signal per call → red.
func TestRosterPublisher_CoalescesBursts(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	builds := func() int {
		f.origins.mu.Lock()
		defer f.origins.mu.Unlock()
		return f.origins.batchCalls
	}
	f.m.rosterFlush()
	base := builds()
	entered, release := f.blockResolver()

	f.m.rosterChanged()
	waitEntered(t, entered)
	for i := 0; i < 200; i++ {
		f.m.rosterChanged()
	}
	release()
	f.m.rosterFlush()
	if n := builds() - base; n != 2 {
		t.Fatalf("1 signal + a burst of 200 while the first build ran cost %d builds, want 2", n)
	}
}

// A-2: the publisher is the module's: Stop joins it, so it never builds
// after Stop returns (the DB closes next), and Stop waits for a build in
// flight rather than abandoning it. Mutation gate: not in the WaitGroup →
// red.
func TestRosterPublisher_StopsWithTheModule(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(1), "sid-1", 1000)
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	entered, release := f.blockResolver()
	f.m.rosterChanged()
	waitEntered(t, entered)

	stopped := make(chan struct{})
	go func() {
		_ = f.m.Stop(context.Background())
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while the publisher was still inside a build")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop never returned after the build finished")
	}

	f.m.rosterChanged()
	select {
	case f.m.rosterBarrier <- make(chan struct{}):
		t.Fatal("the publisher is still serving after Stop")
	default:
	}
}
