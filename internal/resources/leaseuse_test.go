package resources

import (
	"math"
	"testing"
	"time"
)

// startsOf builds the startMS lookup from a map; a pid not in it has no start.
func startsOf(m map[int]int64) func(int) (int64, bool) {
	return func(pid int) (int64, bool) {
		v, ok := m[pid]
		return v, ok
	}
}

const gib = uint64(1) << 30

func TestComputeLeaseUse_ProcessScopeTree(t *testing.T) {
	procs := []Proc{
		{PID: 1, PPID: 0, Pcpu: 500, RSSBytes: 9 * gib}, // not under the holder
		{PID: 100, PPID: 1, Pcpu: 10, RSSBytes: gib},
		{PID: 101, PPID: 100, Pcpu: 20, RSSBytes: gib},
		{PID: 102, PPID: 101, Pcpu: 30, RSSBytes: gib},
		{PID: 200, PPID: 1, Pcpu: 40, RSSBytes: gib}, // a sibling
	}
	got := ComputeLeaseUse(procs, []LeaseTree{{ID: "a", Scope: ScopeProcess, Root: 100}}, nil, 10, 16*gib)
	u := got["a"]
	if u.Procs != 3 || u.Empty || !near(u.Pcpu, 60) || u.RSSBytes != 3*gib {
		t.Fatalf("usage = %+v", u)
	}
	// cpu 60/10 = 6 %, mem 3/16 = 18.75 %: the larger one.
	if !near(u.Use, 18.75) {
		t.Fatalf("Use = %v, want 18.75", u.Use)
	}
}

func TestComputeLeaseUse_UseIsTheLargerOfCPUAndMem(t *testing.T) {
	procs := []Proc{{PID: 100, PPID: 1, Pcpu: 400, RSSBytes: gib}}
	u := ComputeLeaseUse(procs, []LeaseTree{{ID: "a", Scope: ScopeProcess, Root: 100}}, nil, 10, 16*gib)["a"]
	if !near(u.Use, 40) { // 400/10 beats 6.25
		t.Fatalf("Use = %v, want 40", u.Use)
	}
	if u := ComputeLeaseUse(procs, []LeaseTree{{ID: "a", Scope: ScopeProcess, Root: 100}}, nil, 0, 0)["a"]; u.Use != 0 || u.Procs != 1 {
		t.Fatalf("no ncpu/mem: %+v, want Use 0 and the process still counted", u)
	}
}

// Codex attack (high): the larger of two components is not additive. A lease
// that burns CPU and one that fills memory each report their own maximum, and
// a host figure that is itself a maximum cannot be reduced by the sum of
// those. The components do add up, so a caller can subtract them one by one.
func TestComputeLeaseUse_ComponentsAddUpWhereUseDoesNot(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 1, Pcpu: 400},         // 40 % of 10 cores, no memory
		{PID: 200, PPID: 1, RSSBytes: 6 * gib}, // 37.5 % of 16 GiB, no cpu
	}
	got := ComputeLeaseUse(procs, []LeaseTree{
		{ID: "cpu", Scope: ScopeProcess, Root: 100},
		{ID: "mem", Scope: ScopeProcess, Root: 200},
	}, nil, 10, 16*gib)
	if !near(got["cpu"].CPU, 40) || !near(got["cpu"].Mem, 0) || !near(got["mem"].CPU, 0) || !near(got["mem"].Mem, 37.5) {
		t.Fatalf("components = %+v", got)
	}
	if !near(got["cpu"].Use, 40) || !near(got["mem"].Use, 37.5) {
		t.Fatalf("Use = %v / %v", got["cpu"].Use, got["mem"].Use)
	}
	// What the two leases really take of the host: 40 % cpu, 37.5 % memory.
	// The sum of Use (77.5) is larger than the host's own maximum (40).
	if sumUse := got["cpu"].Use + got["mem"].Use; sumUse <= math.Max(got["cpu"].CPU+got["mem"].CPU, got["cpu"].Mem+got["mem"].Mem) {
		t.Fatalf("sum of Use %v should exceed the larger summed component: the test no longer shows the problem", sumUse)
	}
}

