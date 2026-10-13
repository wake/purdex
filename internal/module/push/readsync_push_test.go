package push

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/push/apns"
	"github.com/wake/purdex/internal/reads"
	"github.com/wake/purdex/internal/team"
)

// BG-4b: the reads module's feed → pending per read_sync registration → carried by the next push of any kind → acknowledged
// by version; flushed after 15 minutes of silence; dropped on every path that removes a registration.

type fakeReadsFeed struct {
	mu          sync.Mutex
	fn          func(reads.Read)
	subscribed  int
	unsubscribe int
}

func (f *fakeReadsFeed) SubscribeReads(fn func(reads.Read)) func() {
	f.mu.Lock()
	f.fn = fn
	f.subscribed++
	f.mu.Unlock()
	return func() { f.mu.Lock(); f.fn = nil; f.unsubscribe++; f.mu.Unlock() }
}

func (f *fakeReadsFeed) publish(rd reads.Read) {
	f.mu.Lock()
	fn := f.fn
	f.mu.Unlock()
	if fn != nil {
		fn(rd)
	}
}

type rsPushEnv struct {
	*agentEnv
	feed  *fakeReadsFeed
	clock *rsClock
}

// newRSPushEnv is newAgentEnv with the reads feed and the flush timers replaced by fakes, before Start.
func newRSPushEnv(t *testing.T) *rsPushEnv {
	t.Helper()
	e := newEnv(t)
	clock := &fakeClock{now: time.Unix(5_000_000, 0)}
	te := &triggerEnv{env: e, apns: &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}, events: &fakeEvents{}}
	feed := &fakeNotifyFeed{}
	e.mod.events, e.mod.notify = te.events, feed
	e.mod.newAPNs = func() apnsClient { return te.apns }
	e.mod.pres = NewPresence(clock.Now)
	e.mod.gate = NewGate(clock.Now)
	e.mod.asks = newOpenAsks(clock.Now)
	e.mod.holdFor = time.Hour
	rf, rc := &fakeReadsFeed{}, newRSClock()
	e.mod.readsFeed = rf
	e.mod.rs.after, e.mod.rs.now = rc.after, rc.Now
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.mod.Stop(context.Background()) })
	return &rsPushEnv{agentEnv: &agentEnv{triggerEnv: te, feed: feed, clock: clock}, feed: rf, clock: rc}
}

func readSyncDevice(tabs ...string) func(*push.DeviceRequest) {
	return func(r *push.DeviceRequest) { r.Prefs = push.Prefs{Tabs: tabs, ReadSync: true} }
}

func (e *rsPushEnv) read(code string, seq uint64) {
	e.feed.publish(reads.Read{Code: code, Through: reads.Cursor{Epoch: "boot-1", Seq: seq}})
}

func (e *rsPushEnv) feed2() *fakeNotifyFeed { return e.agentEnv.feed }

func (f *fakeAPNS) snapshotCalls() []sendCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sendCall(nil), f.calls...)
}

// noSendsMoreThan waits a moment and fails if more than n pushes were sent.
func (e *rsPushEnv) noSendsMoreThan(t *testing.T, n int) {
	t.Helper()
	time.Sleep(150 * time.Millisecond)
	if got := e.apns.count(); got > n {
		t.Fatalf("%d pushes sent, want no more than %d", got, n)
	}
}

func purdexOfCall(t *testing.T, c sendCall) map[string]any { return purdexOf(t, c.Payload) }

func readsOfCall(t *testing.T, c sendCall) []reads.Read {
	t.Helper()
	var p struct {
		Purdex struct {
			Reads []reads.Read `json:"reads"`
		} `json:"purdex"`
	}
	if err := json.Unmarshal([]byte(c.Payload), &p); err != nil {
		t.Fatal(err)
	}
	return p.Purdex.Reads
}

