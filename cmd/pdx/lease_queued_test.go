package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/cmd/pdx/daemonclient"
	"github.com/wake/purdex/internal/resources"
)

func queuedDaemon(pos int, waitedMS int64) *fakeLeaseDaemon {
	return &fakeLeaseDaemon{post: func(resources.LeaseRequest) (int, any) {
		return 200, resources.LeaseResponse{ID: leaseRow, State: resources.StateQueued, Position: pos, WaitedMS: waitedMS,
			QueuedReason: resources.QueuedBehindQueue, PositionID: "pos_1"}
	}}
}

// askDaemon answers an ask request waiting at a position, then the polls.
func askDaemon(polls ...resources.LeaseResponse) *fakeLeaseDaemon {
	return &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting, Position: 1, PositionID: "pos_1"}
		},
		pollList: polls,
	}
}

// Queue mode asks once and never polls: the daemon answers at once, and the
// place is kept without this call. (M: poll in queue mode.)
func TestAcquire_QueueModeNeverPolls(t *testing.T) {
	fixedHolder(t)
	d := queuedDaemon(2, 0)
	d.pollList = []resources.LeaseResponse{{ID: leaseRow, State: resources.StateHeld, Granted: true}} // a poll would be answered held
	code, stdout, _ := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build", "--queue", "--client-id", leaseCID)
	posts, polls, deletes := d.snapshot()
	assert.Zero(t, polls)
	assert.Empty(t, deletes)
	require.Len(t, posts, 1)
	assert.Equal(t, resources.CallQueue, posts[0].Mode)
	assert.False(t, posts[0].Parkable)
	assert.Equal(t, ExitQueued, code, stdout)
}

// A queue-mode request the daemon could not queue (no session to queue for: it
// answers waiting, as it does for wait mode) waits like wait mode; telling the
// caller "you are number N, you will be told" would be false, since no notice
// can reach a caller without a session.
func TestAcquire_QueueModeWithoutASessionWaitsLikeWaitMode(t *testing.T) {
	fixedHolder(t)
	d := &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting, Position: 1}
		},
		pollList: []resources.LeaseResponse{{ID: leaseRow, State: resources.StateHeld, Granted: true}},
	}
	code, stdout, _ := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build", "--queue", "--client-id", leaseCID)
	assert.Equal(t, ExitOK, code)
	assert.True(t, parseAcquireLine(t, stdout).Granted)
	_, polls, _ := d.snapshot()
	assert.Equal(t, 1, polls)
}

// A queued answer ends the call at once: the line says state, place and why,
// nothing is sent to cancel (that would lose the place), and the exit code is 15.
// (M: route it to fail-open and the command would run.)
func TestAcquire_QueuedExits15WithoutCancelling(t *testing.T) {
	fixedHolder(t)
	d := queuedDaemon(3, 125_000)
	code, stdout, stderr := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build", "--queue", "--client-id", leaseCID)
	assert.Equal(t, ExitQueued, code)
	assert.Equal(t, 15, ExitQueued)
	assert.Empty(t, stderr)
	l := parseAcquireLine(t, stdout)
	assert.False(t, l.Granted)
	assert.Equal(t, "queued", l.State)
	assert.Equal(t, 3, l.Position)
	assert.Equal(t, resources.QueuedBehindQueue, l.QueuedReason)
	assert.Equal(t, int64(125_000), l.WaitedMS)
	assert.Empty(t, l.FailOpen)
	_, _, deletes := d.snapshot()
	assert.Empty(t, deletes, "no DELETE: it would cancel the queued row")
}

// A replaced answer (a newer call took the row over) is defensive: not run, 15, no DELETE.
func TestAcquire_ReplacedExits15WithoutCancelling(t *testing.T) {
	fixedHolder(t)
	d := &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting, Position: 1}
		},
		pollList: []resources.LeaseResponse{{ID: leaseRow, State: resources.StateReplaced}},
	}
	code, stdout, _ := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build", "--client-id", leaseCID)
	assert.Equal(t, ExitQueued, code)
	assert.Equal(t, "replaced", parseAcquireLine(t, stdout).State)
	_, _, deletes := d.snapshot()
	assert.Empty(t, deletes)
}

