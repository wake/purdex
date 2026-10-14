package teammod

import (
	"context"
	"errors"
	"fmt"
	"github.com/wake/purdex/internal/module/agent"
	"reflect"
	"strings"
	"testing"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/resources"
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

// #2761: the failed op says WHY in its detail, decided here and not guessed by the CLI. A host whose mods have said hello
// (hooks work) is busy; a host that never heard one is the one that needs `pdx setup`. The seconds come from the budget in force
// and the load from the resources sample, left out without one. Mutation gate: drop the hello condition (always the setup
// hint, or never), or the load note → a case red.
type fakeHostReader struct {
	h  resources.HostUse
	ok bool
}

func (r fakeHostReader) LatestHost() (resources.HostUse, bool) { return r.h, r.ok }

func TestSpawnView_StartTimeoutDetailSaysBusyOrNoHooks(t *testing.T) {
	load := "（load 18.1／16.4／16.3，10 核；記憶體已用 84%，壓力：警告）"
	waited := "（已等 60 秒；最後看到：pane 上還沒有 frame）" // the fixture's clock moves 250 ms a poll: the 60 s budget is spent to the poll
	sample := fakeHostReader{h: resources.HostUse{Load1: 18.1, Load5: 16.4, Load15: 16.3, NCPU: 10, Mem: 84, Pressure: 2}, ok: true}
	for _, c := range []struct {
		name   string
		hello  bool
		reader resources.HostReader
		want   string
	}{
		{"hooks seen, sample", true, sample, "主機忙：member 沒在 60 秒內完成啟動" + waited + load + "，名額已釋出，可以稍後重試"},
		{"hooks seen, no sample", true, nil, "主機忙：member 沒在 60 秒內完成啟動" + waited + "，名額已釋出，可以稍後重試"},
		{"hooks seen, sample not usable yet", true, fakeHostReader{}, "主機忙：member 沒在 60 秒內完成啟動" + waited + "，名額已釋出，可以稍後重試"},
		{"hooks never seen, sample", false, sample, "member 沒在 60 秒內完成啟動" + waited + load + "，這台主機從沒看過 Purdex hooks（pdx setup --agent cc）"},
		{"hooks never seen, no sample", false, nil, "member 沒在 60 秒內完成啟動" + waited + "，這台主機從沒看過 Purdex hooks（pdx setup --agent cc）"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, root := newSpawnFixture(t, 1)
			f.m.hostReader = c.reader
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
			if op.Detail != c.want {
				t.Errorf("detail\n got %q\nwant %q", op.Detail, c.want)
			}
		})
	}
	// the pressure levels (1 normal, 2 warning, 4 critical); an unknown one is left out
	for lvl, want := range map[int]string{1: "，壓力：正常", 2: "，壓力：警告", 4: "，壓力：嚴重", 0: ""} {
		m := &Module{hostReader: fakeHostReader{h: resources.HostUse{Load1: 1, NCPU: 2, Mem: 50, Pressure: lvl}, ok: true}}
		if got, w := m.hostLoadNote(), "（load 1.0／0.0／0.0，2 核；記憶體已用 50%"+want+"）"; got != w {
			t.Errorf("pressure %d: %q, want %q", lvl, got, w)
		}
	}
	// any other reason carries no detail
	f, root := newSpawnFixture(t, 1)
	row := f.runOp(1, root, nil)
	row.Reason = team.SpawnReasonCreateFailed
	if op, _ := f.m.spawnView(row, ""); op.Detail != "" {
		t.Errorf("detail of another reason = %q", op.Detail)
	}
}

// #2853: a member that registers at the 30th second is a success (the budget is 60 s). Mutation gate: a 20 s budget → red.
func TestSpawn_RegisteringAtThirtySecondsSucceeds(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	done := false
	f.m.spawnSleep = func(context.Context, time.Duration) {
		f.clock.Add(30_000)
		if !done {
			done = true
			f.register("%0", "sid-m1")
		}
	}
	if op := f.runOp(1, root, nil); op.State != team.SpawnDone || op.SessionID != "sid-m1" {
		t.Fatalf("op = %+v, want done: a member that registers at 30 s is in time", op)
	}
}