// Codex attack (high): a root pid that is in the table with another start time
// is a reused pid. Its new owner and that owner's children are not the
// lease's, so nothing is measured for it. An unknown start on either side
// measures as before.
func TestComputeLeaseUse_ReusedRootIsUnverifiedNotMeasured(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 1, Pcpu: 900, RSSBytes: 8 * gib}, // an unrelated hog that got the pid
		{PID: 101, PPID: 100, Pcpu: 100, RSSBytes: gib},
	}
	holder := int64(5_000_000)
	lease := LeaseTree{ID: "a", Scope: ScopeProcess, Root: 100, RootStartMS: holder}

	reused := ComputeLeaseUse(procs, []LeaseTree{lease}, startsOf(map[int]int64{100: holder + 90_000}), 10, 16*gib)["a"]
	if !reused.Unverified || reused.Empty || reused.Procs != 0 || reused.Use != 0 {
		t.Fatalf("reused root = %+v, want unverified, not empty, nothing measured", reused)
	}
	same := ComputeLeaseUse(procs, []LeaseTree{lease}, startsOf(map[int]int64{100: holder + 400}), 10, 16*gib)["a"]
	if same.Unverified || same.Procs != 2 {
		t.Fatalf("same second = %+v, want measured (start times compare to the second)", same)
	}
	unknownTable := ComputeLeaseUse(procs, []LeaseTree{lease}, startsOf(nil), 10, 16*gib)["a"]
	if unknownTable.Unverified || unknownTable.Procs != 2 {
		t.Fatalf("no start in the table = %+v, want measured", unknownTable)
	}
	lease.RootStartMS = 0
	unknownHolder := ComputeLeaseUse(procs, []LeaseTree{lease}, startsOf(map[int]int64{100: holder}), 10, 16*gib)["a"]
	if unknownHolder.Unverified || unknownHolder.Procs != 2 {
		t.Fatalf("no expected start = %+v, want measured", unknownHolder)
	}
}

// Baselines only grow: the later lease's baseline holds everything that ran at
// its grant. So a process that started between two grants is the earlier
// lease's alone (the later one did not exist yet), and one that started after
// both is split. Splitting a "combined new tree" in two would hand the later
// lease a process that was already running when it was granted.
func TestComputeLeaseUse_LaterLeaseDoesNotInheritWhatStartedBeforeItsGrant(t *testing.T) {
	procs := []Proc{
		{PID: 50, PPID: 1},
		{PID: 60, PPID: 50, Pcpu: 100}, // under the session before either grant
		{PID: 70, PPID: 50, Pcpu: 200}, // started between the grants: only A can own it
		{PID: 80, PPID: 50, Pcpu: 400}, // started after both grants
	}
	starts := map[int]int64{60: 6000, 70: 7000}
	a := LeaseTree{ID: "a", Scope: ScopeSessionNew, Root: 50, Baseline: []BaselineEntry{{PID: 60, StartMS: 6000}}}
	b := LeaseTree{ID: "b", Scope: ScopeSessionNew, Root: 50, Baseline: []BaselineEntry{{PID: 60, StartMS: 6000}, {PID: 70, StartMS: 7000}}}
	got := ComputeLeaseUse(procs, []LeaseTree{a, b}, startsOf(starts), 10, 16*gib)
	// pcpu: A = 200 + 400/2, B = 400/2.
	if !near(got["a"].Pcpu, 400) || !near(got["b"].Pcpu, 200) {
		t.Fatalf("a = %v, b = %v pcpu, want 400 and 200", got["a"].Pcpu, got["b"].Pcpu)
	}
}

func TestComputeLeaseUse_ProcessScopeRootGoneIsEmpty(t *testing.T) {
	procs := []Proc{{PID: 100, PPID: 1, Pcpu: 10, RSSBytes: gib}}
	u := ComputeLeaseUse(procs, []LeaseTree{{ID: "a", Scope: ScopeProcess, Root: 999}}, nil, 10, 16*gib)["a"]
	if !u.Empty || u.Procs != 0 || u.Use != 0 {
		t.Fatalf("usage = %+v, want empty", u)
	}
	if got := ComputeLeaseUse(nil, []LeaseTree{{ID: "a", Scope: ScopeProcess, Root: 100}}, nil, 10, 16*gib); !got["a"].Empty {
		t.Fatalf("no process table: %+v, want empty", got["a"])
	}
}

// ccTable is a session: the agent (pid 50) with an MCP server (60) that was
// there before the command, and a shell (70) running vitest (71, 72).
func ccTable() []Proc {
	return []Proc{
		{PID: 50, PPID: 1, Pcpu: 5, RSSBytes: gib},
		{PID: 60, PPID: 50, Pcpu: 1, RSSBytes: gib},
		{PID: 61, PPID: 60, Pcpu: 2, RSSBytes: gib}, // the MCP server's own child
		{PID: 70, PPID: 50, Pcpu: 10, RSSBytes: gib},
		{PID: 71, PPID: 70, Pcpu: 100, RSSBytes: gib},
		{PID: 72, PPID: 71, Pcpu: 100, RSSBytes: gib},
	}
}