// The next push of each kind to a read_sync phone carries the pending reads, and the plain phone gets none (and keeps none).
// Mutation gate: attach only to agent pushes / attach to every phone → red.
func TestReadSyncPush_RidesEveryKindOfPushToReadSyncDevicesOnly(t *testing.T) {
	e := newRSPushEnv(t)
	e.device(tokA, "en", "mlab", readSyncDevice("c1"))
	e.device(tokB, "en", "mlab", tabsOf("c1")) // does not sync reads
	devA, devB := push.DeviceID(tokA), push.DeviceID(tokB)

	e.read("r1", 1)
	if e.mod.rs.Pending(devA) != 1 || e.mod.rs.Pending(devB) != 0 {
		t.Fatalf("pending: A %d B %d", e.mod.rs.Pending(devA), e.mod.rs.Pending(devB))
	}
	e.feed2().emit(nev("c1", "PdxStop", "idle", stopDetail("done"))) // an agent push
	byToken := map[string]sendCall{}
	for _, c := range e.waitSends(t, 2) {
		byToken[c.Token] = c
	}
	if got := readsOfCall(t, byToken[tokA]); len(got) != 1 || got[0].Code != "r1" {
		t.Fatalf("A's agent push reads = %v", got)
	}
	if got := readsOfCall(t, byToken[tokB]); len(got) != 0 {
		t.Fatalf("B's agent push reads = %v", got)
	}

	e.read("r2", 1)
	e.events.emit("opened", leadApproval("ap1")) // an approval push
	var lead sendCall
	for _, c := range e.waitSends(t, 4)[2:] {
		if c.Token == tokA {
			lead = c
		}
	}
	if got := readsOfCall(t, lead); len(got) != 1 || got[0].Code != "r2" {
		t.Fatalf("A's approval push reads = %v (r1 was acknowledged by the first push)", got)
	}

	e.read("r3", 1)
	e.device(tokA, "en", "mlab", func(r *push.DeviceRequest) {
		r.Prefs = push.Prefs{Tabs: []string{"c1"}, ReadSync: true, BadgePush: true}
	})
	e.mod.closes.window = 20 * time.Millisecond
	e.events.readOpen = func() ([]team.Approval, error) { return nil, nil }
	e.events.emit("closed", closedCard("cy", team.KindLead)) // a background badge push
	var badge sendCall
	deadline := time.Now().Add(3 * time.Second)
	for badge.Payload == "" && time.Now().Before(deadline) {
		for _, c := range e.apns.snapshotCalls() {
			if c.Token == tokA && c.H.PushType == "background" {
				badge = c
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := readsOfCall(t, badge); len(got) == 0 {
		t.Fatalf("the background badge push did not carry the pending read: %s", badge.Payload)
	}
}

// 20 pending → the 16 oldest go, 4 stay for the next push.
func TestReadSyncPush_TwentyPendingSixteenGoFourStay(t *testing.T) {
	e := newRSPushEnv(t)
	e.device(tokA, "en", "mlab", readSyncDevice("c1"))
	for i := 1; i <= 20; i++ {
		e.read(fmt.Sprintf("r%02d", i), uint64(i))
	}
	e.feed2().emit(nev("c1", "PdxStop", "idle", stopDetail("x")))
	c := e.waitSends(t, 1)[0]
	got := readsOfCall(t, c)
	if len(got) != 16 || got[0].Code != "r01" || got[15].Code != "r16" {
		t.Fatalf("carried %d: %v", len(got), got)
	}
	deadline := time.Now().Add(2 * time.Second) // the acknowledgement follows the send
	for e.mod.rs.Pending(push.DeviceID(tokA)) != 4 {
		if time.Now().After(deadline) {
			t.Fatalf("%d pending after the ack, want 4", e.mod.rs.Pending(push.DeviceID(tokA)))
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.feed2().emit(nev("c1", "PdxStop", "idle", stopDetail("y")))
	if got := readsOfCall(t, e.waitSends(t, 2)[1]); len(got) != 4 || got[0].Code != "r17" {
		t.Fatalf("the next push carried %v", got)
	}
}

// A newer T for a carried code that arrives while the push is in flight survives its acknowledgement.
// Mutation gate: ack by code → red.
func TestReadSyncPush_ANewerReadArrivingInFlightSurvivesTheAck(t *testing.T) {
	e := newRSPushEnv(t)
	e.device(tokA, "en", "mlab", readSyncDevice("c1"))
	e.apns.block = make(chan struct{})
	e.read("r1", 1)
	e.read("r2", 1)
	e.feed2().emit(nev("c1", "PdxStop", "idle", stopDetail("x")))
	e.waitSends(t, 1) // inside Send, blocked
	e.read("r1", 2)   // a newer read of a carried code, while the push is in flight
	close(e.apns.block)
	deadline := time.Now().Add(3 * time.Second)
	for e.mod.rs.Pending(push.DeviceID(tokA)) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("%d pending, want only r1(2)", e.mod.rs.Pending(push.DeviceID(tokA)))
		}
		time.Sleep(5 * time.Millisecond)
	}
	left := e.mod.rs.Reserve(push.DeviceID(tokA))
	if len(left) != 1 || left[0].Code != "r1" || left[0].Through.Seq != 2 {
		t.Fatalf("left = %v", left)
	}
}

// A push that failed (after the sender's one retry) acknowledges nothing: every read goes again next time.
// Mutation gate: ack before the answer → red.
func TestReadSyncPush_AFailedSendKeepsEverything(t *testing.T) {
	e := newRSPushEnv(t)
	e.device(tokA, "en", "mlab", readSyncDevice("c1"))
	e.apns.script = []apns.Result{{Class: apns.Rejected, Status: 400, Reason: "BadTopic"}}
	e.read("r1", 1)
	e.read("r2", 1)
	e.feed2().emit(nev("c1", "PdxStop", "idle", stopDetail("x")))
	e.waitSends(t, 1)
	time.Sleep(100 * time.Millisecond)
	if n := e.mod.rs.Pending(push.DeviceID(tokA)); n != 2 {
		t.Fatalf("%d pending after a failed push, want 2", n)
	}
}

// Reads that were trimmed to fit the 4 KiB stay pending.
func TestReadSyncPush_ReadsTrimmedForSizeStayPending(t *testing.T) {
	e := newRSPushEnv(t)
	e.device(tokA, "en", "mlab", readSyncDevice("c1"))
	for i := 0; i < 16; i++ {
		e.feed.publish(reads.Read{
			Code:    strings.Repeat("x", 60) + fmt.Sprintf("%04d", i),
			Through: reads.Cursor{Epoch: strings.Repeat("e", 120) + fmt.Sprintf("%04d", i), Seq: 1},
		})
	}
	big := leadApproval("ap1")
	big.Payload = json.RawMessage(`{"reason":"` + strings.Repeat("long ", 200) + `"}`)
	var open []team.Approval
	for i := 0; i < 40; i++ { // 32 maximal keys of fixed fields
		open = append(open, sessionCard(fmt.Sprintf("o%02d", i), team.KindLead, fmt.Sprintf("%s%02d", strings.Repeat("s", 62), i)))
	}
	e.events.readOpen = func() ([]team.Approval, error) { return open, nil }
	e.events.emit("opened", big)
	c := e.waitSends(t, 1)[0]
	carried := len(readsOfCall(t, c))
	if len(c.Payload) > 4096 {
		t.Fatalf("%d bytes", len(c.Payload))
	}
	if carried == 16 {
		t.Skip("the fixture left room for every read; nothing was trimmed")
	}
	deadline := time.Now().Add(2 * time.Second) // the acknowledgement follows the send
	for e.mod.rs.Pending(push.DeviceID(tokA)) != 16-carried {
		if time.Now().After(deadline) {
			t.Fatalf("%d pending, want the %d that did not fit", e.mod.rs.Pending(push.DeviceID(tokA)), 16-carried)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A push that fails to build carries nothing and acknowledges nothing.
func TestSender_ABuildFailureAcknowledgesNothing(t *testing.T) {
	d := device(strings.Repeat("a1", 32), "en", "mlab")
	d.Prefs.ReadSync = true
	book, a := newBook(d), &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}
	s := newTestSender(book, a, &slept{})
	car := &fakeCarrier{pending: []reads.Read{{Code: "r1", Through: reads.Cursor{Epoch: "e", Seq: 1}}}}
	s.reads = car
	runOne(t, s, Job{DeviceIDs: []string{d.DeviceID}, Make: func(push.Device) (push.Content, bool) {
		return push.Content{Title: "t", Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1", Event: strings.Repeat("e", 5000)}, true // cannot fit
	}})
	if a.count() != 0 || len(car.sent) != 0 {
		t.Fatalf("calls %d, acked %v: a push that could not be built carried or acknowledged something", a.count(), car.sent)
	}
	if book.errors[d.DeviceID] != "payload" {
		t.Fatalf("errors = %v", book.errors)
	}
}

type fakeCarrier struct {
	pending []reads.Read
	sent    [][]reads.Read
}

func (f *fakeCarrier) Reserve(string) []reads.Read   { return f.pending }
func (f *fakeCarrier) Sent(_ string, s []reads.Read) { f.sent = append(f.sent, s) }

// The flush: with reads pending and nothing pushed for the interval, one background push of kind "reads" with the BG-1b
// headers carries them; it does not fire while pushes keep going. Mutation gate: count from the first read → red.
func TestReadSyncPush_TheFlushAfterTheIntervalAndNotWhilePushesKeepGoing(t *testing.T) {
	e := newRSPushEnv(t)
	e.device(tokA, "en", "mlab", readSyncDevice("c1"))
	e.read("r1", 1)
	e.clock.advance(10 * time.Minute)
	e.feed2().emit(nev("c1", "PdxStop", "idle", stopDetail("x"))) // a push at +10 min: it carries r1 (acknowledged)
	e.waitSends(t, 1)
	e.read("r2", 1)
	e.clock.advance(14 * time.Minute) // 14 min after the push
	e.noSendsMoreThan(t, 1)
	e.clock.advance(time.Minute + time.Second)
	flush := e.waitSends(t, 2)[1]
	if flush.H.PushType != "background" || flush.H.Priority != 5 || flush.H.CollapseID != "" {
		t.Fatalf("flush headers = %+v", flush.H)
	}
	p := purdexOfCall(t, flush)
	if p["kind"] != "reads" {
		t.Fatalf("flush kind = %v: %s", p["kind"], flush.Payload)
	}
	if got := readsOfCall(t, flush); len(got) != 1 || got[0].Code != "r2" {
		t.Fatalf("flush reads = %v", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for e.mod.rs.Pending(push.DeviceID(tokA)) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the flushed read was not acknowledged")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A flush for a registration that no longer syncs reads, or whose list is empty by now, sends nothing.
func TestReadSyncPush_AFlushWithNothingToCarrySendsNothing(t *testing.T) {
	e := newRSPushEnv(t)
	e.device(tokA, "en", "mlab", readSyncDevice("c1"))
	snd := e.mod.sender.Load()
	snd.Enqueue(Job{DeviceIDs: []string{push.DeviceID(tokA)}, Make: func(d push.Device) (push.Content, bool) {
		return push.Content{Kind: "reads", Background: true}, true
	}})
	e.noSends(t)
}

// Every path that removes a registration drops its pending reads and timer.
func TestReadSyncPush_RemovalPathsDropPendingAndTimers(t *testing.T) {
	t.Run("unregister", func(t *testing.T) {
		e := newRSPushEnv(t)
		e.device(tokA, "en", "mlab", readSyncDevice("c1"))
		e.read("r1", 1)
		if rec := e.do("DELETE", "/api/push/devices/"+push.DeviceID(tokA), ""); rec.Code != http.StatusNoContent {
			t.Fatalf("delete: %d", rec.Code)
		}
		if e.mod.rs.Pending(push.DeviceID(tokA)) != 0 || e.clock.live() != 0 {
			t.Fatalf("pending %d timers %d", e.mod.rs.Pending(push.DeviceID(tokA)), e.clock.live())
		}
	})
	t.Run("APNs says remove", func(t *testing.T) {
		e := newRSPushEnv(t)
		e.apns.script = []apns.Result{{Class: apns.Remove, Status: 410, Reason: "Unregistered"}}
		e.device(tokA, "en", "mlab", readSyncDevice("c1"))
		e.read("r1", 1)
		e.feed2().emit(nev("c1", "PdxStop", "idle", stopDetail("x")))
		e.waitSends(t, 1)
		deadline := time.Now().Add(2 * time.Second)
		for e.mod.rs.Pending(push.DeviceID(tokA)) != 0 || e.clock.live() != 0 {
			if time.Now().After(deadline) {
				t.Fatalf("pending %d timers %d", e.mod.rs.Pending(push.DeviceID(tokA)), e.clock.live())
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	t.Run("owner revoked", func(t *testing.T) {
		e := newRSPushEnv(t)
		d := device(tokA, "en", "mlab")
		d.OwnerDeviceID = "d_owner000001"
		d.Prefs.ReadSync = true
		if _, err := e.mod.store.Upsert(d); err != nil {
			t.Fatal(err)
		}
		e.mod.mu.Lock()
		e.mod.devices[d.DeviceID] = d
		e.mod.mu.Unlock()
		e.read("r1", 1)
		if e.mod.rs.Pending(d.DeviceID) != 1 {
			t.Fatal("not pending")
		}
		e.mod.dropOwned([]string{"d_owner000001"})
		if e.mod.rs.Pending(d.DeviceID) != 0 || e.clock.live() != 0 {
			t.Fatalf("pending %d timers %d", e.mod.rs.Pending(d.DeviceID), e.clock.live())
		}
	})
	t.Run("registered again without read_sync", func(t *testing.T) {
		e := newRSPushEnv(t)
		e.device(tokA, "en", "mlab", readSyncDevice("c1"))
		e.read("r1", 1)
		e.device(tokA, "en", "mlab", tabsOf("c1"))
		if e.mod.rs.Pending(push.DeviceID(tokA)) != 0 || e.clock.live() != 0 {
			t.Fatal("the pending reads of a registration that stopped syncing survived")
		}
		e.read("r2", 1)
		if e.mod.rs.Pending(push.DeviceID(tokA)) != 0 {
			t.Fatal("a registration that does not sync took a read")
		}
	})
	t.Run("Stop", func(t *testing.T) {
		e := newRSPushEnv(t)
		e.device(tokA, "en", "mlab", readSyncDevice("c1"))
		e.read("r1", 1)
		if e.clock.live() != 1 {
			t.Fatalf("timers %d", e.clock.live())
		}
		_ = e.mod.Stop(context.Background())
		if e.clock.live() != 0 {
			t.Fatalf("%d flush timers left after Stop", e.clock.live())
		}
		e.feed.mu.Lock()
		defer e.feed.mu.Unlock()
		if e.feed.unsubscribe != 1 {
			t.Fatalf("the reads feed was unsubscribed %d times", e.feed.unsubscribe)
		}
	})
}

// A phone that re-registers under a new device id (a new APNs token) leaves the old registration's pending reads behind in
// no one's hands: they are dropped.
func TestReadSyncPush_ReRegisteringUnderANewDeviceIDDropsTheOldPending(t *testing.T) {
	e := newRevokeEnv(t)
	must(t, e.push.Start(context.Background()))
	rf := &fakeReadsFeed{}
	_ = rf
	p := e.pair(t, pairA)
	body := func(tok string) string {
		return reqBody(tok, func(r *push.DeviceRequest) { r.Prefs = push.Prefs{ReadSync: true} })
	}
	if rec := e.call(&p, "POST", "/api/push/devices", body(tokA)); rec.Code != http.StatusOK {
		t.Fatalf("register: %d", rec.Code)
	}
	e.push.rs.Add(push.DeviceID(tokA), reads.Read{Code: "r1", Through: reads.Cursor{Epoch: "e", Seq: 1}}, time.Time{})
	if rec := e.call(&p, "POST", "/api/push/devices", body(tokB)); rec.Code != http.StatusOK {
		t.Fatalf("register again: %d", rec.Code)
	}
	if n := e.push.rs.Pending(push.DeviceID(tokA)); n != 0 {
		t.Fatalf("the old registration still holds %d pending reads", n)
	}
}

// Every agent push carries the hook cursor of its event; with no epoch it carries none.
// Mutation gate: not wired → red.
func TestAgentTrigger_EventCursorOnTheAgentPush(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	ev := nev("c1", "PdxStop", "idle", stopDetail("x"))
	ev.Event.Epoch, ev.Event.Seq = "boot-9", 4242
	e.feed.emit(ev)
	cur, _ := purdexOfCall(t, e.waitSends(t, 1)[0])["event_cursor"].(map[string]any)
	if cur["epoch"] != "boot-9" || cur["seq"] != float64(4242) {
		t.Fatalf("event_cursor = %v", cur)
	}
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("y"))) // epoch "" (a frame not from the emit slot)
	if _, has := purdexOfCall(t, e.waitSends(t, 2)[1])["event_cursor"]; has {
		t.Fatal("event_cursor with no epoch")
	}
}
