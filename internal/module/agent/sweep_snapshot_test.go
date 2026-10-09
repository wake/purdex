package agent

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
)

// The older sweep tests stage the process world through the per-PID seams (isPidAliveFn / processStartTimeFn /
// readProcessInfoFn). Without a snapshot they keep working as they are: the default here makes every snapshot fail, so
// the sweep falls back to the seams. The tests below turn the snapshot on.
func init() {
	snapshotProcessesFn = func(context.Context) (procTable, error) { return nil, errors.New("snapshot disabled in tests") }
}

// fakeTable is a process table: only the pids in it are known (anything else is ErrNotInSnapshot, as for a pid that
// started after the table was read).
type fakeTable struct {
	start map[int]string
	ppid  map[int]int
	err   map[int]error // a pid whose start text the table could not give
}

func (f fakeTable) StartTime(pid int) (string, error) {
	if e, ok := f.err[pid]; ok {
		return "", e
	}
	if s, ok := f.start[pid]; ok {
		return s, nil
	}
	return "", fmt.Errorf("pid %d: %w", pid, agentpkg.ErrNotInSnapshot)
}

func (f fakeTable) PPID(pid int) (int, error) {
	if p, ok := f.ppid[pid]; ok {
		return p, nil
	}
	return 0, fmt.Errorf("pid %d: %w", pid, agentpkg.ErrNotInSnapshot)
}

// forkCounter installs the per-PID seams over a world and counts how often each is asked: every ask is a `ps` fork.
type forkCounter struct {
	startAsks, infoAsks []int
	snapshots           atomic.Int32
}

func (c *forkCounter) install(t *testing.T, alive map[int]bool, start map[int]string, ppid map[int]int, table *fakeTable) {
	t.Helper()
	installSweepCanonicalSeams(t, alive, start, ppid)
	baseStart, baseInfo := processStartTimeFn, readProcessInfoFn
	processStartTimeFn = func(pid int) (string, error) { c.startAsks = append(c.startAsks, pid); return baseStart(pid) }
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		c.infoAsks = append(c.infoAsks, pid)
		return baseInfo(pid)
	}
	orig := snapshotProcessesFn
	t.Cleanup(func() { snapshotProcessesFn = orig })
	snapshotProcessesFn = func(context.Context) (procTable, error) {
		c.snapshots.Add(1)
		if table == nil {
			return nil, errors.New("no snapshot")
		}
		return *table, nil
	}
}

func seedCC(t *testing.T, m *Module, pane string, pid int, start string) {
	t.Helper()
	if _, err := m.frames.Upsert(store.Frame{PaneID: pane, AgentType: "cc", PID: pid, PPID: 1, ProcessStartTime: start,
		Status: agentpkg.StatusIdle, StartedAt: 50, LastSeenAt: 50, Verified: true}); err != nil {
		t.Fatal(err)
	}
}

func seedCodexUnder(t *testing.T, m *Module, pane string, pid, ppid int, start string) {
	t.Helper()
	if _, err := m.frames.Upsert(store.Frame{PaneID: pane, AgentType: "codex", PID: pid, PPID: ppid, ProcessStartTime: start,
		Status: agentpkg.StatusRunning, StartedAt: 50, LastSeenAt: 50, Verified: true}); err != nil {
		t.Fatal(err)
	}
}

