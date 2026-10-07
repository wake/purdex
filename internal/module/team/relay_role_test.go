package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// everySwitch is every setting of the two host switches (spec §8.7 (a)).
var everySwitch = []hostconfig.RelaySwitches{
	{SelfSolo: true, SelfLead: true},
	{SelfSolo: true, SelfLead: false},
	{SelfSolo: false, SelfLead: true},
	{SelfSolo: false, SelfLead: false},
}

// makeMember makes sid an active member of live team uid(9), led by sid-2.
func (f *fixture) makeMember(sid string) {
	f.t.Helper()
	seedTeam(f.t, f.m.store, uid(9), "sid-2", f.clock.Load())
	seedMember(f.t, f.m.store, "op-1", uid(9), sid, f.clock.Load())
}

func (f *fixture) hello(sid string) (int, team.RelayHelloResponse, []byte) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: sid, ModVersion: "1", Agent: "cc"})
	var h team.RelayHelloResponse
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &h); err != nil {
			f.t.Fatalf("decode hello: %v; body=%s", err, body)
		}
	}
	return code, h, body
}

func (f *fixture) self(sid, action string) (int, team.RelaySelfResponse, []byte) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/relay/self", team.RelaySelfRequest{SessionID: sid, Action: action})
	var r team.RelaySelfResponse
	if code == http.StatusOK {
		if err := json.Unmarshal(body, &r); err != nil {
			f.t.Fatalf("decode self: %v; body=%s", err, body)
		}
	}
	return code, r, body
}

// Spec U13 / §8.7: a member has no switch — its hello answers role member
// and self_relay off whatever the host switches say (the binding d3 ask).
// Mutation gate: relayRole back to "none" → red.
func TestRelayHello_MemberAnswersSelfRelayOff(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	for _, sw := range everySwitch {
		f.switches.set(sw)
		code, h, body := f.hello("sid-1")
		if code != http.StatusOK || !h.OK || h.Role != "member" || h.SelfRelay != "off" {
			t.Fatalf("member hello under %+v: %d %s, want role member, self_relay off", sw, code, body)
		}
	}
	f.switches.set(hostconfig.DefaultRelaySwitches)
	if code, h, body := f.hello("sid-2"); code != http.StatusOK || h.Role != "lead" || h.SelfRelay != "on" {
		t.Fatalf("the team's lead: %d %s, want role lead, on", code, body)
	}
}

// Spec §8.1 / §14: a member's self-relay begin is 409 member_relay_is_leads
// (exit 13) under every switch setting, and nothing is opened — no op, no
// approval row, no event, no id minted. Mutation gate: relayRole back to
// "none" → red (begin answers 201, or self_relay_off).
func TestRelayBegin_MemberIsRefused(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	for _, sw := range everySwitch {
		f.switches.set(sw)
		code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1"))
		if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberRelayIsLeads {
			t.Fatalf("member begin under %+v: %d %s, want 409 %s", sw, code, body, team.ErrMemberRelayIsLeads)
		}
	}
	if active, _ := f.m.store.ListActiveRelayOps(); len(active) != 0 {
		t.Fatalf("active ops after refused begins = %+v", active)
	}
	if open, _ := f.m.store.ListOpen(); len(open) != 0 {
		t.Fatalf("open approvals after refused begins = %+v", open)
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after refused begins", n)
	}
	// The team's lead may still self-relay (self_lead on): its op gets the
	// first ids, so the refusals minted none.
	f.switches.set(hostconfig.DefaultRelaySwitches)
	if out := f.begin("sid-2"); out.Op.ID != rid(1) || out.RequestID != rid(2) {
		t.Fatalf("lead begin = %+v, want op %s request %s", out, rid(1), rid(2))
	}
}

// Spec §8.7 (a): `/relay on` and `/relay off` in a member answer
// member_relay_is_leads and store no pause; status is 200 with member true,
// self_relay off and no host switch.
func TestRelaySelf_OnOffInAMemberIsRefusedStatusSaysMember(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	for _, action := range []string{"on", "off"} {
		code, _, body := f.self("sid-1", action)
		if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberRelayIsLeads {
			t.Fatalf("member self %s: %d %s, want 409 %s", action, code, body, team.ErrMemberRelayIsLeads)
		}
	}
	if paused, err := f.m.store.SelfRelayPaused("sid-1"); err != nil || paused {
		t.Fatalf("a refused self off stored a pause: paused=%v err=%v", paused, err)
	}
	code, r, body := f.self("sid-1", "status")
	if code != http.StatusOK || r.SelfRelay != "off" || r.HostSwitch || !r.Member {
		t.Fatalf("member status: %d %s, want off, no host switch, member", code, body)
	}
}

