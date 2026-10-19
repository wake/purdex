package orphans

import (
	"errors"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
)

var t0 = time.Date(2026, 10, 19, 20, 0, 0, 0, time.UTC)

const me = 501

func proc(pid, ppid int, name string, age time.Duration) facts.ProcRec {
	return facts.ProcRec{PID: pid, PPID: ppid, UID: me, Name: name, Start: t0.Add(-age)}
}

// ctxOf is the snapshot's lookups: argv by pid, kinds by pid, and a Fresh that hands out the staged tables one after another (the last one
// repeats). reads counts the calls.
type ctxOf struct {
	argv   map[int][]string
	kind   map[int]string
	tables [][]facts.ProcRec
	reads  int
}

func (c *ctxOf) context() facts.ProcContext {
	return facts.ProcContext{
		ArgsOf: func(pid int) []string { return c.argv[pid] },
		KindOf: func(p facts.ProcRec) string { return c.kind[p.PID] },
		Fresh: func() ([]facts.ProcRec, error) {
			i := min(c.reads, len(c.tables)-1)
			c.reads++
			return c.tables[i], nil
		},
	}
}

func newScanner(now *time.Time) *Scanner {
	s := New(Config{UID: me})
	s.sleep = func(d time.Duration) { *now = now.Add(d) }
	s.clock = func() time.Time { return *now }
	return s
}

