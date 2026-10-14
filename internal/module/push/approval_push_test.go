package push

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/push/apns"
	"github.com/wake/purdex/internal/team"
)

// P-1: approval pushes with buttons, and the 60 s late push for a card that waits while the person is at the computer.

// holdFakes stands in for the hold set's timers: nothing fires by itself, a test fires them, so no test waits on the wall clock
// for a thing that should (or should not) have happened.
type holdFakes struct {
	mu   sync.Mutex
	fns  []*holdFake
	last time.Duration
}

type holdFake struct {
	fn      func()
	stopped bool
	fired   bool
}

func (f *holdFake) Stop() bool { f.stopped = true; return true }

func (ft *holdFakes) after(d time.Duration, fn func()) holdTimer {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &holdFake{fn: fn}
	ft.fns = append(ft.fns, t)
	ft.last = d
	return t
}

// pending is how many timers are set and have neither fired nor been stopped.
func (ft *holdFakes) pending() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	n := 0
	for _, t := range ft.fns {
		if !t.fired && !t.stopped {
			n++
		}
	}
	return n
}

// fire runs every pending timer, on the caller's goroutine, and returns when they have returned.
func (ft *holdFakes) fire() {
	ft.mu.Lock()
	var run []*holdFake
	for _, t := range ft.fns {
		if !t.fired && !t.stopped {
			t.fired = true
			run = append(run, t)
		}
	}
	ft.mu.Unlock()
	for _, t := range run {
		t.fn()
	}
}

var sentinelSeq atomic.Int64

// settled queues a marker push behind everything already queued and waits for it: the sender is one goroutine, so when the
// marker has gone out every earlier job has been sent or dropped. It returns the approval pushes sent so far, so "nothing was
// sent" is a fact and not a guess after a sleep.
func (te *triggerEnv) settled(t *testing.T) []sendCall {
	t.Helper()
	snd := te.mod.sender.Load()
	var ids []string
	for _, d := range te.mod.snapshot() {
		ids = append(ids, d.DeviceID)
	}
	marker := "sentinel-" + strconv.FormatInt(sentinelSeq.Add(1), 10)
	if !snd.Enqueue(Job{DeviceIDs: ids, SkipOpen: true, Make: func(push.Device) (push.Content, bool) {
		return push.Content{Title: marker, Body: "b", Kind: "sentinel"}, true
	}}) {
		t.Fatal("the marker was refused")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		calls := te.apns.snapshotCalls()
		for _, c := range calls {
			if strings.Contains(c.Payload, marker) {
				var out []sendCall
				for _, c := range calls {
					if !strings.Contains(c.Payload, `"kind":"sentinel"`) {
						out = append(out, c)
					}
				}
				return out
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the marker push never went out")
	return nil
}

func (te *triggerEnv) noApprovalSends(t *testing.T) {
	t.Helper()
	if n := len(te.settled(t)); n != 0 {
		t.Fatalf("approval pushes sent = %d, want none", n)
	}
}

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
	return lateEnvWith(t, pres), pres
}

func lateEnvWith(t *testing.T, pres presenceChecker) *triggerEnv {
	t.Helper()
	e := newEnv(t)
	te := &triggerEnv{env: e, apns: &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}, events: &fakeEvents{}, timers: &holdFakes{}}
	e.mod.events = te.events
	e.mod.newAPNs = func() apnsClient { return te.apns }
	e.mod.presence = pres
	e.mod.holds.timerFn = te.timers.after
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.mod.Stop(context.Background()) })
	te.register(tokA, "en", "mlab")
	return te
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
	te.events.emit("opened", leadApproval("ap1"))
	te.noApprovalSends(t)
	te.mustLog(t, buf, "approval ap1 kind=lead held 60s (at_computer)")
	if te.timers.pending() != 1 || te.timers.last != 60*time.Second {
		t.Fatalf("timers pending = %d, delay = %v", te.timers.pending(), te.timers.last)
	}
	te.timers.fire()
	calls := te.settled(t)
	if len(calls) != 1 || !strings.Contains(calls[0].Payload, `"category":"PDX_APPROVAL"`) || calls[0].H.CollapseID != "ap1" {
		t.Fatalf("calls = %+v", calls)
	}
	te.mustLog(t, buf, "approval ap1 kind=lead late push")
}

