package orphans

import (
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

// A loop stays flagged as long as it keeps being above the limit, keeping its first time; a quiet reading drops it, and a new run starts over.
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

	now = now.Add(3 * time.Minute)
	c = busy()
	s.Observe(now, withChild(shell), c.context())
	if got := s.Report().Loops[0].Since; !got.Equal(first) {
		t.Fatalf("since moved from %v to %v", first, got)
	}

	now = now.Add(time.Minute)
	s.Observe(now, withChild(shell), quiet.context())
	if r := s.Report(); len(r.Loops) != 0 {
		t.Fatalf("a quiet reading kept the loop: %+v", r.Loops)
	}

	now = now.Add(time.Minute)
	c = busy()
	s.Observe(now, withChild(shell), c.context())
	if got := s.Report().Loops[0].Since; got.Before(now.Add(-time.Second)) {
		t.Fatalf("a new run kept the old start: %v", got)
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