func TestScanner_DevPathOrphansOlderThanTwoHours(t *testing.T) {
	now := t0
	s := newScanner(&now)
	procs := []facts.ProcRec{
		proc(1, 0, "launchd", 100*time.Hour),
		proc(10, 1, "node", 3*time.Hour),                                        // a worktree's dev server, orphaned
		proc(11, 1, "node", 90*time.Minute),                                     // too young
		proc(12, 1, "node", 5*time.Hour),                                        // not under a dev path
		proc(13, 10, "node", 5*time.Hour),                                       // has a parent
		proc(14, 1, "vite", 4*time.Hour),                                        // .scratch
		proc(15, 1, "wwd", 6*time.Hour),                                         // a test wwd under /tmp
		proc(16, 1, "pdx", 7*time.Hour),                                         // /private/tmp/pdx*
		proc(17, 1, "node", 26*time.Hour),                                       // playwright cli (kind)
		proc(18, 1, "codex", 9*time.Hour),                                       // codex broker (kind)
		{PID: 19, PPID: 1, UID: 0, Name: "node", Start: t0.Add(-8 * time.Hour)}, // another user's
	}
	c := &ctxOf{
		argv: map[int][]string{
			10: {"node", "/Users/wake/Workspace/wake/purdex/.claude/worktrees/x/node_modules/.bin/vite"},
			11: {"node", "/Users/wake/Workspace/wake/purdex/.claude/worktrees/y/server.js"},
			12: {"node", "/Users/wake/Workspace/wake/purdex/spa/server.js"},
			14: {"vite", "/Users/wake/Workspace/a/.scratch/demo/vite.js"},
			15: {"/private/var/folders/ab/T/wwd-test123/wwd", "--config", "x"},
			16: {"/Users/wake/.config/pdx/bin/pdx", "start", "--data-dir", "/private/tmp/pdx-test-9"},
			19: {"node", "/Users/wake/Workspace/wake/purdex/.claude/worktrees/z/a.js"},
		},
		kind:   map[int]string{17: "playwright", 18: "codex-broker"},
		tables: [][]facts.ProcRec{procs},
	}
	s.Observe(now, facts.ProcSnapshot{Procs: procs}, c.context())
	r := s.Report()
	if r == nil {
		t.Fatal("no report after a scan")
	}
	got := map[int]string{}
	for _, p := range r.Procs {
		got[p.PID] = p.Kind
	}
	want := map[int]string{10: "worktree", 14: "scratch", 15: "test-wwd", 16: "pdx-tmp", 17: "playwright", 18: "codex-broker"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for pid, k := range want {
		if got[pid] != k {
			t.Errorf("pid %d: %q want %q (all: %v)", pid, got[pid], k, got)
		}
	}
	// the oldest first
	if r.Procs[0].PID != 17 || r.Procs[0].Age < 25*time.Hour {
		t.Errorf("order/age: %+v", r.Procs[0])
	}
}

// The scan only lists. A process without a start time (a reader that cannot tell) cannot be called old, so it is not listed.
func TestScanner_UnknownStartIsNotOld(t *testing.T) {
	now := t0
	s := newScanner(&now)
	p := facts.ProcRec{PID: 10, PPID: 1, UID: me, Name: "node"}
	c := &ctxOf{argv: map[int][]string{10: {"node", "/x/.claude/worktrees/a/s.js"}}, tables: [][]facts.ProcRec{{p}}}
	s.Observe(now, facts.ProcSnapshot{Procs: []facts.ProcRec{p}}, c.context())
	if r := s.Report(); r == nil || len(r.Procs) != 0 {
		t.Fatalf("%+v", r)
	}
}

// children makes n fresh children of shell, each with a new pid.
func children(shell int, first, n int) []facts.ProcRec {
	var out []facts.ProcRec
	for i := 0; i < n; i++ {
		// no start time: the reader cannot tell, and such a child is counted (the burst cannot call it older than itself)
		out = append(out, facts.ProcRec{PID: first + i, PPID: shell, UID: me, Name: "sleep"})
	}
	return out
}

// Children that were already there when the burst began are not forks of it: a shell that keeps several long-lived children is not a loop.
func TestScanner_ChildrenAlreadyThereAreNotForks(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	table := []facts.ProcRec{shell}
	for i := 0; i < 5; i++ { // five background jobs, alive for an hour: 5 distinct children inside a quarter second is > 10/s if counted
		table = append(table, facts.ProcRec{PID: 9000 + i, PPID: 500, UID: me, Name: "sleep", Start: t0.Add(-time.Hour)})
	}
	c := &ctxOf{tables: [][]facts.ProcRec{table}}
	s.Observe(now, facts.ProcSnapshot{Procs: table}, c.context())
	if r := s.Report(); r == nil || len(r.Loops) != 0 {
		t.Fatalf("long-lived children counted as forks: %+v", r)
	}
}

// Linux's start time is the boot time (whole seconds) plus the start ticks, so a child can seem to have started up to a second before it
// did. A child is new when it was not there at the burst's first read, never by comparing its start time with the clock.
func TestScanner_ChildrenAreNewByNotBeingInTheFirstRead(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	var tables [][]facts.ProcRec
	for i := 0; i < 40; i++ {
		kids := children(500, 9000+i, 1)
		kids[0].Start = t0.Add(-900 * time.Millisecond) // starts that seem to be before the burst began, all of them
		tables = append(tables, append([]facts.ProcRec{shell}, kids...))
	}
	c := &ctxOf{tables: tables}
	s.Observe(now, facts.ProcSnapshot{Procs: tables[0]}, c.context())
	if r := s.Report(); r == nil || len(r.Loops) != 1 || r.Loops[0].Rate < 50 {
		t.Fatalf("a loop whose children's start times looked old was missed: %+v", r)
	}
}

// A table that cannot be read says nothing about the loop: the continuous run is over, and a loop seen again starts a new one. Otherwise
// ten minutes of failures would be counted as ten minutes above the limit.
func TestScanner_AFailedReadEndsTheRun(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	busy := func() *ctxOf {
		var tables [][]facts.ProcRec
		for i := 0; i < 40; i++ {
			tables = append(tables, append([]facts.ProcRec{shell}, children(500, 9000+i, 1)...))
		}
		return &ctxOf{tables: tables}
	}
	c := busy()
	s.Observe(now, withChild(shell), c.context())
	if len(s.Report().Loops) != 1 {
		t.Fatal("no loop to begin with")
	}
	now = now.Add(CheckEvery)
	broken := facts.ProcContext{Fresh: func() ([]facts.ProcRec, error) { return nil, errTable }}
	s.Observe(now, withChild(shell), broken)
	if r := s.Report(); len(r.Loops) != 0 {
		t.Fatalf("a failed read kept the loop: %+v", r.Loops)
	}
	now = now.Add(CheckEvery)
	c = busy()
	calledAt := now // the fake sleep moves now during the burst; the run starts at the time the scan was made
	s.Observe(calledAt, withChild(shell), c.context())
	if got := s.Report().Loops[0].Since; !got.Equal(calledAt) {
		t.Fatalf("the run did not start over: since %v, scan made at %v", got, calledAt)
	}
}

var errTable = errors.New("table unreadable")

// A child is a process, not a pid: a pid handed to a new child during the burst, after the first read's child of that pid exited, is a new
// fork. Counting by pid alone would take it for the one that was there and under-count the rate.
func TestScanner_AReusedPidIsANewChild(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	var tables [][]facts.ProcRec
	for i := 0; i < 40; i++ {
		// every read has a child with the same pid 9000, but a different process each time (a different start)
		kid := facts.ProcRec{PID: 9000, PPID: 500, UID: me, Name: "true", Start: t0.Add(time.Duration(i) * time.Millisecond)}
		tables = append(tables, []facts.ProcRec{shell, kid})
	}
	c := &ctxOf{tables: tables}
	s.Observe(now, facts.ProcSnapshot{Procs: tables[0]}, c.context())
	if r := s.Report(); r == nil || len(r.Loops) != 1 || r.Loops[0].Rate < 50 {
		t.Fatalf("a pid reused for every new child was counted once: %+v", r)
	}
}

// A burst that runs past its wall-clock budget (a slow table, a big host) is given up: that round says nothing, the run is over, and the
// sampler is held up no longer than the budget plus one read.
func TestScanner_ABurstOverItsBudgetIsGivenUp(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	reads := 0
	slow := facts.ProcContext{Fresh: func() ([]facts.ProcRec, error) {
		reads++
		now = now.Add(time.Second) // every read takes a second
		return []facts.ProcRec{shell, {PID: 9000 + reads, PPID: 500, UID: me, Name: "true"}}, nil
	}}
	s.Observe(now, withChild(shell), slow)
	if r := s.Report(); r == nil || len(r.Loops) != 0 {
		t.Fatalf("a burst past its budget claimed a loop: %+v", r)
	}
	if limit := int(burstBudget/time.Second) + 2; reads > limit {
		t.Fatalf("%d reads of a one-second table: the budget did not stop it (limit %d)", reads, limit)
	}
}

// withChild is a snapshot of a shell and one child it has just now.
func withChild(shell facts.ProcRec) facts.ProcSnapshot {
	return facts.ProcSnapshot{Procs: append([]facts.ProcRec{shell}, children(shell.PID, 9000, 1)...)}
}

// A ppid-1 shell whose children keep changing is measured by a burst of table reads: the distinct children over the time the burst took.
func TestScanner_AForkLoopIsMeasuredByABurst(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	// one new child per read, reads 10 ms apart: ~100 children a second
	var tables [][]facts.ProcRec
	for i := 0; i < 40; i++ {
		tables = append(tables, append([]facts.ProcRec{shell}, children(500, 9000+i, 1)...))
	}
	c := &ctxOf{tables: tables}
	snap := facts.ProcSnapshot{Procs: tables[0]}
	s.Observe(now, snap, c.context())
	r := s.Report()
	if r == nil || len(r.Loops) != 1 {
		t.Fatalf("%+v", r)
	}
	l := r.Loops[0]
	if l.PID != 500 || l.Name != "sh" || l.Rate < 50 || !l.Since.Equal(now) && l.Since.After(now) {
		t.Fatalf("loop: %+v", l)
	}
	if c.reads < 5 {
		t.Fatalf("only %d reads in the burst", c.reads)
	}
}

// The snapshot need not show a child: the faster the loop the shorter its child lives, and a snapshot rarely catches one (a /usr/bin/true
// loop on mlab was missed that way when a child was required). The shell is measured by the burst alone.
func TestScanner_ALoopWhoseChildIsNotInTheSnapshotIsStillMeasured(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "bash", time.Hour)
	var tables [][]facts.ProcRec
	for i := 0; i < 40; i++ {
		tables = append(tables, append([]facts.ProcRec{shell}, children(500, 9000+i, 1)...))
	}
	c := &ctxOf{tables: tables}
	s.Observe(now, facts.ProcSnapshot{Procs: []facts.ProcRec{shell}}, c.context()) // no child in the snapshot itself
	if r := s.Report(); r == nil || len(r.Loops) != 1 || r.Loops[0].PID != 500 || r.Loops[0].Rate < 50 {
		t.Fatalf("%+v", r)
	}
}