// An interrupted wait still cancels and exits 12, as before.
func TestAcquire_CancelPathUnchanged(t *testing.T) {
	fixedHolder(t)
	d := &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting, Position: 1}
		},
		hold: true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	code, _, _ := driveLeaseWith(t, ctx, d, nil, "acquire", "--kind", "build", "--client-id", leaseCID)
	assert.Equal(t, ExitCancelled, code)
	_, _, deletes := d.snapshot()
	require.Len(t, deletes, 1)
	assert.Contains(t, deletes[0], "client_id="+leaseCID)
}

// The text for the model and for the person: the place, that it will be told,
// and not to get around the queue.
func TestQueuedText(t *testing.T) {
	got := queuedText(resources.LeaseResponse{Position: 3, QueuedReason: resources.QueuedBehindQueue}, "")
	assert.Equal(t, "主機資源忙，這個指令還沒執行：排在第 3 位。輪到你時我會通知你，先做別的事；不要改成背景執行或加 & 繞過排隊。", got)
	own := queuedText(resources.LeaseResponse{Position: 1, QueuedReason: resources.QueuedOwnRunning}, "")
	assert.Contains(t, own, "等你自己的上一個指令跑完")
	assert.NotContains(t, own, "第 1 位")
	rep := queuedText(resources.LeaseResponse{State: resources.StateReplaced}, "")
	assert.Contains(t, rep, "這個呼叫已被同一個 session 較新的呼叫取代")
}

// `pdx lease run` on a queued answer: the text on stderr, exit 15, the command
// does not run, nothing is released or cancelled.
func TestRun_QueuedDoesNotRunTheCommand(t *testing.T) {
	marker := t.TempDir() + "/ran"
	d := queuedDaemon(2, 61_000)
	code, stdout, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build", "--queue"}, "touch", marker)
	assert.Equal(t, ExitQueued, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "排在第 2 位")
	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "the command must not run")
	_, _, deletes := d.snapshot()
	assert.Empty(t, deletes, "no release, no cancel")
}

func TestRun_QueuedOwnRunningText(t *testing.T) {
	d := &fakeLeaseDaemon{post: func(resources.LeaseRequest) (int, any) {
		return 200, resources.LeaseResponse{ID: leaseRow, State: resources.StateQueued, Position: 1, QueuedReason: resources.QueuedOwnRunning}
	}}
	code, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build", "--queue"}, "true")
	assert.Equal(t, ExitQueued, code)
	assert.Contains(t, stderr, "等你自己的上一個指令跑完")
	assert.NotContains(t, stderr, "第 1 位")
}

