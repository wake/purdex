package teammod

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Adopt's decide, its refusals through the route and its winner hook (adopt plan PL-1c).

func TestAdoptDecide_ApproveInsertsTheMemberAndRoleIsMember(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	a := f.adoptOK(uid(10), "_def456")
	if code, h, body := f.hello("sid-2"); code != http.StatusOK || h.Role != "none" {
		t.Fatalf("before: hello = %d %s", code, body)
	}
	f.events()
	code, body := f.decide(a.ID, "approve")
	if after := decodeApproval(t, body); code != http.StatusOK || after.State != team.StateApproved {
		t.Fatalf("decide = %d %s", code, body)
	}
	mem := memberBySpawn(t, f.m.store, a.ID)
	if mem.SessionID != "sid-2" || mem.TeamID != uid(1) || mem.Origin != team.MemberOriginAdopted || mem.Ref != "_def456" ||
		mem.NoticePending != team.NoticeAdopted || mem.PID != 20 || mem.Cwd != "/w2" {
		t.Fatalf("member = %+v", mem)
	}
	if code, h, body := f.hello("sid-2"); code != http.StatusOK || h.Role != "member" || h.SelfRelay != "off" {
		t.Fatalf("after: hello = %d %s, want role member, self_relay off", code, body)
	}
	if code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-2")); code != http.StatusConflict ||
		decodeErr(t, body).Error != team.ErrMemberRelayIsLeads {
		t.Fatalf("begin = %d %s, want 409 member_relay_is_leads", code, body)
	}
	if ops := f.opsOf(); len(ops) != 1 || ops[0] != "closed" {
		t.Errorf("events = %v, want [closed]", ops)
	}
}

func TestAdoptDecide_DenyChangesNothingForTheTarget(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	a := f.adoptOK(uid(10), "_def456")
	if code, body := f.decide(a.ID, "deny"); code != http.StatusOK {
		t.Fatalf("deny = %d %s", code, body)
	}
	if rows, _ := f.m.store.MembersOf(uid(1)); len(rows) != 0 {
		t.Fatalf("members after a deny = %+v", rows)
	}
	if _, h, _ := f.hello("sid-2"); h.Role != "none" {
		t.Fatalf("role = %s, want none", h.Role)
	}
}

// Each of the eight codes through the route: 409 with the code, the row cancelled with it as its close_reason,
// one closed event, no member. The store's table (PL-1b) proves the transaction; this proves the mapping.
func TestAdoptDecide_EachRefusalIs409WithItsCode(t *testing.T) {
	cases := map[string]func(f *fixture){
		team.ErrNotLead: func(f *fixture) {
			if _, err := f.m.store.db.Exec(`UPDATE teams SET ended_at = 5 WHERE id = ?`, uid(1)); err != nil {
				t.Fatal(err)
			}
		},
		team.ErrRemoteUnsupported: func(f *fixture) {
			if _, err := f.m.store.db.Exec(`UPDATE approval_requests SET host_id = 'elsewhere' WHERE id = ?`, uid(10)); err != nil {
				t.Fatal(err)
			}
		},
		team.ErrAdoptTargetNotFound: func(f *fixture) { f.origins.hide("sid-2") },
		team.ErrAdoptSelf: func(f *fixture) {
			if _, err := f.m.store.db.Exec(`UPDATE approval_requests SET payload_json = json_set(payload_json, '$.target_session_id', 'sid-1') WHERE id = ?`, uid(10)); err != nil {
				t.Fatal(err)
			}
		},
		team.ErrAdoptTargetIsLead:  func(f *fixture) { seedTeam(t, f.m.store, uid(9), "sid-2", f.clock.Load()) },
		team.ErrAdoptAlreadyMember: func(f *fixture) { f.makeMemberOfLead("sid-2") },
		team.ErrRequestOpen: func(f *fixture) {
			other := openApproval(uid(11), "sid-9", f.clock.Load())
			other.Kind, other.Payload = team.KindAdopt, mustAdoptPayload(t, f, uid(10))
			if _, _, _, err := f.m.store.Create(other, "other"); err != nil {
				t.Fatal(err)
			}
		},
		team.ErrTeamFull: func(f *fixture) {
			for _, sid := range []string{"sid-5", "sid-6", "sid-7"} {
				f.makeMemberOfLead(sid)
			}
		},
	}
	for code, arrange := range cases {
		t.Run(code, func(t *testing.T) {
			f := newFixture(t)
			f.approveLead(uid(1))
			f.adoptOK(uid(10), "_def456")
			arrange(f)
			f.events()
			got, body := f.decide(uid(10), "approve")
			if e := decodeErr(t, body); got != http.StatusConflict || e.Error != code {
				t.Fatalf("decide = %d %s, want 409 %s", got, body, code)
			}
			row, _, _ := f.m.store.Get(uid(10))
			if row.State != team.StateCancelled || row.CloseReason != code {
				t.Fatalf("row = %s / %q, want cancelled / %s", row.State, row.CloseReason, code)
			}
			if ops := f.opsOf(); len(ops) != 1 || ops[0] != "closed" {
				t.Errorf("events = %v, want [closed]", ops)
			}
			var n int
			if err := f.m.store.db.QueryRow(`SELECT COUNT(*) FROM team_members WHERE spawn_op = ?`, uid(10)).Scan(&n); err != nil || n != 0 {
				t.Errorf("member rows for the refused adoption = %d (%v)", n, err)
			}
		})
	}
}

