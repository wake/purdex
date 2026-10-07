package teammod

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"testing/fstest"
	"time"

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

// Spec §7.2 step 4, coordinator decision 9: the launch line's --plugin-dir
// tree is extracted from the embedded mod when it is absent, and never
// rewritten once it is there (pdx setup owns it).
func TestSpawn_ExtractsThePluginTreeOnlyWhenAbsent(t *testing.T) {
	old := agentcc.PluginSource
	agentcc.PluginSource = fstest.MapFS{
		".claude-plugin/plugin.json": {Data: []byte(`{"name":"purdex","version":"0"}`)},
		"hooks/register.js":          {Data: []byte("export function register() {}")},
	}
	t.Cleanup(func() { agentcc.PluginSource = old })
	f, root := newSpawnFixture(t, 3)
	js := filepath.Join(agentcc.PluginRoot(f.core.Cfg.DataDir), "hooks", "register.js")
	for i, want := range []string{"export function register() {}", "// the owner's edit"} {
		if i == 1 {
			if err := os.WriteFile(js, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		f.register(fmt.Sprintf("%%%d", i), fmt.Sprintf("sid-m%d", i))
		if code, op, e := f.spawn(spawnID(i+1), root, nil); code != 200 || op.State != team.SpawnDone {
			t.Fatalf("spawn %d = %d %+v %+v", i, code, op, e)
		}
		if got, err := os.ReadFile(js); err != nil || string(got) != want {
			t.Fatalf("spawn %d: register.js = %q (%v), want %q", i, got, err, want)
		}
	}
}

// waitSpawn polls op id until it leaves running (5 s at most).
func waitSpawn(t *testing.T, s *Store, id string) spawnRow {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if op, ok, err := s.GetSpawnOp(id); err == nil && ok && op.State != team.SpawnRunning {
			return op
		}
	}
	t.Fatalf("spawn op %s still running after 5 s", id)
	return spawnRow{}
}

// Spec §7.2 step 3, §9.3, §15: a daemon stopped right after it recorded
// session_created; the restarted daemon (a second Module over the same
// team.db) continues from that step: no second create, one launch, done.
// The first runner then wakes with its stale read and loses the CAS that
// is the right to send. Mutation gate: drop the step CAS → two sends.
func TestSpawn_RestartMidOpOpensNothingTwice(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	letGo := sync.OnceFunc(func() { close(release) })
	t.Cleanup(letGo) // before the fixture's Stop, which joins the held runner
	f.m.beforeSpawnStep = func(op spawnRow) {
		if op.Step == team.StepSessionCreated {
			once.Do(func() { close(held); <-release })
		}
	}
	f.m.spawnWait = 10 * time.Millisecond
	if code, op, e := f.spawn(spawnID(1), root, nil); code != 200 || op.State != team.SpawnRunning {
		t.Fatalf("spawn = %d %+v %+v", code, op, e)
	}
	<-held
	f.register("%0", "sid-m1")
	g := New().WithTitles(f.titles)
	g.logf, g.now, g.spawnSleep = func(string, ...any) {}, func() int64 { return f.clock.Load() }, f.fastSleep
	if err := g.Init(f.core); err != nil {
		t.Fatal(err)
	}
	if err := g.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Stop(context.Background()); _ = g.Close() })
	op := waitSpawn(t, g.store, spawnID(1))
	letGo()
	_ = f.m.Stop(context.Background()) // joins the first runner
	if op.State != team.SpawnDone || f.sessions.count() != 1 || len(f.tmux.RawKeysSent()) != 1 {
		t.Fatalf("op %s, creates %d, sends %+v: want done, 1, 1", op.State, f.sessions.count(), f.tmux.RawKeysSent())
	}
}

// Spec §9.3: at boot a launched op past its 20 s budget is killed (the
// recorded id, under the recorded generation) and fails
// member_start_timeout; one whose tmux session is gone is abandoned and
// nothing is killed.
func TestSpawn_RestartPastBudgetKillsAndFails(t *testing.T) {
	cases := []struct {
		name, step string
		session    bool
		reason     string
	}{
		{"launched past its budget", team.StepLaunched, true, team.SpawnReasonStartTimeout},
		{"launched, session gone", team.StepLaunched, false, team.SpawnReasonAbandoned},
		{"session_created, session gone", team.StepSessionCreated, false, team.SpawnReasonAbandoned},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, root := newSpawnFixture(t, 2)
			id := spawnID(1)
			mustCreateSpawn(t, f.m.store, spawnRow{ID: id, TeamID: uid(1), HostID: "h:1", OriginSessionID: "sid-1", Cwd: root,
				TmuxName: "tm-0000000100", Step: team.StepAccepted, State: team.SpawnRunning, CreatedAt: 1, UpdatedAt: 1})
			mustStep(t, f.m.store, id, team.StepAccepted, spawnUpdate{Step: team.StepSessionCreated,
				TmuxID: "$7", TmuxInstance: "4242:1700000000", PaneID: "%7", At: 1}, true)
			if c.step == team.StepLaunched {
				mustStep(t, f.m.store, id, team.StepSessionCreated, spawnUpdate{Step: team.StepLaunched, LaunchedAt: f.clock.Load() - 21_000, At: 2}, true)
			}
			if c.session {
				f.tmux.AddSessionWithID("$7", "tm-0000000100", root)
			}
			if err := f.m.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if op := waitSpawn(t, f.m.store, id); op.State != team.SpawnFailed || op.Reason != c.reason {
				t.Fatalf("op = %s %s, want failed %s", op.State, op.Reason, c.reason)
			}
			want := []tmux.KillIfInstanceCall(nil)
			if c.session {
				want = []tmux.KillIfInstanceCall{{SessionID: "$7", Expected: "4242:1700000000"}}
			}
			if got := f.tmux.KillIfInstanceCalls(); !reflect.DeepEqual(got, want) || len(f.tmux.RawKeysSent()) != 0 {
				t.Fatalf("kills = %+v (want %+v), keys = %+v", got, want, f.tmux.RawKeysSent())
			}
		})
	}
}

// Spec §9.3: Stop leaves a running op at its recorded step, for the next
// boot, and returns once its runner has.
func TestSpawn_StopLeavesTheOpRunning(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	polling := make(chan struct{}, 1)
	f.m.spawnSleep = func(ctx context.Context, _ time.Duration) {
		select {
		case polling <- struct{}{}:
		default:
		}
		<-ctx.Done()
	}
	f.m.spawnWait = 10 * time.Millisecond
	f.spawn(spawnID(1), root, nil)
	<-polling
	if err := f.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if op, _, _ := f.m.store.GetSpawnOp(spawnID(1)); op.State != team.SpawnRunning || op.Step != team.StepLaunched || len(f.tmux.KillIfInstanceCalls()) != 0 {
		t.Fatalf("after Stop: %s at %s, kills %+v; want running at launched, no kill", op.State, op.Step, f.tmux.KillIfInstanceCalls())
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
