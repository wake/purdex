package teammod

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// MR-1 (member relay spec v2, U-M1, D1–D3): a person's /relay in a LOCAL member's session is always let through, on the
// ordinary session's path. `manual` is the begin's property, stored in the row's payload; every gate reads it from the
// row. Without it everything stays as before.

func manualReq(sid string) team.RelayBeginRequest {
	r := beginReq(sid)
	r.Manual = true
	return r
}

func (f *fixture) beginManual(sid string) (int, []byte) {
	f.t.Helper()
	return f.do(http.MethodPost, "/api/relay/begin", manualReq(sid))
}

func (f *fixture) manualOpened(sid string) team.RelayBeginResponse {
	f.t.Helper()
	code, body := f.beginManual(sid)
	if code != http.StatusCreated {
		f.t.Fatalf("manual begin %s: %d %s", sid, code, body)
	}
	var out team.RelayBeginResponse
	if err := json.Unmarshal(body, &out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func selfPayloadOf(t *testing.T, f *fixture, rid string) team.SelfRelayPayload {
	t.Helper()
	a, ok, err := f.m.store.Get(rid)
	if err != nil || !ok {
		t.Fatalf("row %s: ok=%v err=%v", rid, ok, err)
	}
	var sp team.SelfRelayPayload
	if err := json.Unmarshal(a.Payload, &sp); err != nil {
		t.Fatal(err)
	}
	return sp
}

// A member's manual begin opens a card under the ordinary session's switch. Mutation gates: drop `!manual` from begin's
// first role check → 409 member_relay_is_leads (red); a member's own state (always "off") kept for manual → 409
// self_relay_off (red); the payload without manual → the stored flag is false (red).
func TestManualBegin_ALocalMemberOpensACardUnderTheOrdinarySwitch(t *testing.T) {
	for _, sw := range everySwitch {
		f := newFixture(t)
		f.makeMember("sid-1")
		f.switches.set(sw)
		code, body := f.beginManual("sid-1")
		if !sw.SelfSolo {
			if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrSelfRelayOff {
				t.Fatalf("manual begin with solo off (%+v): %d %s, want 409 %s", sw, code, body, team.ErrSelfRelayOff)
			}
			continue
		}
		if code != http.StatusCreated {
			t.Fatalf("manual begin under %+v: %d %s, want 201", sw, code, body)
		}
		var out team.RelayBeginResponse
		_ = json.Unmarshal(body, &out)
		if op := f.op(out.Op.ID); op.State != team.RelayAwaitingApproval {
			t.Fatalf("op = %s, want awaiting_approval", op.State)
		}
		if st := f.rowState(out.RequestID); st != team.StateOpen {
			t.Fatalf("row = %s, want open (a person approves)", st)
		}
		if !selfPayloadOf(t, f, out.RequestID).Manual {
			t.Fatal("the row's payload does not say manual")
		}
		// the same member without manual is still refused
		g := newFixture(t)
		g.makeMember("sid-1")
		g.switches.set(sw)
		code, body = g.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1"))
		if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberRelayIsLeads {
			t.Fatalf("non-manual member begin: %d %s, want 409 %s", code, body, team.ErrMemberRelayIsLeads)
		}
	}
}

// The member cannot lift the pause (`/relay on` is refused), and the pause is about relays the mod starts: a person's
// /relay ignores it. Mutation gate: honour the pause for manual → 409 self_relay_paused (red).
func TestManualBegin_AMembersPauseIsIgnored(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	if err := f.m.store.SetSelfRelayPaused("sid-1", true, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	f.manualOpened("sid-1")
}

// Begin's re-check under createMu: a session that became a member since the first check is let through when manual and
// refused when not. Mutation gate: drop `!manual` from the re-check → the manual one is refused (red).
func TestManualBegin_TheRecheckUnderCreateMuLetsAManualOneThrough(t *testing.T) {
	f := newFixture(t)
	f.m.afterOpenCheck = func(string) { f.makeMember("sid-1") }
	f.manualOpened("sid-1")

	g := newFixture(t)
	g.m.afterOpenCheck = func(string) { g.makeMember("sid-1") }
	code, body := g.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1"))
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberRelayIsLeads {
		t.Fatalf("non-manual: %d %s, want 409 %s", code, body, team.ErrMemberRelayIsLeads)
	}
}

// A remote member's manual begin waits for MR-2: relay_unsupported, nothing opened.
func TestManualBegin_ARemoteMemberIsUnsupportedUntilMR2(t *testing.T) {
	f := newFixture(t)
	seedRemote(t, f.m.store, "mk-1", "sid-1", f.clock.Load())
	code, body := f.beginManual("sid-1")
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrRelayUnsupported {
		t.Fatalf("remote member manual begin: %d %s, want 409 %s", code, body, team.ErrRelayUnsupported)
	}
	if active, _ := f.m.store.ListActiveRelayOps(); len(active) != 0 {
		t.Fatalf("ops opened: %+v", active)
	}
	if open, _ := f.m.store.ListOpen(); len(open) != 0 {
		t.Fatalf("approvals opened: %+v", open)
	}
}

// manual is part of the begin's replay identity. Mutation gate: leave it out of the comparator → the second begin
// answers 201 with the first op (red).
func TestManualBegin_ReplayWithAnotherManualIsAConflict(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	first := manualReq("sid-1")
	first.RequestID = uid(5)
	if code, body := f.do(http.MethodPost, "/api/relay/begin", first); code != http.StatusCreated {
		t.Fatalf("first: %d %s", code, body)
	}
	same := first
	if code, body := f.do(http.MethodPost, "/api/relay/begin", same); code != http.StatusCreated {
		t.Fatalf("an identical replay: %d %s, want 201", code, body)
	}
	other := first
	other.Manual = false
	if code, body := f.do(http.MethodPost, "/api/relay/begin", other); code != http.StatusConflict {
		t.Fatalf("the same request_id without manual: %d %s, want 409", code, body)
	}
}

// The click's approve reads manual from the ROW. Mutation gates: read it from anywhere else / ignore it → the manual
// member row is cancelled (red); a non-manual row of a session that became a member is still cancelled (existing test).
func TestManualApprove_AMembersManualRowIsApprovedAndItsOpClaimed(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	out := f.manualOpened("sid-1")
	code, body := f.decide(out.RequestID, "approve")
	if code != http.StatusOK {
		t.Fatalf("approve: %d %s", code, body)
	}
	if st := f.rowState(out.RequestID); st != team.StateApproved {
		t.Fatalf("row = %s, want approved", st)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayClaimed {
		t.Fatalf("op = %s (%s), want claimed", op.State, op.Reason)
	}
}

// A solo begin that became a member afterwards is NOT manual: still cancelled at the approve, even though a manual
// member relay is allowed. Mutation gate: lift the cancel for every member → approved (red).
func TestManualApprove_ANonManualRowOfANewMemberIsStillCancelled(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.makeMember("sid-1")
	code, body := f.decide(out.RequestID, "approve")
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberRelayIsLeads {
		t.Fatalf("approve: %d %s, want 409 %s", code, body, team.ErrMemberRelayIsLeads)
	}
}

// The lift is for a LOCAL member only (a remote one waits for MR-2): a manual row of a session that became a member of a
// team led on another host after its begin is still cancelled at the approve. Mutation gate: treat any member as local
// at the approve → approved (red).
func TestManualApprove_ARowOfASessionThatBecameARemoteMemberIsCancelled(t *testing.T) {
	f := newFixture(t)
	out := f.manualOpened("sid-1") // no role at begin
	seedRemote(t, f.m.store, "mk-1", "sid-1", f.clock.Load())
	code, body := f.decide(out.RequestID, "approve")
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberRelayIsLeads {
		t.Fatalf("approve: %d %s, want 409 %s", code, body, team.ErrMemberRelayIsLeads)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayCancelled {
		t.Fatalf("op = %s, want cancelled", op.State)
	}
}

// The op paths: an approved manual row whose op was not moved yet (afterClose missed it) is reconciled — claimed for a
// local member, cancelled for a session that became a remote one. Mutation gates: closedRowReport cancelling every
// member → the first is cancelled (red); treating every member as local → the second is claimed (red).
func TestManualReconcile_AnApprovedManualRowIsClaimedForALocalMemberOnly(t *testing.T) {
	for _, remote := range []bool{false, true} {
		f := newFixture(t)
		out := f.manualOpened("sid-1")
		if _, won, err := f.m.store.CloseIfOpen(out.RequestID, Close{State: team.StateApproved, DecidedAt: 1}); err != nil || !won {
			t.Fatalf("approve in the store: won=%v err=%v", won, err)
		}
		if remote {
			seedRemote(t, f.m.store, "mk-1", "sid-1", f.clock.Load())
		} else {
			f.makeMember("sid-1")
		}
		f.m.reconcileRelays()
		op := f.op(out.Op.ID)
		if remote && op.State != team.RelayCancelled {
			t.Fatalf("remote: op = %s, want cancelled", op.State)
		}
		if !remote && op.State != team.RelayClaimed {
			t.Fatalf("local: op = %s (%s), want claimed", op.State, op.Reason)
		}
	}
}

// Unattended mode never approves a member's manual relay and never spends: the begin falls back to an open card, the
// sweeps leave it, a person's click approves it without a spend. Mutation gates: create it approved → row approved
// (red); the sweep approves it → red; spend → self_left drops (red).
func TestManualUnattended_AMembersManualRelayAlwaysWaitsForAPerson(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	f.qrule.set(true, nil)
	f.unatt.set(true)
	f.setQuota("sid-1", 2)
	f.events()
	out := f.manualOpened("sid-1")
	if st := f.rowState(out.RequestID); st != team.StateOpen {
		t.Fatalf("row = %s after an unattended manual begin, want open", st)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayAwaitingApproval {
		t.Fatalf("op = %s, want awaiting_approval", op.State)
	}
	for range 3 {
		f.m.tick()
		f.sweep()
	}
	if st := f.rowState(out.RequestID); st != team.StateOpen {
		t.Fatalf("row = %s after the sweeps, want still open", st)
	}
	if n := f.selfLeft("sid-1"); n != 2 {
		t.Fatalf("self_left = %d, want untouched 2", n)
	}
	if code, body := f.decide(out.RequestID, "approve"); code != http.StatusOK {
		t.Fatalf("click: %d %s", code, body)
	}
	if n := f.selfLeft("sid-1"); n != 2 {
		t.Fatalf("self_left = %d after the person's approve, want 2 (a member has no quota of its own)", n)
	}
}

// An unattended LEAD or solo session is unchanged: the same switch settings still approve it at begin.
func TestManualUnattended_ASoloSessionIsStillApprovedAtBegin(t *testing.T) {
	f := newFixture(t)
	f.unatt.set(true)
	r := manualReq("sid-1") // manual from a session with no role is just a begin
	code, body := f.do(http.MethodPost, "/api/relay/begin", r)
	if code != http.StatusCreated {
		t.Fatalf("begin: %d %s", code, body)
	}
	var out team.RelayBeginResponse
	_ = json.Unmarshal(body, &out)
	if st := f.rowState(out.RequestID); st != team.StateApproved {
		t.Fatalf("row = %s, want approved", st)
	}
	_ = hostconfig.DefaultRelaySwitches
}

// D3: the cleared that moves a member row stamps that row's team on the self op, in the same transaction. A lead's
// self op is not stamped. Mutation gate: no stamp → red.
func TestRelayStore_ClearedStampsTheMembersTeamOnTheSelfOp(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, "team-1", "L1", 1000)
	m1 := seedMember(t, s, "sp-1", "team-1", "M1", 1000)

	claimedOp(t, s, "op-m", "M1", m1.Ref)
	mustReport(t, s, "op-m", RelayReport{State: team.RelayCleared, NewSessionID: "M2", NewRef: "_mmm222", At: 6000})
	if op, _, _ := s.GetRelayOp("op-m"); op.TeamID != "team-1" {
		t.Fatalf("member's self op team_id = %q, want team-1", op.TeamID)
	}

	claimedOp(t, s, "op-l", "L1", "_abc123")
	mustReport(t, s, "op-l", RelayReport{State: team.RelayCleared, NewSessionID: "L2", NewRef: "_lll222", At: 7000})
	if op, _, _ := s.GetRelayOp("op-l"); op.TeamID != "" {
		t.Fatalf("a lead's self op team_id = %q, want empty", op.TeamID)
	}
}

// D3: the lead is told at done, only for a self op that has a team (only a manual member relay reaches cleared as a
// member). Mutation gates: outcomeNoticeAsync still skipping self ops → nothing sent (red); the done text not the
// manual one → red; failed/cancelled announced → red; a self op without a team (the lead's own) announced → red.
func TestManualNotice_TheLeadHearsAboutAManualMemberRelay(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1") // team uid(9), lead sid-2
	op := team.RelayOp{ID: "op-1", Kind: team.RelayKindSelf, SessionID: "sid-1", Ref: "_abc123", NewSessionID: "sid-1b", NewRef: "_new111", TeamID: uid(9), State: team.RelayDone}
	f.m.outcomeNoticeAsync(op)
	waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	want := "[pdx team] member 由使用者手動接力：_abc123 → _new111"
	if got := f.sender.calls()[0].Text; got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}

	for _, st := range []team.RelayState{team.RelayFailed, team.RelayCancelled} {
		bad := op
		bad.State = st
		f.m.outcomeNoticeAsync(bad)
	}
	noTeam := op
	noTeam.TeamID = ""
	f.m.outcomeNoticeAsync(noTeam)
	time.Sleep(50 * time.Millisecond)
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("%d notices, want exactly the one: a failed/cancelled manual relay and a lead's own self op say nothing", n)
	}
	if strings.Contains(f.sender.calls()[0].Text, "接力完成") {
		t.Fatal("the generic member text was sent for a manual relay")
	}
}
