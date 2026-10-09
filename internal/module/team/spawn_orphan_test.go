// internal/module/team/spawn_orphan_test.go
package teammod

import (
	"context"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// Boot sweep of spawn sessions nobody knows (#2341, from the #2337 attack review): a session that carries the spawn tag but
// whose op is gone or FAILED is the leftover of a crash between a lost record and the kill (or between the create and the
// record). A running op's session is the runner's; a DONE op's session is a live member's — both stay. A session without the
// tag is somebody else's and is never touched. Nothing here talks to a real tmux server: the fake executor is the whole world.

func (f *fixture) taggedSession(name, op string) {
	f.t.Helper()
	f.tmux.AddSession(name, "/w")
	if op != "" {
		f.tmux.SetSessionTag(name, spawnTagOption, op)
	}
}

func (f *fixture) hasSession(name string) bool {
	f.t.Helper()
	ss, err := f.tmux.ListSessions(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	for _, s := range ss {
		if s.Name == name {
			return true
		}
	}
	return false
}

func TestOrphanSweep_KillsATaggedSessionOfAFailedOrUnknownOp(t *testing.T) {
	f, root := newSpawnFixture(t, 4)
	failed := f.acceptOp(1, root, nil)
	if _, err := f.m.store.FailSpawnOp(failed, team.SpawnReasonAbandoned, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	f.taggedSession("orphan-failed", failed)
	f.taggedSession("orphan-unknown", spawnID(99)) // no such op
	f.m.reapOrphanSpawnSessions()
	for _, name := range []string{"orphan-failed", "orphan-unknown"} {
		if f.hasSession(name) {
			t.Errorf("%s: a tagged session of a failed or unknown op survived the sweep", name)
		}
	}
}

// Mutation gate: kill every tagged session whose op is not RUNNING (the issue's first wording) → the done case is red.
func TestOrphanSweep_LeavesWhatIsNotAnOrphan(t *testing.T) {
	f, root := newSpawnFixture(t, 4)
	running := f.acceptOp(1, root, nil)
	done := f.acceptOp(2, root, nil)
	if _, err := f.m.store.db.Exec(`UPDATE spawn_ops SET state = 'done', step = 'registered' WHERE id = ?`, done); err != nil {
		t.Fatal(err)
	}
	f.taggedSession("tagged-running", running)
	f.taggedSession("tagged-done", done) // a live member's session: its tag stays for good
	f.taggedSession("users-own", "")
	f.m.reapOrphanSpawnSessions()
	for _, name := range []string{"tagged-running", "tagged-done", "users-own"} {
		if !f.hasSession(name) {
			t.Errorf("%s was killed by the sweep", name)
		}
	}
}

// A user option of another name is not ours, whatever its value.
func TestOrphanSweep_IgnoresAnotherOption(t *testing.T) {
	f, _ := newSpawnFixture(t, 4)
	f.tmux.AddSession("other-tag", "/w")
	f.tmux.SetSessionTag("other-tag", "@something_else", spawnID(99))
	f.m.reapOrphanSpawnSessions()
	if !f.hasSession("other-tag") {
		t.Fatal("a session with another option was killed")
	}
}

// A tmux that cannot be read kills nothing (and the sweep does not panic); one session whose identity cannot be read does
// not stop the others.
func TestOrphanSweep_AnUnreadableTmuxKillsNothing(t *testing.T) {
	f, _ := newSpawnFixture(t, 4)
	f.taggedSession("orphan", spawnID(99))
	f.tmux.SetPaneIdentityErr(context.DeadlineExceeded)
	f.m.reapOrphanSpawnSessions()
	if !f.hasSession("orphan") {
		t.Fatal("a session whose identity could not be read was killed")
	}
	f.tmux.SetPaneIdentityErr(nil)
	f.m.reapOrphanSpawnSessions()
	if f.hasSession("orphan") {
		t.Fatal("the orphan survived once tmux could be read")
	}
}

// The sweep runs at boot, before any runner starts.
func TestOrphanSweep_RunsAtBoot(t *testing.T) {
	f, _ := newSpawnFixture(t, 4)
	f.taggedSession("orphan", spawnID(99))
	f.m.resumeSpawns()
	if f.hasSession("orphan") {
		t.Fatal("resumeSpawns did not sweep")
	}
}
