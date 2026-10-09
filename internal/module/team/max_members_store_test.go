package teammod

import (
	"context"
	"testing"
	"time"
)

func maxOf(t *testing.T, s *Store, id string) int {
	t.Helper()
	tm, ok := getTeam(t, s, id)
	if !ok {
		t.Fatalf("no team %s", id)
	}
	return tm.Grant.MaxMembers
}

func TestSetMaxMembers_CountsLikeTheSpawnCapAndWrites(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, uid(1), "sid-1", 1000) // max 3
	seedMember(t, s, "op-1", uid(1), "sid-m1", 2000)
	seedMember(t, s, "op-2", uid(1), "sid-m2", 3000)
	if r, err := s.SetMaxMembers(uid(1), 1); err != nil || r.Outcome != MaxBelowInUse || r.InUse != 2 || maxOf(t, s, uid(1)) != 3 {
		t.Fatalf("below in_use = %+v %v, want refusal with in_use 2 and nothing written", r, err)
	}
	if r, err := s.SetMaxMembers(uid(1), 2); err != nil || r.Outcome != MaxSet || r.InUse != 2 || maxOf(t, s, uid(1)) != 2 {
		t.Fatalf("to exactly in_use = %+v %v, want set", r, err)
	}
	if r, err := s.SetMaxMembers(uid(1), 8); err != nil || r.Outcome != MaxSet || maxOf(t, s, uid(1)) != 8 {
		t.Fatalf("raise = %+v %v", r, err)
	}
	if r, err := s.SetMaxMembers("no-such", 4); err != nil || r.Outcome != MaxNoTeam {
		t.Fatalf("unknown = %+v %v", r, err)
	}
}

func TestSetMaxMembers_EndedTeamIsNotFound(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, uid(1), "sid-1", 1000)
	if ok, err := s.EndTeam(uid(1), "sid-1", "lead_gone", 2000); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if r, err := s.SetMaxMembers(uid(1), 4); err != nil || r.Outcome != MaxNoTeam {
		t.Fatalf("ended = %+v %v, want not found", r, err)
	}
}

// The count and the write share one write lock: a member insert tried between them must be kept out until the commit.
// Mutation gate: count outside the transaction / without the lock -> the insert gets in -> red.
func TestSetMaxMembers_NothingGetsInBetweenTheCountAndTheWrite(t *testing.T) {
	s := openTestStore(t)
	seedTeam(t, s, uid(1), "sid-1", 1000)
	seedMember(t, s, "op-1", uid(1), "sid-m1", 2000)
	inserted := false
	s.afterMaxMembersCount = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		_, err := s.db.ExecContext(ctx, `INSERT INTO team_members (spawn_op, team_id, host_id, session_id, ref, cwd, tmux_session, state, created_at, updated_at)
			VALUES ('op-2', ?, 'h:1', 'sid-m2', '_mem002', '/w', 'tm', 'active', 3000, 3000)`, uid(1))
		inserted = err == nil
	}
	if r, err := s.SetMaxMembers(uid(1), 1); err != nil || r.Outcome != MaxSet {
		t.Fatalf("set = %+v %v", r, err)
	}
	if inserted {
		t.Fatal("a member insert got in between the in_use count and the write")
	}
}
