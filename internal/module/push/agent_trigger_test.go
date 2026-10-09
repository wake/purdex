package push

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/push/apns"
	"github.com/wake/purdex/internal/team"
)

// PU-3 Task 5: the agent trigger end to end with a fake APNs, the fake hook feed and the fake approval feed.

type fakeNotifyFeed struct {
	mu          sync.Mutex
	fn          func(agent.NotifyEvent)
	subscribed  int
	unsubscribe int
}

func (f *fakeNotifyFeed) SubscribeNotify(fn func(agent.NotifyEvent)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fn = fn
	f.subscribed++
	return func() { f.mu.Lock(); f.unsubscribe++; f.fn = nil; f.mu.Unlock() }
}

func (f *fakeNotifyFeed) emit(ev agent.NotifyEvent) {
	f.mu.Lock()
	fn := f.fn
	f.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

type agentEnv struct {
	*triggerEnv
	feed  *fakeNotifyFeed
	clock *fakeClock
}

func newAgentEnv(t *testing.T, hold time.Duration, open ...team.Approval) *agentEnv {
	t.Helper()
	e := newEnv(t)
	clock := &fakeClock{now: time.Unix(5_000_000, 0)}
	te := &triggerEnv{env: e, apns: &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}, events: &fakeEvents{open: open}}
	feed := &fakeNotifyFeed{}
	e.mod.events, e.mod.notify = te.events, feed
	e.mod.newAPNs = func() apnsClient { return te.apns }
	e.mod.pres = NewPresence(clock.Now)
	e.mod.gate = NewGate(clock.Now)
	e.mod.asks = newOpenAsks(clock.Now)
	e.mod.holdFor = hold
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.mod.Stop(context.Background()) })
	return &agentEnv{triggerEnv: te, feed: feed, clock: clock}
}

var tsSeq int64 = 1000

// nev is one live hook frame. Every call gets a fresh BroadcastTs unless the test sets it.
func nev(code, raw, status string, detail map[string]any) agent.NotifyEvent {
	tsSeq++
	return agent.NotifyEvent{SessionCode: code, SessionName: "dev", SessionID: "sid-1",
		Event: agentpkg.NormalizedEvent{AgentType: "cc", Status: status, RawEventName: raw, BroadcastTs: tsSeq, Detail: detail}}
}

func (e *agentEnv) device(token, locale, label string, mut func(*push.DeviceRequest)) {
	e.do("POST", "/api/push/devices", reqBody(token, func(r *push.DeviceRequest) {
		r.Locale, r.HostLabel = locale, label
		if mut != nil {
			mut(r)
		}
	}))
}

func (e *agentEnv) putPresence(active bool, ttlMs int, code string) {
	b, _ := json.Marshal(push.PresenceRequest{ClientID: "mac-1", Active: active, TTLMs: ttlMs, Sessions: []push.PresenceSession{{Code: code, Name: "dev"}}})
	e.do("PUT", "/api/push/presence", string(b))
}

func tabsOf(codes ...string) func(*push.DeviceRequest) {
	return func(r *push.DeviceRequest) { r.Prefs = push.Prefs{Tabs: codes} }
}

func stopDetail(msg string) map[string]any { return map[string]any{"last_assistant_message": msg} }

func TestAgentTrigger_AStopInATabPushesItsMessage(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("All **done**.")))
	c := e.waitSends(t, 1)[0]
	for _, want := range []string{`"kind":"agent"`, `"event":"Stop"`, `"session_code":"c1"`, `"session_id":"sid-1"`, `"body":"All done."`, `"title":"mlab: dev"`} {
		if !strings.Contains(c.Payload, want) {
			t.Fatalf("payload lacks %s: %s", want, c.Payload)
		}
	}
	if c.H.CollapseID != "agent-c1" {
		t.Fatalf("collapse id = %q", c.H.CollapseID)
	}
}

func TestAgentTrigger_NotInTabsIsNotPushedUnlessNotifyWithoutTab(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("other"))
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.noSends(t)
	e.device(tokA, "en", "mlab", func(r *push.DeviceRequest) {
		r.Prefs = push.Prefs{Tabs: []string{"other"}, Agents: map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}}}
	})
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.waitSends(t, 1)
}

func TestAgentTrigger_APresentMacSuppressesAndAnExpiredOneDoesNot(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.putPresence(true, 5000, "c1")
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.noSends(t)
	e.clock.advance(6 * time.Second) // the presence expired
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.waitSends(t, 1)
	e.putPresence(false, 45000, "c1") // an inactive window does not count
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.waitSends(t, 2)
}

