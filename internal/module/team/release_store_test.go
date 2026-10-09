package teammod

import (
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Release and the notice outbox in the store (plan PL-1b2).

func releaseWorld(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	seedTeam(t, s, "team-1", "lead-1", 1000)
	seedMember(t, s, "op-1", "team-1", "sid-m1", 2000)
	return s
}

func relayOp(t *testing.T, s *Store, id, sid string, state team.RelayState) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO relay_ops (id, kind, host_id, session_id, ref, state, handoff_path, created_at, updated_at)
		VALUES (?, 'self', 'h:1', ?, '_r', ?, '/p', 1, 1)`, id, sid, string(state)); err != nil {
		t.Fatal(err)
	}
}

func rowOf(t *testing.T, s *Store, key string) memberRow {
	t.Helper()
	m, ok := memberByKey(t, s, "team-1", key)
	if !ok {
		t.Fatalf("no row %s", key)
	}
	return m
}

func TestReleaseMember_SetsReleasedEndedAtAndTheNotice(t *testing.T) {
	s := releaseWorld(t)
	released, err := s.ReleaseMember("op-1", "sid-m1", 7000)
	if err != nil || !released {
		t.Fatalf("released=%v err=%v", released, err)
	}
	m := rowOf(t, s, "op-1")
	if m.State != team.MemberReleased || m.EndedAt != 7000 || m.UpdatedAt != 7000 || m.NoticePending != team.NoticeReleased || m.NoticeSince != 7000 {
		t.Fatalf("row = %+v", m)
	}
	// a released member is no member: relayRole answers none, and nothing treats it as active
	if _, _, found, err := s.ActiveMemberInLiveTeam("sid-m1"); err != nil || found {
		t.Fatalf("active member found=%v err=%v", found, err)
	}
	// a second release is a no-op
	if again, err := s.ReleaseMember("op-1", "sid-m1", 8000); err != nil || again {
		t.Fatalf("second release = %v %v", again, err)
	}
	if m := rowOf(t, s, "op-1"); m.EndedAt != 7000 {
		t.Fatalf("ended_at moved: %d", m.EndedAt)
	}
}

// A member is not released in the middle of its own relay: any non-terminal op blocks it, a finished one does not.
// Mutation gate: guard on claimed|writing|written only → the awaiting_approval / requested cases red.
func TestReleaseMember_RefusedWhileARelayOpIsOpen(t *testing.T) {
	for _, st := range []team.RelayState{team.RelayAwaitingApproval, team.RelayRequested, team.RelayClaimed, team.RelayWriting, team.RelayWritten} {
		t.Run(string(st), func(t *testing.T) {
			s := releaseWorld(t)
			relayOp(t, s, "r1", "sid-m1", st)
			if released, err := s.ReleaseMember("op-1", "sid-m1", 7000); err != nil || released {
				t.Fatalf("released=%v err=%v", released, err)
			}
			if m := rowOf(t, s, "op-1"); m.State != team.MemberActive || m.NoticePending != "" || m.EndedAt != 0 {
				t.Fatalf("row = %+v", m)
			}
		})
	}
	for _, st := range []team.RelayState{team.RelayDone, team.RelayFailed, team.RelayCancelled} {
		s := releaseWorld(t)
		relayOp(t, s, "r1", "sid-m1", st)
		if released, err := s.ReleaseMember("op-1", "sid-m1", 7000); err != nil || !released {
			t.Fatalf("%s: released=%v err=%v", st, released, err)
		}
	}
}

func TestReleaseMember_OnlyTheRowThatHoldsTheSessionAndIsActive(t *testing.T) {
	s := releaseWorld(t)
	for name, c := range map[string][2]string{"another key": {"op-x", "sid-m1"}, "another session": {"op-1", "sid-other"}} {
		if released, err := s.ReleaseMember(c[0], c[1], 7000); err != nil || released {
			t.Errorf("%s: released=%v err=%v", name, released, err)
		}
	}
	if _, err := s.db.Exec(`UPDATE team_members SET state = 'gone' WHERE spawn_op = 'op-1'`); err != nil {
		t.Fatal(err)
	}
	if released, err := s.ReleaseMember("op-1", "sid-m1", 7000); err != nil || released {
		t.Errorf("a gone row was released: %v %v", released, err)
	}
	if m := rowOf(t, s, "op-1"); m.State != team.MemberGone || m.NoticePending != "" {
		t.Fatalf("row = %+v", m)
	}
}

// A released row may be followed by a new adoption of the same session: a new row, the old stays released.
func TestReleaseMember_ASessionCanBeAdoptedAgain(t *testing.T) {
	s := releaseWorld(t)
	if released, err := s.ReleaseMember("op-1", "sid-m1", 7000); err != nil || !released {
		t.Fatal(released, err)
	}
	p := adoptPayload("team-1", "lead-1", "sid-m1")
	openAdopt(t, s, "ad-2", p)
	if _, won, refused, err := s.CloseAdoptApproved("ad-2", adoptClose(), p, chkOK(), adoptedRow("ad-2", p)); err != nil || !won || refused != "" {
		t.Fatalf("won=%v refused=%q err=%v", won, refused, err)
	}
	if rowOf(t, s, "op-1").State != team.MemberReleased || rowOf(t, s, "ad-2").State != team.MemberActive {
		t.Fatal("old and new rows")
	}
}

func TestMarkMemberKilledAndGone_SetEndedAt(t *testing.T) {
	s := releaseWorld(t)
	seedMember(t, s, "op-2", "team-1", "sid-m2", 2100)
	if gone, err := s.MarkMemberGone("op-1", "sid-m1", 6000); err != nil || !gone {
		t.Fatal(gone, err)
	}
	if m := rowOf(t, s, "op-1"); m.State != team.MemberGone || m.EndedAt != 6000 {
		t.Fatalf("gone row = %+v", m)
	}
	// killed after gone keeps the time the row first left active
	if killed, err := s.MarkMemberKilled("op-1", "sid-m1", 9000); err != nil || !killed {
		t.Fatal(killed, err)
	}
	if m := rowOf(t, s, "op-1"); m.State != team.MemberKilled || m.EndedAt != 6000 {
		t.Fatalf("killed-after-gone row = %+v", m)
	}
	if killed, err := s.MarkMemberKilled("op-2", "sid-m2", 9500); err != nil || !killed {
		t.Fatal(killed, err)
	}
	if m := rowOf(t, s, "op-2"); m.State != team.MemberKilled || m.EndedAt != 9500 {
		t.Fatalf("killed row = %+v", m)
	}
}

// The outbox: pending notices oldest first; an adopted notice only while the row is active; a released
// one whatever became of the row; ClearNotice is conditional on kind AND since, so an older send never
// clears a newer notice. Mutation gate: clear on the row key alone → red.
func TestPendingNoticesAndClearNotice(t *testing.T) {
	s := releaseWorld(t)
	p := adoptPayload("team-1", "lead-1", "sid-t")
	openAdopt(t, s, "ad-1", p)
	row := adoptedRow("ad-1", p)
	row.NoticeSince = 3000
	if _, won, _, err := s.CloseAdoptApproved("ad-1", adoptClose(), p, chkOK(), row); err != nil || !won {
		t.Fatal(won, err)
	}
	if released, err := s.ReleaseMember("op-1", "sid-m1", 4000); err != nil || !released {
		t.Fatal(released, err)
	}
	got, err := s.PendingNotices()
	if err != nil || len(got) != 2 || got[0].SpawnOp != "ad-1" || got[0].NoticePending != team.NoticeAdopted || got[1].SpawnOp != "op-1" || got[1].NoticePending != team.NoticeReleased {
		t.Fatalf("pending = %+v err=%v", got, err)
	}
	// the adopted member was marked gone: its adopted notice is no longer owed, a released one still is
	if _, err := s.db.Exec(`UPDATE team_members SET state = 'gone' WHERE spawn_op IN ('ad-1', 'op-1')`); err != nil {
		t.Fatal(err)
	}
	got, _ = s.PendingNotices()
	if len(got) != 1 || got[0].SpawnOp != "op-1" {
		t.Fatalf("after gone: %+v", got)
	}
	// a stale clear (older since, or the other kind) changes nothing; the exact one clears
	for name, c := range map[string][3]any{"older since": {"op-1", team.NoticeReleased, int64(3999)}, "other kind": {"op-1", team.NoticeAdopted, int64(4000)}} {
		if cleared, err := s.ClearNotice(c[0].(string), c[1].(string), c[2].(int64)); err != nil || cleared {
			t.Errorf("%s: cleared=%v err=%v", name, cleared, err)
		}
	}
	if cleared, err := s.ClearNotice("op-1", team.NoticeReleased, 4000); err != nil || !cleared {
		t.Fatalf("exact clear: %v %v", cleared, err)
	}
	if m := rowOf(t, s, "op-1"); m.NoticePending != "" || m.NoticeSince != 0 {
		t.Fatalf("row = %+v", m)
	}
	if got, _ := s.PendingNotices(); len(got) != 0 {
		t.Fatalf("still pending: %+v", got)
	}
}