// The last self-relay card that timed out stays open with unanswered_at set: it is still a card to push late.
func TestLate_AnUnansweredSelfRelayCardIsStillPushed(t *testing.T) {
	te, _ := lateEnv(t, true)
	a := relayApproval("ap1", "self_relay")
	a.Payload = json.RawMessage(`{"used_percentage":71,"unanswered_at":1700000000000}`)
	te.events.emit("opened", a)
	te.timers.fire()
	if n := len(te.settled(t)); n != 1 {
		t.Fatalf("sends = %d", n)
	}
}

// Away: pushed at once, and no late push follows.
func TestLate_AwayIsPushedAtOnceAndNotAgain(t *testing.T) {
	te, _ := lateEnv(t, false)
	te.events.emit("opened", leadApproval("ap1"))
	if n := len(te.settled(t)); n != 1 {
		t.Fatalf("sends = %d", n)
	}
	if te.timers.pending() != 0 {
		t.Fatalf("a late push was scheduled for a card pushed at once")
	}
}

// Closed before the wait is over: nothing is pushed, and the log says why.
func TestLate_ClosedBeforeTheWaitIsNotPushed(t *testing.T) {
	te, _ := lateEnv(t, true)
	buf := captureLog(t)
	a := leadApproval("ap1")
	te.events.emit("opened", a)
	te.events.emit("closed", a)
	te.timers.fire()
	te.mustLog(t, buf, "approval ap1 kind=lead not pushed: closed")
	te.noApprovalSends(t)
}

