package teammod

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"testing/fstest"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

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
	f.m.startSpawn(f.acceptOp(1, root, nil))
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
	g.spawnWG.Wait()
	letGo()
	_ = f.m.Stop(context.Background()) // joins the first runner
	op, _, _ := g.store.GetSpawnOp(spawnID(1))
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
			id := f.acceptOp(1, root, nil)
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
			f.m.spawnWG.Wait()
			if op, _, _ := f.m.store.GetSpawnOp(id); op.State != team.SpawnFailed || op.Reason != c.reason {
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

// Review H3: an accepted op whose session of its name already exists (a
// daemon that died between the create and its record) adopts it only when
// one tmux answer shows the session carries the op's own id as its tag.
// Without a tag (a stranger's session, or the name on a restarted server)
// or with another op's, it is tmux_name_taken: no key, no kill. Mutation
// gate: adopt by name → the stranger cases red.
func TestSpawn_BootAdoptsOnlyTheSessionTaggedWithItsOp(t *testing.T) {
	cases := []struct {
		name, tag, reason string
		state             team.SpawnState
	}{
		{"no tag", "", team.SpawnReasonNameTaken, team.SpawnFailed},
		{"another op's tag", spawnID(7), team.SpawnReasonNameTaken, team.SpawnFailed},
		{"its own tag", spawnID(1), "", team.SpawnDone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, root := newSpawnFixture(t, 2)
			id := f.acceptOp(1, root, nil)
			f.tmux.AddSession("tm-0000000100", root) // $0
			f.tmux.SetActivePaneMetadata("tm-0000000100", tmux.TmuxPaneMetadata{SessionID: "$0", PaneID: "%0"})
			f.tmux.SetPaneCwd("%0", root)
			if c.tag != "" {
				f.tmux.SetSessionTag("tm-0000000100", spawnTagOption, c.tag)
			}
			f.register("%0", "sid-m1")
			if err := f.m.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			f.m.spawnWG.Wait()
			op, _, _ := f.m.store.GetSpawnOp(id)
			sends := len(f.tmux.RawKeysSent())
			if op.State != c.state || op.Reason != c.reason || f.sessions.count() != 0 || (sends == 1) != (c.state == team.SpawnDone) {
				t.Fatalf("op %s %s, creates %d, sends %d", op.State, op.Reason, f.sessions.count(), sends)
			}
			if len(f.tmux.KillIfInstanceCalls()) != 0 {
				t.Fatalf("kills = %+v", f.tmux.KillIfInstanceCalls())
			}
		})
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
		if op := f.runOp(i+1, root, nil); op.State != team.SpawnDone {
			t.Fatalf("spawn %d = %+v", i, op)
		}
		if got, err := os.ReadFile(js); err != nil || string(got) != want {
			t.Fatalf("spawn %d: register.js = %q (%v), want %q", i, got, err, want)
		}
	}
}