// The mode is chosen by PDX_LEASE_CALLER when no flag is given, and a flag wins.
// (M: swap fg and bg.)
func TestCaller_EnvPicksTheModeAndFlagsOverride(t *testing.T) {
	modeOf := func(env map[string]string, flags ...string) string {
		d := &fakeLeaseDaemon{}
		code, _, stderr := driveRunEnv(t, d, nil, env, append([]string{"--kind", "build"}, flags...), "true")
		require.Equal(t, ExitOK, code, stderr)
		posts, _, _ := d.snapshot()
		require.Len(t, posts, 1)
		return posts[0].Mode
	}
	assert.Equal(t, resources.CallQueue, modeOf(map[string]string{"PDX_LEASE_CALLER": "fg"}))
	assert.Equal(t, resources.CallAsk, modeOf(map[string]string{"PDX_LEASE_CALLER": "bg"}))
	assert.Equal(t, resources.CallWait, modeOf(nil))
	assert.Equal(t, resources.CallWait, modeOf(map[string]string{"PDX_LEASE_CALLER": "other"}))
	assert.Equal(t, resources.CallWait, modeOf(map[string]string{"PDX_LEASE_CALLER": "fg"}, "--wait"))
	assert.Equal(t, resources.CallAsk, modeOf(map[string]string{"PDX_LEASE_CALLER": "fg"}, "--ask"))
	assert.Equal(t, resources.CallQueue, modeOf(map[string]string{"PDX_LEASE_CALLER": "bg"}, "--queue"))

	// acquire reads it too, through the same getenv.
	fixedHolder(t)
	d := &fakeLeaseDaemon{}
	code, _, _ := driveLeaseEnv(t, context.Background(), d, map[string]string{"PDX_LEASE_CALLER": "fg"}, []daemonclient.Option{leadClockOpt()}, "acquire", "--kind", "build")
	assert.Equal(t, ExitOK, code)
	posts, _, _ := d.snapshot()
	require.Len(t, posts, 1)
	assert.Equal(t, resources.CallQueue, posts[0].Mode)

	// Two modes at once are a usage error.
	code, _, _ = driveRunEnv(t, &fakeLeaseDaemon{}, nil, nil, []string{"--kind", "build", "--queue", "--ask"}, "true")
	assert.Equal(t, ExitUsage, code)
}

// --wait stays a switch, and keeps the old duration forms.
func TestWaitFlag_BareAndDurationForms(t *testing.T) {
	for name, c := range map[string]struct {
		flags []string
		waitS int
	}{
		"bare":       {[]string{"--wait"}, 0},
		"equals":     {[]string{"--wait=2m"}, 0},
		"separate":   {[]string{"--wait", "2m"}, 0},
		"single":     {[]string{"-wait", "90s"}, 0},
		"bare+after": {[]string{"--wait", "--kind", "build"}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			d := &fakeLeaseDaemon{}
			flags := c.flags
			if name != "bare+after" {
				flags = append(append([]string{}, flags...), "--kind", "build")
			}
			code, _, stderr := driveRunCmd(t, d, nil, flags, "true")
			require.Equal(t, ExitOK, code, stderr)
			posts, _, _ := d.snapshot()
			require.Len(t, posts, 1)
			assert.Equal(t, resources.CallWait, posts[0].Mode)
			assert.Equal(t, c.waitS, posts[0].WaitS)
		})
	}
	// A duration that is out of range is still a usage error.
	code, _, _ := driveRunCmd(t, &fakeLeaseDaemon{}, nil, []string{"--kind", "build", "--wait", "-5s"}, "true")
	assert.Equal(t, ExitUsage, code)
}

// Ask mode polls as wait mode does, and runs on a held answer that carries the position.
func TestRun_AskWaitsThenRuns(t *testing.T) {
	marker := t.TempDir() + "/ran"
	d := askDaemon(
		resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting, PositionID: "pos_1"},
		resources.LeaseResponse{ID: leaseRow, State: resources.StateHeld, Granted: true, PositionID: "pos_1"},
	)
	code, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build", "--ask"}, "touch", marker)
	assert.Equal(t, ExitOK, code, stderr)
	_, err := os.Stat(marker)
	assert.NoError(t, err, "the command runs after the answer")
	_, polls, _ := d.snapshot()
	assert.Equal(t, 2, polls)
}

// An ask request that ended without being run: not run, exit 15, the line, no
// fail-open. (M: let it reach fail-open.)
func TestRun_AskCancelledExits15WithoutRunning(t *testing.T) {
	for _, reason := range []string{
		resources.EndCancelledBySession, resources.EndNoAnswer, resources.EndIdleTimeout,
		resources.EndDone, resources.EndReleasedAfterLimit, resources.EndDrained,
	} {
		t.Run(reason, func(t *testing.T) {
			marker := t.TempDir() + "/ran"
			d := askDaemon(resources.LeaseResponse{ID: leaseRow, State: resources.StateEnded, EndReason: reason})
			code, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build", "--ask"}, "touch", marker)
			assert.Equal(t, ExitQueued, code)
			assert.Contains(t, stderr, "pdx lease: 已取消，指令沒有執行")
			assert.NotContains(t, stderr, "直接執行")
			_, err := os.Stat(marker)
			assert.True(t, os.IsNotExist(err))
		})
	}
}

