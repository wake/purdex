// internal/module/team/remote_members_test.go
package teammod

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// newRemote is an active remote member row (cross-host team spec §5.1).
func newRemote(mk, sid, leadHost string, at int64) remoteMemberRow {
	return remoteMemberRow{MK: mk, MemberSessionID: sid, Ref: "_r" + mk, TeamID: "team-L", TeamName: "T",
		LeadHostID: leadHost, LeadSessionID: "lead-sid", LeadRef: "_lead01", LeadAddress: "lead/x [lead01]",
		LeadTitle: "lead", LeadPID: 7, LeadProcStart: "ps1", Origin: "adopted", State: remoteActive,
		PID: 42, ProcStart: "ps2", Cwd: "/w", Title: "m", CreatedAt: at, UpdatedAt: at}
}

func seedRemote(t *testing.T, s *Store, mk, sid string, at int64) remoteMemberRow {
	t.Helper()
	r := newRemote(mk, sid, "host-L", at)
	if err := s.InsertRemoteMember(r); err != nil {
		t.Fatalf("seed remote member %s: %v", mk, err)
	}
	return r
}

func TestRemoteMembers_InsertIdempotentOnMKAndOneActivePerSession(t *testing.T) {
	s := openTestStore(t)
	r := seedRemote(t, s, "mk-1", "sid-1", 1000)
	again := r
	again.Title, again.UpdatedAt = "other", 2000
	if err := s.InsertRemoteMember(again); err != nil {
		t.Fatalf("replayed insert: %v", err)
	}
	got, ok, err := s.RemoteMember("mk-1")
	if err != nil || !ok || !reflect.DeepEqual(got, r) {
		t.Fatalf("got %+v ok=%v err=%v, want %+v untouched", got, ok, err, r)
	}
	if err := s.InsertRemoteMember(newRemote("mk-2", "sid-1", "host-L", 3000)); err == nil {
		t.Fatal("a second active row for one session was stored")
	}
	if ok, err := s.SetRemoteMemberState("mk-1", []string{remoteActive}, remoteReleased, 4000); err != nil || !ok {
		t.Fatalf("release: %v %v", ok, err)
	}
	seedRemote(t, s, "mk-2", "sid-1", 5000) // the released row frees the session
}

// The CAS is the monotonic basis of §5.2: a change whose from-state is not
// the row's current one finds nothing and changes nothing.
func TestRemoteMembers_CASFromAllowedStatesOnly(t *testing.T) {
	s := openTestStore(t)
	seedRemote(t, s, "mk-1", "sid-1", 1000)
	if ok, err := s.SetRemoteMemberState("mk-1", []string{remoteActive}, remoteReleased, 2000); err != nil || !ok {
		t.Fatalf("active→released: %v %v", ok, err)
	}
	// A late kill finds the row released: ignored, never forced back.
	if ok, err := s.SetRemoteMemberState("mk-1", []string{remoteActive}, remoteKilled, 3000); err != nil || ok {
		t.Fatalf("killed over released: ok=%v err=%v", ok, err)
	}
	got, _, _ := s.RemoteMember("mk-1")
	if got.State != remoteReleased || got.UpdatedAt != 2000 {
		t.Fatalf("row = %+v", got)
	}
	if ok, err := s.SetRemoteMemberState("mk-nope", []string{remoteActive}, remoteGone, 1); err != nil || ok {
		t.Fatalf("unknown mk: ok=%v err=%v", ok, err)
	}
	if _, err := s.SetRemoteMemberState("mk-1", []string{remoteActive}, "bogus", 1); err == nil {
		t.Fatal("an unknown target state was accepted")
	}
}

// Only active → a terminal state is a move (§5.2); nothing comes back to active
// and one terminal state never overwrites another (codex attack).
func TestRemoteMembers_CASRefusesIllegalTransitions(t *testing.T) {
	s := openTestStore(t)
	seedRemote(t, s, "mk-1", "sid-1", 1000)
	if _, err := s.SetRemoteMemberState("mk-1", []string{remoteActive}, remoteActive, 2000); err == nil {
		t.Fatal("active → active accepted")
	}
	for _, from := range []string{remoteReleased, remoteKilled, remoteGone, remoteEnded} {
		if _, err := s.SetRemoteMemberState("mk-1", []string{from}, remoteActive, 2000); err == nil {
			t.Fatalf("%s → active accepted", from)
		}
		if _, err := s.SetRemoteMemberState("mk-1", []string{from}, remoteEnded, 2000); err == nil {
			t.Fatalf("%s → ended accepted", from)
		}
	}
	if _, err := s.SetRemoteMemberState("mk-1", []string{remoteActive, remoteReleased}, remoteGone, 2000); err == nil {
		t.Fatal("a from-set holding a terminal state accepted")
	}
	if got, _, _ := s.RemoteMember("mk-1"); got.State != remoteActive || got.UpdatedAt != 1000 {
		t.Fatalf("row = %+v, want untouched", got)
	}
}