// A shell with one long-lived child is not a loop, and a shell with no children is not measured as one.
func TestScanner_ASteadyChildIsNotALoop(t *testing.T) {
	now := t0
	s := newScanner(&now)
	steady := proc(500, 1, "sh", time.Hour)
	idle := proc(501, 1, "bash", time.Hour)
	child := facts.ProcRec{PID: 9000, PPID: 500, UID: me, Name: "sleep", Start: t0}
	table := []facts.ProcRec{steady, idle, child}
	c := &ctxOf{tables: [][]facts.ProcRec{table}}
	s.Observe(now, facts.ProcSnapshot{Procs: table}, c.context())
	if r := s.Report(); r == nil || len(r.Loops) != 0 {
		t.Fatalf("%+v", r)
	}
}

// Only the user's own ppid-1 shells are looked at; a shell under another parent, or another user's, is not.
func TestScanner_OnlyTheUsersPpid1Shells(t *testing.T) {
	now := t0
	s := newScanner(&now)
	mine := proc(500, 1, "sh", time.Hour)
	nested := proc(501, 400, "sh", time.Hour)
	other := facts.ProcRec{PID: 502, PPID: 1, UID: 0, Name: "sh", Start: t0.Add(-time.Hour)}
	var tables [][]facts.ProcRec
	for i := 0; i < 40; i++ {
		tables = append(tables, append([]facts.ProcRec{mine, nested, other}, append(children(500, 9000+i, 1), append(children(501, 8000+i, 1), children(502, 7000+i, 1)...)...)...))
	}
	c := &ctxOf{tables: tables}
	s.Observe(now, facts.ProcSnapshot{Procs: tables[0]}, c.context())
	r := s.Report()
	if r == nil || len(r.Loops) != 1 || r.Loops[0].PID != 500 {
		t.Fatalf("%+v", r)
	}
}

