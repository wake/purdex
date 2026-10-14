package push

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push/apns"
	"github.com/wake/purdex/internal/team"
)

// P-1: approval pushes with buttons, and the 60 s late push for a card that waits while the person is at the computer.

const testLate = 40 * time.Millisecond

func captureLog(t *testing.T) *lockedBuf {
	t.Helper()
	buf := &lockedBuf{}
	prev := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return buf
}

func (te *triggerEnv) mustLog(t *testing.T, buf *lockedBuf, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("log lacks %q:\n%s", want, buf.String())
}

// atComputer is a presence that can be flipped.
type atComputer struct{ v atomic.Bool }

func (p *atComputer) ShowsName(string) bool { return false }
func (p *atComputer) ShowsCode(string) bool { return false }
func (p *atComputer) AtComputer() bool      { return p.v.Load() }

func lateEnv(t *testing.T, at bool) (*triggerEnv, *atComputer) {
	t.Helper()
	pres := &atComputer{}
	pres.v.Store(at)
	e := newEnv(t)
	te := &triggerEnv{env: e, apns: &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}, events: &fakeEvents{}}
	e.mod.events = te.events
	e.mod.newAPNs = func() apnsClient { return te.apns }
	e.mod.presence = pres
	e.mod.lateAfter = testLate
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.mod.Stop(context.Background()) })
	te.register(tokA, "en", "mlab")
	return te, pres
}

func relayApproval(id, kind string) team.Approval {
	a := leadApproval(id)
	a.Kind = team.Kind(kind)
	a.Payload = json.RawMessage(`{"used_percentage":72,"lead_title":"l","member_title":"m"}`)
	return a
}

func (te *triggerEnv) stateOf(id string) apState {
	p := &te.mod.ap
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.states[id]; e != nil {
		return e.state
	}
	return apNone
}

// At the computer: nothing at once, one push once the wait is over and the card is still open; the push carries the category.
// Mutation gate: push at once (skip the hold) -> red.
func TestLate_AtTheComputerNothingAtOnceThenOnePushWhenStillOpen(t *testing.T) {
	te, _ := lateEnv(t, true)
	buf := captureLog(t)
	te.mod.lateAfter = 300 * time.Millisecond
	te.events.emit("opened", leadApproval("ap1"))
	te.noSends(t) // 150 ms < 300 ms
	te.mustLog(t, buf, "approval ap1 kind=lead held 60s (at_computer)")
	calls := te.waitSends(t, 1)
	if !strings.Contains(calls[0].Payload, `"category":"PDX_APPROVAL"`) || calls[0].H.CollapseID != "ap1" {
		t.Fatalf("call = %+v", calls[0])
	}
	te.mustLog(t, buf, "approval ap1 kind=lead late push")
	time.Sleep(3 * testLate)
	if n := te.apns.count(); n != 1 {
		t.Fatalf("sends = %d, want exactly one", n)
	}
}

// The last self-relay card that timed out stays open with unanswered_at set: it is still a card to push late.
func TestLate_AnUnansweredSelfRelayCardIsStillPushed(t *testing.T) {
	te, _ := lateEnv(t, true)
	a := relayApproval("ap1", "self_relay")
	a.Payload = json.RawMessage(`{"used_percentage":71,"unanswered_at":1700000000000}`)
	te.events.emit("opened", a)
	te.waitSends(t, 1)
}

// Away: pushed at once, and no late push follows.
func TestLate_AwayIsPushedAtOnceAndNotAgain(t *testing.T) {
	te, _ := lateEnv(t, false)
	te.events.emit("opened", leadApproval("ap1"))
	te.waitSends(t, 1)
	time.Sleep(4 * testLate)
	if n := te.apns.count(); n != 1 {
		t.Fatalf("sends = %d", n)
	}
}

// Closed before the wait is over: nothing is pushed, and the log says why.
func TestLate_ClosedBeforeTheWaitIsNotPushed(t *testing.T) {
	te, _ := lateEnv(t, true)
	buf := captureLog(t)
	a := leadApproval("ap1")
	te.events.emit("opened", a)
	te.events.emit("closed", a)
	te.noSends(t)
	te.mustLog(t, buf, "approval ap1 kind=lead not pushed: closed")
}

// The close lands between the late check's read ("open") and its enqueue; the read is stale on purpose, so only the tombstone
// can stop it. Mutation gate: drop the tombstone (closedApproval) -> red.
func TestLate_CloseBetweenTheReadAndTheEnqueueSendsNothing(t *testing.T) {
	te, _ := lateEnv(t, true)
	a := leadApproval("ap1")
	te.events.readOpen = func() ([]team.Approval, error) { return []team.Approval{a}, nil } // always "open"
	te.mod.apAfterRead = func(string) { te.events.emit("closed", a) }
	te.events.emit("opened", a)
	te.noSends(t)
	if te.stateOf("ap1") != apClosed {
		t.Fatalf("state = %v", te.stateOf("ap1"))
	}
}

