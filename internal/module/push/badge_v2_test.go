package push

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/team"
)

// BG-1a (badge spec §3.1, §5): the card kinds the badge counts, the `c:` key, team membership in the agent trigger, and
// prefs.muted / prefs.badge_push.

func sessionCard(id string, kind team.Kind, sessionID string) team.Approval {
	a := leadApproval(id)
	a.Kind = kind
	a.Origin.Tmux = ""
	a.Origin.SessionID = sessionID
	return a
}

func purdexOf(t *testing.T, payload string) map[string]any {
	t.Helper()
	var p struct {
		Purdex map[string]any `json:"purdex"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	return p.Purdex
}

// Counted: lead, self_relay, member_relay, adopt, hook_ask (terminal-only included), hook_permission. Not counted: a kind
// the badge does not know. The open set is read at send time, so one pushed lead carries the whole count.
// Mutation gate: openState back to ApprovalContent's filter → red.
func TestBadgeV2_CountsEveryCardKind(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	open := []team.Approval{
		sessionCard("k1", team.KindLead, "s1"), sessionCard("k2", team.KindSelfRelay, "s2"), sessionCard("k3", team.KindMemberRelay, "s3"),
		sessionCard("k4", team.KindAdopt, "s4"), sessionCard("k5", team.KindHookAsk, "s5"), sessionCard("k6", team.KindHookPermission, "s6"),
		sessionCard("k7", team.Kind("something_else"), "s7"),
	}
	open[4].Payload = json.RawMessage(`{"terminal_only":true}`)
	te.events.readOpen = func() ([]team.Approval, error) { return open, nil }
	te.events.emit("opened", leadApproval("k1"))
	p := te.waitSends(t, 1)[0].Payload
	if n, ok := payloadOf(t, p); !ok || n != 6 {
		t.Fatalf("open_approvals = %d (present %v), want 6: %s", n, ok, p)
	}
	keys, _ := keysOf(t, p)
	if want := "c:s1,c:s2,c:s3,c:s4,c:s5,c:s6"; strings.Join(keys, ",") != want {
		t.Fatalf("keys = %v, want %s", keys, want)
	}
}

// A terminal-only ask counts but gets no visible push (ApprovalContent is unchanged); so do an adopt and a permission card.
func TestBadgeV2_CountedButNotPushedKindsMakeNoPush(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	ask := sessionCard("t1", team.KindHookAsk, "s1")
	ask.Payload = json.RawMessage(`{"terminal_only":true}`)
	te.events.readOpen = func() ([]team.Approval, error) { return []team.Approval{ask}, nil }
	te.events.emit("opened", ask)
	for _, k := range []team.Kind{team.KindAdopt, team.KindHookPermission} {
		te.events.emit("opened", sessionCard("x-"+string(k), k, "s2"))
	}
	te.noSends(t)
}

// Several cards of one session whose tmux session does not resolve give one `c:` key; a resolved one stays `s:`; a card with
// no session id at all stays `a:`. Mutation gate: key by approval id again → red; c: before s: → red.
func TestBadgeV2_CKeyForUnresolvedSessionsOfTheSameOrigin(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.mod.sessions = fakeCodes(map[string]string{"dev": "c0de01"})
	te.register(tokA, "en", "mlab")
	resolved := withTmux(sessionCard("r1", team.KindLead, "sid-res"), "dev:@1.%2")
	te.events.readOpen = func() ([]team.Approval, error) {
		return []team.Approval{
			resolved, // s:c0de01 even though it has a session id
			sessionCard("u1", team.KindLead, "sid-gone"), sessionCard("u2", team.KindHookAsk, "sid-gone"), // one c:
			withTmux(sessionCard("u3", team.KindSelfRelay, "sid-gone"), "killed:@1.%2"), // tmux session gone: still c:
			sessionCard("n1", team.KindLead, ""), sessionCard("n2", team.KindLead, ""),  // no session id: a: each
		}, nil
	}
	te.events.emit("opened", resolved)
	p := te.waitSends(t, 1)[0].Payload
	keys, ok := keysOf(t, p)
	if want := "a:n1,a:n2,c:sid-gone,s:c0de01"; !ok || strings.Join(keys, ",") != want {
		t.Fatalf("keys = %v (present %v), want %s: %s", keys, ok, want, p)
	}
	if n, _ := payloadOf(t, p); n != 6 {
		t.Fatalf("open_approvals = %d, want 6 approvals", n)
	}
}

func TestBadgeV2_CKeysShareTheCapOf32(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	var open []team.Approval
	for i := 0; i < 40; i++ {
		open = append(open, sessionCard(fmt.Sprintf("ap%02d", i), team.KindLead, fmt.Sprintf("sid-%02d", i)))
	}
	te.events.readOpen = func() ([]team.Approval, error) { return open, nil }
	te.events.emit("opened", open[0])
	keys, _ := keysOf(t, te.waitSends(t, 1)[0].Payload)
	if len(keys) != 32 || keys[0] != "c:sid-00" || keys[31] != "c:sid-31" {
		t.Fatalf("keys = %v", keys)
	}
}

// fakeMembership answers per session id and counts its calls.
type fakeMembership struct {
	members map[string]bool
	err     error
	calls   atomic.Int32
}

func (f *fakeMembership) Membership(sid string) (bool, error) {
	f.calls.Add(1)
	return f.members[sid], f.err
}

func withMembership(e *agentEnv, f *fakeMembership) {
	e.mod.membership = f.Membership
}

func withoutTabNotify(r *push.DeviceRequest) {
	r.Prefs = push.Prefs{Agents: map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}}}
}

func errEv() agent.NotifyEvent {
	return nev("c1", "PdxStopFailure", "error", map[string]any{"error": "boom"})
}

// Member spec R1: a member's waiting and idle are never pushed, tab or not; its error pushes like any session's and carries
// purdex.member: true. Mutation gate: drop rule 4a → red; drop the member flag → red; keep a member tab clause → red.
func TestMemberPush_WaitingAndIdleAreNeverPushedAnErrorIsAndCarriesTheFlag(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	withMembership(e, &fakeMembership{members: map[string]bool{"sid-1": true}})
	e.device(tokA, "en", "mlab", func(r *push.DeviceRequest) {
		r.Prefs = push.Prefs{Tabs: []string{"c1"}, Agents: map[string]push.AgentPrefs{"cc": {NotifyWithoutTab: true}}}
	})
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.feed.emit(nev("c1", "PermissionRequest", "waiting", nil))
	e.noSends(t)                                   // a tab and notify_without_tab on: still quiet
	e.device(tokA, "en", "mlab", withoutTabNotify) // no tab
	e.feed.emit(errEv())
	p := e.waitSends(t, 1)[0].Payload
	if got := purdexOf(t, p)["member"]; got != true {
		t.Fatalf("purdex.member = %v: %s", got, p)
	}
}

// A member's error with the type switched off and no tab is not pushed (the normal rules).
func TestMemberPush_ErrorWithTheTypeOffIsNotPushed(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	withMembership(e, &fakeMembership{members: map[string]bool{"sid-1": true}})
	e.device(tokA, "en", "mlab", func(r *push.DeviceRequest) {
		r.Prefs = push.Prefs{Agents: map[string]push.AgentPrefs{"cc": {Enabled: bptr(false)}}}
	})
	e.feed.emit(errEv())
	e.noSends(t)
}

// A lead is not a member: it pushes like any session.
func TestMemberPush_ALeadPushesLikeAnySession(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	withMembership(e, &fakeMembership{members: map[string]bool{"sid-other": true}})
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	p := e.waitSends(t, 1)[0].Payload
	if _, has := purdexOf(t, p)["member"]; has {
		t.Fatalf("member present on a lead: %s", p)
	}
}

// A member's card (approval path) is still pushed: the member rule never touched it.
func TestMemberPush_AMembersCardStillPushes(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	withMembership(e, &fakeMembership{members: map[string]bool{"sid-1": true}})
	e.device(tokA, "en", "mlab", nil)
	e.events.emit("opened", leadApproval("ap1"))
	if sends := e.waitSends(t, 1); !strings.Contains(sends[0].Payload, `"kind":"lead"`) {
		t.Fatalf("the card did not push: %s", sends[0].Payload)
	}
}

// A non-member pushes without a tab (option 甲) and its payload has no member field.
func TestMemberPush_NonMemberPushesWithoutATabAndLeavesTheFlagOut(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	withMembership(e, &fakeMembership{})
	e.device(tokA, "en", "mlab", withoutTabNotify)
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	p := e.waitSends(t, 1)[0].Payload
	if _, has := purdexOf(t, p)["member"]; has {
		t.Fatalf("member present on a non-member: %s", p)
	}
}

// A read error is "unknown" (member spec R4): the gate treats the session as not a member, so a waiting event is pushed, and
// the payload leaves the flag out so the phone counts it. Mutation gate: unknown → member (gate) → red; error → member:true in
// the payload → red.
func TestMemberPush_ReadErrorIsNotAMemberAndIsNotAnnounced(t *testing.T) {
	e := newAgentEnv(t, 0)
	withMembership(e, &fakeMembership{err: errors.New("db gone")})
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PermissionRequest", "waiting", nil))
	p := e.waitSends(t, 1)[0].Payload
	if _, has := purdexOf(t, p)["member"]; has {
		t.Fatalf("an unknown must not be announced as a member: %s", p)
	}
}

// No team module (nothing registered) means no teams: nobody is a member.
func TestMemberPush_NoReaderMeansNoMembers(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", withoutTabNotify)
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.waitSends(t, 1)
}

// Membership is read once per event, not once per device.
func TestMemberPush_ReadOncePerEvent(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	f := &fakeMembership{members: map[string]bool{"sid-1": true}}
	withMembership(e, f)
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	e.device(tokB, "en", "mlab", tabsOf("c1"))
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.waitSends(t, 2)
	if n := f.calls.Load(); n != 1 {
		t.Fatalf("Membership called %d times for one event and two devices, want 1", n)
	}
}

// An event with no session id is never a member (and the reader is not asked).
func TestMemberPush_NoSessionIDIsNotAMember(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	f := &fakeMembership{members: map[string]bool{"": true}}
	withMembership(e, f)
	e.device(tokA, "en", "mlab", withoutTabNotify)
	ev := nev("c1", "PdxStop", "idle", nil)
	ev.SessionID = ""
	e.feed.emit(ev)
	e.waitSends(t, 1)
}

// A membership read that hangs (a contended team.db) must not hold the notify subscriber: the hub gives it a 64-event queue
// and drops past it. The read is bounded by a budget and single-flight; a read that is late or still outstanding is
// "unknown" (member spec R4: not a member, pushed, not announced), and a later event reads again once it has returned.
// Mutation gate: a synchronous read → the emit blocks past the budget → red; late → member → red.
func TestMemberPush_AHungReadDoesNotHoldTheSubscriberAndCountsAsNotAMember(t *testing.T) {
	e := newAgentEnv(t, 0)
	release := make(chan struct{})
	var calls atomic.Int32
	e.mod.membership = func(string) (bool, error) {
		if calls.Add(1) == 1 {
			<-release // the first read hangs
		}
		return true, nil // once it returns, the session is a member
	}
	e.mod.memBudget = 30 * time.Millisecond
	e.device(tokA, "en", "mlab", tabsOf("c1"))
	for i := 0; i < 5; i++ { // the first waits one budget; the next four find the read still outstanding and do not wait at all
		start := time.Now()
		e.feed.emit(nev("c1", "PermissionRequest", "waiting", nil))
		if d := time.Since(start); d > time.Second {
			t.Fatalf("event %d held the subscriber for %v", i, d)
		}
	}
	sends := e.waitSends(t, 5) // unknown = not a member: all five waiting events pushed, none announced
	for _, s := range sends {
		if _, has := purdexOf(t, s.Payload)["member"]; has {
			t.Fatalf("a late read must not be announced as a member: %s", s.Payload)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("reads = %d while the first was outstanding, want 1", n)
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	e.feed.emit(nev("c1", "PermissionRequest", "waiting", nil)) // the read has returned: a member now, waiting is quiet
	time.Sleep(100 * time.Millisecond)
	if got := e.apns.count(); got != 5 {
		t.Fatalf("a member's waiting event was pushed after the read returned (%d sends)", got)
	}
}

// A muted session gets no agent push even in a tab; its approval push still goes.
// Mutation gate: muted ignored → red.
func TestMuted_NoAgentPushButTheApprovalStillPushes(t *testing.T) {
	e := newAgentEnv(t, time.Hour)
	e.device(tokA, "en", "mlab", func(r *push.DeviceRequest) {
		r.Prefs = push.Prefs{Tabs: []string{"c1"}, Muted: []string{"c1"}}
	})
	e.feed.emit(nev("c1", "PdxStop", "idle", nil))
	e.noSends(t)
	e.events.emit("opened", leadApproval("ap1"))
	if sends := e.waitSends(t, 1); !strings.Contains(sends[0].Payload, `"kind":"lead"`) {
		t.Fatalf("the approval push did not go: %s", sends[0].Payload)
	}
}

// Prefs with muted and badge_push round-trip the store: written, read back by a fresh module over the same data dir.
func TestPrefs_MutedAndBadgePushRoundTripTheStore(t *testing.T) {
	e := newEnv(t)
	rec := e.do("POST", "/api/push/devices", reqBody(tokA, func(r *push.DeviceRequest) {
		r.Prefs = push.Prefs{Tabs: []string{"c1"}, Muted: []string{"m1", "m2"}, BadgePush: true}
	}))
	if rec.Code != 200 && rec.Code != 201 {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	devs, err := e.mod.store.List()
	if err != nil || len(devs) != 1 {
		t.Fatalf("store list: %v %v", devs, err)
	}
	got := devs[0].Prefs
	if strings.Join(got.Muted, ",") != "m1,m2" || !got.BadgePush || strings.Join(got.Tabs, ",") != "c1" {
		t.Fatalf("stored prefs = %+v", got)
	}
	// an older App sends neither: they stay zero
	e.do("POST", "/api/push/devices", reqBody(tokA, nil))
	devs, _ = e.mod.store.List()
	if len(devs[0].Prefs.Muted) != 0 || devs[0].Prefs.BadgePush {
		t.Fatalf("a re-registration without the fields must clear them: %+v", devs[0].Prefs)
	}
}

// A session id too long to be a real one (a damaged or hostile origin) never becomes a c: key: it would push every payload of
// the host over 4 KiB while the card is open. The card falls back to its own a: key; 64 bytes (a UUID is 36) still keys.
// Mutation gate: no length bound → red.
func TestBadgeV2_OversizedSessionIDFallsBackToAnApprovalKey(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	edge := strings.Repeat("y", 64)
	open := []team.Approval{
		sessionCard("big", team.KindHookPermission, strings.Repeat("x", 5000)),
		sessionCard("edge", team.KindLead, edge),
		leadApproval("ap1"), // the pushed card itself is open, or the sender drops its push
	}
	te.events.readOpen = func() ([]team.Approval, error) { return open, nil }
	te.events.emit("opened", leadApproval("ap1"))
	p := te.waitSends(t, 1)[0].Payload
	keys, _ := keysOf(t, p)
	if want := "a:big,c:sid-1,c:" + edge; strings.Join(keys, ",") != want {
		t.Fatalf("keys = %.120v, want %s", keys, want)
	}
	if len(p) > 4096 {
		t.Fatalf("payload %d bytes", len(p))
	}
}

// Invalid muted is refused like invalid tabs.
func TestPrefs_InvalidMutedIsRefused(t *testing.T) {
	e := newEnv(t)
	rec := e.do("POST", "/api/push/devices", reqBody(tokA, func(r *push.DeviceRequest) { r.Prefs.Muted = []string{""} }))
	if rec.Code != 400 {
		t.Fatalf("code = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
