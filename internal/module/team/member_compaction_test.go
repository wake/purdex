package teammod

import (
	"encoding/json"
	"net/http"
	"testing"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

func (f *fixture) memberCompaction(inbox, target string, set *team.CompactionOverride, reset bool) (int, team.MemberCompactionView, team.APIError) {
	f.t.Helper()
	code, raw := f.do(http.MethodPost, "/api/team/member-compaction",
		team.MemberCompactionRequest{OriginInbox: inbox, Target: target, Set: set, Reset: reset})
	var v team.MemberCompactionView
	if code != http.StatusOK {
		return code, v, decodeErr(f.t, raw)
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		f.t.Fatalf("decode %s: %v", raw, err)
	}
	return code, v, team.APIError{}
}

func TestMemberCompaction_Refusals(t *testing.T) {
	f, root := newTeamFixture(t, 2)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	if code, _, e := f.memberCompaction("/tmp/20.sock", m1.Ref, nil, false); code != 409 || e.Error != team.ErrNotLead {
		t.Fatalf("non-lead = %d %+v, want 409 not_lead", code, e)
	}
	seedTeam(t, f.m.store, uid(7), "sid-2", 1)
	other := newMember("op-9", uid(7), "sid-m9", ipeers.RefID("sid-m9"), 1)
	if err := f.m.store.InsertMember(other); err != nil {
		t.Fatal(err)
	}
	if code, _, e := f.memberCompaction("/tmp/10.sock", other.Ref, nil, false); code != 409 || e.Error != team.ErrNotYourMember {
		t.Fatalf("other team's member = %d %+v, want 409 not_your_member", code, e)
	}
	f.remoteRow("abc12", "other-host", "mk1", rowActive)
	f.m.cmdCaller = &fakeHostCaller{aliases: map[string]string{"remote-a": "other-host"}}
	if code, _, e := f.memberCompaction("/tmp/10.sock", "remote-a/_rabc12", &team.CompactionOverride{Auto: ptr(false)}, false); code != 409 || e.Error != team.ErrRemoteUnsupported {
		t.Fatalf("remote member = %d %+v, want 409 remote_unsupported", code, e)
	}
}

func TestMemberCompaction_ReadMergeReset(t *testing.T) {
	f, root := newTeamFixture(t, 2)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	f.memberAuto(false)
	code, v, e := f.memberCompaction("/tmp/10.sock", m1.Ref, nil, false)
	if code != 200 || v.Ref != m1.Ref || v.Address == "" || v.Role != "member" || v.Override != (team.CompactionOverride{}) {
		t.Fatalf("read = %d %+v %+v", code, v, e)
	}
	if code, _, e := f.memberCompaction("/tmp/10.sock", m1.Ref, &team.CompactionOverride{Idle: ptr(false)}, false); code != 200 {
		t.Fatalf("set idle = %d %+v", code, e)
	}
	_, v, _ = f.memberCompaction("/tmp/10.sock", m1.Ref, &team.CompactionOverride{Threshold: ptr(50)}, false)
	if v.Override.Idle == nil || *v.Override.Idle || v.Override.Threshold == nil || *v.Override.Threshold != 50 || v.Effective.Threshold != 50 {
		t.Fatalf("merge lost a field: %+v", v)
	}
	if code, _, e := f.memberCompaction("/tmp/10.sock", m1.Ref, &team.CompactionOverride{Threshold: ptr(0)}, false); code != 400 {
		t.Fatalf("threshold 0 = %d %+v, want 400", code, e)
	}
	if got, _ := f.m.store.CompactionOverride("sid-m1"); got.Threshold == nil || *got.Threshold != 50 {
		t.Fatalf("a refused set wrote: %+v", got)
	}
	if code, _, _ := f.memberCompaction("/tmp/10.sock", m1.Ref, &team.CompactionOverride{Auto: ptr(true)}, true); code != 400 {
		t.Fatalf("reset+set = %d, want 400", code)
	}
	_, v, _ = f.memberCompaction("/tmp/10.sock", m1.Ref, nil, true)
	if v.Override != (team.CompactionOverride{}) {
		t.Fatalf("reset left %+v", v.Override)
	}
}

func TestTeamList_CompactionOnlyForActiveLocalMembers(t *testing.T) {
	f, root := newTeamFixture(t, 3)
	f.member(1, root, "sid-m1", "w-one", nil)
	m2 := f.member(2, root, "sid-m2", "w-two", nil)
	m3 := f.member(3, root, "sid-m3", "w-three", nil)
	if err := f.m.store.SetMemberState(m2.SpawnOp, team.MemberKilled, 9); err != nil {
		t.Fatal(err)
	}
	if err := f.m.store.SetMemberState(m3.SpawnOp, team.MemberReleased, 9); err != nil {
		t.Fatal(err)
	}
	_, tv, _ := f.teamView("/tmp/10.sock")
	for _, mv := range tv.Members {
		if want := mv.SessionID == "sid-m1"; (mv.Compaction != nil) != want {
			t.Fatalf("%s (%s): compaction = %+v", mv.SessionID, mv.State, mv.Compaction)
		}
	}
}

func TestMemberCompaction_OnlyActiveMembers(t *testing.T) {
	for _, st := range []team.MemberState{team.MemberKilled, team.MemberReleased} {
		f, root := newTeamFixture(t, 1)
		m1 := f.member(1, root, "sid-m1", "w-one", nil)
		if err := f.m.store.SetMemberState(m1.SpawnOp, st, 9); err != nil {
			t.Fatal(err)
		}
		for name, set := range map[string]*team.CompactionOverride{"read": nil, "set": {Threshold: ptr(40)}} {
			if code, _, e := f.memberCompaction("/tmp/10.sock", m1.Ref, set, false); code != 409 || e.Error != team.ErrNotYourMember {
				t.Fatalf("%s %s = %d %+v, want 409 not_your_member", st, name, code, e)
			}
		}
		if got, _ := f.m.store.CompactionOverride("sid-m1"); got != (team.CompactionOverride{}) {
			t.Fatalf("%s: a refused set wrote %+v", st, got)
		}
	}
}

func TestMemberCompaction_RefusedDuringARelay(t *testing.T) {
	cases := map[string]string{
		"own session": `INSERT INTO relay_ops (id, kind, host_id, session_id, ref, state, handoff_path, created_at, updated_at)
			VALUES ('r1', 'member', 'h:1', 'sid-m1', '_r', 'writing', '/p', 1, 1)`,
		"cleared into it": `INSERT INTO relay_ops (id, kind, host_id, session_id, new_session_id, ref, state, handoff_path, created_at, updated_at)
			VALUES ('r1', 'member', 'h:1', 'sid-old', 'sid-m1', '_r', 'cleared', '/p', 1, 1)`,
	}
	for name, q := range cases {
		f, root := newTeamFixture(t, 1)
		m1 := f.member(1, root, "sid-m1", "w-one", nil)
		if _, err := f.m.store.db.Exec(q); err != nil {
			t.Fatal(err)
		}
		for what, set := range map[string]*team.CompactionOverride{"set": {Threshold: ptr(40)}, "reset": nil} {
			code, _, e := f.memberCompaction("/tmp/10.sock", m1.Ref, set, what == "reset")
			if code != 409 || e.Error != team.ErrRelayOpen || e.Op == nil {
				t.Fatalf("%s %s = %d %+v, want 409 relay_open with the op", name, what, code, e)
			}
		}
		if got, _ := f.m.store.CompactionOverride("sid-m1"); got != (team.CompactionOverride{}) {
			t.Fatalf("%s: wrote %+v", name, got)
		}
		if code, _, e := f.memberCompaction("/tmp/10.sock", m1.Ref, nil, false); code != 200 {
			t.Fatalf("%s: read = %d %+v, want 200", name, code, e)
		}
	}
}

func TestMemberCompaction_UnreadableViewWritesNothing(t *testing.T) {
	f, root := newTeamFixture(t, 1)
	m1 := f.member(1, root, "sid-m1", "w-one", nil)
	// auto holds text: reading the override (hence the view) fails.
	if _, err := f.m.store.db.Exec(`INSERT INTO session_compaction (session_id, auto, threshold, idle, updated_at) VALUES ('sid-m1', 'x', NULL, NULL, 1)`); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.memberCompaction("/tmp/10.sock", m1.Ref, &team.CompactionOverride{Threshold: ptr(40)}, false); code != 500 {
		t.Fatalf("set on an unreadable view = %d, want 500", code)
	}
	var th *int
	if err := f.m.store.db.QueryRow(`SELECT threshold FROM session_compaction WHERE session_id = 'sid-m1'`).Scan(&th); err != nil || th != nil {
		t.Fatalf("threshold = %v err=%v, want untouched NULL", th, err)
	}
}