// The close lands between the late check's read ("open") and its enqueue; the read is stale on purpose, so only the tombstone
// can stop it. Mutation gate: drop the tombstone (closedApproval) -> red.
func TestLate_CloseBetweenTheReadAndTheEnqueueSendsNothing(t *testing.T) {
	te, _ := lateEnv(t, true)
	a := leadApproval("ap1")
	te.events.readOpen = func() ([]team.Approval, error) { return []team.Approval{a}, nil } // always "open"
	te.mod.apAfterRead = func(string) { te.events.emit("closed", a) }
	te.events.emit("opened", a)
	te.timers.fire()
	te.noApprovalSends(t)
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
	if n := len(te.settled(t)); n != 1 {
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
	if n := len(te.settled(t)); n != 1 {
		t.Fatalf("sends = %d", n)
	}
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
			te.timers.fire()
			if n := len(te.settled(t)); n != 1 {
				t.Fatalf("sends = %d", n)
			}
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
	fired := make(chan struct{})
	go func() { te.timers.fire(); close(fired) }()
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
	<-fired
	te.noApprovalSends(t)
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
	// Stop clears the running flag first and swaps the sender out last. While the push path is parked the sender must stay:
	// a Stop that did not wait would have swapped it out within a moment. (A slow machine can only make this miss a broken
	// Stop, never fail a correct one.)
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if te.mod.sender.Load() == nil {
			t.Fatal("Stop went on to swap the sender out while a push path was between reserve and enqueue")
		}
		select {
		case <-stopped:
			t.Fatal("Stop returned while a push path was between reserve and enqueue")
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	<-stopped
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
	te.noApprovalSends(t)
	te.mustLog(t, buf, "approval ask1 kind=hook_ask not pushed: at_computer")
	if te.stateOf("ask1") != apNone {
		t.Fatalf("a hook_ask was held")
	}
	pres.v.Store(false)
	two := ask
	two.ID = "ask2"
	te.events.emit("opened", two)
	calls := te.settled(t)
	if len(calls) != 1 || strings.Contains(calls[0].Payload, "category") {
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
	calls := te.settled(t)
	if len(calls) != 3 {
		t.Fatalf("sends = %d, want 3", len(calls))
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

// The late push fails before it enqueues (no device): the spent timer must not leave the card held for ever, with no timer.
// Mutation gate: put the card back to held after a late failure -> red.
func TestLate_AFailedLatePushDoesNotLeaveTheCardHeld(t *testing.T) {
	te, _ := lateEnv(t, true)
	buf := captureLog(t)
	te.mod.mu.Lock()
	te.mod.devices = map[string]push.Device{} // the phone went away during the wait
	te.mod.mu.Unlock()
	te.events.emit("opened", leadApproval("ap1"))
	te.timers.fire()
	te.mustLog(t, buf, "approval ap1 kind=lead not pushed: no_devices")
	if st := te.stateOf("ap1"); st != apNone {
		t.Fatalf("state = %v, want unknown (a held card with no timer is never pushed)", st)
	}
}

// An approval of a kind that is never pushed (adopt, a terminal-only hook_ask) says so in the log.
func TestLate_AKindThatIsNeverPushedIsLogged(t *testing.T) {
	te, _ := lateEnv(t, false)
	buf := captureLog(t)
	adopt := leadApproval("ad1")
	adopt.Kind = team.KindAdopt
	te.events.emit("opened", adopt)
	te.mustLog(t, buf, "approval ad1 kind=adopt not pushed: kind")
	te.noApprovalSends(t)
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

// The person is away at the opening and at the computer by the time the push is about to be queued (the second presence
// check): the card waits for the late check like any card opened at the computer, and is pushed then if still open, whatever
// the presence says by then. Mutation gate: go back to dropping the card there -> red.
func TestLate_ACardWhoseSecondPresenceCheckSaysAtTheComputerIsHeldAndPushedLate(t *testing.T) {
	var calls atomic.Int32
	te := lateEnvWith(t, presenceFake{at: func() bool { return calls.Add(1) == 2 }}) // false, true, then false again
	buf := captureLog(t)
	te.events.emit("opened", leadApproval("ap1"))
	te.noApprovalSends(t)
	te.mustLog(t, buf, "approval ap1 kind=lead held 60s (at_computer)")
	if te.stateOf("ap1") != apHeld || te.timers.pending() != 1 {
		t.Fatalf("state = %v, timers = %d", te.stateOf("ap1"), te.timers.pending())
	}
	te.timers.fire()
	if got := te.settled(t); len(got) != 1 {
		t.Fatalf("sends = %d, want the late push", len(got))
	}
}

// A card that closes while it waits after the second check is not pushed.
func TestLate_ACardHeldBySecondPresenceCheckIsNotPushedWhenItClosed(t *testing.T) {
	var calls atomic.Int32
	te := lateEnvWith(t, presenceFake{at: func() bool { return calls.Add(1) == 2 }})
	a := leadApproval("ap1")
	te.events.emit("opened", a)
	te.events.emit("closed", a)
	te.timers.fire()
	te.noApprovalSends(t)
}

// An older Mac App shows the requesting session (ShowsName): the card waits for the late check too, which pushes it without
// looking at the presence or the name again. Mutation gate: go back to dropping the card on ShowsName -> red.
func TestLate_ACardAMacShowsIsHeldAndPushedLate(t *testing.T) {
	te := lateEnvWith(t, presenceFake{shows: map[string]bool{"dev": true}}) // leadApproval's origin tmux session is "dev"
	buf := captureLog(t)
	te.events.emit("opened", leadApproval("ap1"))
	te.noApprovalSends(t)
	te.mustLog(t, buf, "approval ap1 kind=lead held 60s (shows_name)")
	if te.stateOf("ap1") != apHeld || te.timers.pending() != 1 {
		t.Fatalf("state = %v, timers = %d", te.stateOf("ap1"), te.timers.pending())
	}
	te.timers.fire()
	if got := te.settled(t); len(got) != 1 {
		t.Fatalf("sends = %d, want the late push", len(got))
	}
}

// A hook_ask keeps today's rule on both paths: a Mac shows it -> not pushed, nothing held.
func TestLate_AHookAskAMacShowsIsNotHeld(t *testing.T) {
	te := lateEnvWith(t, presenceFake{shows: map[string]bool{"dev": true}})
	buf := captureLog(t)
	ask := leadApproval("ask1")
	ask.Kind = team.KindHookAsk
	ask.Payload = json.RawMessage(`{"questions":[{"question":"q"}]}`)
	te.events.emit("opened", ask)
	te.noApprovalSends(t)
	te.mustLog(t, buf, "approval ask1 kind=hook_ask not pushed: shows_name")
	if te.timers.pending() != 0 || te.stateOf("ask1") != apNone {
		t.Fatalf("a hook_ask was held")
	}
}

// A repeated opened that arrives while a push path (or the late callback) holds the card as pushing must not take the state:
// it would turn it back into held with a new timer, and the card would be pushed twice.
// Mutation gate: let an opened event hold a pushing card -> red.
func TestLate_ARepeatedOpenedDoesNotTakeAPushingCard(t *testing.T) {
	te, _ := lateEnv(t, true)
	a := leadApproval("ap1")
	te.mod.ap.mu.Lock()
	te.mod.ap.states["ap1"] = &apEntry{state: apPushing, at: time.Now()}
	te.mod.ap.mu.Unlock()
	te.events.emit("opened", a)
	if te.stateOf("ap1") != apPushing || te.timers.pending() != 0 {
		t.Fatalf("state = %v, timers = %d", te.stateOf("ap1"), te.timers.pending())
	}
}
