package push

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/team"
)

// BG-1b (badge spec §3.1 "Background badge push", §5 BG-1b): a card that closes tells the phones that asked, silently.

// fakeTimer stands in for time.AfterFunc: it records every schedule and lets the test fire or see a cancel.
type fakeTimer struct {
	scheduled []time.Duration
	fn        func()
	cancelled int
}

func (f *fakeTimer) schedule(d time.Duration, fn func()) func() {
	f.scheduled = append(f.scheduled, d)
	f.fn = fn
	return func() { f.cancelled++; f.fn = nil }
}

func (f *fakeTimer) fire() {
	fn := f.fn
	f.fn = nil
	if fn != nil {
		fn()
	}
}

// A fixed window, not a sliding one: the first close starts it, later closes add ids and never move the deadline.
// Mutation gate: reschedule on every close (sliding) → red.
func TestCloseCoalescer_TheWindowIsFixedNotSliding(t *testing.T) {
	var batches [][]string
	ft := &fakeTimer{}
	c := newCloseCoalescer(func(ids []string) { batches = append(batches, ids) })
	c.schedule = ft.schedule
	c.add("ap1")
	c.add("ap2")
	c.add("ap3")
	if len(ft.scheduled) != 1 || ft.scheduled[0] != 2*time.Second {
		t.Fatalf("schedules = %v, want exactly one of 2 s", ft.scheduled)
	}
	ft.fire()
	if len(batches) != 1 || strings.Join(batches[0], ",") != "ap1,ap2,ap3" {
		t.Fatalf("batches = %v", batches)
	}
	c.add("ap4") // after the window fired, a new one starts
	if len(ft.scheduled) != 2 {
		t.Fatalf("schedules = %v, want a second window", ft.scheduled)
	}
	ft.fire()
	if len(batches) != 2 || strings.Join(batches[1], ",") != "ap4" {
		t.Fatalf("batches = %v", batches)
	}
}

// The same approval closing twice in a window is one id.
func TestCloseCoalescer_DuplicateIDsCollapse(t *testing.T) {
	var batches [][]string
	ft := &fakeTimer{}
	c := newCloseCoalescer(func(ids []string) { batches = append(batches, ids) })
	c.schedule = ft.schedule
	c.add("ap1")
	c.add("ap1")
	ft.fire()
	if len(batches) != 1 || len(batches[0]) != 1 {
		t.Fatalf("batches = %v", batches)
	}
}

// Stop cancels a window that has not fired; nothing is delivered after, and a close after Stop starts nothing.
// Mutation gate: Stop leaves the timer → red.
func TestCloseCoalescer_StopCancelsThePendingWindow(t *testing.T) {
	var batches [][]string
	ft := &fakeTimer{}
	c := newCloseCoalescer(func(ids []string) { batches = append(batches, ids) })
	c.schedule = ft.schedule
	c.add("ap1")
	c.stop()
	if ft.cancelled != 1 {
		t.Fatalf("cancelled = %d", ft.cancelled)
	}
	ft.fire()
	c.add("ap2")
	if len(batches) != 0 || len(ft.scheduled) != 1 {
		t.Fatalf("after Stop: batches %v schedules %v", batches, ft.scheduled)
	}
	c.reset() // Start again
	c.add("ap3")
	if len(ft.scheduled) != 2 {
		t.Fatalf("after reset: schedules %v", ft.scheduled)
	}
}

