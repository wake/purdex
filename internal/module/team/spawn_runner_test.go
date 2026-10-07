package teammod

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/team"
)

// Spec §7.2 steps 3–4, U20 (a): the op's tmux session is created tagged with
// the op id (its ownership token, review H3), and the launch line,
// team.member_command (re-quoted word by word, P4-4) with --plugin-dir and
// the asked model and effort, is typed once into its window 0.
func TestSpawn_LaunchLineHasPluginDirModelAndEffort(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	op := f.runOp(1, root, func(r *spawnRow) { r.Model, r.Effort, r.Title = "opus[1m]", "high", "worker" })
	if op.Step != team.StepLaunched || op.PaneID != "%0" || op.TmuxID != "$0" || op.TmuxInstance != "4242:1700000000" {
		t.Fatalf("op = %+v", op)
	}
	keys := "'claude' '--dangerously-skip-permissions' --plugin-dir '" + agentcc.PluginRoot(f.core.Cfg.DataDir) + "' --model 'opus[1m]' --effort high\n"
	if got := f.tmux.RawKeysSent(); len(got) != 1 || got[0].Target != "$0:0" || !reflect.DeepEqual(got[0].Keys, []string{keys}) {
		t.Fatalf("keys = %+v, want one send of %q to $0:0", got, keys)
	}
	if id, _ := f.tmux.PaneIdentity(t.Context(), "%0", spawnTagOption); id.Tag != spawnID(1) {
		t.Fatalf("the session's ownership tag = %q, want the op id", id.Tag)
	}
}

// U20 (c): without a model and an effort the line carries neither.
func TestSpawn_WithoutModelSendsNoModelFlag(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.teamCfg.s.MemberCommand = "X=1 claude"
	f.runOp(1, root, nil)
	keys := "X='1' 'claude' --plugin-dir '" + agentcc.PluginRoot(f.core.Cfg.DataDir) + "'\n"
	if got := f.tmux.RawKeysSent(); len(got) != 1 || got[0].Keys[0] != keys {
		t.Fatalf("keys = %+v, want %q", got, keys)
	}
}

// Review H2: the cwd the POST checked is resolved and checked again before
// the create (a), and the pane's real directory once more before any key is
// sent (b). A directory swapped for a symlink out of the roots is refused
// either way: (a) creates nothing, (b) kills the session it created; no key
// goes out. Mutation gates: drop either check → its case red.
func TestSpawn_ACwdThatLeftTheRootsIsNeverLaunched(t *testing.T) {
	outside := t.TempDir()
	cases := []struct {
		name, reason   string
		creates, kills int
		duringCreate   bool
	}{
		{"swapped before the create", team.SpawnReasonCreateFailed, 0, 0, false},
		{"swapped during the create", team.SpawnReasonLaunchFailed, 1, 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, root := newSpawnFixture(t, 2)
			work := filepath.Join(root, "work")
			if err := os.Mkdir(work, 0o755); err != nil {
				t.Fatal(err)
			}
			swap := func() {
				if err := os.Remove(work); err != nil || os.Symlink(outside, work) != nil {
					t.Error("swap failed")
				}
			}
			if c.duringCreate {
				f.sessions.beforeCreate = swap
			} else {
				f.m.beforeSpawnStep = func(op spawnRow) {
					if op.Step == team.StepAccepted {
						swap()
					}
				}
			}
			if op := f.runOp(1, work, nil); op.State != team.SpawnFailed || op.Reason != c.reason {
				t.Fatalf("op = %+v, want failed %s", op, c.reason)
			}
			if f.sessions.count() != c.creates || len(f.tmux.KillIfInstanceCalls()) != c.kills || len(f.tmux.RawKeysSent()) != 0 {
				t.Fatalf("creates %d kills %+v keys %+v", f.sessions.count(), f.tmux.KillIfInstanceCalls(), f.tmux.RawKeysSent())
			}
		})
	}
}

// I3: the tmux server restarts between the create and the launch. The read
// that confirms the pane sees another generation, so nothing is sent, and
// the kill is declined by the new server: whoever holds that id is left alone.
func TestSpawn_TmuxRestartBetweenStepsTouchesNothing(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.m.beforeSpawnStep = func(op spawnRow) {
		if op.Step == team.StepSessionCreated {
			f.tmux.SetInstance("5151:1700000999")
		}
	}
	if op := f.runOp(1, root, nil); op.State != team.SpawnFailed || op.Reason != team.SpawnReasonLaunchFailed {
		t.Fatalf("op = %+v", op)
	}
	if len(f.tmux.RawKeysSent()) != 0 || !f.tmux.HasSession("tm-0000000100") || len(f.tmux.KillIfInstanceCalls()) != 1 {
		t.Fatalf("keys %+v, session kept %v, kills %+v", f.tmux.RawKeysSent(), f.tmux.HasSession("tm-0000000100"), f.tmux.KillIfInstanceCalls())
	}
}

// What fails before the launch: a session already named like the op's is
// somebody else's (no create, no kill); a create error; a member_command
// that no longer reads kills the created session; a stored row team.db
// refuses to advance (corrupt) is abandoned, not retried, its session killed.
func TestSpawn_FailuresBeforeTheLaunch(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(f *fixture)
		reason string
		kills  int
	}{
		{"name taken", func(f *fixture) { f.tmux.AddSession("tm-0000000100", "/") }, team.SpawnReasonNameTaken, 0},
		{"create error", func(f *fixture) { f.sessions.createErr = errors.New("tmux: no server") }, team.SpawnReasonCreateFailed, 0},
		{"command unreadable", func(f *fixture) { f.teamCfg.err = errors.New("stored value invalid") }, team.SpawnReasonLaunchFailed, 1},
		{"stored row corrupt", func(f *fixture) {
			f.m.beforeSpawnStep = func(op spawnRow) {
				if op.Step == team.StepSessionCreated {
					_, _ = f.m.store.db.Exec(`UPDATE spawn_ops SET pane_id = '' WHERE id = ?`, op.ID)
				}
			}
		}, team.SpawnReasonAbandoned, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, root := newSpawnFixture(t, 2)
			c.setup(f)
			if op := f.runOp(1, root, nil); op.State != team.SpawnFailed || op.Reason != c.reason {
				t.Fatalf("op = %+v, want failed %s", op, c.reason)
			}
			if n := len(f.tmux.KillIfInstanceCalls()); n != c.kills || len(f.tmux.RawKeysSent()) != 0 {
				t.Fatalf("kills = %d (want %d), keys = %+v", n, c.kills, f.tmux.RawKeysSent())
			}
		})
	}
}