// A replay of the same membership is a no-op; the same mk naming another
// session, team, host or origin is an id_conflict, not a silent success.
func TestRemoteMembers_InsertSameMKOtherContentIsAConflict(t *testing.T) {
	s := openTestStore(t)
	r := seedRemote(t, s, "mk-1", "sid-1", 1000)
	// The lead fields and state may differ on a replay (lead_moved, a later state).
	moved := r
	moved.LeadSessionID, moved.LeadRef, moved.UpdatedAt = "lead-2", "_lead02", 5000
	if err := s.InsertRemoteMember(moved); err != nil {
		t.Fatalf("replay with moved lead: %v", err)
	}
	for name, mut := range map[string]func(*remoteMemberRow){
		"session":   func(x *remoteMemberRow) { x.MemberSessionID = "sid-other" },
		"team":      func(x *remoteMemberRow) { x.TeamID = "team-other" },
		"lead host": func(x *remoteMemberRow) { x.LeadHostID = "host-other" },
		"origin":    func(x *remoteMemberRow) { x.Origin = "spawned" },
	} {
		x := r
		mut(&x)
		if err := s.InsertRemoteMember(x); !errors.Is(err, ErrRemoteMemberConflict) {
			t.Fatalf("%s: err = %v, want ErrRemoteMemberConflict", name, err)
		}
	}
	if got, _, _ := s.RemoteMember("mk-1"); got.MemberSessionID != "sid-1" || got.UpdatedAt != 1000 {
		t.Fatalf("row = %+v, want untouched", got)
	}
}

func TestSessionRole(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, "team-1", "lead-1", 1000)
	seedMember(t, s, "op-1", "team-1", "local-m", 1000)
	seedRemote(t, s, "mk-1", "remote-m", 1000)
	for sid, want := range map[string]sessionRole{
		"lead-1":   sessionRoleLead,
		"local-m":  sessionRoleMemberLocal,
		"remote-m": sessionRoleMemberRemote,
		"nobody":   sessionRoleNone,
	} {
		got, err := s.SessionRole(sid)
		if err != nil || got != want {
			t.Fatalf("role(%s) = %q err=%v, want %q", sid, got, err, want)
		}
	}
	// A remote row that is no longer active, and a local member of an ended team, are no role.
	if _, err := s.SetRemoteMemberState("mk-1", []string{remoteActive}, remoteEnded, 2000); err != nil {
		t.Fatal(err)
	}
	if ended, err := s.EndTeam("team-1", "lead-1", team.TeamEndLeadGone, 2000); err != nil || !ended {
		t.Fatalf("end: %v %v", ended, err)
	}
	for _, sid := range []string{"remote-m", "local-m", "lead-1"} {
		if got, err := s.SessionRole(sid); err != nil || got != sessionRoleNone {
			t.Fatalf("role(%s) = %q err=%v, want none", sid, got, err)
		}
	}
}