func mustAdoptPayload(t *testing.T, f *fixture, id string) json.RawMessage {
	t.Helper()
	a, ok, err := f.m.store.Get(id)
	if err != nil || !ok {
		t.Fatalf("get %s: %v %v", id, ok, err)
	}
	return a.Payload
}

// The registry failing at the click is a 503 and writes nothing: the request stays open.
func TestAdoptDecide_RegistryErrorIs503AndTheRowStaysOpen(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	f.adoptOK(uid(10), "_def456")
	f.origins.setReadErr(true)
	if code, body := f.decide(uid(10), "approve"); code != http.StatusServiceUnavailable {
		t.Fatalf("decide = %d %s, want 503", code, body)
	}
	if row, _, _ := f.m.store.Get(uid(10)); row.State != team.StateOpen {
		t.Fatalf("row = %s, want open", row.State)
	}
}

// afterApproved is the one winner point: five approvals by five paths, one kick each; a refusal and a deny kick none.
// Mutation gate: kick from handleCreateAdopt instead → the click, sweep, tick and boot rows red.
func TestAfterApproved_RunsOnEveryApprovePath(t *testing.T) {
	paths := map[string]func(f *fixture){
		"click":  func(f *fixture) { f.adoptOK(uid(10), "_def456"); f.decide(uid(10), "approve") },
		"create": func(f *fixture) { f.unatt.set(true); f.adoptOK(uid(10), "_def456") },
		"sweep":  func(f *fixture) { f.adoptOK(uid(10), "_def456"); f.unatt.set(true); f.sweep() },
		"tick":   func(f *fixture) { f.adoptOK(uid(10), "_def456"); f.unatt.set(true); f.m.tick() },
		"boot": func(f *fixture) {
			f.adoptOK(uid(10), "_def456")
			f.unatt.set(true)
			if err := f.m.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
		},
		"deny": func(f *fixture) { f.adoptOK(uid(10), "_def456"); f.decide(uid(10), "deny") },
		"refused": func(f *fixture) {
			f.adoptOK(uid(10), "_def456")
			f.origins.hide("sid-2")
			f.decide(uid(10), "approve")
		},
	}
	want := map[string]int32{"click": 1, "create": 1, "sweep": 1, "tick": 1, "boot": 1, "deny": 0, "refused": 0}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.approveLead(uid(1))
			var kicks atomic.Int32
			f.m.noticeKick = func() { kicks.Add(1) }
			run(f)
			if got := kicks.Load(); got != want[name] {
				t.Fatalf("kicks = %d, want %d", got, want[name])
			}
		})
	}
}

// An open adopt request locks nobody (spec §6.6 names only the lead request and the relay).
func TestAdopt_NoHookLockWhileOpen(t *testing.T) {
	f := newFixture(t)
	f.approveLead(uid(1))
	f.adoptOK(uid(10), "_def456")
	for _, ev := range []string{"PreToolUse", "PermissionRequest"} {
		code, body := f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", ev, "sid-1"))
		if code != http.StatusOK || string(body) != "{}\n" {
			t.Fatalf("%s: %d %q, want 200 {}", ev, code, body)
		}
	}
}