// The budget is 60 s on the wire, in the spec and in the module's own default.
func TestSpawn_BudgetIsSixtySeconds(t *testing.T) {
	if team.SpawnRegisterS != 60 {
		t.Fatalf("SpawnRegisterS = %d", team.SpawnRegisterS)
	}
	f, _ := newSpawnFixture(t, 1)
	if f.m.spawnBudget != 60_000 {
		t.Fatalf("spawnBudget = %d", f.m.spawnBudget)
	}
}

// What the poll last saw on the op's pane, for the timeout's detail: no frame, a frame not verified, a frame with no session id. (The
// registry-missing case needs the origins fake: seen with the full timeout run above.) Mutation gate: collapse the states → red.
type fakeFrames2 struct{ list []agent.TerminalSession }

func (r fakeFrames2) LiveSessions(context.Context, string) ([]agent.TerminalSession, error) {
	return r.list, nil
}

func TestSpawn_PaneStateSaysWhatIsMissing(t *testing.T) {
	for _, c := range []struct {
		name string
		list []agent.TerminalSession
		want string
	}{
		{"no frame", nil, "pane 上還沒有 frame"},
		{"another pane", []agent.TerminalSession{{PaneID: "%9", Verified: true, SessionID: "s"}}, "pane 上還沒有 frame"},
		{"not verified", []agent.TerminalSession{{PaneID: "%0", Verified: false, SessionID: "s"}}, "有 frame，尚未驗證"},
		{"no session id", []agent.TerminalSession{{PaneID: "%0", Verified: true}}, "有 frame，還沒有 session id"},
	} {
		m := &Module{frames: fakeFrames2{c.list}}
		if _, got := m.paneState("%0"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The book of last-seen states is bounded, and a read error never reaches the detail (codex on #2853). Mutation gates: no cap → the
// first id is still there (red); the error text in the state → red.
func TestSpawnSeenBook_IsBounded(t *testing.T) {
	var b spawnSeenBook
	for i := 0; i < spawnSeenCap+10; i++ {
		b.set(fmt.Sprintf("op-%d", i), "x")
	}
	if _, ok := b.get("op-0"); ok {
		t.Fatal("the oldest entry survived the cap")
	}
	if _, ok := b.get(fmt.Sprintf("op-%d", spawnSeenCap+9)); !ok {
		t.Fatal("the newest entry is gone")
	}
}

type failingFrames struct{}

func (failingFrames) LiveSessions(context.Context, string) ([]agent.TerminalSession, error) {
	return nil, errors.New("sqlite: /Users/secret/path/frames.db is locked")
}

func TestSpawn_FrameReadErrorStaysOutOfTheDetail(t *testing.T) {
	var logged []string
	m := &Module{frames: failingFrames{}, logf: func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }}
	_, state := m.paneState("%0")
	if state != "讀不到 frame" || strings.Contains(state, "secret") {
		t.Fatalf("state = %q, want the fixed words", state)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "secret") {
		t.Fatalf("log = %v, want the cause there", logged)
	}
}

// One poll reads the frames once (codex on #2853: a second read for the diagnosis doubled the load of a busy host).
type pollCountFrames struct{ n *int }

func (c pollCountFrames) LiveSessions(context.Context, string) ([]agent.TerminalSession, error) {
	*c.n++
	return nil, nil
}

func TestSpawn_OnePollReadsTheFramesOnce(t *testing.T) {
	f, root := newSpawnFixture(t, 1)
	n := 0
	f.m.frames = pollCountFrames{&n}
	polls := 0
	f.m.spawnSleep = func(context.Context, time.Duration) { polls++; f.clock.Add(250) }
	f.runOp(1, root, nil)
	if polls == 0 {
		t.Fatal("no poll ran")
	}
	if n > polls {
		t.Fatalf("%d frame reads for %d polls: more than one per poll", n, polls)
	}
}
