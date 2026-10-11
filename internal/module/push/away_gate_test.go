package push

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
)

// PA-1 (push-away-presence spec §4.2–§4.4): the gates, with the real presence store behind the HTTP route.

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// putV2 reports a v2 entry for client "mac-1": idleMs of idle, away after awayMs, ttl 60 s.
func (e *agentEnv) putV2(idleMs, awayMs int64, locked bool, lockedMs int64) {
	r := v2("mac-1", idleMs, awayMs)
	r.Locked, r.LockedMs = bptr(locked), i64(lockedMs)
	e.putReq(r)
}

func (e *agentEnv) putReq(r push.PresenceRequest) {
	rec := e.do("PUT", "/api/push/presence", mustJSON(r))
	if rec.Code != 204 {
		panic(rec.Body.String())
	}
}

// At the computer: no phone push for any event, even a session no window shows. Away (the report expired): pushed.
// Mutation gate: drop rule 4′ → red.
func TestAway_AtTheComputerNoAgentEventIsPushed(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.putV2(1000, 300000, false, 0)
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("done")))
	e.feed.emit(nev("c1", "PdxStopFailure", "error", map[string]any{"error": "boom"}))
	e.noSends(t)
	e.clock.advance(5 * time.Minute) // no report within the TTL is away
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("done 2")))
	e.waitSends(t, 1)
}

// The user leaves: the report ages to away_after; nothing that happened at the computer is replayed.
func TestAway_TheBoundaryAndNoReplay(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.putV2(0, 60000, false, 0)
	e.clock.advance(59 * time.Second)
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("while at the computer")))
	e.noSends(t)
	e.clock.advance(time.Second) // 60 s of idle: away
	e.feed.emit(nev("c1", "PdxStop", "idle", stopDetail("after leaving")))
	e.waitSends(t, 1)
	time.Sleep(100 * time.Millisecond)
	if n := e.apns.count(); n != 1 {
		t.Fatalf("sends = %d: the event of the at-computer minute was replayed", n)
	}
}

// Locked: 59 s is at the computer, 61 s away.
func TestAway_LockedBoundary(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.putV2(0, 300000, true, 0)
	e.clock.advance(59 * time.Second)
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.noSends(t)
	e.clock.advance(2 * time.Second)
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.waitSends(t, 1)
}

// A gated-out error does not touch rule 7: the same error right after the user leaves is still the first of its bucket.
func TestAway_AGatedErrorDoesNotOpenTheDebounce(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.putV2(0, 300000, false, 0)
	e.feed.emit(nev("c1", "PdxStopFailure", "error", map[string]any{"error": "rate_limit"}))
	e.noSends(t)
	e.putV2(400000, 300000, false, 0) // away now
	e.feed.emit(nev("c1", "PdxStopFailure", "error", map[string]any{"error": "rate_limit"}))
	e.waitSends(t, 1)
}

// A v2 entry never feeds the legacy session match; a v1 window still does, alongside it.
func TestAway_AV2EntryDoesNotHideASessionByTheLegacyRule(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	r := v2("mac-1", 400000, 300000) // away by its own N
	r.Active, r.Sessions = true, []push.PresenceSession{{Code: "c1", Name: "dev"}}
	e.putReq(r)
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.waitSends(t, 1)
	r2 := put("mac-2", true, 45000, sess("c1", "dev")) // an older App's window that shows c1
	e.putReq(r2)
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	time.Sleep(100 * time.Millisecond)
	if e.apns.count() != 1 {
		t.Fatalf("sends = %d, the legacy rule did not hide c1", e.apns.count())
	}
}

