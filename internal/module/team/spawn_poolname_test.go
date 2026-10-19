package teammod

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The member name of a spawn (name pool v2 spec §5.2): reserved when the op is accepted, stored on it, on the answer, and
// released at every end of the op.

type reserveCall struct {
	lead, op, tmux string
	at             time.Time
}

// fakeNames is the peers name service as the team module uses it: the reservation calls, recorded.
type fakeNames struct {
	ipeers.NameService // the rest is never called
	mu                 sync.Mutex
	code               string // what ReserveMember gives
	err                error
	reserved           []reserveCall
	released           map[string]int
}

func (n *fakeNames) ReserveMember(_ context.Context, lead, op, tmux string, at time.Time) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.reserved = append(n.reserved, reserveCall{lead, op, tmux, at})
	return n.code, n.err
}

func (n *fakeNames) ReleaseReservation(op string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.released == nil {
		n.released = map[string]int{}
	}
	n.released[op]++
}

func (n *fakeNames) releasedOps() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for op := range n.released {
		out = append(out, op)
	}
	sort.Strings(out)
	return out
}

func (n *fakeNames) reserveCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.reserved)
}

func withNames(f *fixture, code string) *fakeNames {
	n := &fakeNames{code: code}
	f.m.names = n
	return n
}

func namedOp(o *spawnRow) { o.PoolName = "mia" }

func TestSpawnPoolName_ReservedAtAcceptStoredAndAnswered(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	n := withNames(f, "mia")
	f.holdRunners()
	code, op, e := f.spawn(1, root, nil)
	if code != 200 || op.PoolName != "mia" {
		t.Fatalf("spawn = %d %+v %+v, want the answer to carry pool_name mia", code, op, e)
	}
	if len(n.reserved) != 1 || n.reserved[0].lead != "sid-1" || n.reserved[0].op != spawnID(1) || n.reserved[0].tmux != "tm-0000000100" {
		t.Fatalf("reserve calls = %+v, want one for the lead sid-1, op 1, its tmux name", n.reserved)
	}
	if row, _, _ := f.m.store.GetSpawnOp(spawnID(1)); row.PoolName != "mia" {
		t.Fatalf("stored op = %+v, want pool_name mia", row)
	}
	if got := n.releasedOps(); len(got) != 0 {
		t.Fatalf("released %v while the op runs", got)
	}
	// the same request again joins the op: no second reservation
	if _, op2, _ := f.spawn(1, root, nil); op2.PoolName != "mia" || n.reserveCount() != 1 {
		t.Fatalf("a replay reserved again: %d calls, answer %+v", n.reserveCount(), op2)
	}
}

// A refused accept leaves nothing reserved. Mutation gate: do not release on an error → red.
func TestSpawnPoolName_ARefusedAcceptReleasesIt(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	n := withNames(f, "mia")
	f.acceptOp(9, root, nil) // the team is full
	code, _, e := f.spawn(1, root, nil)
	if code != 409 || e.Error != team.ErrTeamFull {
		t.Fatalf("spawn = %d %+v, want team_full", code, e)
	}
	if got := n.releasedOps(); len(got) != 1 || got[0] != spawnID(1) {
		t.Fatalf("released = %v, want the refused op's name", got)
	}
}

func TestSpawnPoolName_NoNameServiceOrNoNameFreeStillSpawns(t *testing.T) {
	t.Run("no name service", func(t *testing.T) {
		f, root := newSpawnFixture(t, 1)
		f.holdRunners()
		if code, op, e := f.spawn(1, root, nil); code != 200 || op.PoolName != "" {
			t.Fatalf("spawn = %d %+v %+v", code, op, e)
		}
	})
	t.Run("nothing free", func(t *testing.T) {
		f, root := newSpawnFixture(t, 1)
		withNames(f, "")
		f.holdRunners()
		if code, op, e := f.spawn(1, root, nil); code != 200 || op.PoolName != "" {
			t.Fatalf("spawn = %d %+v %+v", code, op, e)
		}
	})
	t.Run("the service fails", func(t *testing.T) {
		f, root := newSpawnFixture(t, 1)
		n := withNames(f, "mia")
		n.err = errors.New("pool busy")
		f.holdRunners()
		if code, op, e := f.spawn(1, root, nil); code != 200 || op.PoolName != "" {
			t.Fatalf("spawn = %d %+v %+v", code, op, e)
		}
	})
}