// A loop stays flagged as long as the last few bursts together are above the limit, keeping its first time. Once it stops, the old bursts
// age out of the window and it is dropped (not on the first quiet reading: one reading says little); a loop seen again starts a new run.
func TestScanner_ALoopKeepsItsFirstTimeUntilItGoesQuiet(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	busy := func() *ctxOf {
		var tables [][]facts.ProcRec
		for i := 0; i < 40; i++ {
			tables = append(tables, append([]facts.ProcRec{shell}, children(500, 9000+i, 1)...))
		}
		return &ctxOf{tables: tables}
	}
	quiet := &ctxOf{tables: [][]facts.ProcRec{{shell, {PID: 9000, PPID: 500, UID: me, Name: "sleep", Start: t0}}}}

	c := busy()
	s.Observe(now, withChild(shell), c.context())
	first := s.Report().Loops[0].Since

	now = now.Add(CheckEvery)
	c = busy()
	s.Observe(now, withChild(shell), c.context())
	if got := s.Report().Loops[0].Since; !got.Equal(first) {
		t.Fatalf("since moved from %v to %v", first, got)
	}

	dropped := 0
	for i := 1; i <= rateHistory+1 && dropped == 0; i++ {
		now = now.Add(CheckEvery)
		s.Observe(now, withChild(shell), quiet.context())
		if len(s.Report().Loops) == 0 {
			dropped = i
		}
	}
	if dropped == 0 || dropped > rateHistory {
		t.Fatalf("the loop was dropped after %d quiet checks, want within %d (and not before the old bursts aged out)", dropped, rateHistory)
	}

	now = now.Add(CheckEvery)
	c = busy()
	calledAt := now
	s.Observe(calledAt, withChild(shell), c.context())
	if got := s.Report().Loops[0].Since; !got.Equal(calledAt) {
		t.Fatalf("a new run kept the old start: %v, scan made at %v", got, calledAt)
	}
}

// A long break between two checks (the machine slept, the sampler stalled) ends the run: bursts from before it are not joined to those after
// it, and the loop's first time is not carried over it. Otherwise a loop seen before the break and gone during it would still be flagged on
// waking, with a start long enough ago to be pushed on the clock alone.
func TestScanner_ALongBreakEndsTheRun(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	busy := bursting(shell, 8) // eight new children in a burst: a fast loop
	s.Observe(now, withChild(shell), busy.context())
	if len(s.Report().Loops) != 1 {
		t.Fatal("no loop to begin with")
	}
	now = now.Add(3 * time.Hour) // the machine slept
	quiet := &ctxOf{tables: [][]facts.ProcRec{{shell, {PID: 8000, PPID: 500, UID: me, Name: "true", Start: t0.Add(-time.Hour)}}}}
	s.Observe(now, withChild(shell), quiet.context())
	if r := s.Report(); len(r.Loops) != 0 {
		t.Fatalf("a loop from before a 3 h break was carried over it: %+v", r.Loops)
	}
	// seen again after the break, it starts a new run now, not 3 hours ago
	calledAt := now.Add(CheckEvery)
	s.Observe(calledAt, withChild(shell), bursting(shell, 8).context())
	if r := s.Report(); len(r.Loops) != 1 || !r.Loops[0].Since.Equal(calledAt) {
		t.Fatalf("the new run did not start at the scan: %+v", r.Loops)
	}
}

