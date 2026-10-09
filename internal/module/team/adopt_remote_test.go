// internal/module/team/adopt_remote_test.go
package teammod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The remote adopt on L (cross-host team spec §4.3, plan X3c). remoteFixture: air26 = hostM, announcing every kind and
// allowing us; sid-1 leads team uid(1). The target is sid-rt on air26, ref _rt1234.

const (
	remoteTarget = "air26/_rt1234"
	rtSession    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

func remoteSession(sid, ref string) ipeers.PeerRecord {
	return ipeers.PeerRecord{RowKind: "session", Ref: ref, Name: "ios", Title: "iOS", Cwd: "/w/ios",
		Agent: &ipeers.AgentInfo{Type: "cc", SessionID: sid, PID: 77, ProcStart: "ps9"}}
}

func adoptFixture(t *testing.T) (*fixture, *fakeHostCaller) {
	t.Helper()
	f, fc := remoteFixture(t)
	fc.paired = map[string]bool{"hostM": true, "hostN": true}
	f.m.peerRecords = func(_ context.Context, host string) ([]ipeers.PeerRecord, error) {
		if host != "hostM" {
			return nil, errors.New("unexpected host " + host)
		}
		return []ipeers.PeerRecord{remoteSession(rtSession, "_rt1234"), remoteSession("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "_ot5678")}, nil
	}
	return f, fc
}

func (f *fixture) adoptionOf(id string) (int, team.Adoption) {
	f.t.Helper()
	code, body := f.do(http.MethodGet, team.AdoptionsRoute+id, nil)
	var ad team.Adoption
	_ = json.Unmarshal(body, &ad)
	return code, ad
}

// Refusals come before any approval is opened: capability, host, target.
func TestRemoteAdoptCreate_RefusalsBeforeAnyApproval(t *testing.T) {
	f, fc := adoptFixture(t)
	f.wantAdoptRefusal(uid(20), "ghost/_rt1234", http.StatusConflict, team.ErrAdoptTargetNotFound)
	f.wantAdoptRefusal(uid(21), "air26/_nope99", http.StatusConflict, team.ErrAdoptTargetNotFound)

	fc.caps["hostM"] = ipeers.TeamCaps{Kinds: []string{CmdRelease}, AllowTeam: true} // no adopt in team.kinds
	f.wantAdoptRefusal(uid(22), remoteTarget, http.StatusConflict, team.ErrRemoteUnsupported)
	fc.caps["hostM"] = ipeers.TeamCaps{Kinds: allKinds, AllowTeam: false}
	f.wantAdoptRefusal(uid(23), remoteTarget, http.StatusConflict, "host_not_allowed")
	fc.caps["hostM"] = ipeers.TeamCaps{Kinds: allKinds, AllowTeam: true}

	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) { return nil, errors.New("timeout") }
	f.wantAdoptRefusal(uid(24), remoteTarget, http.StatusServiceUnavailable, "remote_unreachable")
	if cmds := f.commandsOf(CmdAdopt); len(cmds) != 0 {
		t.Fatalf("a refused create queued %+v", cmds)
	}
}

// The approval is the user's consent: a click writes the joining row AND the adopt command in one transaction, owes no
// local notice, and the membership is what the adoptions route reports.
func TestRemoteAdopt_ClickWritesJoiningRowAndCommandTogether(t *testing.T) {
	f, _ := adoptFixture(t)
	ap := f.adoptOK(uid(30), remoteTarget)
	p, err := team.AdoptPayloadOf(ap)
	if err != nil || p.TargetHostID != "hostM" || p.TargetHostAlias != "air26" || p.TargetSessionID != rtSession || p.TargetRef != "_rt1234" {
		t.Fatalf("payload = %+v err=%v", p, err)
	}
	if code, _ := f.adoptionOf(uid(30)); code != http.StatusConflict {
		t.Fatalf("adoptions before the click = %d, want 409 not_approved", code)
	}
	if cmds := f.commandsOf(CmdAdopt); len(cmds) != 0 {
		t.Fatalf("a command exists before the approval: %+v", cmds)
	}
	f.approveClick(uid(30))

	state, _ := f.memberRowState(uid(30))
	if state != team.AdoptionJoining {
		t.Fatalf("row = %s, want joining", state)
	}
	var host, sid, notice string
	if err := f.m.store.db.QueryRow(`SELECT host_id, session_id, notice_pending FROM team_members WHERE spawn_op = ?`, uid(30)).Scan(&host, &sid, &notice); err != nil {
		t.Fatal(err)
	}
	if host != "hostM" || sid != rtSession || notice != "" {
		t.Fatalf("row host=%q session=%q notice=%q", host, sid, notice)
	}
	cmds := f.commandsOf(CmdAdopt)
	if len(cmds) != 1 || cmds[0].ID != uid(30) || cmds[0].MK != uid(30) || cmds[0].HostID != "hostM" || cmds[0].State != cmdPending {
		t.Fatalf("commands = %+v", cmds)
	}
	var tc team.TeamCommand
	_ = json.Unmarshal(cmds[0].Body, &tc)
	if tc.TargetSessionID != rtSession || tc.TargetRef != "_rt1234" || tc.MK != uid(30) || tc.ToHostID != "hostM" || tc.Lead.SessionID != "sid-1" {
		t.Fatalf("command body = %+v", tc)
	}
	if code, ad := f.adoptionOf(uid(30)); code != http.StatusOK || ad.State != team.AdoptionJoining {
		t.Fatalf("adoptions = %d %+v", code, ad)
	}
	// a seat is held by the joining row
	if used, _ := seatsTaken(f.m.store.db, uid(1), ""); used != 1 {
		t.Fatalf("seats = %d, want the joining row to hold one", used)
	}
}

// Crash cut (spec §11): the row does not exist without its command. Mutation: enqueue after the commit → no rollback.
func TestRemoteAdopt_NoRowWithoutItsCommand(t *testing.T) {
	f, _ := adoptFixture(t)
	f.adoptOK(uid(31), remoteTarget)
	// A command id already taken by another command makes the enqueue fail: the approve must leave nothing.
	f.enqueue(f.cmd(uid(31), CmdRelease, "hostM", "mkx"))
	code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(31)+"/decide", appApprove(nil))
	if code == http.StatusOK {
		t.Fatalf("approve succeeded though its command could not be queued: %s", body)
	}
	var n int
	_ = f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_members WHERE spawn_op = ?`, uid(31)).Scan(&n)
	if n != 0 {
		t.Fatalf("a joining row was left without its command")
	}
	if a, _, _ := f.m.store.Get(uid(31)); a.State != team.StateOpen {
		t.Fatalf("the request is %s, want still open", a.State)
	}
}

// Unattended mode: the create itself approves, in the same way.
func TestRemoteAdopt_UnattendedWritesJoiningRowAndCommandAtCreate(t *testing.T) {
	f, _ := adoptFixture(t)
	f.unatt.set(true)
	code, body := f.adopt(uid(32), remoteTarget)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, body)
	}
	if ap := decodeApproval(t, body); ap.State != team.StateApproved {
		t.Fatalf("state = %s", ap.State)
	}
	if state, _ := f.memberRowState(uid(32)); state != team.AdoptionJoining {
		t.Fatalf("row = %s", state)
	}
	if cmds := f.commandsOf(CmdAdopt); len(cmds) != 1 {
		t.Fatalf("commands = %+v", cmds)
	}
}

// A replay of the same request answers the stored approval without asking the host again.
func TestRemoteAdopt_ReplayDoesNotAskTheHostAgain(t *testing.T) {
	f, _ := adoptFixture(t)
	first := f.adoptOK(uid(33), remoteTarget)
	f.m.peerRecords = func(context.Context, string) ([]ipeers.PeerRecord, error) {
		t.Error("the host was asked for a replay")
		return nil, errors.New("down")
	}
	code, body := f.adopt(uid(33), remoteTarget)
	if code != http.StatusOK || decodeApproval(t, body).ID != first.ID {
		t.Fatalf("replay = %d %s", code, body)
	}
}

// The adoptions route reports the membership as the lead host holds it, and the 10 minute void as void.
func TestRemoteAdopt_AdoptionsRouteStates(t *testing.T) {
	f, _ := adoptFixture(t)
	f.adoptOK(uid(34), remoteTarget)
	f.approveClick(uid(34))
	set := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.m.store.db.Exec(sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, state, reason string
		want                team.Adoption
	}{
		{"active", "active", "", team.Adoption{State: team.AdoptionActive}},
		{"failed", "failed", "target_gone", team.Adoption{State: team.AdoptionFailed, Code: "target_gone"}},
		{"void", "failed", "remote_unreachable", team.Adoption{State: team.AdoptionVoid, Code: "remote_unreachable"}},
	} {
		set(`UPDATE team_members SET state = ?, end_reason = ? WHERE spawn_op = ?`, c.state, c.reason, uid(34))
		code, ad := f.adoptionOf(uid(34))
		if code != http.StatusOK || ad.State != c.want.State || ad.Code != c.want.Code || ad.ApprovalID != uid(34) {
			t.Fatalf("%s: %d %+v, want %+v", c.name, code, ad, c.want)
		}
	}
	if code, _ := f.adoptionOf(uid(99)); code != http.StatusNotFound {
		t.Fatalf("unknown id = %d", code)
	}
}

// The long-poll returns as soon as the membership leaves joining.
func TestRemoteAdopt_AdoptionsWaitReturnsWhenItLeavesJoining(t *testing.T) {
	f, _ := adoptFixture(t)
	f.adoptOK(uid(35), remoteTarget)
	f.approveClick(uid(35))
	done := make(chan team.Adoption, 1)
	go func() {
		_, ad := f.adoptionOf(uid(35) + "?wait=10")
		done <- ad
	}()
	f.clock.Add(1)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'active' WHERE spawn_op = ?`, uid(35)); err != nil {
		t.Fatal(err)
	}
	if ad := <-done; ad.State != team.AdoptionActive {
		t.Fatalf("waited result = %+v", ad)
	}
}

// A second remote adopt of a session that is already a member in play is refused.
func TestRemoteAdopt_AlreadyMemberOnThatHost(t *testing.T) {
	f, _ := adoptFixture(t)
	f.adoptOK(uid(36), remoteTarget)
	f.approveClick(uid(36))
	f.wantAdoptRefusal(uid(37), remoteTarget, http.StatusConflict, team.ErrAdoptAlreadyMember)
}
