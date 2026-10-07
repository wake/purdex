package teammod

import (
	"errors"
	"reflect"
	"testing"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// Spec §7.2 steps 3–6, U20 (a): the launch line is team.member_command with
// --plugin-dir and the asked model (quoted) and effort, typed into window 0
// of the tmux session the op created; once a verified frame on its pane and
// a live registry entry show the member, it is stored, titled, and the op
// answers done with it.
func TestSpawn_LaunchLineHasPluginDirModelEffortAndRegisters(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.register("%0", "sid-m1")
	code, op, e := f.spawn(spawnID(1), root, func(r *team.SpawnRequest) {
		r.Model, r.Effort, r.Title = "opus[1m]", "high", "worker"
	})
	if code != 200 || op.State != team.SpawnDone || op.Step != team.StepRegistered || op.LeadAddress != "mlab/n10" {
		t.Fatalf("spawn = %d %+v %+v", code, op, e)
	}
	keys := "'claude' '--dangerously-skip-permissions' --plugin-dir '" + agentcc.PluginRoot(f.core.Cfg.DataDir) + "' --model 'opus[1m]' --effort high\n"
	if got := f.tmux.RawKeysSent(); len(got) != 1 || got[0].Target != "$0:0" || !reflect.DeepEqual(got[0].Keys, []string{keys}) {
		t.Fatalf("keys = %+v, want one send of %q to $0:0", got, keys)
	}
	ref := ipeers.RefID("sid-m1")
	want := team.Member{SessionID: "sid-m1", Ref: ref, Address: "mlab/" + ref, TeamID: uid(1), HostID: "h:1", Title: "worker",
		Cwd: root, TmuxSession: "tm-0000000100", State: team.MemberActive, Model: "opus[1m]", Effort: "high",
		SpawnOp: spawnID(1), CreatedAt: op.Member.CreatedAt}
	if !reflect.DeepEqual(*op.Member, want) {
		t.Fatalf("member = %+v, want %+v", *op.Member, want)
	}
	rows, _ := f.m.store.MembersOf(uid(1))
	if len(rows) != 1 || rows[0].PaneID != "%0" || rows[0].TmuxID != "$0" || rows[0].TmuxInstance != "4242:1700000000" || rows[0].PID != 31 {
		t.Fatalf("member rows = %+v", rows)
	}
	if !reflect.DeepEqual(f.titles.claims, [][2]string{{"sid-m1", "worker"}}) {
		t.Fatalf("title claims = %v", f.titles.claims)
	}
}

// U20 (c): without --model and --effort the line carries neither; the
// command is the host's team.member_command as written.
func TestSpawn_WithoutModelSendsNoModelFlag(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.teamCfg.s.MemberCommand = "X=1 claude"
	f.register("%0", "sid-m1")
	if code, op, e := f.spawn(spawnID(1), root, nil); code != 200 || op.State != team.SpawnDone {
		t.Fatalf("spawn = %d %+v %+v", code, op, e)
	}
	keys := "X='1' 'claude' --plugin-dir '" + agentcc.PluginRoot(f.core.Cfg.DataDir) + "'\n"
	if got := f.tmux.RawKeysSent(); len(got) != 1 || got[0].Keys[0] != keys {
		t.Fatalf("keys = %+v, want %q", got, keys)
	}
}

// Spec §7.2 step 5, §15: a member that does not register within 20 s of the
// launch has its tmux session killed (by the recorded id, under the recorded
// generation) and the op fails member_start_timeout; the slot is free at
// once. Mutation gate: skip the kill → the kill assertion goes red.
func TestSpawn_StartTimeoutKillsAndFreesTheSlot(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	code, op, _ := f.spawn(spawnID(1), root, nil)
	if code != 200 || op.State != team.SpawnFailed || op.Reason != team.SpawnReasonStartTimeout {
		t.Fatalf("spawn = %d %+v", code, op)
	}
	if got := f.tmux.KillIfInstanceCalls(); !reflect.DeepEqual(got, []tmux.KillIfInstanceCall{{SessionID: "$0", Expected: "4242:1700000000"}}) {
		t.Fatalf("kills = %+v", got)
	}
	if f.tmux.HasSession("tm-0000000100") {
		t.Fatal("the timed-out member's tmux session is still there")
	}
	f.register("%1", "sid-m2")
	if code, op, e := f.spawn(spawnID(2), root, nil); code != 200 || op.State != team.SpawnDone {
		t.Fatalf("the next spawn after a timeout = %d %+v %+v (the slot must be free)", code, op, e)
	}
}

// I3: the tmux server restarts between the create and the launch. The send
// is declined by the new server and so is the kill: nothing of whoever now
// holds that session id is touched, and the op fails launch_failed.
func TestSpawn_TmuxRestartBetweenStepsTouchesNothing(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.m.beforeSpawnStep = func(op spawnRow) {
		if op.Step == team.StepSessionCreated {
			f.tmux.SetInstance("5151:1700000999")
		}
	}
	code, op, _ := f.spawn(spawnID(1), root, nil)
	if code != 200 || op.State != team.SpawnFailed || op.Reason != team.SpawnReasonLaunchFailed {
		t.Fatalf("spawn = %d %+v", code, op)
	}
	if got := f.tmux.RawKeysSent(); len(got) != 0 {
		t.Fatalf("keys sent to a server of another generation: %+v", got)
	}
	if !f.tmux.HasSession("tm-0000000100") || len(f.tmux.KillIfInstanceCalls()) != 1 {
		t.Fatalf("the session must stay (kill declined): has=%v kills=%+v", f.tmux.HasSession("tm-0000000100"), f.tmux.KillIfInstanceCalls())
	}
}

// What fails before the launch: a session already named like the op's is
// somebody else's (no create, no kill); a create error; a member_command
// that no longer reads kills the created session; a stored row that team.db
// refuses to advance (corrupt) is abandoned, not retried, and its session
// killed.
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
			if code, op, _ := f.spawn(spawnID(1), root, nil); code != 200 || op.State != team.SpawnFailed || op.Reason != c.reason {
				t.Fatalf("spawn = %d %+v, want failed %s", code, op, c.reason)
			}
			if n := len(f.tmux.KillIfInstanceCalls()); n != c.kills || len(f.tmux.RawKeysSent()) != 0 {
				t.Fatalf("kills = %d (want %d), keys = %+v", n, c.kills, f.tmux.RawKeysSent())
			}
		})
	}
}