// Spec §8.7 (a): a lead reads relay.self_lead and an ordinary session
// relay.self_solo; the pause narrows a lead too.
func TestRelayHello_LeadReadsTheLeadSwitch(t *testing.T) {
	f := newFixture(t)
	seedTeam(t, f.m.store, uid(9), "sid-1", f.clock.Load())
	f.switches.set(hostconfig.RelaySwitches{SelfSolo: true, SelfLead: false})
	if code, h, body := f.hello("sid-1"); code != http.StatusOK || h.Role != "lead" || h.SelfRelay != "off" {
		t.Fatalf("lead, self_lead off: %d %s", code, body)
	}
	if code, h, body := f.hello("sid-2"); code != http.StatusOK || h.Role != "none" || h.SelfRelay != "on" {
		t.Fatalf("solo, self_solo on: %d %s", code, body)
	}
	if code, r, body := f.self("sid-1", "status"); code != http.StatusOK || r.SelfRelay != "off" || r.HostSwitch || r.Member {
		t.Fatalf("lead status, self_lead off: %d %s", code, body)
	}
	code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1"))
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrSelfRelayOff {
		t.Fatalf("lead begin, self_lead off: %d %s, want 409 %s", code, body, team.ErrSelfRelayOff)
	}

	f.switches.set(hostconfig.RelaySwitches{SelfSolo: false, SelfLead: true})
	if code, h, body := f.hello("sid-1"); code != http.StatusOK || h.Role != "lead" || h.SelfRelay != "on" {
		t.Fatalf("lead, self_lead on: %d %s", code, body)
	}
	if code, h, body := f.hello("sid-2"); code != http.StatusOK || h.Role != "none" || h.SelfRelay != "off" {
		t.Fatalf("solo, self_solo off: %d %s", code, body)
	}
	if code, r, body := f.self("sid-1", "off"); code != http.StatusOK || r.SelfRelay != "paused" || !r.HostSwitch || r.Member {
		t.Fatalf("lead self off: %d %s, want paused under self_lead", code, body)
	}
}

// D4: when its team ends, a member is an ordinary session again — role
// none, the solo switch, its own pause, and begin opens. A killed member of
// a live team is none too.
func TestRelayRole_MemberOfAnEndedTeamIsNone(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	if ended, err := f.m.store.EndTeam(uid(9), "sid-2", team.TeamEndLeadGone, f.clock.Load()); err != nil || !ended {
		t.Fatalf("end: ended=%v err=%v", ended, err)
	}
	if code, h, body := f.hello("sid-1"); code != http.StatusOK || h.Role != "none" || h.SelfRelay != "on" {
		t.Fatalf("member of an ended team: %d %s, want role none, on", code, body)
	}
	if code, h, body := f.hello("sid-2"); code != http.StatusOK || h.Role != "none" {
		t.Fatalf("lead of an ended team: %d %s, want role none", code, body)
	}
	if code, r, body := f.self("sid-1", "off"); code != http.StatusOK || r.SelfRelay != "paused" || r.Member {
		t.Fatalf("self off: %d %s", code, body)
	}
	if code, r, body := f.self("sid-1", "on"); code != http.StatusOK || r.SelfRelay != "on" {
		t.Fatalf("self on: %d %s", code, body)
	}
	if out := f.begin("sid-1"); out.Op.SessionID != "sid-1" {
		t.Fatalf("begin = %+v", out)
	}

	g := newFixture(t)
	seedTeam(t, g.m.store, uid(8), "lead-x", g.clock.Load())
	seedMember(t, g.m.store, "op-2", uid(8), "sid-2", g.clock.Load())
	if err := g.m.store.SetMemberState("op-2", team.MemberKilled, g.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if code, h, body := g.hello("sid-2"); code != http.StatusOK || h.Role != "none" || h.SelfRelay != "on" {
		t.Fatalf("killed member: %d %s, want role none, on", code, body)
	}
}

// Spec §6.2 member_cannot_lead (no nested teams in v1): an active member of
// a live team asking for lead is 409 member_cannot_lead (exit 13), and
// nothing is stored or broadcast. Once its team has ended it may ask (D4).
// A member read that fails is a 500, never a pass.
func TestCreate_MemberCannotLead(t *testing.T) {
	f := newFixture(t)
	f.makeMember("sid-1")
	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberCannotLead || e.Approval != nil {
		t.Fatalf("member asks for lead: %d %s, want 409 %s", code, body, team.ErrMemberCannotLead)
	}
	if _, ok, _ := f.m.store.Get(uid(1)); ok {
		t.Fatal("member_cannot_lead stored the request")
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after member_cannot_lead", n)
	}
	if ended, err := f.m.store.EndTeam(uid(9), "sid-2", team.TeamEndLeadGone, f.clock.Load()); err != nil || !ended {
		t.Fatalf("end: ended=%v err=%v", ended, err)
	}
	f.create(uid(1)) // a member of an ended team is an ordinary session

	g := newFixture(t)
	if _, err := g.m.store.db.Exec(`DROP TABLE team_members`); err != nil {
		t.Fatal(err)
	}
	code, body = g.do(http.MethodPost, "/api/team/approvals", g.createReq(uid(1)))
	if e := decodeErr(t, body); code != http.StatusInternalServerError || e.Error != errStorage {
		t.Fatalf("create with an unreadable member table: %d %s, want 500 %s", code, body, errStorage)
	}
}

// P4-3 review H1: the approve re-checks the member rule in its own
// transaction. An origin that became an active member after it asked gets
// 409 member_cannot_lead without an approval; the row stays open, no team.
func TestDecide_MemberCannotLeadAtApprove(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.makeMember("sid-1")
	f.events()
	code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", appApprove(nil))
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberCannotLead || e.Approval != nil {
		t.Fatalf("approve of a new member: %d %s, want 409 %s", code, body, team.ErrMemberCannotLead)
	}
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("row = %s, want open", a.State)
	}
	if _, ok := getTeam(t, f.m.store, uid(1)); ok || len(f.events()) != 0 {
		t.Fatalf("team made=%v or an event was sent", ok)
	}
}

