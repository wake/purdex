package teammod

import (
	"context"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// lastInUse is the in_use of team uid(1) in the last event, failing when there is none.
func lastInUse(t *testing.T, evs []team.RosterEventValue, what string) int {
	t.Helper()
	if len(evs) == 0 {
		t.Fatalf("%s: no team.roster event", what)
	}
	for _, tr := range evs[len(evs)-1].Teams {
		if tr.ID == uid(1) {
			return tr.InUse
		}
	}
	t.Fatalf("%s: team missing from %+v", what, evs)
	return 0
}

// in_use changes when a spawn is accepted and when a running spawn ends failed. Mutation gate: drop the
// accept-time notify -> red.
func TestSpawnRoster_AcceptAndFailAreAnnounced(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.holdRunners()
	w := f.watchRoster()
	w.drain() // the false-green trap: signals of the fixture's setup
	if code, op, e := f.spawn(1, root, nil); code != 200 || op.State != team.SpawnRunning {
		t.Fatalf("spawn = %d %+v %+v", code, op, e)
	}
	if n := lastInUse(t, w.drain(), "accepted"); n != 1 {
		t.Fatalf("in_use after accept = %d, want 1", n)
	}
	f.m.failSpawn(spawnID(1), team.SpawnReasonAbandoned)
	if n := lastInUse(t, w.drain(), "failed"); n != 0 {
		t.Fatalf("in_use after fail = %d, want 0", n)
	}
}

// The start timeout ends the op failed through its own path (timeOutSpawn).
func TestSpawnRoster_TimeoutIsAnnounced(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.m.spawnSleep = func(_ context.Context, _ time.Duration) { f.clock.Add(21_000) }
	id := f.acceptOp(1, root, nil)
	w := f.watchRoster()
	w.drain()
	f.m.startSpawn(id)
	f.m.spawnWG.Wait()
	if op, _, _ := f.m.store.GetSpawnOp(id); op.State != team.SpawnFailed || op.Reason != team.SpawnReasonStartTimeout {
		t.Fatalf("op = %+v, want failed start timeout", op)
	}
	if n := lastInUse(t, w.drain(), "timed out"); n != 0 {
		t.Fatalf("in_use after timeout = %d, want 0", n)
	}
}

// GET /api/team carries the same in_use: a held starting spawn is a place taken.
func TestSpawnRoster_TeamViewCarriesInUse(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	f.holdRunners()
	if code, _, e := f.spawn(1, root, nil); code != 200 {
		t.Fatalf("spawn = %d %+v", code, e)
	}
	code, v, e := f.teamView("/tmp/10.sock")
	if code != 200 || v.InUse == nil || *v.InUse != 1 {
		t.Fatalf("team view = %d in_use %v %+v, want 1", code, v.InUse, e)
	}
}