func TestComputeLeaseUse_SessionNewExcludesBaselineAndItsSubtree(t *testing.T) {
	starts := map[int]int64{60: 6000, 61: 6100, 70: 7000, 71: 7100, 72: 7200}
	// Only pid 60 is in the baseline; its child 61 is not listed, and is still excluded.
	l := LeaseTree{ID: "a", Scope: ScopeSessionNew, Root: 50, Baseline: []BaselineEntry{{PID: 60, StartMS: 6000}}}
	u := ComputeLeaseUse(ccTable(), []LeaseTree{l}, startsOf(starts), 10, 16*gib)["a"]
	if u.Procs != 3 || u.Empty || !near(u.Pcpu, 210) || u.RSSBytes != 3*gib {
		t.Fatalf("usage = %+v, want the shell 70 and 71, 72 (the agent itself and the MCP subtree are not charged)", u)
	}
}

func TestComputeLeaseUse_SessionNewNeverChargesTheRoot(t *testing.T) {
	l := LeaseTree{ID: "a", Scope: ScopeSessionNew, Root: 50}
	procs := []Proc{{PID: 50, PPID: 1, Pcpu: 90, RSSBytes: 5 * gib}}
	u := ComputeLeaseUse(procs, []LeaseTree{l}, startsOf(nil), 10, 16*gib)["a"]
	if !u.Empty || u.Procs != 0 || u.Use != 0 {
		t.Fatalf("usage = %+v, want empty: the agent process is not the command", u)
	}
}

func TestComputeLeaseUse_BaselinePidReusedIsCharged(t *testing.T) {
	// pid 60 was an MCP server with start 6000; now pid 60 is something else.
	l := LeaseTree{ID: "a", Scope: ScopeSessionNew, Root: 50, Baseline: []BaselineEntry{{PID: 60, StartMS: 6000}}}
	u := ComputeLeaseUse(ccTable(), []LeaseTree{l}, startsOf(map[int]int64{60: 9999, 61: 1, 70: 1, 71: 1, 72: 1}), 10, 16*gib)["a"]
	if u.Procs != 5 {
		t.Fatalf("Procs = %d, want 5: a reused pid is a new process and is charged", u.Procs)
	}
}

func TestComputeLeaseUse_BaselineStartUnknownIsCharged(t *testing.T) {
	l := LeaseTree{ID: "a", Scope: ScopeSessionNew, Root: 50, Baseline: []BaselineEntry{{PID: 60, StartMS: 6000}}}
	u := ComputeLeaseUse(ccTable(), []LeaseTree{l}, startsOf(map[int]int64{}), 10, 16*gib)["a"]
	if u.Procs != 5 {
		t.Fatalf("Procs = %d, want 5: a start that cannot be read does not match", u.Procs)
	}
	// And with no lookup at all.
	if u := ComputeLeaseUse(ccTable(), []LeaseTree{l}, nil, 10, 16*gib)["a"]; u.Procs != 5 {
		t.Fatalf("nil startMS: Procs = %d, want 5", u.Procs)
	}
}

func TestComputeLeaseUse_TwoLeasesOneNewTreeSplitEvenly(t *testing.T) {
	starts := map[int]int64{60: 6000}
	base := []BaselineEntry{{PID: 60, StartMS: 6000}}
	leases := []LeaseTree{
		{ID: "a", Scope: ScopeSessionNew, Root: 50, Baseline: base},
		{ID: "b", Scope: ScopeSessionNew, Root: 50, Baseline: base},
	}
	got := ComputeLeaseUse(ccTable(), leases, startsOf(starts), 10, 16*gib)
	whole := ComputeLeaseUse(ccTable(), leases[:1], startsOf(starts), 10, 16*gib)["a"]
	if whole.Use <= 0 || whole.Procs != 3 {
		t.Fatalf("the whole new tree = %+v, want 3 processes with a use", whole)
	}
	if !near(got["a"].Use, whole.Use/2) || !near(got["b"].Use, whole.Use/2) {
		t.Fatalf("a = %v, b = %v, want %v each", got["a"].Use, got["b"].Use, whole.Use/2)
	}
	if sum := got["a"].Use + got["b"].Use; !near(sum, whole.Use) {
		t.Fatalf("sum = %v, want the tree's %v", sum, whole.Use)
	}
	if got["a"].Pcpu+got["b"].Pcpu > whole.Pcpu+1e-9 || got["a"].RSSBytes+got["b"].RSSBytes > whole.RSSBytes {
		t.Fatalf("pcpu/rss split exceeds the tree: %+v %+v vs %+v", got["a"], got["b"], whole)
	}
	if got["a"].Empty || got["b"].Empty {
		t.Fatal("a shared non-empty tree is not empty for either lease")
	}
}

