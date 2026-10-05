package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// ---------------------------------------------------------------------------
// Fixtures for the owner pass
// ---------------------------------------------------------------------------

// countSnapshots counts the calls to whatever takeProcSnapshotFn is installed
// when it runs. Install it after the process fixtures (withProcessTree and
// friends), so the counted function is the fixture's view and not the real
// process table.
func countSnapshots(t *testing.T) *int {
	t.Helper()
	n := new(int)
	orig := takeProcSnapshotFn
	takeProcSnapshotFn = func() (agentpkg.ProcessView, error) {
		*n++
		return orig()
	}
	t.Cleanup(func() { takeProcSnapshotFn = orig })
	return n
}

// countListings counts the pane listings the pass makes, through the fake's
// read hook: ListAllPanes is the only tmux read a pass makes, and the hook
// sees every call, the ones that go on to fail included.
func countListings(fake *tmux.FakeExecutor) *int {
	n := new(int)
	fake.SetReadHook(func(_ context.Context, op tmux.ReadOp, _ string) error {
		if op == tmux.ReadListAllPanes {
			*n++
		}
		return nil
	})
	return n
}

// runPass is the inventory's use of a pass: Resolve every code, then Confirm
// once.
func runPass(ctx context.Context, m *Module, src ProcessSource, codes ...string) map[string]OwnerResult {
	pass := m.NewOwnerPass(src)
	for _, code := range codes {
		pass.Resolve(ctx, code)
	}
	return pass.Confirm(ctx)
}

// rootSession is one session seeded by seedRootSessions: a pane of its own
// holding one root agent frame.
type rootSession struct {
	code  string
	frame store.Frame
}

// seedRootSessions gives tmux sessions $0..$n-1 one pane each, %10+i, whose
// process 2000+i is the parent of a live cc 1000+i with its own frame and
// session id, so every session has exactly one root and each root is told
// apart by its frame id. The process fixtures are installed here, so a counter
// installed afterwards wraps them.
func seedRootSessions(t *testing.T, m *Module, fake *tmux.FakeExecutor, n int) []rootSession {
	t.Helper()
	tree := map[int]int{}
	live := map[int]string{}
	sessions := make([]rootSession, 0, n)
	for i := 0; i < n; i++ {
		tmuxID := fmt.Sprintf("$%d", i)
		paneID := fmt.Sprintf("%%%d", 10+i)
		agentPID, panePID := 1000+i, 2000+i
		attachPane(fake, paneID, tmuxID, strconv.Itoa(panePID))
		frame := seedIdentityFrame(t, m, paneID, "cc", agentPID, fmt.Sprintf("t%d", agentPID),
			int64(10+i), fmt.Sprintf("sess-%d", i), "/w")
		tree[agentPID] = panePID
		tree[panePID] = 1
		live[agentPID] = fmt.Sprintf("t%d", agentPID)
		sessions = append(sessions, rootSession{code: codeOf(t, tmuxID), frame: frame})
	}
	withProcessTree(t, tree)
	withLivePids(t, live)
	return sessions
}

// armPerPIDSeams makes every per-PID process seam fail the test if it is
// called, so a pass that answers proves it read through its own view.
func armPerPIDSeams(t *testing.T) {
	t.Helper()
	origRead, origStart, origAlive := readProcessInfoFn, processStartTimeFn, isPidAliveFn
	readProcessInfoFn = func(pid int) (agentpkg.ProcessInfo, error) {
		t.Fatalf("readProcessInfoFn(%d) called: the walk must read only the pass's view", pid)
		return agentpkg.ProcessInfo{}, nil
	}
	processStartTimeFn = func(pid int) (string, error) {
		t.Fatalf("processStartTimeFn(%d) called: the walk must read only the pass's view", pid)
		return "", nil
	}
	isPidAliveFn = func(pid int) bool {
		t.Fatalf("isPidAliveFn(%d) called: the walk must read only the pass's view", pid)
		return false
	}
	t.Cleanup(func() {
		readProcessInfoFn, processStartTimeFn, isPidAliveFn = origRead, origStart, origAlive
	})
}

// tableView is a ProcessView over a fixed table, standing in for a caller's
// own snapshot: it answers from its maps alone, never from the package seams,
// and records every PID it is asked the parent of.
type tableView struct {
	ppid  map[int]int    // a PID with no entry reports PPID 1
	start map[int]string // the live PIDs and their start time
	asked []int
}

func (v *tableView) Alive(pid int) bool {
	_, ok := v.start[pid]
	return ok
}

