package teammod

import (
	"context"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// The reader the peers name service rebuilds its reservations from after a boot (name pool v2 spec §5.2): the open spawn ops that
// carry a name. It reads team.db only.

func TestSpawnReservationReader_ListsTheOpenOpsThatCarryAName(t *testing.T) {
	s := openTestStore(t)
	mk := func(n int, state team.SpawnState, step, pool string) {
		op := poolNameOp(t, n, pool)
		op.State, op.Step = state, step
		if _, _, _, err := insertSpawnOp(s.db, op, "hash"); err != nil {
			t.Fatal(err)
		}
	}
	mk(1, team.SpawnRunning, team.StepAccepted, "mia")
	mk(2, team.SpawnAwaitingResources, team.StepAccepted, "anna")
	mk(3, team.SpawnRunning, team.StepAccepted, "") // no name
	mk(4, team.SpawnRunning, team.StepAccepted, "hiro")
	if _, err := s.db.Exec(`UPDATE spawn_ops SET state = 'failed', reason = 'create_failed' WHERE id = ?`, spawnID(4)); err != nil {
		t.Fatal(err)
	}
	mk(5, team.SpawnRunning, team.StepAccepted, "zed")
	if _, err := s.db.Exec(`UPDATE spawn_ops SET state = 'done', step = 'registered' WHERE id = ?`, spawnID(5)); err != nil {
		t.Fatal(err)
	}

	got, err := spawnReservationReader{s}.OpenReservations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byOp := map[string]string{}
	for _, r := range got {
		byOp[r.OpID] = r.Code
		if r.TmuxName == "" || !r.CreatedAt.Equal(time.UnixMilli(1_000_000)) {
			t.Fatalf("reservation %+v: want the op's tmux name and created_at", r)
		}
	}
	if len(byOp) != 2 || byOp[spawnID(1)] != "mia" || byOp[spawnID(2)] != "anna" {
		t.Fatalf("reservations = %v, want only the running mia and the awaiting anna", byOp)
	}
}

// The reader honours its context: a cancelled one reads nothing.
func TestSpawnReservationReader_StopsWithItsContext(t *testing.T) {
	s := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (spawnReservationReader{s}).OpenReservations(ctx); err == nil {
		t.Fatal("a cancelled read succeeded")
	}
}
