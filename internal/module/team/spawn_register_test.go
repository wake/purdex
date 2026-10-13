package teammod

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// Spec §7.2 steps 5–6: a verified frame on the op's pane plus a live
// registry entry make the member: stored with the asked model and effort,
// titled, op done.
func TestSpawn_AMemberThatRegistersIsStoredAndTitled(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.register("%0", "sid-m1")
	op := f.runOp(1, root, func(r *spawnRow) { r.Model, r.Effort, r.Title = "opus[1m]", "high", "worker" })
	if op.State != team.SpawnDone || op.SessionID != "sid-m1" {
		t.Fatalf("op = %+v", op)
	}
	rows, _ := f.m.store.MembersOf(uid(1))
	want := memberRow{SpawnOp: spawnID(1), TeamID: uid(1), HostID: "h:1", SessionID: "sid-m1", Ref: ipeers.RefID("sid-m1"),
		Title: "worker", Cwd: root, TmuxSession: "tm-0000000100", TmuxID: "$0", TmuxInstance: "4242:1700000000", PaneID: "%0",
		PID: 31, ProcStart: "p31", Model: "opus[1m]", Effort: "high", State: team.MemberActive, Origin: team.MemberOriginSpawned}
	if len(rows) != 1 {
		t.Fatalf("member rows = %+v", rows)
	}
	want.CreatedAt, want.UpdatedAt = rows[0].CreatedAt, rows[0].UpdatedAt
	if rows[0] != want || !reflect.DeepEqual(f.titles.claims, [][2]string{{"sid-m1", "worker"}}) {
		t.Fatalf("member = %+v\nwant     %+v\nclaims %v", rows[0], want, f.titles.claims)
	}
}

// Spec §7.2 step 5: no registration within 60 s of the launch → the session
// is killed (recorded id, recorded generation) and the op fails
// member_start_timeout, so it no longer counts against the limit. Mutation
// gate: skip the kill → red.
func TestSpawn_StartTimeoutKillsAndFreesTheSlot(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	if op := f.runOp(1, root, nil); op.State != team.SpawnFailed || op.Reason != team.SpawnReasonStartTimeout {
		t.Fatalf("op = %+v", op)
	}
	if got := f.tmux.KillIfInstanceCalls(); !reflect.DeepEqual(got, []tmux.KillIfInstanceCall{{SessionID: "$0", Expected: "4242:1700000000"}}) {
		t.Fatalf("kills = %+v", got)
	}
	if n, _ := f.m.store.CountRunningSpawns(uid(1), ""); n != 0 || f.tmux.HasSession("tm-0000000100") {
		t.Fatalf("running ops %d, session still there %v", n, f.tmux.HasSession("tm-0000000100"))
	}
}

// Spec §7.2 step 5 ("Wait up to 60 s … On timeout: kill"), review R1 as the
// critic read it: every poll judges the deadline first, so a poll that wakes
// past launched_at + 60 s times out even when the member has shown up by
// then. Mutation gate: look at the member before the deadline → red.
func TestSpawn_PastTheBudgetItTimesOutEvenWithAMemberThere(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	f.m.spawnSleep = func(context.Context, time.Duration) {
		f.clock.Add(61_000)
		f.register("%0", "sid-m1")
	}
	if op := f.runOp(1, root, nil); op.State != team.SpawnFailed || op.Reason != team.SpawnReasonStartTimeout || len(f.tmux.KillIfInstanceCalls()) != 1 {
		t.Fatalf("op = %+v, kills %+v", op, f.tmux.KillIfInstanceCalls())
	}
	if rows, _ := f.m.store.MembersOf(uid(1)); len(rows) != 0 {
		t.Fatalf("a member past the budget was stored: %+v", rows)
	}
}

