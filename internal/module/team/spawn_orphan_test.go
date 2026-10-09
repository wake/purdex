// internal/module/team/spawn_orphan_test.go
package teammod

import (
	"context"
	"testing"

	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
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

func TestOrphanSweep_KillsATaggedSessionOfAFailedOp(t *testing.T) {
	f, root := newSpawnFixture(t, 4)
	failed := f.acceptOp(1, root, nil)
	if _, err := f.m.store.FailSpawnOp(failed, team.SpawnReasonAbandoned, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	f.taggedSession("orphan-failed", failed)
	f.m.reapOrphanSpawnSessions()
	if f.hasSession("orphan-failed") {
		t.Error("a tagged session of a failed op survived the sweep")
	}
}

// codex R1 P1 / attacker high: the tag is an ordinary tmux user option — a user can set it to anything, a shared tmux server
// can hold another daemon's sessions. Only an op THIS daemon knows (and has failed) authorises a kill; a tag naming no op of
// ours is left alone (and logged). Mutation gate: kill when the op is not found → red.
func TestOrphanSweep_ATagNamingNoKnownOpIsLeftAlone(t *testing.T) {
	f, _ := newSpawnFixture(t, 4)
	f.taggedSession("forged", "whatever-the-user-typed")
	f.taggedSession("another-daemons", spawnID(99)) // a well-formed id this database has never seen
	f.m.reapOrphanSpawnSessions()
	for _, name := range []string{"forged", "another-daemons"} {
		if !f.hasSession(name) {
			t.Errorf("%s: a session whose tag names no op of this daemon was killed", name)
		}
	}
}

// codex attacker high 2: between the identity read that found the orphan and the kill the owner may have changed (the user
// cleared or rewrote the tag). The kill is re-checked against a second read taken right before it. Mutation gate: kill on the
// first read alone → red.
func TestOrphanSweep_ASessionWhoseTagChangedBeforeTheKillSurvives(t *testing.T) {
	f, root := newSpawnFixture(t, 4)
	failed := f.acceptOp(1, root, nil)
	if _, err := f.m.store.FailSpawnOp(failed, team.SpawnReasonAbandoned, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	f.taggedSession("reclaimed", failed)
	f.m.afterOrphanIdentity = func(name string) { f.tmux.SetSessionTag(name, spawnTagOption, "") } // the user takes it over
	f.m.reapOrphanSpawnSessions()
	if !f.hasSession("reclaimed") {
		t.Fatal("a session whose tag was cleared after the first read was killed")
	}
}

// codex R1 P2: a running row that is corrupt is failed by the listing at boot; the sweep runs after it, so its session goes
// in the same boot instead of surviving to the next one.
func TestOrphanSweep_ACorruptRunningOpIsSweptInTheSameBoot(t *testing.T) {
	f, root := newSpawnFixture(t, 4)
	bad := f.acceptOp(1, root, nil)
	if _, err := f.m.store.db.Exec(`UPDATE spawn_ops SET cwd = '' WHERE id = ?`, bad); err != nil { // checkRunning refuses it
		t.Fatal(err)
	}
	f.taggedSession("corrupt-op", bad)
	f.m.resumeSpawns()
	if f.hasSession("corrupt-op") {
		t.Fatal("the session of an op the boot just failed as corrupt survived")
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
	failed := f.acceptOp(1, "/w", nil)
	if _, err := f.m.store.FailSpawnOp(failed, team.SpawnReasonAbandoned, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	f.taggedSession("orphan", failed)
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
	f, root := newSpawnFixture(t, 4)
	failed := f.acceptOp(1, root, nil)
	if _, err := f.m.store.FailSpawnOp(failed, team.SpawnReasonAbandoned, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	f.taggedSession("orphan", failed)
	f.m.resumeSpawns()
	if f.hasSession("orphan") {
		t.Fatal("resumeSpawns did not sweep")
	}
}

// codex re-review P2: the second read compares who owns the session (generation, id, tag), not where its pane is — a pane
// that moved or a shell that changed directory between the two reads is ordinary activity. Mutation gate: compare the whole
// identity → red.
func TestOrphanSweep_APaneThatMovedBetweenTheReadsStillGetsReaped(t *testing.T) {
	f, root := newSpawnFixture(t, 4)
	failed := f.acceptOp(1, root, nil)
	if _, err := f.m.store.FailSpawnOp(failed, team.SpawnReasonAbandoned, f.clock.Load()); err != nil {
		t.Fatal(err)
	}
	f.taggedSession("moving", failed)
	ss, _ := f.tmux.ListSessions(context.Background())
	var sid string
	for _, x := range ss {
		if x.Name == "moving" {
			sid = x.ID
		}
	}
	f.m.afterOrphanIdentity = func(name string) { // another pane became the active one: same session, same generation, same tag
		f.tmux.SetActivePaneMetadata(name, tmux.TmuxPaneMetadata{SessionID: sid, PaneID: "%99"})
	}
	f.m.reapOrphanSpawnSessions()
	if f.hasSession("moving") {
		t.Fatal("an orphan whose pane moved between the reads survived")
	}
}