// R8: a request that waited at a position runs only on a held answer that
// still names it; otherwise the room is given back. (M: run on any held.)
func TestRun_AskRefusesAHeldWithoutPosition(t *testing.T) {
	marker := t.TempDir() + "/ran"
	d := askDaemon(resources.LeaseResponse{ID: leaseRow, State: resources.StateHeld, Granted: true})
	code, _, _ := driveRunCmd(t, d, nil, []string{"--kind", "build", "--ask"}, "touch", marker)
	assert.Equal(t, ExitQueued, code)
	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err))
	_, _, deletes := d.snapshot()
	assert.Equal(t, []string{"/api/resources/leases/" + leaseRow}, deletes, "the room is given back, without a notice request")
}

// Abandoned and cancelled still fail open: the caller runs.
func TestRun_AbandonedStillFailsOpen(t *testing.T) {
	marker := t.TempDir() + "/ran"
	d := askDaemon(resources.LeaseResponse{ID: leaseRow, State: resources.StateEnded, EndReason: resources.EndAbandoned})
	code, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build", "--ask"}, "touch", marker)
	assert.Equal(t, ExitOK, code, stderr)
	_, err := os.Stat(marker)
	assert.NoError(t, err)
	assert.Contains(t, stderr, "ended_abandoned")
}

// run sends the command (argv joined, cut at 200 bytes) and the working
// directory; --command overrides the text.
func TestRun_SendsCommandAndCwd(t *testing.T) {
	d := &fakeLeaseDaemon{}
	code, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build"}, "echo", "hello", "world")
	require.Equal(t, ExitOK, code, stderr)
	posts, _, _ := d.snapshot()
	require.Len(t, posts, 1)
	assert.Equal(t, "echo hello world", posts[0].Command)
	wd, _ := os.Getwd()
	assert.Equal(t, wd, posts[0].Cwd)

	d = &fakeLeaseDaemon{}
	long := strings.Repeat("字", 100) // 300 bytes
	code, _, stderr = driveRunCmd(t, d, nil, []string{"--kind", "build", "--command", long}, "true")
	require.Equal(t, ExitOK, code, stderr)
	posts, _, _ = d.snapshot()
	require.Len(t, posts, 1)
	assert.LessOrEqual(t, len(posts[0].Command), 200)
	assert.True(t, strings.HasPrefix(long, posts[0].Command))

	d = &fakeLeaseDaemon{}
	fixedHolder(t)
	code, _, _ = driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build", "--command", "go test", "--cwd", "/x/y")
	assert.Equal(t, ExitOK, code)
	posts, _, _ = d.snapshot()
	require.Len(t, posts, 1)
	assert.Equal(t, "go test", posts[0].Command)
	assert.Equal(t, "/x/y", posts[0].Cwd)
}

// Inside a script (the parent is not the shell the mod wrapped) a queued answer
// adds the whole-script line. (M: invert the comparison.)
func TestRun_ScriptStepAddsTheWholeScriptLine(t *testing.T) {
	d := queuedDaemon(2, 0)
	// fixedHolder makes the parent 4242.
	_, _, inScript := driveRunEnv(t, d, nil, map[string]string{"PDX_LEASE_SHELL": "9999"}, []string{"--kind", "build", "--queue"}, "true")
	assert.Contains(t, inScript, "這支腳本做到一半被排隊，前面的步驟已經執行")
	assert.Contains(t, inScript, "pdx lease run --kind build -- <整支腳本>")

	_, _, direct := driveRunEnv(t, queuedDaemon(2, 0), nil, map[string]string{"PDX_LEASE_SHELL": "4242"}, []string{"--kind", "build", "--queue"}, "true")
	assert.NotContains(t, direct, "這支腳本")

	_, _, unknown := driveRunEnv(t, queuedDaemon(2, 0), nil, nil, []string{"--kind", "build", "--queue"}, "true")
	assert.NotContains(t, unknown, "這支腳本")

	_, _, weight := driveRunEnv(t, queuedDaemon(2, 0), nil, map[string]string{"PDX_LEASE_SHELL": "9999"}, []string{"--weight", "30", "--queue"}, "true")
	assert.Contains(t, weight, "pdx lease run --weight 30 -- <整支腳本>")
}