// P4-5 re-review: two runners of one op meet at the deadline. The timeout
// takes its decision first, a compare-and-set from launched, the same one
// the registration makes, and only its winner kills. Registration first:
// the late timeout loses and kills nothing. Timeout first: it kills, and
// the registration's CAS fails, so no member is written. Mutation gate:
// kill before the CAS → the first case red.
func TestSpawn_AtTheDeadlineOnlyOneRunnerActs(t *testing.T) {
	// launched is an op launched now in its own tagged session $0 (pane %0),
	// whose member has registered; the test plays both runners.
	launched := func(t *testing.T) (*fixture, spawnRow) {
		f, root := newSpawnFixture(t, 2)
		f.tmux.FailKillIfInstance = true // a killed session would hide the second runner's CAS
		id := f.acceptOp(1, root, nil)
		f.tmux.AddSession("tm-0000000100", root)
		f.tmux.SetSessionTag("tm-0000000100", spawnTagOption, id)
		mustStep(t, f.m.store, id, team.StepAccepted, spawnUpdate{Step: team.StepSessionCreated,
			TmuxID: "$0", TmuxInstance: "4242:1700000000", PaneID: "%0", At: 1}, true)
		mustStep(t, f.m.store, id, team.StepSessionCreated, spawnUpdate{Step: team.StepLaunched, LaunchedAt: f.clock.Load(), At: 1}, true)
		f.register("%0", "sid-m1")
		op, _, _ := f.m.store.GetSpawnOp(id)
		return f, op
	}
	t.Run("registration first", func(t *testing.T) {
		f, op := launched(t)
		if _, won := f.m.spawnRegister(op); !won {
			t.Fatal("the registration did not win")
		}
		f.clock.Add(61_000)
		f.m.spawnRegister(op) // the second runner, with its read from before
		now, _, _ := f.m.store.GetSpawnOp(op.ID)
		if now.State != team.SpawnRunning || now.Step != team.StepRegistered || len(f.tmux.KillIfInstanceCalls()) != 0 {
			t.Fatalf("op %s at %s, kills %+v", now.State, now.Step, f.tmux.KillIfInstanceCalls())
		}
	})
	t.Run("timeout first", func(t *testing.T) {
		f, op := launched(t)
		f.frames.afterRead = func() {
			f.clock.Add(61_000)
			f.m.spawnRegister(op) // the second runner times the op out
		}
		if _, won := f.m.spawnRegister(op); won {
			t.Fatal("the registration won after the timeout")
		}
		now, _, _ := f.m.store.GetSpawnOp(op.ID)
		rows, _ := f.m.store.MembersOf(uid(1))
		if now.State != team.SpawnFailed || now.Reason != team.SpawnReasonStartTimeout || len(rows) != 0 || len(f.tmux.KillIfInstanceCalls()) != 1 {
			t.Fatalf("op %s %s, members %+v, kills %+v", now.State, now.Reason, rows, f.tmux.KillIfInstanceCalls())
		}
	})
}

// Review H4: tmux restarts after the launch and the new server's %0 shows an
// unrelated verified, live frame. The pane id alone is not the member: the
// same read that confirms the pane sees another generation, so the op is
// abandoned and nobody is stored. Mutation gate: accept on the frame → red.
func TestSpawn_ARestartedTmuxServersPaneIsNotTheMember(t *testing.T) {
	f, root := newSpawnFixture(t, 2)
	f.m.beforeSpawnStep = func(op spawnRow) {
		if op.Step == team.StepLaunched {
			f.tmux.SetInstance("5151:1700000999")
			f.register("%0", "sid-stranger")
		}
	}
	if op := f.runOp(1, root, nil); op.State != team.SpawnFailed || op.Reason != team.SpawnReasonAbandoned {
		t.Fatalf("op = %+v", op)
	}
	if rows, _ := f.m.store.MembersOf(uid(1)); len(rows) != 0 {
		t.Fatalf("a stranger was stored as the member: %+v", rows)
	}
}

