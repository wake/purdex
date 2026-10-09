package teammod

import "testing"

// seatWindow is the registration window of spawnFinish: the member row is inserted, the op is still running.
// One seat, held by one spawn, must count once. Max 3 (seedTeam).
func seatWindow(t *testing.T) (*Store, string) {
	t.Helper()
	s := openTestStore(t)
	seedTeam(t, s, uid(1), "sid-1", 1000)
	op := newSpawn(t, uid(1), 2000)
	mustCreateSpawn(t, s, op)
	seedMember(t, s, op.ID, uid(1), "sid-m1", 3000)
	return s, op.ID
}

// Mutation gate: a plain sum of running ops + active members -> in_use 2 -> red.
func TestSeats_SetMaxMembersToTheRealCountInTheRegistrationWindow(t *testing.T) {
	s, _ := seatWindow(t)
	r, err := s.SetMaxMembers(uid(1), 1)
	if err != nil || r.Outcome != MaxSet || r.InUse != 1 || maxOf(t, s, uid(1)) != 1 {
		t.Fatalf("set to the real count = %+v %v, want set with in_use 1", r, err)
	}
}

func TestSeats_SpawnCapAndAdoptSeatsCountOnceInTheRegistrationWindow(t *testing.T) {
	s, _ := seatWindow(t)
	if used, limit, err := s.SeatsUsed(uid(1)); err != nil || used != 1 || limit != 3 {
		t.Fatalf("adopt seats = %d/%d %v, want 1/3", used, limit, err)
	}
	if _, err := s.SetMaxMembers(uid(1), 2); err != nil {
		t.Fatal(err)
	}
	// 1 seat taken of 2: a second spawn fits.
	next := newSpawn(t, uid(1), 4000)
	next.OriginSessionID = "sid-1"
	if _, _, _, err := s.AcceptSpawnOp(next, "h"); err != nil {
		t.Fatalf("spawn refused in the window: %v", err)
	}
}

func TestSeats_RosterInUseCountsOnceInTheRegistrationWindow(t *testing.T) {
	s, opID := seatWindow(t)
	got, err := s.InUseOfTeams([]string{uid(1)})
	if err != nil || got[uid(1)] != 1 {
		t.Fatalf("in_use = %v %v, want 1", got, err)
	}
	// A running op with no member row still counts, and a gone member's op does not hold a second seat.
	mustCreateSpawn(t, s, newSpawn(t, uid(1), 5000))
	if got, _ := s.InUseOfTeams([]string{uid(1)}); got[uid(1)] != 2 {
		t.Fatalf("in_use with a starting spawn = %v, want 2", got)
	}
	_ = opID
}

// Mutation gate: the batch query as a plain sum (no NOT EXISTS) -> the window team counts 2 -> red.
func TestSeats_InUseOfTeamsEqualsThePerTeamCountForEveryTeam(t *testing.T) {
	s := openTestStore(t)
	// 1: member mid-registration (op running + its member row).
	seedTeam(t, s, uid(1), "sid-1", 1000)
	op := newSpawn(t, uid(1), 2000)
	mustCreateSpawn(t, s, op)
	seedMember(t, s, op.ID, uid(1), "sid-m1", 3000)
	// 2: a running spawn only.
	seedTeam(t, s, uid(2), "sid-2", 1000)
	mustCreateSpawn(t, s, newSpawn(t, uid(2), 2000))
	// 3: empty.
	seedTeam(t, s, uid(3), "sid-3", 1000)
	ids := []string{uid(1), uid(2), uid(3)}
	got, err := s.InUseOfTeams(ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		want, err := seatsTaken(s.db, id, "")
		if err != nil {
			t.Fatal(err)
		}
		if got[id] != want {
			t.Fatalf("team %s: InUseOfTeams %d, per-team %d (all %v)", id, got[id], want, got)
		}
	}
	if got[uid(1)] != 1 || got[uid(2)] != 1 || len(got) != 2 {
		t.Fatalf("got %v, want teams 1 and 2 at 1 seat, 3 absent", got)
	}
}