func (v *tableView) StartTime(pid int) (string, error) {
	start, ok := v.start[pid]
	if !ok {
		return "", fmt.Errorf("pid %d is not alive", pid)
	}
	return start, nil
}

func (v *tableView) PPID(pid int) (int, error) {
	v.asked = append(v.asked, pid)
	if ppid, ok := v.ppid[pid]; ok {
		return ppid, nil
	}
	return 1, nil
}

func (v *tableView) Read(pid int) (agentpkg.ProcessInfo, error) {
	ppid, err := v.PPID(pid)
	if err != nil {
		return agentpkg.ProcessInfo{}, err
	}
	return agentpkg.ProcessInfo{PID: pid, PPID: ppid}, nil
}

// ---------------------------------------------------------------------------
// 1. One process view and two listings per pass, whatever the session count
// ---------------------------------------------------------------------------

// TestOwnerPass_OneSnapshotTwoListings_PerPass is the reason the pass exists
// (spec R2): the inventory asks about every session, and before the pass each
// session paid its own process reads and its own tmux round trips. Asserted at
// two sizes, so a cost that grows with the session count cannot pass for a
// constant one.
func TestOwnerPass_OneSnapshotTwoListings_PerPass(t *testing.T) {
	for _, n := range []int{3, 6} {
		t.Run(fmt.Sprintf("%d sessions", n), func(t *testing.T) {
			m, fake, _ := newProvenanceQueryModule(t)
			sessions := seedRootSessions(t, m, fake, n)
			snapshots := countSnapshots(t)
			listings := countListings(fake)

			codes := make([]string, 0, n)
			for _, s := range sessions {
				codes = append(codes, s.code)
			}
			results := runPass(context.Background(), m, nil, codes...)

			for i, s := range sessions {
				got := results[s.code]
				if got.Err != nil || !got.Found || got.Owner.FrameID != s.frame.FrameID {
					t.Fatalf("session %d: result = %+v, want its own root %s", i, got, s.frame.FrameID)
				}
			}
			if *snapshots != 1 {
				t.Fatalf("process snapshots = %d over %d sessions, want 1 per pass", *snapshots, n)
			}
			if *listings != 2 {
				t.Fatalf("pane listings = %d over %d sessions, want 2 per pass", *listings, n)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. A pass with nothing to walk costs nothing
// ---------------------------------------------------------------------------

// TestOwnerPass_NoFrames_NoListingNoSnapshot — with no frame anywhere there is
// no agent to report, and no pane listing or process table could change that.
// The first use stops at the frames store.
func TestOwnerPass_NoFrames_NoListingNoSnapshot(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	attachPane(fake, "%1", "$0", "200")
	attachPane(fake, "%2", "$1", "210")
	snapshots := countSnapshots(t)
	listings := countListings(fake)

	codeA, codeB := codeOf(t, "$0"), codeOf(t, "$1")
	results := runPass(context.Background(), m, nil, codeA, codeB)

	for _, code := range []string{codeA, codeB} {
		if got, ok := results[code]; !ok || got.Err != nil || got.Found {
			t.Fatalf("result for %s = %+v (present %v), want found:false, err:nil", code, got, ok)
		}
	}
	if *listings != 0 || *snapshots != 0 {
		t.Fatalf("listings = %d, snapshots = %d; want 0 and 0 with no frames", *listings, *snapshots)
	}
}

// ---------------------------------------------------------------------------
// 3. The re-check is per pane, and a moved pane is nobody's answer
// ---------------------------------------------------------------------------

// TestOwnerPass_PaneMovedBetweenListings_DroppedInBatch — session A's pane is
// moved into B (`join-pane`) after the first listing, during the walk. The
// second listing places it in B, so A's candidate is dropped. B gets nothing
// either: it never had the pane when the pass enumerated it, and a candidate
// is only ever confirmed for the session it was found in. Session C, untouched
// in the same pass, is still answered — the drop is per pane, not per pass.
func TestOwnerPass_PaneMovedBetweenListings_DroppedInBatch(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	attachPane(fake, "%1", "$0", "200") // A's only pane; it moves to B
	attachPane(fake, "%3", "$2", "400") // C's pane; it stays
	seedIdentityFrame(t, m, "%1", "cc", 100, "t100", 10, "sess-a", "/a")
	c := seedIdentityFrame(t, m, "%3", "cc", 300, "t300", 30, "sess-c", "/c")
	withProcessTree(t, map[int]int{100: 200, 200: 1, 300: 400, 400: 1})
	withLivePids(t, map[int]string{100: "t100", 300: "t300"})
	withProcessReadHook(t, func() { fake.SetPaneSessionID("%1", "$1") })
	listings := countListings(fake)

	codeA, codeB, codeC := codeOf(t, "$0"), codeOf(t, "$1"), codeOf(t, "$2")
	results := runPass(context.Background(), m, nil, codeA, codeB, codeC)

	if got := results[codeA]; got.Err != nil || got.Found {
		t.Fatalf("A = %+v, want found:false, err:nil — its pane left it before the re-check", got)
	}
	if got := results[codeB]; got.Err != nil || got.Found {
		t.Fatalf("B = %+v, want found:false, err:nil — it did not have the pane at enumeration", got)
	}
	if got := results[codeC]; got.Err != nil || !got.Found || got.Owner.FrameID != c.FrameID {
		t.Fatalf("C = %+v, want its own root %s", got, c.FrameID)
	}
	if *listings != 2 {
		t.Fatalf("pane listings = %d, want 2", *listings)
	}
}

// ---------------------------------------------------------------------------
// 4–5. A re-check that did not complete in time confirms nothing
// ---------------------------------------------------------------------------

// seedCandidateAndNoCandidate gives session $0 a root in %1 and session $1 a
// frame in %2 that is not inside %2's process tree, so $1 has nothing for the
// re-check to confirm and Resolve already finalises it.
func seedCandidateAndNoCandidate(t *testing.T, m *Module, fake *tmux.FakeExecutor) (codeA, codeB string) {
	t.Helper()
	attachPane(fake, "%1", "$0", "200")
	attachPane(fake, "%2", "$1", "210")
	seedIdentityFrame(t, m, "%1", "cc", 100, "t100", 10, "sess-a", "/a")
	seedIdentityFrame(t, m, "%2", "cc", 110, "t110", 20, "sess-b", "/b")
	// 110 → 300 → 1 never passes %2's process 210.
	withProcessTree(t, map[int]int{100: 200, 200: 1, 110: 300, 300: 1})
	withLivePids(t, map[int]string{100: "t100", 110: "t110"})
	return codeOf(t, "$0"), codeOf(t, "$1")
}

// TestOwnerPass_ConfirmListingFails_CandidatesErr — a second listing that fails
// as a whole checked nothing (spec D5). A candidate session reports the
// failure, never "no owner" (#988), because "no owner" would tell the SPA a
// live agent is not there. A session Resolve found no candidate for needed no
// re-check and keeps its answer.
func TestOwnerPass_ConfirmListingFails_CandidatesErr(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	codeA, codeB := seedCandidateAndNoCandidate(t, m, fake)

	pass := m.NewOwnerPass(nil)
	pass.Resolve(context.Background(), codeA)
	pass.Resolve(context.Background(), codeB)
	fake.SetListAllPanesError(errors.New("tmux list-panes -a: server exited unexpectedly"))
	results := pass.Confirm(context.Background())

	if got := results[codeA]; got.Err == nil || got.Found {
		t.Fatalf("candidate session = %+v, want Err — the re-check failed, so nothing is confirmed", got)
	}
	if got, ok := results[codeB]; !ok || got.Err != nil || got.Found {
		t.Fatalf("no-candidate session = %+v (present %v), want found:false, err:nil", got, ok)
	}
}

// TestOwnerPass_ContextEndsBeforeConfirm_CandidatesErr — the context ends
// between the walk and the re-check. A candidate cannot be confirmed by a
// request that is out of time, so it is "no answer" (an error), not the owner
// found so far. The no-candidate session was final at Resolve, before the
// context ended, and keeps that answer.
func TestOwnerPass_ContextEndsBeforeConfirm_CandidatesErr(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	codeA, codeB := seedCandidateAndNoCandidate(t, m, fake)

	ctx, cancel := context.WithCancel(context.Background())
	pass := m.NewOwnerPass(nil)
	pass.Resolve(ctx, codeA)
	pass.Resolve(ctx, codeB)
	cancel()
	results := pass.Confirm(ctx)

	if got := results[codeA]; !errors.Is(got.Err, context.Canceled) || got.Found {
		t.Fatalf("candidate session = %+v, want Err wrapping context.Canceled", got)
	}
	if got, ok := results[codeB]; !ok || got.Err != nil || got.Found {
		t.Fatalf("no-candidate session = %+v (present %v), want the found:false, err:nil Resolve decided", got, ok)
	}
}

// ---------------------------------------------------------------------------
// 6–7. Enumeration
// ---------------------------------------------------------------------------

// TestOwnerPass_UnresolvablePane_ContributesNothing — a framed pane the first
// listing does not place, or places with a PID that does not parse, has
// nothing to walk against. It contributes nothing, and that is not an error;
// nor does it cost a process read, or even the process view.
func TestOwnerPass_UnresolvablePane_ContributesNothing(t *testing.T) {
	cases := map[string]func(fake *tmux.FakeExecutor){
		// %5 reports a session name but no session id, so it is absent from
		// the listing.
		"absent from the listing": func(fake *tmux.FakeExecutor) {
			fake.SetPaneSessionName("%5", "work")
			fake.SetPanePID("%5", "200")
		},
		"unparseable pid": func(fake *tmux.FakeExecutor) {
			attachPane(fake, "%5", "$0", "not-a-pid")
		},
	}
	for name, arrange := range cases {
		t.Run(name, func(t *testing.T) {
			m, fake, _ := newProvenanceQueryModule(t)
			arrange(fake)
			seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
			withProcessTree(t, map[int]int{100: 200, 200: 1})
			withLivePids(t, map[int]string{100: "t100"})
			var seen []int
			withRecordedReads(t, &seen)
			snapshots := countSnapshots(t)

			code := codeOf(t, "$0")
			results := runPass(context.Background(), m, nil, code)

			if got, ok := results[code]; !ok || got.Err != nil || got.Found {
				t.Fatalf("result = %+v (present %v), want found:false, err:nil", got, ok)
			}
			if len(seen) != 0 || *snapshots != 0 {
				t.Fatalf("reads = %v, snapshots = %d; want none — there is nothing to walk against", seen, *snapshots)
			}
		})
	}
}

// TestOwnerPass_EnumerationListingFails_EverySessionErr — when the first
// listing fails, no session's panes are known, so every session's answer is
// the failure (spec D5), never "no owner". The failure is sticky: the pass
// does not list again for the next session, and with no pane to walk it never
// takes a process view.
func TestOwnerPass_EnumerationListingFails_EverySessionErr(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	sessions := seedRootSessions(t, m, fake, 2)
	snapshots := countSnapshots(t)
	listings := countListings(fake)
	fake.SetListAllPanesError(errors.New("tmux list-panes -a: server exited unexpectedly"))

	results := runPass(context.Background(), m, nil, sessions[0].code, sessions[1].code)

	for i, s := range sessions {
		if got := results[s.code]; got.Err == nil || got.Found {
			t.Fatalf("session %d = %+v, want Err", i, got)
		}
	}
	if *snapshots != 0 {
		t.Fatalf("process snapshots = %d, want 0 — no pane was ever walked", *snapshots)
	}
	if *listings != 1 {
		t.Fatalf("pane listings = %d, want 1 — the failure is the pass's answer, not retried per session", *listings)
	}
}

// ---------------------------------------------------------------------------
// 8. A caller's process source
// ---------------------------------------------------------------------------

// TestOwnerPass_CallerSource_CalledOnceAndWalked — the inventory hands the pass
// the snapshot it already took for the registry, so the pass must use that
// view and no other: src is called once for the whole pass, the pass never
// takes a snapshot of its own, and every process question of the walk goes to
// src's view. The per-PID seams are armed, so an answer that came from
// anywhere else fails the test.
func TestOwnerPass_CallerSource_CalledOnceAndWalked(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	attachPane(fake, "%1", "$0", "200")
	attachPane(fake, "%2", "$1", "210")
	a := seedIdentityFrame(t, m, "%1", "cc", 100, "t100", 10, "sess-a", "/a")
	b := seedIdentityFrame(t, m, "%2", "cc", 110, "t110", 20, "sess-b", "/b")
	armPerPIDSeams(t)
	origSnap := takeProcSnapshotFn
	takeProcSnapshotFn = func() (agentpkg.ProcessView, error) {
		t.Fatalf("takeProcSnapshotFn called: the pass was given a source")
		return nil, nil
	}
	t.Cleanup(func() { takeProcSnapshotFn = origSnap })

	view := &tableView{
		ppid:  map[int]int{100: 200, 200: 1, 110: 210, 210: 1},
		start: map[int]string{100: "t100", 110: "t110"},
	}
	calls := 0
	src := func() (agentpkg.ProcessView, error) {
		calls++
		return view, nil
	}

	codeA, codeB := codeOf(t, "$0"), codeOf(t, "$1")
	results := runPass(context.Background(), m, src, codeA, codeB)

	if got := results[codeA]; got.Err != nil || !got.Found || got.Owner.FrameID != a.FrameID {
		t.Fatalf("A = %+v, want root %s", got, a.FrameID)
	}
	if got := results[codeB]; got.Err != nil || !got.Found || got.Owner.FrameID != b.FrameID {
		t.Fatalf("B = %+v, want root %s", got, b.FrameID)
	}
	if calls != 1 {
		t.Fatalf("src called %d times, want 1 for the whole pass", calls)
	}
	if want := []int{100, 200, 110, 210}; !slices.Equal(view.asked, want) {
		t.Fatalf("src's view was asked the parents of %v, want %v", view.asked, want)
	}
}

// TestOwnerPass_CallerSourceFails_EverySessionErr_CalledOnce — a source that
// fails is the answer for every session that needed it, and it is not asked
// again: the inventory's source returns the snapshot error it already has,
// and asking again per session would only repeat it (or, for a source that
// takes a snapshot, pay for one per session — the cost the pass removes).
func TestOwnerPass_CallerSourceFails_EverySessionErr_CalledOnce(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	sessions := seedRootSessions(t, m, fake, 2)
	snapshots := countSnapshots(t)
	snapErr := errors.New("sysctl kern.proc.all: cannot allocate memory")
	calls := 0
	src := func() (agentpkg.ProcessView, error) {
		calls++
		return nil, snapErr
	}

	results := runPass(context.Background(), m, src, sessions[0].code, sessions[1].code)

	for i, s := range sessions {
		if got := results[s.code]; !errors.Is(got.Err, snapErr) || got.Found {
			t.Fatalf("session %d = %+v, want Err wrapping the source's error", i, got)
		}
	}
	if calls != 1 {
		t.Fatalf("src called %d times, want 1 — a failure is not retried", calls)
	}
	if *snapshots != 0 {
		t.Fatalf("takeProcSnapshotFn called %d times, want 0 — the pass was given a source", *snapshots)
	}
}

// ---------------------------------------------------------------------------
// 9. The production wiring, on real processes
// ---------------------------------------------------------------------------

// spawnOrphanSleep starts a `sleep` whose parent exits at once, so the sleep is
// re-parented to PID 1 and a walk from it reaches the root in one step. It
// returns once the process table shows PPID 1. The sleep's output is
// redirected, or it would hold sh's stdout open and Output would wait the
// whole 30 s.
func spawnOrphanSleep(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("sh", "-c", "sleep 30 >/dev/null 2>&1 & echo $!").Output()
	if err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("parse sleep pid %q: %v", out, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, err := agentpkg.SnapshotProcesses(context.Background())
		if err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		if ppid, err := snap.PPID(pid); err == nil && ppid == 1 {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("sleep %d was never re-parented to PID 1", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestOwnerPass_RealProcesses_ProductionSnapshot_NoPerPIDReads runs the pass
// with its production process source on a real process: the frame's identity
// is the start time the real process table reports, and every per-PID seam is
// armed to fail. An answer therefore proves the pass took a real snapshot and
// walked it, and that the walk read nothing but PPID from it — no per-PID
// read, so no `ps` fork and no argument read at all (spec D10).
func TestOwnerPass_RealProcesses_ProductionSnapshot_NoPerPIDReads(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the real-process pass is pinned on darwin, where the snapshot reads sysctl")
	}
	m, fake, _ := newProvenanceQueryModule(t)
	pid := spawnOrphanSleep(t)
	snap, err := agentpkg.SnapshotProcesses(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	start, err := snap.StartTime(pid)
	if err != nil {
		t.Fatalf("start time of sleep %d: %v", pid, err)
	}
	frame := seedIdentityFrame(t, m, "%5", "cc", pid, start, 42, "sess-real", "/w")
	attachPane(fake, "%5", "$0", strconv.Itoa(pid))
	armPerPIDSeams(t)
	listings := countListings(fake)

	code := codeOf(t, "$0")
	results := runPass(context.Background(), m, nil, code)

	if got := results[code]; got.Err != nil || !got.Found || got.Owner.FrameID != frame.FrameID {
		t.Fatalf("result = %+v, want the sleep's frame %s", got, frame.FrameID)
	}
	if *listings != 2 {
		t.Fatalf("pane listings = %d, want 2", *listings)
	}
}