// Immediate push and late push at the same time: one enqueue. The first push path is parked after it reserved the card.
// Mutation gate: mark pushed after the enqueue instead of reserving first -> red.
func TestLate_ImmediateAndLateTogetherEnqueueOnce(t *testing.T) {
	te, _ := lateEnv(t, false)
	a := leadApproval("ap1")
	te.events.open = []team.Approval{a}
	pa := toPushApproval(a)
	reserved, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	te.mod.apAfterReserve = func(string) {
		if first.CompareAndSwap(false, true) { // only the first path parks; a second one that got past the reserve must not hang the test
			close(reserved)
			<-release
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); te.mod.pushApproval(a, pa, te.mod.ap.gen, false, false) }()
	<-reserved
	te.mod.pushApproval(a, pa, te.mod.ap.gen, true, true) // the late path arrives while the first is between reserve and enqueue
	close(release)
	wg.Wait()
	te.waitSends(t, 1)
	time.Sleep(3 * testLate)
	if n := te.apns.count(); n != 1 {
		t.Fatalf("sends = %d, want 1", n)
	}
}

// A hold set that is full pushes at once, and does not look at the presence again. Mutation gate: re-check the presence
// on that path -> red.
func TestLate_AFullHoldSetPushesAtOnceWhateverThePresenceSays(t *testing.T) {
	te, _ := lateEnv(t, true)
	buf := captureLog(t)
	for i := 0; i < maxHolds; i++ {
		if !te.mod.holds.after(time.Hour, func() {}) {
			t.Fatalf("hold %d refused", i)
		}
	}
	te.events.emit("opened", leadApproval("ap1"))
	te.waitSends(t, 1)
	te.mustLog(t, buf, "hold set full: pushed at once")
}

// The reader is missing, or the read fails: the card is pushed anyway and the log says read_failed.
func TestLate_AReaderThatCannotAnswerPushesAnyway(t *testing.T) {
	for name, setup := range map[string]func(*triggerEnv){
		"error": func(te *triggerEnv) {
			te.events.readOpen = func() ([]team.Approval, error) { return nil, errors.New("boom") }
		},
		"missing": func(te *triggerEnv) { te.mod.reader = nil },
	} {
		t.Run(name, func(t *testing.T) {
			te, _ := lateEnv(t, true)
			buf := captureLog(t)
			setup(te)
			te.events.emit("opened", leadApproval("ap1"))
			te.waitSends(t, 1)
			te.mustLog(t, buf, "read_failed")
		})
	}
}

// Stop and a restart while the late callback is past its running check (parked after its read): the callback belongs to the
// earlier run and sends nothing into the new one, and the new run begins empty.
// Mutation gate: drop the generation check -> red.
func TestLate_AStaleCallbackOfAnEarlierRunSendsNothing(t *testing.T) {
	te, _ := lateEnv(t, true)
	parked, release := make(chan struct{}), make(chan struct{})
	te.mod.apAfterRead = func(string) { close(parked); <-release }
	te.events.emit("opened", leadApproval("ap1"))
	<-parked
	if err := te.mod.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	te.mod.apAfterRead = nil
	if err := te.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if te.stateOf("ap1") != apNone {
		t.Fatalf("a restarted module kept state %v", te.stateOf("ap1"))
	}
	close(release)
	te.noSends(t)
	if te.stateOf("ap1") != apNone {
		t.Fatalf("the stale callback left state %v in the new run", te.stateOf("ap1"))
	}
}