// P4-3 review H2 (U13): a session that became a member after its solo
// begin is never claimed. Its approve is 409 member_relay_is_leads in the
// approve transaction, and the request and op are cancelled instead: the
// mod follows the ROW, so an approved row would still write and /clear.
func TestDecide_SelfRelayOfANewMemberIsCancelled(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.makeMember("sid-1")
	f.events()
	code, body := f.decide(out.RequestID, "approve")
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberRelayIsLeads {
		t.Fatalf("approve: %d %s, want 409 %s", code, body, team.ErrMemberRelayIsLeads)
	}
	if a, _, _ := f.m.store.Get(out.RequestID); a.State != team.StateCancelled || f.countOps("closed") != 1 {
		t.Fatalf("row = %s, want cancelled with one closed event", a.State)
	}
	if op := f.op(out.Op.ID); op.State != team.RelayCancelled || op.Reason != team.ErrMemberRelayIsLeads {
		t.Fatalf("op = %s (%s), want cancelled (%s)", op.State, op.Reason, team.ErrMemberRelayIsLeads)
	}
}

// H2, the op paths: an approved row whose op was not moved yet (afterClose
// missed it) is reconciled at boot, and a member's op is cancelled, not claimed.
func TestReconcile_ANewMembersApprovedRowDoesNotClaim(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	if _, won, err := f.m.store.CloseIfOpen(out.RequestID, Close{State: team.StateApproved, DecidedAt: 1}); err != nil || !won {
		t.Fatalf("approve in the store: won=%v err=%v", won, err)
	}
	f.makeMember("sid-1")
	f.m.reconcileRelays()
	if op := f.op(out.Op.ID); op.State != team.RelayCancelled || op.Reason != team.ErrMemberRelayIsLeads {
		t.Fatalf("op = %s (%s), want cancelled (%s)", op.State, op.Reason, team.ErrMemberRelayIsLeads)
	}
}

// H2, begin's TOCTOU: the role is read again under createMu just before
// the op is created, so a session that became a member meanwhile opens nothing.
func TestRelayBegin_MemberSinceTheCheckIsRefused(t *testing.T) {
	f := newFixture(t)
	f.m.afterOpenCheck = func(string) { f.makeMember("sid-1") }
	code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1"))
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberRelayIsLeads {
		t.Fatalf("begin: %d %s, want 409 %s", code, body, team.ErrMemberRelayIsLeads)
	}
	if active, _ := f.m.store.ListActiveRelayOps(); len(active) != 0 {
		t.Fatalf("ops opened: %+v", active)
	}
}

// Plan v3 deviation 12: a role that cannot be read is a 500 on hello, self
// and begin, never "none" (fail closed, spec §8.7 (d)). Both switches are
// off, so the role read is the only store read on hello, status and begin:
// a role read that swallowed the error would answer 200 / 409 here.
func TestRelayRole_StoreErrorIs500(t *testing.T) {
	for _, table := range []string{"teams", "team_members"} {
		t.Run(table, func(t *testing.T) {
			f := newFixture(t)
			f.switches.set(hostconfig.RelaySwitches{})
			if _, err := f.m.store.db.Exec(`DROP TABLE ` + table); err != nil {
				t.Fatal(err)
			}
			check := func(what string, code int, body []byte) {
				t.Helper()
				if e := decodeErr(t, body); code != http.StatusInternalServerError || e.Error != errStorage {
					t.Fatalf("%s with an unreadable role: %d %s, want 500 %s", what, code, body, errStorage)
				}
			}
			code, _, body := f.hello("sid-1")
			check("hello", code, body)
			for _, action := range []string{"status", "on", "off"} {
				code, _, body := f.self("sid-1", action)
				check("self "+action, code, body)
			}
			code, body = f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-1"))
			check("begin", code, body)
			if paused, err := f.m.store.SelfRelayPaused("sid-1"); err != nil || paused {
				t.Fatalf("self off stored a pause: paused=%v err=%v", paused, err)
			}
			if active, _ := f.m.store.ListActiveRelayOps(); len(active) != 0 {
				t.Fatalf("begin opened an op: %+v", active)
			}
		})
	}
}