// §3.2: the rows whose lead host is no longer paired — the judgement only;
// the clean-up that acts on it is X2c's.
func TestLiveRemoteMembersOutsideHosts(t *testing.T) {
	s := openTestStore(t)
	for i, host := range []string{"host-A", "host-B", "host-C"} {
		r := newRemote("mk-"+host, "sid-"+host, host, int64(1000+i))
		if err := s.InsertRemoteMember(r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SetRemoteMemberState("mk-host-C", []string{remoteActive}, remoteEnded, 2000); err != nil {
		t.Fatal(err)
	}
	got, err := s.LiveRemoteMembersOutsideHosts([]string{"host-A"})
	if err != nil || len(got) != 1 || got[0].MK != "mk-host-B" {
		t.Fatalf("got %+v err=%v, want only host-B's live row (host-C's is already ended)", got, err)
	}
	if got, _ := s.LiveRemoteMembersOutsideHosts(nil); len(got) != 2 {
		t.Fatalf("no paired host: got %d rows, want 2", len(got))
	}
}

// §5.3 — every gate refuses a remote member. Mutation gate: isLiveMemberIn
// (or sessionRoleIn) not reading remote_members → each of these goes red.
func TestGates_RemoteMemberIsAMember(t *testing.T) {
	t.Run("lead create approve → member_cannot_lead", func(t *testing.T) {
		s := openTestStore(t)
		seedRemote(t, s, "mk-1", "sid-r", 1000)
		g := team.Grant{MaxMembers: 3, Roots: []string{"/w"}}
		if _, _, _, err := s.Create(openApproval("lead-req", "sid-r", 1100), "k"); err != nil {
			t.Fatal(err)
		}
		_, _, err := s.CloseLeadApproved("lead-req", approveClose(1200, g), leadTeam("lead-req", "sid-r", "_abc123", g, 1200))
		if !errors.Is(err, ErrMemberCannotLead) {
			t.Fatalf("err = %v, want ErrMemberCannotLead", err)
		}
		if _, ok, _ := s.LiveTeamByLead("sid-r"); ok {
			t.Fatal("a remote member now leads a team")
		}
	})

	t.Run("local adopt of it → adopt_already_member", func(t *testing.T) {
		s := adoptWorld(t)
		seedRemote(t, s, "mk-1", "sid-t", 1500)
		p := adoptPayload("team-1", "lead-1", "sid-t")
		openAdopt(t, s, "ad-1", p)
		_, won, refused, err := s.CloseAdoptApproved("ad-1", adoptClose(), p, chkOK(), adoptedRow("ad-1", p))
		if err != nil || !won || refused != team.ErrAdoptAlreadyMember {
			t.Fatalf("won=%v refused=%q err=%v", won, refused, err)
		}
		if ms, _ := s.MembersOf("team-1"); len(ms) != 0 {
			t.Fatalf("a local member row was stored: %+v", ms)
		}
	})

	t.Run("relay target of a cleared → target has role", func(t *testing.T) {
		s := openTestStore(t)
		seedTeam(t, s, "team-1", "L1", 1000)
		seedRemote(t, s, "mk-1", "sid-r", 1000)
		claimedOp(t, s, "op-l", "L1", "_abc123")
		_, _, err := s.ReportRelay("op-l", RelayReport{State: team.RelayCleared, NewSessionID: "sid-r", NewRef: "_rrr222", At: 5000})
		if !errors.Is(err, ErrClearedTargetHasRole) {
			t.Fatalf("err = %v, want ErrClearedTargetHasRole", err)
		}
		if tm, ok, _ := s.LiveTeamByLead("L1"); !ok || tm.ID != "team-1" {
			t.Fatal("the lead moved although the cleared was refused")
		}
	})

	// A relay claimed before the session was adopted remotely: its cleared would
	// leave remote_members bound to the old session, so it is refused whole.
	t.Run("cleared of a remote member's own session is refused", func(t *testing.T) {
		s := openTestStore(t)
		seedRemote(t, s, "mk-1", "sid-r", 1000)
		claimedOp(t, s, "op-r", "sid-r", "_abc123")
		_, _, err := s.ReportRelay("op-r", RelayReport{State: team.RelayCleared, NewSessionID: "sid-new", NewRef: "_nnn222", At: 5000})
		if !errors.Is(err, ErrClearedRemoteMember) {
			t.Fatalf("err = %v, want ErrClearedRemoteMember", err)
		}
		if op, ok, _ := s.GetRelayOp("op-r"); !ok || op.State == team.RelayCleared {
			t.Fatalf("op = %+v ok=%v, want it left as it was", op, ok)
		}
		if got, _, _ := s.RemoteMember("mk-1"); got.MemberSessionID != "sid-r" || got.State != remoteActive {
			t.Fatalf("remote row = %+v", got)
		}
	})

	t.Run("self-relay approve is cancelled", func(t *testing.T) {
		s := openTestStore(t)
		seedRemote(t, s, "mk-1", "sid-1", 600)
		op := selfOp("op-1", "sid-1", "_abc123", 1000)
		a := selfRelayRow(op)
		if _, _, err := s.CreateSelfRelayApproved(op, a, "h1", unattendedClose(2000, nil)); !errors.Is(err, ErrMemberRelayIsLeads) {
			t.Fatalf("err = %v, want ErrMemberRelayIsLeads", err)
		}
		if _, ok, _ := s.Get(a.ID); ok {
			t.Fatal("the approval row was kept")
		}
	})
}

func TestRelayRole_RemoteMemberIsMember(t *testing.T) {
	f := newFixture(t)
	seedRemote(t, f.m.store, "mk-1", "sid-remote", 1000)
	code, h, body := f.hello("sid-remote")
	if code != http.StatusOK || h.Role != "member" || h.SelfRelay != "off" {
		t.Fatalf("remote member hello: %d %s, want role member, self_relay off", code, body)
	}
}

func TestAdoptCreate_RemoteMemberTargetIsAlreadyMember(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	seedRemote(t, f.m.store, "mk-1", "sid-2", f.clock.Load())
	f.wantAdoptRefusal(uid(10), "_def456", http.StatusConflict, team.ErrAdoptAlreadyMember)
}

func TestCreate_RemoteMemberCannotLead(t *testing.T) {
	f := newFixture(t)
	seedRemote(t, f.m.store, "mk-1", "sid-1", 1000)
	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	if e := decodeErr(t, body); code != http.StatusConflict || e.Error != team.ErrMemberCannotLead {
		t.Fatalf("remote member asks for lead: %d %s, want 409 %s", code, body, team.ErrMemberCannotLead)
	}
	if _, ok, _ := f.m.store.Get(uid(1)); ok {
		t.Fatal("the request was stored")
	}
}
