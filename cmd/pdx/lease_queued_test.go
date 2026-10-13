package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

var queuedNow = time.Date(2026, 10, 13, 22, 0, 0, 0, time.UTC)

func queuedDaemon(pos int, waitedMS int64, parkedInMin int) *fakeLeaseDaemon {
	return &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting, Position: pos}
		},
		pollList: []resources.LeaseResponse{{ID: leaseRow, State: resources.StateQueued, Position: pos, WaitedMS: waitedMS,
			ParkedUntil: queuedNow.Add(time.Duration(parkedInMin) * time.Minute).UnixMilli()}},
	}
}

func withQueuedNow(t *testing.T) {
	old := leaseNow
	leaseNow = func() time.Time { return queuedNow }
	t.Cleanup(func() { leaseNow = old })
}

// The request says it reads "queued", and every poll names the client.
func TestAcquire_AsksParkableAndPollsWithItsClientID(t *testing.T) {
	fixedHolder(t)
	d := &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting, Position: 1}
		},
		pollList: []resources.LeaseResponse{
			{ID: leaseRow, State: resources.StateWaiting, Position: 1},
			{ID: leaseRow, State: resources.StateHeld, Granted: true},
		},
	}
	code, _, _ := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build", "--client-id", leaseCID)
	require.Equal(t, ExitOK, code)
	posts, _, _ := d.snapshot()
	require.Len(t, posts, 1)
	assert.True(t, posts[0].Parkable)
	d.mu.Lock()
	defer d.mu.Unlock()
	require.Len(t, d.pollQueries, 2)
	for _, q := range d.pollQueries {
		assert.Contains(t, q, "client_id="+leaseCID)
		assert.Contains(t, q, "wait=25")
	}
}

// A queued answer ends the call at once: the line says state, place and when the
// park ends, nothing is sent to cancel (that would lose the place), and the exit
// code is 15. (M: route it to fail-open and the command would run.)
func TestAcquire_QueuedExits15WithoutCancelling(t *testing.T) {
	fixedHolder(t)
	d := queuedDaemon(3, 125_000, 14)
	code, stdout, stderr := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build", "--client-id", leaseCID)
	assert.Equal(t, ExitQueued, code)
	assert.Equal(t, 15, ExitQueued)
	assert.Empty(t, stderr)
	l := parseAcquireLine(t, stdout)
	assert.False(t, l.Granted)
	assert.Equal(t, "queued", l.State)
	assert.Equal(t, 3, l.Position)
	assert.Equal(t, queuedNow.Add(14*time.Minute).UnixMilli(), l.ParkedUntil)
	assert.Equal(t, int64(125_000), l.WaitedMS)
	assert.Empty(t, l.FailOpen)
	_, _, deletes := d.snapshot()
	assert.Empty(t, deletes, "no DELETE: it would cancel the parked row")
}

// Replaced (a newer call took the row over) is the same: exit 15, no DELETE,
// not granted. (M: let it fall into the generic path and the DELETE goes out.)
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
	l := parseAcquireLine(t, stdout)
	assert.False(t, l.Granted)
	assert.Equal(t, "replaced", l.State)
	_, _, deletes := d.snapshot()
	assert.Empty(t, deletes)
}

// A POST that is a replay of a parked row answers queued straight away.
func TestAcquire_QueuedOnThePostAnswer(t *testing.T) {
	fixedHolder(t)
	d := &fakeLeaseDaemon{post: func(resources.LeaseRequest) (int, any) {
		return 200, resources.LeaseResponse{ID: leaseRow, State: resources.StateQueued, Position: 2, ParkedUntil: queuedNow.UnixMilli()}
	}}
	code, stdout, _ := driveLease(t, context.Background(), d, nil, "acquire", "--kind", "build")
	assert.Equal(t, ExitQueued, code)
	assert.Equal(t, "queued", parseAcquireLine(t, stdout).State)
	_, polls, deletes := d.snapshot()
	assert.Zero(t, polls)
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

// The text for the model and for the person: place, how long it has waited, how
// long the place is kept, and not to get around it.
func TestQueuedText(t *testing.T) {
	r := resources.LeaseResponse{Position: 3, WaitedMS: 125_000, ParkedUntil: queuedNow.Add(14*time.Minute + 20*time.Second).UnixMilli()}
	got := queuedText(r, queuedNow)
	assert.Equal(t, "主機資源忙，這個指令還沒執行：排隊第 3 位，已等 2 分鐘。位置會保留 15 分鐘；先做不需要這個指令的事，之後用同一個指令重跑就會接回原位。不要改成背景執行或加 & 繞過排隊。", got)
	short := queuedText(resources.LeaseResponse{Position: 1, WaitedMS: 20_000, ParkedUntil: queuedNow.Add(10 * time.Second).UnixMilli()}, queuedNow)
	assert.Contains(t, short, "排隊第 1 位，已等不到 1 分鐘")
	assert.Contains(t, short, "位置會保留 1 分鐘")
	gone := queuedText(resources.LeaseResponse{Position: 1, WaitedMS: 0}, queuedNow)
	assert.Contains(t, gone, "位置會保留一段時間")
	rep := queuedText(resources.LeaseResponse{State: resources.StateReplaced}, queuedNow)
	assert.Contains(t, rep, "這個呼叫已被同一個 session 較新的呼叫取代")
}

// `pdx lease run` on a queued answer: the text on stderr, exit 15, the command
// does not run, nothing is released or cancelled.
func TestRun_QueuedDoesNotRunTheCommand(t *testing.T) {
	fixedHolder(t)
	withQueuedNow(t)
	marker := t.TempDir() + "/ran"
	d := queuedDaemon(2, 61_000, 5)
	code, stdout, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build"}, "touch", marker)
	assert.Equal(t, ExitQueued, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "排隊第 2 位，已等 1 分鐘")
	assert.Contains(t, stderr, "位置會保留 5 分鐘")
	assert.NotContains(t, stderr, "超量放行")
	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "the command must not run")
	_, _, deletes := d.snapshot()
	assert.Empty(t, deletes, "no release, no cancel")
}

func TestRun_ReplacedDoesNotRunEither(t *testing.T) {
	fixedHolder(t)
	marker := t.TempDir() + "/ran"
	d := &fakeLeaseDaemon{
		post: func(resources.LeaseRequest) (int, any) {
			return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateWaiting}
		},
		pollList: []resources.LeaseResponse{{ID: leaseRow, State: resources.StateReplaced}},
	}
	code, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build"}, "touch", marker)
	assert.Equal(t, ExitQueued, code)
	assert.Contains(t, stderr, "已被同一個 session 較新的呼叫取代")
	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err))
	_, _, deletes := d.snapshot()
	assert.Empty(t, deletes)
}

// A granted run is as before, without the overrun line.
func TestRun_GrantedNoLongerMentionsOverrun(t *testing.T) {
	fixedHolder(t)
	d := &fakeLeaseDaemon{post: func(resources.LeaseRequest) (int, any) {
		return 201, resources.LeaseResponse{ID: leaseRow, State: resources.StateHeld, Granted: true, WaitedMS: 3000, Overrun: true, Host: resources.LeaseHost{Measured: 70}}
	}}
	code, _, stderr := driveRunCmd(t, d, nil, []string{"--kind", "build"}, "true")
	assert.Equal(t, ExitOK, code)
	assert.False(t, strings.Contains(stderr, "超量"), stderr)
	assert.Contains(t, stderr, "等了 3 秒主機資源")
}