// Stop waits for a push path that already reserved the card, and nothing is sent after Stop returned.
// Mutation gate: drop inflight.Wait -> red.
func TestLate_StopWaitsForAPushPathUnderWay(t *testing.T) {
	te, _ := lateEnv(t, false)
	reserved, release := make(chan struct{}), make(chan struct{})
	te.mod.apAfterReserve = func(string) { close(reserved); <-release }
	go te.events.emit("opened", leadApproval("ap1"))
	<-reserved
	stopped := make(chan struct{})
	go func() { te.mod.Stop(context.Background()); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a push path was between reserve and enqueue")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	<-stopped
	n := te.apns.count()
	time.Sleep(3 * testLate)
	if te.apns.count() != n {
		t.Fatalf("a send happened after Stop returned")
	}
}

// A queued job whose card closes before it is sent is dropped by the sender.
// Mutation gate: DropIfClosed not set on the job -> red.
func TestSender_AQueuedApprovalJobIsDroppedWhenItsCardClosed(t *testing.T) {
	te, _ := lateEnv(t, false)
	buf := captureLog(t)
	te.apns.mu.Lock()
	te.apns.block = make(chan struct{})
	te.apns.mu.Unlock()
	te.events.emit("opened", leadApproval("ap1")) // the sender is stuck sending this one
	deadline := time.Now().Add(3 * time.Second)
	for te.apns.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	two := leadApproval("ap2")
	te.events.emit("opened", two) // queued behind it
	te.events.emit("closed", two) // and its card closes before it is sent
	te.apns.mu.Lock()
	close(te.apns.block)
	te.apns.mu.Unlock()
	te.mustLog(t, buf, "approval ap2 not pushed: dropped_at_send")
	if n := te.apns.count(); n != 1 {
		t.Fatalf("sends = %d, want only the first", n)
	}
}

// The sender's final check, on its own: closed -> dropped; open -> sent; the open set unreadable -> sent anyway.
func TestSender_DropIfClosedNeedsAReadableOpenSet(t *testing.T) {
	cases := []struct {
		name string
		snap openSnap
		want int
	}{
		{"closed", openSnap{ok: true, ownOpen: false}, 0},
		{"open", openSnap{ok: true, ownOpen: true}, 1},
		{"unreadable", openSnap{ok: false}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := device(strings.Repeat("a1", 32), "en", "mlab")
			a := &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}
			s := newTestSender(newBook(d), a, &slept{})
			s.openState = func(context.Context, Job) openSnap { return c.snap }
			j := leadJob(d.DeviceID)
			j.Approval, j.DropIfClosed = &JobApproval{ID: "ap1"}, true
			runOne(t, s, j)
			if a.count() != c.want {
				t.Fatalf("sends = %d, want %d", a.count(), c.want)
			}
		})
	}
}

// hook_ask is not affected: at the computer it is not held (today's rule), away it is pushed with no category.
func TestLate_HookAskIsNotHeldAndHasNoButtons(t *testing.T) {
	te, pres := lateEnv(t, true)
	buf := captureLog(t)
	ask := leadApproval("ask1")
	ask.Kind = team.KindHookAsk
	ask.Payload = json.RawMessage(`{"questions":[{"question":"red or blue?"}]}`)
	te.events.emit("opened", ask)
	te.noSends(t)
	te.mustLog(t, buf, "approval ask1 kind=hook_ask not pushed: at_computer")
	if te.stateOf("ask1") != apNone {
		t.Fatalf("a hook_ask was held")
	}
	pres.v.Store(false)
	two := ask
	two.ID = "ask2"
	te.events.emit("opened", two)
	calls := te.waitSends(t, 1)
	if strings.Contains(calls[0].Payload, "category") {
		t.Fatalf("hook_ask payload has a category: %s", calls[0].Payload)
	}
}

// The three kinds all get the category at once when away, and a repeated opened is not a second push.
func TestLate_EveryButtonKindCarriesTheCategory(t *testing.T) {
	te, _ := lateEnv(t, false)
	te.events.emit("opened", leadApproval("a1"))
	te.events.emit("opened", relayApproval("a2", "self_relay"))
	te.events.emit("opened", relayApproval("a3", "member_relay"))
	te.events.emit("opened", relayApproval("a3", "member_relay")) // repeated
	calls := te.waitSends(t, 3)
	time.Sleep(3 * testLate)
	if te.apns.count() != 3 {
		t.Fatalf("sends = %d, want 3", te.apns.count())
	}
	for _, c := range calls {
		if !strings.Contains(c.Payload, `"category":"PDX_APPROVAL"`) {
			t.Fatalf("payload = %s", c.Payload)
		}
	}
}

// No device: nothing to push, and the log says so; the card goes back to what it was.
func TestLate_NoDevicesIsLoggedAndNotRecorded(t *testing.T) {
	e := newEnv(t)
	buf := captureLog(t)
	te := &triggerEnv{env: e, apns: &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}, events: &fakeEvents{}}
	e.mod.events = te.events
	e.mod.newAPNs = func() apnsClient { return te.apns }
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.mod.Stop(context.Background()) })
	te.events.emit("opened", leadApproval("ap1"))
	te.mustLog(t, buf, "approval ap1 kind=lead not pushed: no_devices")
	if te.stateOf("ap1") != apNone {
		t.Fatalf("state = %v", te.stateOf("ap1"))
	}
}

// An id whose close event was dropped does not stay forever: states older than the TTL are pruned on each event.
func TestLate_OldStatesArePruned(t *testing.T) {
	te, _ := lateEnv(t, false)
	now := time.Now()
	te.mod.apClock = func() time.Time { return now }
	te.events.emit("opened", leadApproval("old"))
	te.waitSends(t, 1)
	now = now.Add(apStateTTL + time.Minute)
	te.events.emit("opened", leadApproval("new"))
	te.waitSends(t, 2)
	if te.stateOf("old") != apNone {
		t.Fatalf("the old state was kept")
	}
}