// Spec §9.3: Stop leaves a running op at its recorded step for the next
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
	id := f.acceptOp(1, root, nil)
	f.m.startSpawn(id)
	<-polling
	if err := f.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if op, _, _ := f.m.store.GetSpawnOp(id); op.State != team.SpawnRunning || op.Step != team.StepLaunched || len(f.tmux.KillIfInstanceCalls()) != 0 {
		t.Fatalf("after Stop: %s at %s, kills %+v", op.State, op.Step, f.tmux.KillIfInstanceCalls())
	}
}

// #2761: a member that registers 25 s after the launch (a loaded host: 13–17 s was normal, 20 s often missed) is in time
// under the 60 s budget; one that shows up past 60 s still times out. Mutation gate: SpawnRegisterS back to 20 → the first
// case red.
func TestSpawn_ASlowHostRegistersWithinTheBudget(t *testing.T) {
	for _, c := range []struct {
		name     string
		after    int64
		wantDone bool
	}{{"25 s", 25_000, true}, {"past the budget", 61_000, false}} {
		t.Run(c.name, func(t *testing.T) {
			f, root := newSpawnFixture(t, 1)
			start := f.clock.Load()
			registered := false
			f.m.spawnSleep = func(context.Context, time.Duration) {
				f.clock.Add(1_000)
				if !registered && f.clock.Load()-start >= c.after {
					registered = true
					f.register("%0", "sid-m1")
				}
			}
			op := f.runOp(1, root, nil)
			if c.wantDone && (op.State != team.SpawnRunning && op.State != team.SpawnDone || op.Reason == team.SpawnReasonStartTimeout) {
				t.Fatalf("op = %+v, want a member registered in time", op)
			}
			if !c.wantDone && (op.State != team.SpawnFailed || op.Reason != team.SpawnReasonStartTimeout) {
				t.Fatalf("op = %+v, want member_start_timeout", op)
			}
		})
	}
}

// #2761: the failed op says WHY in its detail, decided here and not guessed by the CLI. A host whose mods have said hello
// (hooks work) is busy; a host that never heard one is the one that needs `pdx setup`. The seconds are the budget in force.
// Mutation gate: drop the hello condition (always the setup hint, or never) → one case red.
func TestSpawnView_StartTimeoutDetailSaysBusyOrNoHooks(t *testing.T) {
	for _, c := range []struct {
		name      string
		hello     bool
		wantBusy  bool
		wantSetup bool
	}{{"hooks seen", true, true, false}, {"hooks never seen", false, false, true}} {
		t.Run(c.name, func(t *testing.T) {
			f, root := newSpawnFixture(t, 1)
			if c.hello {
				f.m.mu.Lock()
				f.m.modSeen["sid-old"] = helloInfo{ModVersion: "5", Agent: "cc", At: f.clock.Load() - 3_600_000}
				f.m.mu.Unlock()
			}
			row := f.runOp(1, root, nil)
			if row.State != team.SpawnFailed || row.Reason != team.SpawnReasonStartTimeout {
				t.Fatalf("op = %+v", row)
			}
			op, err := f.m.spawnView(row, "mlab/lead")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(op.Detail, "60 秒") {
				t.Errorf("detail %q does not carry the budget", op.Detail)
			}
			if got := strings.Contains(op.Detail, "主機忙"); got != c.wantBusy {
				t.Errorf("busy hint = %v, want %v: %q", got, c.wantBusy, op.Detail)
			}
			if got := strings.Contains(op.Detail, "pdx setup --agent cc"); got != c.wantSetup {
				t.Errorf("setup hint = %v, want %v: %q", got, c.wantSetup, op.Detail)
			}
		})
	}
	// any other reason carries no detail
	f, root := newSpawnFixture(t, 1)
	row := f.runOp(1, root, nil)
	row.Reason = team.SpawnReasonCreateFailed
	if op, _ := f.m.spawnView(row, ""); op.Detail != "" {
		t.Errorf("detail of another reason = %q", op.Detail)
	}
}
