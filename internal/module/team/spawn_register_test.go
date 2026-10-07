package teammod

import (
	"context"
	"reflect"
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
		PID: 31, ProcStart: "p31", Model: "opus[1m]", Effort: "high", State: team.MemberActive}
	if len(rows) != 1 {
		t.Fatalf("member rows = %+v", rows)
	}
	want.CreatedAt, want.UpdatedAt = rows[0].CreatedAt, rows[0].UpdatedAt
	if rows[0] != want || !reflect.DeepEqual(f.titles.claims, [][2]string{{"sid-m1", "worker"}}) {
		t.Fatalf("member = %+v\nwant     %+v\nclaims %v", rows[0], want, f.titles.claims)
	}
}

// Spec §7.2 step 5: no registration within 20 s of the launch → the session
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

// Review R1 (ruled by the coordinator): the 20 s bound the wait; a member
// the first poll after it finds registered is accepted, not killed.
// Mutation gate: check the deadline before the frame → red.
func TestSpawn_AMemberSeenOnTheFirstPollPastTheBudgetIsAccepted(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	f.m.spawnSleep = func(context.Context, time.Duration) {
		f.clock.Add(21_000)
		f.register("%0", "sid-m1")
	}
	if op := f.runOp(1, root, nil); op.State != team.SpawnDone || len(f.tmux.KillIfInstanceCalls()) != 0 {
		t.Fatalf("op = %+v, kills %+v", op, f.tmux.KillIfInstanceCalls())
	}
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