// bursting is a context whose burst sees k new children of shell 500 (the first read has one, already there): at reads 3, 7, 11, ...
func bursting(shell facts.ProcRec, k int) *ctxOf {
	base := facts.ProcRec{PID: 8000, PPID: 500, UID: me, Name: "true", Start: t0.Add(-time.Hour)}
	var tables [][]facts.ProcRec
	for r := 0; r < 40; r++ {
		table := []facts.ProcRec{shell, base}
		for j := 0; j < k; j++ {
			if r >= 3+4*j {
				table = append(table, facts.ProcRec{PID: 8100 + j, PPID: 500, UID: me, Name: "true", Start: t0.Add(time.Duration(j) * time.Millisecond)})
			}
		}
		tables = append(tables, table)
	}
	return &ctxOf{tables: tables}
}

// A burst is short, so the rate it gives is coarse (one more child is about 6.7/s): a loop a little over the limit shows one child in
// some bursts and two in others. Judged one burst at a time it would be lost the first time a burst under-counted and its ten minutes
// would start over, so it never pushes. The last few bursts are added together.
func TestScanner_ALoopNearTheLimitIsNotLostToQuantisation(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	counts := []int{1, 2, 2} // 5 new children per 3 bursts of 0.15 s: 11.1 a second
	absent := 0
	var checks int
	for i := 0; i < 28; i++ {
		c := bursting(shell, counts[i%len(counts)])
		s.Observe(now, withChild(shell), c.context())
		if r := s.Report(); i >= 5 && len(r.Loops) == 0 {
			absent++
		}
		checks++
		now = now.Add(CheckEvery)
	}
	r := s.Report()
	if absent != 0 || len(r.Loops) != 1 {
		t.Fatalf("the loop was missing from %d of the checks after it was first found; at the end %+v", absent, r.Loops)
	}
	// Since was set within the first few checks and kept: by the end it is well past ten minutes
	if held := now.Add(-CheckEvery).Sub(r.Loops[0].Since); held < forkHold {
		t.Fatalf("the loop has been held for %v after %d checks, want ≥ %v", held, checks, forkHold)
	}
}

const forkHold = 10 * time.Minute

// The same bursts a little under the limit (8.9 a second) never make a loop, however long they go on.
func TestScanner_ALoopJustUnderTheLimitIsNeverFlagged(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	counts := []int{1, 1, 2} // 4 new children per 3 bursts of 0.15 s: 8.9 a second
	for i := 0; i < 30; i++ {
		c := bursting(shell, counts[i%len(counts)])
		s.Observe(now, withChild(shell), c.context())
		if r := s.Report(); len(r.Loops) != 0 {
			t.Fatalf("check %d flagged a %v/s loop: %+v", i, 8.9, r.Loops)
		}
		now = now.Add(CheckEvery)
	}
}

// The loop check is made at most every CheckEvery, not on each 5 s sample (it reads the table many times); the dev-path scan is cheap and
// is redone each time.
func TestScanner_TheBurstIsRateLimited(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	table := append([]facts.ProcRec{shell}, children(500, 9000, 1)...)
	c := &ctxOf{tables: [][]facts.ProcRec{table}}
	s.Observe(now, facts.ProcSnapshot{Procs: table}, c.context())
	first := c.reads
	if first == 0 {
		t.Fatal("no burst on the first scan")
	}
	now = now.Add(5 * time.Second)
	s.Observe(now, facts.ProcSnapshot{Procs: table}, c.context())
	if c.reads != first {
		t.Fatalf("a second burst %d s later (%d → %d reads)", 5, first, c.reads)
	}
	now = now.Add(CheckEvery)
	s.Observe(now, facts.ProcSnapshot{Procs: table}, c.context())
	if c.reads == first {
		t.Fatal("no burst after CheckEvery")
	}
}

// No Fresh lookup (a reader that cannot): the dev-path scan still works and no loop is ever claimed.
func TestScanner_WithoutFreshThereAreNoLoops(t *testing.T) {
	now := t0
	s := newScanner(&now)
	shell := proc(500, 1, "sh", time.Hour)
	ctx := facts.ProcContext{ArgsOf: func(int) []string { return nil }}
	s.Observe(now, withChild(shell), ctx)
	if r := s.Report(); r == nil || len(r.Loops) != 0 {
		t.Fatalf("%+v", r)
	}
}

// Before the first scan there is no report (the rule is then unjudged, not clean).
func TestScanner_NoReportBeforeTheFirstScan(t *testing.T) {
	now := t0
	if newScanner(&now).Report() != nil {
		t.Fatal("a report before any scan")
	}
}