// The final check: the user came back while the event was held (rule 8's hold).
// Mutation gate: drop the final check → red.
func TestAway_TheFinalCheckDropsAHeldWaitingEvent(t *testing.T) {
	e := newAgentEnv(t, 150*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", map[string]any{"tool_name": "Bash"}))
	time.Sleep(30 * time.Millisecond)
	e.putV2(0, 300000, false, 0) // the user came back during the hold
	e.noSends(t)                 // outlasts the hold
}

// The workbook hold too; and rule 7 stays at arrival, so the dropped error still opened its debounce window.
func TestAway_TheFinalCheckDropsAnErrorHeldForTheWorkbookButItsDebounceStays(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	fl := e.withWorkbook(8 * time.Second)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStopFailure", "error", map[string]any{"error": "boom"}))
	waitFor(t, "the hold to start", func() bool { return fl.started.Load() == 1 })
	e.putV2(0, 300000, false, 0)
	fl.release <- lineAnswer{ok: false}
	e.noSends(t)
	if n := e.mod.gate.DebounceLen(); n != 1 {
		t.Fatalf("debounce keys = %d: rule 7 stays at arrival, so the dropped error still opened its window", n)
	}
}

// The check is made once at the gate and again before the enqueue: away at the gate and at the computer at the enqueue
// drops the event.
func TestAway_BothChecksAreMade(t *testing.T) {
	var calls atomic.Int32
	pf := presenceFake{at: func() bool { return calls.Add(1) >= 2 }}
	e := newAgentEnvWith(t, time.Millisecond, pf)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.noSends(t)
	if calls.Load() < 2 {
		t.Fatalf("AtComputer asked %d time(s), want the gate's and the final", calls.Load())
	}
}

// Approvals (§4.3): the check comes first and unconditionally, so an approval with no tmux origin is held back too.
// Mutation gate: only check when the origin has a tmux name → red.
func TestAway_AtTheComputerNoApprovalIsPushedEvenWithoutATmuxOrigin(t *testing.T) {
	te := newTriggerEnv(t, presenceFake{at: func() bool { return true }})
	te.register(tokA, "en", "mlab")
	noTmux := leadApproval("ap1")
	noTmux.Origin.Tmux = ""
	te.events.emit("opened", noTmux)
	te.events.emit("opened", leadApproval("ap2"))
	te.noSends(t)
}

// Away: an approval is pushed, with or without a tmux origin; the legacy rule still applies for an older window.
func TestAway_AwayApprovalsAreStillPushed(t *testing.T) {
	te := newTriggerEnv(t, presenceFake{shows: map[string]bool{"dev": true}})
	te.register(tokA, "en", "mlab")
	noTmux := leadApproval("ap1")
	noTmux.Origin.Tmux = ""
	te.events.emit("opened", noTmux)
	te.waitSends(t, 1)
	te.events.emit("opened", leadApproval("ap2")) // origin "dev": the legacy window shows it
	time.Sleep(100 * time.Millisecond)
	if te.apns.count() != 1 {
		t.Fatalf("sends = %d", te.apns.count())
	}
}

// The approval's own final check. Mutation gate: drop it → red.
func TestAway_AnApprovalIsCheckedAgainBeforeTheEnqueue(t *testing.T) {
	var calls atomic.Int32
	te := newTriggerEnv(t, presenceFake{at: func() bool { return calls.Add(1) >= 2 }})
	te.register(tokA, "en", "mlab")
	te.events.emit("opened", leadApproval("ap1"))
	te.noSends(t)
	if calls.Load() != 2 {
		t.Fatalf("AtComputer asked %d time(s), want 2", calls.Load())
	}
}

// Rule 8 (§4.4): an ask suppressed because the user is at the computer records no interval, so the `waiting` event of the
// same session, once the user has left, is pushed. Mutation gate: record before the gate → red.
func TestAway_AGatedAskDoesNotHideALaterWaitingEvent(t *testing.T) {
	e := newAgentEnv(t, 60*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.putV2(0, 300000, false, 0)
	e.events.emit("opened", askApproval("ask1", "sid-1", "dev:@1.%2", false))
	e.noSends(t)
	e.putV2(400000, 300000, false, 0) // the user left
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", nil))
	e.waitSends(t, 1)
}

// A pushed ask still hides the waiting event of the same question.
func TestAway_APushedAskStillHidesTheWaitingEvent(t *testing.T) {
	e := newAgentEnv(t, 60*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.events.emit("opened", askApproval("ask1", "sid-1", "dev:@1.%2", false))
	e.waitSends(t, 1)
	e.feed.emit(nev("c1", "PdxPermissionRequest", "waiting", nil))
	time.Sleep(250 * time.Millisecond)
	if e.apns.count() != 1 {
		t.Fatalf("sends = %d", e.apns.count())
	}
}

// No devices: nothing was queued, so the ask is not recorded.
func TestAway_AnAskWithNoDevicesRecordsNothing(t *testing.T) {
	e := newAgentEnv(t, 60*time.Millisecond)
	e.events.emit("opened", askApproval("ask1", "sid-1", "dev:@1.%2", false))
	time.Sleep(50 * time.Millisecond)
	if e.mod.asks.Len() != 0 {
		t.Fatalf("asks kept = %d", e.mod.asks.Len())
	}
}

// The queue refuses the job: not recorded either. Mutation gate: record on a false enqueue → red.
func TestAway_AnAskTheQueueRefusedRecordsNothing(t *testing.T) {
	e := newAgentEnv(t, 60*time.Millisecond)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	snd := e.mod.sender.Load()
	snd.Stop() // a stopped sender does not drain its queue: fill it so Enqueue says false
	for i := 0; i < queueCap; i++ {
		snd.Enqueue(Job{})
	}
	e.events.emit("opened", askApproval("ask1", "sid-1", "dev:@1.%2", false))
	time.Sleep(50 * time.Millisecond)
	if e.mod.asks.Len() != 0 {
		t.Fatalf("asks kept = %d, the refused ask was recorded", e.mod.asks.Len())
	}
}