// A timer callback that was already running when Stop cancelled its window (time.Timer.Stop is false then) must not flush the
// window of the next run: each window has its own generation. R2 attack, high. Mutation gate: flush without the generation → red.
func TestCloseCoalescer_AStaleCallbackCannotFlushALaterWindow(t *testing.T) {
	var batches [][]string
	var callbacks []func()
	c := newCloseCoalescer(func(ids []string) { batches = append(batches, ids) })
	c.schedule = func(d time.Duration, fn func()) func() {
		callbacks = append(callbacks, fn)
		return func() {} // Timer.Stop() == false: the callback is already on its way
	}
	c.add("old")
	c.stop()
	c.reset()
	c.add("new")
	if len(callbacks) != 2 {
		t.Fatalf("schedules = %d", len(callbacks))
	}
	callbacks[0]() // the old window's callback arrives late
	if len(batches) != 0 {
		t.Fatalf("the stale callback flushed the new window early: %v", batches)
	}
	callbacks[1]()
	if len(batches) != 1 || strings.Join(batches[0], ",") != "new" {
		t.Fatalf("batches = %v, want only the new window's id", batches)
	}
}

// stop does not return while a fire is under way, so after Stop (and the sender swap that follows) nothing of the old run can
// still be enqueued. Mutation gate: fire outside the lock → red.
func TestCloseCoalescer_StopWaitsForAFireInFlight(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var fn func()
	c := newCloseCoalescer(func([]string) { close(entered); <-release })
	c.schedule = func(d time.Duration, f func()) func() { fn = f; return func() {} }
	c.add("ap1")
	go fn()
	<-entered
	stopped := make(chan struct{})
	go func() { c.stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("stop returned while a fire was in flight")
	case <-time.After(60 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop never returned")
	}
}

// bgEnv is a started module with a fast window and the given devices registered, device 0 asking for background badge pushes.
func bgEnv(t *testing.T) *triggerEnv {
	t.Helper()
	te := newTriggerEnv(t, nil)
	te.mod.closes.window = 40 * time.Millisecond
	return te
}

func (te *triggerEnv) registerBadge(token string, on bool) {
	te.do("POST", "/api/push/devices", reqBody(token, func(r *push.DeviceRequest) { r.Prefs.BadgePush = on }))
}

func closedCard(id string, kind team.Kind) team.Approval {
	a := sessionCard(id, kind, "sid-"+id)
	a.State = team.StateApproved
	return a
}

// One close → one background push to the device that asked, with the headers and the body of the spec; none to a device that
// did not. Mutation gate: ignore badge_push / send alert headers → red.
func TestBadgeBG_OneCloseOneSilentPushToTheDevicesThatAsked(t *testing.T) {
	te := bgEnv(t)
	te.registerBadge(tokA, true)
	te.registerBadge(tokB, false)
	open := []team.Approval{sessionCard("still", team.KindLead, "s1")}
	te.events.readOpen = func() ([]team.Approval, error) { return open, nil }
	te.events.emit("closed", closedCard("gone", team.KindLead))
	calls := te.waitSends(t, 1)
	time.Sleep(150 * time.Millisecond)
	if n := te.apns.count(); n != 1 {
		t.Fatalf("sends = %d, want 1 (only the device with badge_push)", n)
	}
	c := calls[0]
	if c.Token != tokA {
		t.Fatalf("sent to %s", c.Token)
	}
	if c.H.PushType != "background" || c.H.Priority != 5 || c.H.CollapseID != "" || c.H.Topic != push.BundleID {
		t.Fatalf("headers = %+v", c.H)
	}
	if left := time.Until(c.H.Expiration); left < 50*time.Minute || left > 61*time.Minute {
		t.Fatalf("expiration in %v, want about an hour", left)
	}
	var p struct {
		Aps    map[string]any `json:"aps"`
		Purdex map[string]any `json:"purdex"`
	}
	if err := json.Unmarshal([]byte(c.Payload), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Aps) != 1 || p.Aps["content-available"] != float64(1) {
		t.Fatalf("aps = %v", p.Aps)
	}
	if p.Purdex["kind"] != "badge" || p.Purdex["open_approvals"] != float64(1) {
		t.Fatalf("purdex = %v", p.Purdex)
	}
	if ids, _ := p.Purdex["closed_approval_ids"].([]any); len(ids) != 1 || ids[0] != "gone" {
		t.Fatalf("closed ids = %v", p.Purdex["closed_approval_ids"])
	}
	if keys, _ := p.Purdex["open_approval_keys"].([]any); len(keys) != 1 || keys[0] != "c:s1" {
		t.Fatalf("keys = %v", p.Purdex["open_approval_keys"])
	}
}

// Three closes inside the window → one push listing all three.
func TestBadgeBG_ClosesInTheWindowShareOnePush(t *testing.T) {
	te := bgEnv(t)
	te.mod.closes.window = 200 * time.Millisecond
	te.registerBadge(tokA, true)
	te.events.readOpen = func() ([]team.Approval, error) { return nil, nil }
	for _, id := range []string{"c1", "c2", "c3"} {
		te.events.emit("closed", closedCard(id, team.KindHookAsk))
		time.Sleep(20 * time.Millisecond)
	}
	calls := te.waitSends(t, 1)
	time.Sleep(300 * time.Millisecond)
	if n := te.apns.count(); n != 1 {
		t.Fatalf("sends = %d, want one for the three closes", n)
	}
	var p struct {
		Purdex struct {
			IDs []string `json:"closed_approval_ids"`
		} `json:"purdex"`
	}
	_ = json.Unmarshal([]byte(calls[0].Payload), &p)
	if strings.Join(p.Purdex.IDs, ",") != "c1,c2,c3" {
		t.Fatalf("ids = %v", p.Purdex.IDs)
	}
}

// Every badge kind triggers on its close, whichever way it closed (decided, denied, timed out, cancelled, abandoned — the team
// module broadcasts one `closed` for all of them); an opened approval and a kind the badge does not count do not.
// Mutation gate: trigger on opened / trigger on every kind → red.
func TestBadgeBG_EveryBadgeKindAndEveryClosePathTriggers(t *testing.T) {
	kinds := []team.Kind{team.KindLead, team.KindSelfRelay, team.KindMemberRelay, team.KindAdopt, team.KindHookAsk, team.KindHookPermission}
	states := []team.State{team.StateApproved, team.StateDenied, team.StateTimeout, team.StateCancelled, team.StateAbandoned}
	for _, kind := range kinds {
		for _, st := range states {
			t.Run(fmt.Sprintf("%s/%s", kind, st), func(t *testing.T) {
				te := bgEnv(t)
				te.registerBadge(tokA, true)
				te.events.readOpen = func() ([]team.Approval, error) { return nil, nil }
				a := closedCard("x", kind)
				a.State = st
				te.events.emit("closed", a)
				te.waitSends(t, 1)
			})
		}
	}
}

func TestBadgeBG_OpenedAndOtherKindsDoNotTrigger(t *testing.T) {
	te := bgEnv(t)
	te.registerBadge(tokA, true)
	te.events.readOpen = func() ([]team.Approval, error) { return nil, nil }
	other := closedCard("o", team.Kind("something_else"))
	te.events.emit("closed", other)
	te.events.emit("opened", sessionCard("p", team.KindHookPermission, "s")) // counted but not pushed, and not a close
	ask := sessionCard("t", team.KindHookAsk, "s")
	ask.Payload = json.RawMessage(`{"terminal_only":true}`)
	te.events.emit("opened", ask)
	te.noSends(t)
	time.Sleep(100 * time.Millisecond)
	te.noSends(t)
}

// A failed open-set read sends nothing (the push is about the open set; there is nothing to say without it).
// Mutation gate: send the push without the read → red.
func TestBadgeBG_AFailedReadSendsNothing(t *testing.T) {
	te := bgEnv(t)
	te.registerBadge(tokA, true)
	te.events.readOpen = func() ([]team.Approval, error) { return nil, fmt.Errorf("team.db busy") }
	te.events.emit("closed", closedCard("x", team.KindLead))
	time.Sleep(200 * time.Millisecond)
	te.noSends(t)
}

// A device that did not ask (badge_push off) is not sent a background push.
func TestBadgeBG_NoDeviceAskedNothingIsSent(t *testing.T) {
	te := bgEnv(t)
	te.registerBadge(tokA, false)
	te.events.emit("closed", closedCard("x", team.KindLead))
	time.Sleep(200 * time.Millisecond)
	te.noSends(t)
}

// 20 closes → 16 ids, and with 32 maximal keys the payload stays ≤ 4096 bytes.
func TestBadgeBG_TwentyClosesSixteenIDsUnder4KiB(t *testing.T) {
	te := bgEnv(t)
	te.mod.closes.window = 300 * time.Millisecond
	te.registerBadge(tokA, true)
	var open []team.Approval
	for i := 0; i < 40; i++ { // 40 open cards of 40 distinct sessions with 64-byte ids → 32 maximal c: keys
		open = append(open, sessionCard(fmt.Sprintf("o%02d", i), team.KindLead, fmt.Sprintf("%s%02d", strings.Repeat("s", 62), i)))
	}
	te.events.readOpen = func() ([]team.Approval, error) { return open, nil }
	for i := 0; i < 20; i++ {
		te.events.emit("closed", closedCard(fmt.Sprintf("00000000-0000-4000-8000-%012d", i), team.KindHookAsk))
	}
	calls := te.waitSends(t, 1)
	time.Sleep(400 * time.Millisecond)
	if n := te.apns.count(); n != 1 {
		t.Fatalf("sends = %d", n)
	}
	var p struct {
		Purdex struct {
			Open int      `json:"open_approvals"`
			Keys []string `json:"open_approval_keys"`
			IDs  []string `json:"closed_approval_ids"`
		} `json:"purdex"`
	}
	if err := json.Unmarshal([]byte(calls[0].Payload), &p); err != nil {
		t.Fatal(err)
	}
	if len(calls[0].Payload) > 4096 || len(p.Purdex.IDs) != 16 || len(p.Purdex.Keys) != 32 || p.Purdex.Open != 40 {
		t.Fatalf("%d bytes, ids %d, keys %d, open %d", len(calls[0].Payload), len(p.Purdex.IDs), len(p.Purdex.Keys), p.Purdex.Open)
	}
}

// A full queue drops the job and says so in the log; the window's batch is not retried.
func TestBadgeBG_AFullQueueDropsTheJobWithALogLine(t *testing.T) {
	te := bgEnv(t)
	lb := &lockedBuf{} // the drop line is written by the window's timer goroutine: read it under a lock
	log.SetOutput(lb)
	te.registerBadge(tokA, true)
	te.events.readOpen = func() ([]team.Approval, error) { return nil, nil }
	snd := te.mod.sender.Load()
	snd.Stop() // a stopped sender does not drain its queue
	for i := 0; i < queueCap; i++ {
		snd.Enqueue(Job{})
	}
	te.events.emit("closed", closedCard("x", team.KindLead))
	time.Sleep(200 * time.Millisecond)
	if s := lb.String(); !strings.Contains(s, "badge job dropped") {
		t.Fatalf("no drop line in the log: %s", s)
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// Stop with a pending window sends nothing.
func TestBadgeBG_StopCancelsAPendingWindow(t *testing.T) {
	te := bgEnv(t)
	te.mod.closes.window = 150 * time.Millisecond
	te.registerBadge(tokA, true)
	te.events.readOpen = func() ([]team.Approval, error) { return nil, nil }
	te.events.emit("closed", closedCard("x", team.KindLead))
	if err := te.mod.Stop(nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	te.noSends(t)
}

// The visible approval push is unchanged by all this: headers alert / 10 with its collapse id.
func TestBadgeBG_AVisiblePushKeepsItsHeaders(t *testing.T) {
	te := bgEnv(t)
	te.registerBadge(tokA, true)
	te.events.emit("opened", leadApproval("ap1"))
	c := te.waitSends(t, 1)[0]
	if c.H.PushType != "" || c.H.Priority != 0 || c.H.CollapseID != "ap1" {
		t.Fatalf("headers = %+v: a visible push must keep the zero push type / priority (alert / 10) and its collapse id", c.H)
	}
}