// The `waiting` frame arrives before its hook_ask `opened` (within the hold): only the hook_ask pushes.
// Mutation gate: no hold (check at once) → two pushes → red.
func TestAgentTrigger_AWaitingFrameBeforeItsHookAskPushesOnlyTheAsk(t *testing.T) {
	e := newAgentEnv(t, 120*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", map[string]any{"tool_name": "AskUserQuestion"}))
	time.Sleep(40 * time.Millisecond) // the ask is later than the waiting frame, but inside the hold
	e.events.emit("opened", askApproval("ask1", "sid-1", "dev:@1.%2", false))
	calls := e.waitSends(t, 1)
	time.Sleep(300 * time.Millisecond) // past the hold
	if got := e.apns.count(); got != 1 {
		t.Fatalf("sends = %d, want only the hook_ask push", got)
	}
	if !strings.Contains(calls[0].Payload, `"kind":"hook_ask"`) {
		t.Fatalf("the one push is %s", calls[0].Payload)
	}
}

// A waiting frame with no hook_ask is held, then pushed. The hold really holds: nothing is sent before it ends.
func TestAgentTrigger_AWaitingFrameWithNoAskIsPushedAfterTheHold(t *testing.T) {
	e := newAgentEnv(t, 150*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", map[string]any{"tool_name": "Bash"}))
	time.Sleep(40 * time.Millisecond)
	if e.apns.count() != 0 {
		t.Fatal("a waiting frame was pushed before its hold ended")
	}
	c := e.waitSends(t, 1)[0]
	if !strings.Contains(c.Payload, "Permission required: Bash") {
		t.Fatalf("payload = %s", c.Payload)
	}
}

// The waiting frame comes after the ask was opened and already answered, but within 10 s: still the same question.
func TestAgentTrigger_AnAskOpenedAndClosedWithin10SecondsStillSuppresses(t *testing.T) {
	e := newAgentEnv(t, 60*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.events.emit("opened", askApproval("ask1", "sid-1", "dev:@1.%2", false))
	e.waitSends(t, 1)
	e.clock.advance(3 * time.Second)
	e.events.emit("closed", askApproval("ask1", "sid-1", "dev:@1.%2", false))
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", nil))
	time.Sleep(250 * time.Millisecond)
	if got := e.apns.count(); got != 1 {
		t.Fatalf("sends = %d, want only the ask's", got)
	}
	e.clock.advance(20 * time.Second) // long after: a new waiting event is its own
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", nil))
	e.waitSends(t, 2)
}

// Matched by the tmux session name when the ids differ or are missing.
func TestAgentTrigger_TheAskIsMatchedByTmuxNameToo(t *testing.T) {
	e := newAgentEnv(t, 60*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.events.emit("opened", askApproval("ask1", "another-sid", "dev:@1.%2", false))
	e.waitSends(t, 1)
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", nil)) // SessionName "dev"
	time.Sleep(250 * time.Millisecond)
	if got := e.apns.count(); got != 1 {
		t.Fatalf("sends = %d", got)
	}
}

// A terminal_only ask is not pushed, so the agent's own event must still go. Mutation gate: count every ask → red.
func TestAgentTrigger_ATerminalOnlyAskDoesNotSuppressTheAgentEvent(t *testing.T) {
	e := newAgentEnv(t, 60*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.events.emit("opened", askApproval("ask1", "sid-1", "dev:@1.%2", true))
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", nil))
	e.waitSends(t, 1)
}

// The daemon restarted with an ask open: the snapshot of the new subscription counts. Mutation gate: ignore the
// snapshot → red.
func TestAgentTrigger_AnAskInTheOpenSnapshotSuppresses(t *testing.T) {
	e := newAgentEnv(t, 60*time.Millisecond, askApproval("ask1", "sid-1", "dev:@1.%2", false))
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", nil))
	e.noSends(t)
}

// A probe or sweep frame names its reason as the event: no content, no push, even when its status is waiting.
func TestAgentTrigger_AProbeOrSweepFrameHasNoContent(t *testing.T) {
	e := newAgentEnv(t, 30*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	for _, reason := range []string{"screen_probe", "pid_sweep", "mod_live_late", ""} {
		e.feed.emit(nev("c1", reason, "waiting", map[string]any{"message": "x"}))
		e.feed.emit(nev("c1", reason, "idle", nil))
		e.feed.emit(nev("c1", reason, "error", map[string]any{"error": "boom"}))
	}
	e.noSends(t)
	time.Sleep(100 * time.Millisecond)
	e.noSends(t)
}

// Freshness is recorded for a frame that has no content too: a probe's newer timestamp is seen, so an older Stop that
// arrives behind it is not pushed (rule 0 runs on every frame, rule 9 last). Mutation gate: check the content first → red.
func TestAgentTrigger_ANewerFrameWithNoContentStillRecordsFreshness(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	probe := nev("c1", "screen_probe", "idle", nil)
	older := nev("c1", "PdxStop", "idle", stopDetail("late"))
	older.Event.BroadcastTs = probe.Event.BroadcastTs - 1
	e.feed.emit(probe)
	e.feed.emit(older)
	e.noSends(t)
	newer := nev("c1", "PdxStop", "idle", stopDetail("fresh"))
	e.feed.emit(newer)
	e.waitSends(t, 1)
}

// A mod turn end and the hook Stop each make a frame for one turn; both push, and the phone shows one: they share the
// collapse id agent-<code>, so the later replaces the earlier there. Pinned here so a change to either is deliberate.
func TestAgentTrigger_ModAndHookFramesOfOneTurnShareACollapseID(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	mod := nev("c1", "PdxStop", "idle", stopDetail("done (mod)"))
	mod.Event.Source = "mod"
	hook := nev("c1", "PdxStop", "idle", stopDetail("done (hook)"))
	hook.Event.Source = "hook"
	e.feed.emit(mod)
	e.feed.emit(hook)
	calls := e.waitSends(t, 2)
	if calls[0].H.CollapseID != "agent-c1" || calls[1].H.CollapseID != "agent-c1" {
		t.Fatalf("collapse ids = %q / %q, want agent-c1 for both", calls[0].H.CollapseID, calls[1].H.CollapseID)
	}
	if calls[0].H.CollapseID != calls[1].H.CollapseID || calls[0].Payload == calls[1].Payload {
		t.Fatal("the two frames should be two pushes that replace each other on the phone")
	}
}

// Two devices get the same error; the next identical error within 60 s reaches neither (the debounce is once per event).
func TestAgentTrigger_TwoDevicesShareTheErrorDebounce(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.device(tokB, "zh-TW", "mlab-zh", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStopFailure", "error", map[string]any{"error": "rate_limit", "error_details": "slow down"}))
	calls := e.waitSends(t, 2)
	bodies := calls[0].Payload + calls[1].Payload
	if !strings.Contains(bodies, "slow down") || !strings.Contains(bodies, "mlab: dev") || !strings.Contains(bodies, "mlab-zh：dev") {
		t.Fatalf("payloads = %s", bodies)
	}
	e.clock.advance(10 * time.Second)
	e.feed.emit(nev("c1", "PdxStopFailure", "error", map[string]any{"error": "rate_limit"}))
	time.Sleep(150 * time.Millisecond)
	if got := e.apns.count(); got != 2 {
		t.Fatalf("sends = %d, want the two of the first error and nothing for the repeat", got)
	}
}

// Each device is told in its own locale.
func TestAgentTrigger_EachDeviceInItsOwnLocale(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.device(tokB, "zh-TW", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	calls := e.waitSends(t, 2)
	got := calls[0].Payload + "|" + calls[1].Payload
	if !strings.Contains(got, "Task completed") || !strings.Contains(got, "任務完成") {
		t.Fatalf("payloads = %s", got)
	}
}

// Stop cancels the held events and unsubscribes from both feeds. Mutation gate: stopAll missing → a push after Stop → red.
func TestAgentTrigger_StopCancelsHeldEventsAndUnsubscribes(t *testing.T) {
	e := newAgentEnv(t, 150*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", nil))
	e.mod.Stop(context.Background())
	e.mod.holds.mu.Lock()
	held := len(e.mod.holds.timers)
	e.mod.holds.mu.Unlock()
	if held != 0 {
		t.Fatalf("%d hold timer(s) survived Stop", held)
	}
	time.Sleep(350 * time.Millisecond)
	e.noSends(t)
	if e.feed.unsubscribe != 1 {
		t.Fatalf("unsubscribed from the hook feed %d times", e.feed.unsubscribe)
	}
	e.feed.emit(nev("c1", "PdxStop", "idle", nil)) // nobody listens any more
	e.noSends(t)
}

// Held events are bounded: one beyond the cap is pushed at once rather than lost or queued. Mutation gate: no cap → the
// extra one waits with the rest → red.
func TestAgentTrigger_TheHoldCapPushesTheOverflowAtOnce(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", func(r *push.DeviceRequest) {
		r.Prefs = push.Prefs{Agents: map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}}}
	})
	for i := 0; i < maxHolds; i++ {
		e.feed.emit(nev(strings.Repeat("c", 1)+string(rune('A'+i%26))+string(rune('a'+i/26)), "PdxPermissionRequest", "waiting", nil))
	}
	time.Sleep(100 * time.Millisecond)
	if e.apns.count() != 0 {
		t.Fatal("held events were sent")
	}
	e.feed.emit(nev("overflow", "PdxPermissionRequest", "waiting", nil))
	e.waitSends(t, 1)
}

// With no hook feed in the registry the module still serves approvals (and says so in the log).
func TestAgentTrigger_NoHookFeedStillServesApprovals(t *testing.T) {
	e := newEnv(t)
	te := &triggerEnv{env: e, apns: &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}, events: &fakeEvents{}}
	e.mod.events = te.events
	e.mod.newAPNs = func() apnsClient { return te.apns }
	if err := e.mod.Start(context.Background()); err != nil { // the test core's registry has no agent module
		t.Fatal(err)
	}
	t.Cleanup(func() { e.mod.Stop(context.Background()) })
	if !strings.Contains(e.logs.String(), "hook feed is not available") {
		t.Fatalf("log = %q", e.logs.String())
	}
	te.register(tokA, "en", "mlab")
	te.events.emit("opened", leadApproval("ap1"))
	te.waitSends(t, 1)
}

func askApproval(id, sid, tmux string, terminalOnly bool) team.Approval {
	payload := `{"questions":[{"question":"red or blue?"}]}`
	if terminalOnly {
		payload = `{"terminal_only":true,"questions":[{"question":"q"}]}`
	}
	return team.Approval{ID: id, Kind: team.KindHookAsk, Payload: json.RawMessage(payload), Origin: team.Origin{SessionID: sid, Ref: "_abc123", Name: "worker-1", Tmux: tmux}}
}

// The approval feed runs an event before the snapshot is loaded: a `closed` that outruns the load must not be lost, or
// the snapshot would reopen a closed ask and suppress the session's waiting events for good. Mutation gate: apply
// events without waiting for the load → red.
type racingEvents struct{ ask team.Approval }

func (r *racingEvents) SubscribeApprovals(fn func(string, team.Approval)) ([]team.Approval, func()) {
	done := make(chan struct{})
	go func() { fn("closed", r.ask); close(done) }()
	time.Sleep(60 * time.Millisecond) // the callback goroutine is first
	return []team.Approval{r.ask}, func() {}
}

func TestAgentTrigger_AClosedEventThatOutrunsTheSnapshotIsNotLost(t *testing.T) {
	e := newEnv(t)
	apnsFake := &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}
	feed := &fakeNotifyFeed{}
	e.mod.events = &racingEvents{ask: askApproval("ask1", "sid-1", "dev:@1.%2", false)}
	e.mod.notify = feed
	e.mod.newAPNs = func() apnsClient { return apnsFake }
	e.mod.holdFor = 40 * time.Millisecond
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.mod.Stop(context.Background()) })
	e.do("POST", "/api/push/devices", reqBody(tokA, tabsOf2("c1")))
	time.Sleep(100 * time.Millisecond) // the closed event has been applied
	e.mod.asks.mu.Lock()               // age the ask past the 10 s window the simple way
	for id, a := range e.mod.asks.byID {
		a.opened = a.opened.Add(-time.Minute)
		e.mod.asks.byID[id] = a
	}
	e.mod.asks.mu.Unlock()
	feed.emit(nev("c1", "PdxPermissionRequest", "waiting", nil))
	deadline := time.Now().Add(2 * time.Second)
	for apnsFake.count() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if apnsFake.count() != 1 {
		t.Fatal("the closed ask was reopened by the snapshot: the waiting event was swallowed")
	}
}

func tabsOf2(codes ...string) func(*push.DeviceRequest) { return tabsOf(codes...) }

// A restart starts from the snapshot: an ask an earlier run saw open does not outlive it. Mutation gate: no Clear → red.
func TestAgentTrigger_AStartForgetsAsksOfAnEarlierRun(t *testing.T) {
	e := newEnv(t)
	e.mod.asks.Opened(askApproval("stale", "sid-1", "dev:@1.%2", false))
	e.mod.events = &fakeEvents{} // an empty snapshot: nothing is open now
	e.mod.newAPNs = func() apnsClient { return &fakeAPNS{} }
	e.mod.notify = &fakeNotifyFeed{}
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.mod.Stop(context.Background()) })
	e.mod.asks.mu.Lock()
	for id, a := range e.mod.asks.byID {
		a.opened = a.opened.Add(-time.Minute)
		e.mod.asks.byID[id] = a
	}
	e.mod.asks.mu.Unlock()
	if e.mod.asks.Has("sid-1", "dev") {
		t.Fatal("an ask of an earlier run is still open")
	}
}

// A waiting event held by one run is delivered by that run's sender, not by whatever sender is current when the hold
// ends. Mutation gate: reload the sender in the hold's callback → the new sender gets it → red.
func TestAgentTrigger_AHeldEventIsDeliveredByTheSenderOfItsRun(t *testing.T) {
	e := newAgentEnv(t, 120*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", nil))
	other := &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}
	next := newSender(e.mod, other, "host", push.BundleID)
	next.Start(context.Background())
	defer next.Stop()
	e.mod.sender.Store(next) // a restart installed another sender while the event was held
	e.waitSends(t, 1)
	time.Sleep(100 * time.Millisecond)
	if other.count() != 0 {
		t.Fatal("the held event was delivered by the next run's sender")
	}
}