func TestComputeLeaseUse_OverlapOfDifferentScopesIsSplit(t *testing.T) {
	// A process-scope lease on the shell 70 and a session-new lease on the
	// agent both cover 70, 71 and 72.
	leases := []LeaseTree{
		{ID: "p", Scope: ScopeProcess, Root: 70},
		{ID: "s", Scope: ScopeSessionNew, Root: 50, Baseline: []BaselineEntry{{PID: 60, StartMS: 6000}}},
	}
	got := ComputeLeaseUse(ccTable(), leases, startsOf(map[int]int64{60: 6000}), 10, 16*gib)
	if !near(got["p"].Pcpu, 105) || !near(got["s"].Pcpu, 105) {
		t.Fatalf("p = %v, s = %v, want 105 each (210 split)", got["p"].Pcpu, got["s"].Pcpu)
	}
}

func TestComputeLeaseUse_SessionNewEmptyWhenNoNewDescendant(t *testing.T) {
	l := LeaseTree{ID: "a", Scope: ScopeSessionNew, Root: 50, Baseline: []BaselineEntry{{PID: 60, StartMS: 6000}, {PID: 70, StartMS: 7000}}}
	u := ComputeLeaseUse(ccTable(), []LeaseTree{l}, startsOf(map[int]int64{60: 6000, 70: 7000}), 10, 16*gib)["a"]
	if !u.Empty || u.Procs != 0 {
		t.Fatalf("usage = %+v, want empty: everything under the agent was there at grant", u)
	}
	// The agent itself gone: also empty.
	l = LeaseTree{ID: "a", Scope: ScopeSessionNew, Root: 4242}
	if u := ComputeLeaseUse(ccTable(), []LeaseTree{l}, nil, 10, 16*gib)["a"]; !u.Empty {
		t.Fatalf("root absent: %+v, want empty", u)
	}
}

func TestComputeLeaseUse_DuplicatePidFirstRowWins(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 1, Pcpu: 10, RSSBytes: gib},
		{PID: 100, PPID: 999, Pcpu: 90, RSSBytes: 8 * gib}, // a second row for the same pid is ignored
		{PID: 101, PPID: 100, Pcpu: 20, RSSBytes: gib},
	}
	u := ComputeLeaseUse(procs, []LeaseTree{{ID: "a", Scope: ScopeProcess, Root: 100}}, nil, 10, 16*gib)["a"]
	if u.Procs != 2 || !near(u.Pcpu, 30) || u.RSSBytes != 2*gib {
		t.Fatalf("usage = %+v", u)
	}
}

func TestComputeLeaseUse_ParentLoopTerminates(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 102, Pcpu: 1, RSSBytes: gib},
		{PID: 101, PPID: 100, Pcpu: 1, RSSBytes: gib},
		{PID: 102, PPID: 101, Pcpu: 1, RSSBytes: gib},
		{PID: 103, PPID: 103, Pcpu: 1, RSSBytes: gib}, // its own parent
	}
	done := make(chan map[string]LeaseUsage, 1)
	go func() {
		done <- ComputeLeaseUse(procs, []LeaseTree{
			{ID: "a", Scope: ScopeProcess, Root: 100},
			{ID: "b", Scope: ScopeSessionNew, Root: 100},
			{ID: "c", Scope: ScopeProcess, Root: 103},
		}, nil, 10, 16*gib)
	}()
	select {
	case got := <-done:
		if got["a"].Procs != 3 || got["c"].Procs != 1 {
			t.Fatalf("a = %+v, c = %+v", got["a"], got["c"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ComputeLeaseUse did not return on a parent loop")
	}
}

func TestComputeLeaseUse_NoLeasesIsEmptyMap(t *testing.T) {
	if got := ComputeLeaseUse(ccTable(), nil, nil, 10, 16*gib); got == nil || len(got) != 0 {
		t.Fatalf("got %v, want an empty non-nil map", got)
	}
}