// One table read answers every start-time and ancestor question of the tick: the same canonicalisation as before, no
// per-PID read at all (that is the fork per frame and per ancestor of #2138).
func TestSweep_Snapshot_AnswersEveryLookupWithoutForking(t *testing.T) {
	m := newSweepTestModule(t)
	seedCC(t, m, "%5", 100, "t100")
	seedCodexUnder(t, m, "%5", 200, 100, "t200")
	var fc forkCounter
	fc.install(t, map[int]bool{100: true, 200: true},
		map[int]string{}, map[int]int{}, // the per-PID world knows nothing: any ask would fail the result
		&fakeTable{start: map[int]string{100: "t100", 200: "t200"}, ppid: map[int]int{200: 100, 100: 1}})

	if err := m.sweepOnce(); err != nil {
		t.Fatal(err)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || frames[0].AgentType != "cc" || len(frames[0].Subagents) != 1 || !frames[0].Subagents[0].IsProxy {
		t.Fatalf("frames = %+v, want the codex frame folded into cc as a proxy ref", frames)
	}
	if len(fc.startAsks) != 0 || len(fc.infoAsks) != 0 {
		t.Fatalf("per-PID reads: start %v, info %v - want none", fc.startAsks, fc.infoAsks)
	}
	if fc.snapshots.Load() != 1 {
		t.Fatalf("snapshots = %d, want 1 per tick", fc.snapshots.Load())
	}
}

// One snapshot per tick however many frames and panes there are.
func TestSweep_Snapshot_IsTakenOncePerTick(t *testing.T) {
	m := newSweepTestModule(t)
	alive, start := map[int]bool{}, map[int]string{}
	table := fakeTable{start: map[int]string{}, ppid: map[int]int{}}
	for i := 0; i < 6; i++ {
		pid := 100 + i
		seedCC(t, m, fmt.Sprintf("%%%d", 10+i), pid, fmt.Sprintf("t%d", pid))
		alive[pid], start[pid] = true, fmt.Sprintf("t%d", pid)
		table.start[pid], table.ppid[pid] = fmt.Sprintf("t%d", pid), 1
	}
	var fc forkCounter
	fc.install(t, alive, map[int]string{}, map[int]int{}, &table)
	if err := m.sweepOnce(); err != nil {
		t.Fatal(err)
	}
	if fc.snapshots.Load() != 1 || len(fc.startAsks) != 0 {
		t.Fatalf("snapshots %d, per-PID start asks %v", fc.snapshots.Load(), fc.startAsks)
	}
}

// Nothing to verify, no table to read: an idle daemon forks nothing.
func TestSweep_Snapshot_NotTakenWithoutVerifiedFrames(t *testing.T) {
	m := newSweepTestModule(t)
	var fc forkCounter
	fc.install(t, nil, nil, nil, &fakeTable{})
	if err := m.sweepOnce(); err != nil {
		t.Fatal(err)
	}
	if fc.snapshots.Load() != 0 {
		t.Fatalf("snapshots = %d, want 0", fc.snapshots.Load())
	}
}

// A pid the table did not see (it started after the table was read) is asked of the per-PID reader, and only that pid.
func TestSweep_Snapshot_UnknownPidFallsBackToTheSingleRead(t *testing.T) {
	m := newSweepTestModule(t)
	seedCC(t, m, "%5", 100, "t100")
	seedCodexUnder(t, m, "%5", 200, 100, "t200")
	var fc forkCounter
	fc.install(t, map[int]bool{100: true, 200: true},
		map[int]string{200: "t200"}, map[int]int{200: 100},
		&fakeTable{start: map[int]string{100: "t100"}, ppid: map[int]int{100: 1}}) // 200 is missing from the table
	if err := m.sweepOnce(); err != nil {
		t.Fatal(err)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || len(frames[0].Subagents) != 1 {
		t.Fatalf("frames = %+v, want the codex frame folded (pid 200 read singly)", frames)
	}
	for _, pid := range append(append([]int{}, fc.startAsks...), fc.infoAsks...) {
		if pid != 200 {
			t.Fatalf("a per-PID read of %d, want only the pid the table lacks (start %v, info %v)", pid, fc.startAsks, fc.infoAsks)
		}
	}
	if len(fc.startAsks)+len(fc.infoAsks) == 0 {
		t.Fatal("pid 200 was never asked about")
	}
}

// A table that cannot be read is no reason to stop sweeping: every question goes to the per-PID seams, as before.
func TestSweep_Snapshot_FailureFallsBackToPerPidReads(t *testing.T) {
	m := newSweepTestModule(t)
	seedCC(t, m, "%5", 100, "t100")
	seedCodexUnder(t, m, "%5", 200, 100, "t200")
	var fc forkCounter
	fc.install(t, map[int]bool{100: true, 200: true},
		map[int]string{100: "t100", 200: "t200"}, map[int]int{200: 100, 100: 1}, nil)
	if err := m.sweepOnce(); err != nil {
		t.Fatal(err)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || len(frames[0].Subagents) != 1 {
		t.Fatalf("frames = %+v, want the same canonicalisation through the seams", frames)
	}
	if len(fc.startAsks) == 0 {
		t.Fatal("expected per-PID reads when the snapshot failed")
	}
}

// The identity rule is unchanged: the table's start text decides pid reuse.
func TestSweep_Snapshot_StartTimeMismatchStillClearsAReusedPid(t *testing.T) {
	m := newSweepTestModule(t)
	seedCC(t, m, "%5", 100, "old-start")
	var fc forkCounter
	fc.install(t, map[int]bool{100: true}, nil, nil, &fakeTable{start: map[int]string{100: "new-start"}, ppid: map[int]int{100: 1}})
	if err := m.sweepOnce(); err != nil {
		t.Fatal(err)
	}
	if frames, _ := m.frames.ListByPane("%5"); len(frames) != 0 {
		t.Fatalf("frames = %+v, want the reused pid's frame cleared", frames)
	}
	if len(fc.startAsks) != 0 {
		t.Fatalf("per-PID start asks %v", fc.startAsks)
	}
}

// ... and so is the read-error rule: a start text the table could not give keeps the frame (no destructive cleanup on
// uncertainty).
func TestSweep_Snapshot_AnUnreadableStartKeepsTheFrame(t *testing.T) {
	m := newSweepTestModule(t)
	seedCC(t, m, "%5", 100, "t100")
	var fc forkCounter
	fc.install(t, map[int]bool{100: true}, nil, nil, &fakeTable{err: map[int]error{100: errors.New("unparseable lstart")}, ppid: map[int]int{100: 1}})
	if err := m.sweepOnce(); err != nil {
		t.Fatal(err)
	}
	if frames, _ := m.frames.ListByPane("%5"); len(frames) != 1 {
		t.Fatalf("frames = %+v, want the frame kept", frames)
	}
}

// The prune pass answers the proxy source's identity from the table too.
func TestSweep_Snapshot_PruneUsesTheTable(t *testing.T) {
	m := newSweepTestModule(t)
	if _, err := m.frames.Upsert(store.Frame{PaneID: "%5", AgentType: "cc", PID: 100, PPID: 1, ProcessStartTime: "t100",
		Status: agentpkg.StatusIdle, StartedAt: 50, LastSeenAt: 50, Verified: true,
		Subagents: []agentpkg.SubagentRef{{ID: "proxy:codex:200:t200", Type: "codex", IsProxy: true, SourcePID: 200, SourceStartTime: "t200"}}}); err != nil {
		t.Fatal(err)
	}
	var fc forkCounter
	// pid 200 is alive but is now a different process (start text differs): the proxy ref is stale
	fc.install(t, map[int]bool{100: true, 200: true}, nil, nil,
		&fakeTable{start: map[int]string{100: "t100", 200: "other"}, ppid: map[int]int{100: 1, 200: 1}})
	if err := m.sweepOnce(); err != nil {
		t.Fatal(err)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 1 || len(frames[0].Subagents) != 0 {
		t.Fatalf("frames = %+v, want the stale proxy ref pruned", frames)
	}
	if len(fc.startAsks) != 0 {
		t.Fatalf("per-PID start asks %v", fc.startAsks)
	}
}

// The owned-state check of a fold candidate (a live proxy ref of its own) is answered from the table as well.
func TestSweep_Snapshot_OwnedStateCheckUsesTheTable(t *testing.T) {
	m := newSweepTestModule(t)
	seedCC(t, m, "%5", 100, "t100")
	if _, err := m.frames.Upsert(store.Frame{PaneID: "%5", AgentType: "codex", PID: 200, PPID: 100, ProcessStartTime: "t200",
		Status: agentpkg.StatusRunning, StartedAt: 50, LastSeenAt: 50, Verified: true,
		Subagents: []agentpkg.SubagentRef{{ID: "proxy:gemini:300:t300", Type: "gemini", IsProxy: true, SourcePID: 300, SourceStartTime: "t300"}}}); err != nil {
		t.Fatal(err)
	}
	var fc forkCounter
	fc.install(t, map[int]bool{100: true, 200: true, 300: true}, nil, nil,
		&fakeTable{start: map[int]string{100: "t100", 200: "t200", 300: "t300"}, ppid: map[int]int{100: 1, 200: 100, 300: 200}})
	if err := m.sweepOnce(); err != nil {
		t.Fatal(err)
	}
	frames, _ := m.frames.ListByPane("%5")
	if len(frames) != 2 { // the candidate owns live state: attached as a proxy ref, its row is kept
		t.Fatalf("frames = %d, want both kept (the candidate owns a live proxy ref)", len(frames))
	}
	if len(fc.startAsks) != 0 || len(fc.infoAsks) != 0 {
		t.Fatalf("per-PID reads: start %v, info %v", fc.startAsks, fc.infoAsks)
	}
}

// The deadline for one table read is bounded: a tick must never hang on it.
func TestSweep_Snapshot_ReadHasADeadline(t *testing.T) {
	m := newSweepTestModule(t)
	seedCC(t, m, "%5", 100, "t100")
	var fc forkCounter
	fc.install(t, map[int]bool{100: true}, map[int]string{100: "t100"}, map[int]int{100: 1}, nil)
	var deadline time.Time
	snapshotProcessesFn = func(ctx context.Context) (procTable, error) {
		deadline, _ = ctx.Deadline()
		return nil, errors.New("slow")
	}
	if err := m.sweepOnce(); err != nil {
		t.Fatal(err)
	}
	if deadline.IsZero() {
		t.Fatal("the snapshot context has no deadline")
	}
}