// Every end of a local op gives the name back. Each case is a mutation gate: leave that end without the release → red.
func TestSpawnPoolName_EveryEndReleasesIt(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, f *fixture, root string)
	}{
		{"registered", func(t *testing.T, f *fixture, root string) {
			f.register("%0", "sid-m1")
			if op := f.runOp(1, root, namedOp); op.State != team.SpawnDone {
				t.Fatalf("op = %+v, want done", op)
			}
		}},
		{"member_start_timeout", func(t *testing.T, f *fixture, root string) {
			if op := f.runOp(1, root, namedOp); op.Reason != team.SpawnReasonStartTimeout {
				t.Fatalf("op = %+v, want a start timeout", op)
			}
		}},
		{"failed", func(t *testing.T, f *fixture, root string) {
			id := f.acceptOp(1, root, namedOp)
			f.m.failSpawn(id, team.SpawnReasonCreateFailed)
		}},
		{"abandoned by the runner's abort", func(t *testing.T, f *fixture, root string) {
			id := f.acceptOp(1, root, namedOp)
			f.m.abortSpawn(id, "", "", errors.New("team.db refused"))
		}},
		{"the team ended", func(t *testing.T, f *fixture, root string) {
			f.acceptOp(1, root, namedOp)
			if ended, err := f.m.store.EndTeam(uid(1), "sid-1", team.TeamEndLeadGone, f.clock.Load()); err != nil || !ended {
				t.Fatalf("end team: %v %v", ended, err)
			}
		}},
		{"the team ended with commands", func(t *testing.T, f *fixture, root string) {
			f.acceptOp(1, root, namedOp)
			tm, ok, err := f.m.store.LiveTeamByLead("sid-1")
			if err != nil || !ok {
				t.Fatalf("team: %v %v", ok, err)
			}
			if ended, err := f.m.store.EndTeamWithCommands(tm, team.TeamEndLeadGone, f.clock.Load(), f.m.leadTuple(tm), f.m.newID); err != nil || !ended {
				t.Fatalf("end team: %v %v", ended, err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, root := newSpawnFixture(t, 1)
			n := withNames(f, "mia")
			c.run(t, f, root)
			if got := n.releasedOps(); len(got) != 1 || got[0] != spawnID(1) {
				t.Fatalf("released = %v, want op 1's name given back", got)
			}
		})
	}
}

// An op without a name is not the name service's business: nothing is released for it. (A release is idempotent, but a team end
// must not call the service for every op it ends.)
func TestSpawnPoolName_AnOpWithoutANameReleasesNothingAtATeamEnd(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	n := withNames(f, "mia")
	f.acceptOp(1, root, nil)
	if _, err := f.m.store.EndTeam(uid(1), "sid-1", team.TeamEndLeadGone, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	if got := n.releasedOps(); len(got) != 0 {
		t.Fatalf("released = %v", got)
	}
}

// An op that waits for its resource card holds its name; the card denied gives it back. Mutation gate: no release there → red.
func TestSpawnPoolName_AResourceCardDeniedReleasesIt(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	n := withNames(f, "mia")
	warnedSpawn(t, f, root)
	if got := n.releasedOps(); len(got) != 0 {
		t.Fatalf("released %v while the op waits for its card", got)
	}
	if _, won, err := f.m.store.CloseIfOpen(spawnID(1), Close{State: team.StateDenied, DecidedAt: f.clock.Load()}); err != nil || !won {
		t.Fatalf("close the card: won=%v err=%v", won, err)
	}
	f.m.resumeSpawns() // settles the waiting op from its closed card
	if got := n.releasedOps(); len(got) != 1 || got[0] != spawnID(1) {
		t.Fatalf("released = %v, want op 1 name given back", got)
	}
}

// An op failed by someone else (a stored row that fails its checks, an actor outside this module) is found failed when its runner stops:
// the runner gives the name back. Mutation gate: no release in reapFailedSpawn → red.
func TestSpawnPoolName_AnOpFailedByAnotherActorIsReleasedWhenItsRunnerStops(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	n := withNames(f, "mia")
	id := f.acceptOp(1, root, namedOp)
	if won, err := f.m.store.FailSpawnOp(id, team.SpawnReasonAbandoned, f.clock.Load()); err != nil || !won {
		t.Fatalf("fail the op: won=%v err=%v", won, err)
	}
	if got := n.releasedOps(); len(got) != 0 {
		t.Fatalf("released %v before the runner saw it", got)
	}
	f.m.reapFailedSpawn(id, nil)
	if got := n.releasedOps(); len(got) != 1 || got[0] != id {
		t.Fatalf("released = %v, want op 1 name given back", got)
	}
}