// The final notice comes with the release after the child, and is printed
// after the child's output. (M: release before the child runs.)
func TestRun_PrintsTheFinalNoticeAfterTheOutput(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	d := &fakeLeaseDaemon{delResp: &resources.LeaseResponse{ID: leaseRow, State: resources.StateEnded, Notice: "有其他 session 在排隊，下一個指令會是這次的最後一個；之後還要跑請重新排隊。"}}
	var ranBeforeRelease bool
	d.onDelete = func() { _, err := os.Stat(marker); ranBeforeRelease = err == nil }
	code, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build"}, "touch", marker)
	assert.Equal(t, ExitOK, code)
	assert.True(t, ranBeforeRelease, "the release (and so the notice) comes after the child exited")
	assert.Contains(t, stderr, "pdx lease: 有其他 session 在排隊，下一個指令會是這次的最後一個")
	_, _, deletes := d.snapshot()
	assert.Equal(t, []string{"/api/resources/leases/" + leaseRow + "?notice=1"}, deletes)
}

// Interrupted between the grant and the child's start: the release does not
// ask for a notice. (M: always send notice=1.)
func TestRun_InterruptedBeforeTheChildSendsNoNoticeFlag(t *testing.T) {
	fixedHolder(t)
	ctx, cancel := context.WithCancel(context.Background())
	d := &fakeLeaseDaemon{post: func(resources.LeaseRequest) (int, any) {
		return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateHeld, Granted: true, WaitedMS: 5000}
	}}
	srv := httptest.NewServer(d)
	defer srv.Close()
	cfg := writeTestConfig(t, srv.URL, "admin-tok")
	var stdout bytes.Buffer
	stderr := &cancelOnWrite{cancel: cancel}
	code := runLeaseCmd(ctx, []string{"run", "--config", cfg, "--kind", "build", "--", "true"}, fakeGetenv(nil), &stdout, stderr, leadNoKeepAlive())
	assert.Equal(t, ExitCancelled, code)
	_, _, deletes := d.snapshot()
	assert.Equal(t, []string{"/api/resources/leases/" + leaseRow}, deletes)
}

// `pdx lease release --notice` adds notice=1, for an id and for a client id.
func TestRelease_NoticeFlagAddsTheQuery(t *testing.T) {
	d := &fakeLeaseDaemon{}
	code, _, _ := driveLease(t, context.Background(), d, nil, "release", "--notice", leaseRow)
	assert.Equal(t, ExitOK, code)
	d2 := &fakeLeaseDaemon{}
	code, _, _ = driveLease(t, context.Background(), d2, nil, "release", "--notice", "--client-id", leaseCID)
	assert.Equal(t, ExitOK, code)
	d3 := &fakeLeaseDaemon{}
	code, _, _ = driveLease(t, context.Background(), d3, nil, "release", leaseRow)
	assert.Equal(t, ExitOK, code)
	_, _, a := d.snapshot()
	_, _, b := d2.snapshot()
	_, _, c := d3.snapshot()
	assert.Equal(t, []string{"/api/resources/leases/" + leaseRow + "?notice=1"}, a)
	assert.Equal(t, []string{"/api/resources/leases?client_id=" + leaseCID + "&notice=1"}, b)
	assert.Equal(t, []string{"/api/resources/leases/" + leaseRow}, c)
}
