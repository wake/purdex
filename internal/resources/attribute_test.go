package resources

import (
	"slices"
	"testing"
)

const (
	mb         = uint64(1_000_000)
	attrMem    = uint64(16_000_000_000)
	attrNCPU   = 10
	attrTmux   = "work"
	attrCwdTop = "/tmp/a"
)

func bySession(t *testing.T, got []SessionUse, id string) SessionUse {
	t.Helper()
	for _, s := range got {
		if s.SessionID == id {
			return s
		}
	}
	t.Fatalf("session %q not in %+v", id, got)
	return SessionUse{}
}

// A table with the same pid twice is read with one policy: the first row
// wins, and only that row's parent edge exists (codex attack finding: the
// second row's numbers were charged to the first row's parent).
func TestAttribute_DuplicatePIDFirstRowWins(t *testing.T) {
	procs := []Proc{
		{PID: 10, PPID: 1, RSSBytes: 1 * mb},
		{PID: 20, PPID: 1, RSSBytes: 1 * mb},
		{PID: 30, PPID: 10, RSSBytes: 1 * mb},
		{PID: 30, PPID: 20, RSSBytes: 999 * mb},
	}
	got := Attribute(procs, []Root{{SessionID: "a", PID: 10}, {SessionID: "b", PID: 20}}, attrNCPU, attrMem)
	if a := bySession(t, got, "a"); a.Procs != 2 || a.RSSBytes != 2*mb {
		t.Fatalf("a = %+v; want root + the first pid 30 row", a)
	}
	if b := bySession(t, got, "b"); b.Procs != 1 || b.RSSBytes != 1*mb {
		t.Fatalf("b = %+v; the second pid 30 row must not be reachable", b)
	}
}

func TestAttribute_TreeSums(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 1, Pcpu: 5, RSSBytes: 300 * mb},    // claude (root)
		{PID: 101, PPID: 100, Pcpu: 1, RSSBytes: 20 * mb},   // stdio MCP server
		{PID: 102, PPID: 100, Pcpu: 0, RSSBytes: 5 * mb},    // bash tool shell
		{PID: 103, PPID: 102, Pcpu: 10, RSSBytes: 100 * mb}, // vitest
		{PID: 104, PPID: 103, Pcpu: 90, RSSBytes: 200 * mb}, // worker
		{PID: 105, PPID: 103, Pcpu: 90, RSSBytes: 200 * mb}, // worker
		{PID: 106, PPID: 103, Pcpu: 90, RSSBytes: 200 * mb}, // worker
		{PID: 200, PPID: 1, Pcpu: 400, RSSBytes: 900 * mb},  // unrelated process
	}
	roots := []Root{{SessionID: "a", PID: 100, Tmux: attrTmux, Cwd: attrCwdTop}}

	got := Attribute(procs, roots, attrNCPU, attrMem)
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %+v", got)
	}
	s := got[0]
	if s.Procs != 7 {
		t.Errorf("Procs = %d, want 7", s.Procs)
	}
	if s.Pcpu != 286 {
		t.Errorf("Pcpu = %v, want 286", s.Pcpu)
	}
	if want := 1025 * mb; s.RSSBytes != want {
		t.Errorf("RSSBytes = %d, want %d (root included)", s.RSSBytes, want)
	}
	if s.PID != 100 || s.Tmux != attrTmux || s.Cwd != attrCwdTop {
		t.Errorf("root metadata lost: %+v", s)
	}
}

func TestAttribute_NestedRootNotDoubleCounted(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 1, Pcpu: 10, RSSBytes: 100 * mb},   // claude A
		{PID: 101, PPID: 100, Pcpu: 20, RSSBytes: 50 * mb},  // A's bash
		{PID: 110, PPID: 101, Pcpu: 30, RSSBytes: 200 * mb}, // claude B, started from A's bash
		{PID: 111, PPID: 110, Pcpu: 40, RSSBytes: 70 * mb},  // B's child
	}
	roots := []Root{{SessionID: "a", PID: 100}, {SessionID: "b", PID: 110}}

	got := Attribute(procs, roots, attrNCPU, attrMem)
	a, b := bySession(t, got, "a"), bySession(t, got, "b")
	if a.Procs != 2 || a.Pcpu != 30 || a.RSSBytes != 150*mb {
		t.Errorf("A must stop at B: %+v", a)
	}
	if b.Procs != 2 || b.Pcpu != 70 || b.RSSBytes != 270*mb {
		t.Errorf("B must have its own subtree: %+v", b)
	}
}

func TestAttribute_MissingRootDropped(t *testing.T) {
	procs := []Proc{{PID: 100, PPID: 1, Pcpu: 1, RSSBytes: mb}}
	roots := []Root{{SessionID: "gone", PID: 999}, {SessionID: "here", PID: 100}}
	got := Attribute(procs, roots, attrNCPU, attrMem)
	if len(got) != 1 || got[0].SessionID != "here" {
		t.Fatalf("want only 'here', got %+v", got)
	}
	if empty := Attribute(nil, roots, attrNCPU, attrMem); empty == nil || len(empty) != 0 {
		t.Errorf("no match must be an empty non-nil slice (JSON [] not null), got %#v", empty)
	}
}

func TestAttribute_CycleGuard(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 102, Pcpu: 1, RSSBytes: mb}, // root, parent loops back to its descendant
		{PID: 101, PPID: 100, Pcpu: 1, RSSBytes: mb},
		{PID: 102, PPID: 101, Pcpu: 1, RSSBytes: mb},
		{PID: 103, PPID: 103, Pcpu: 1, RSSBytes: mb}, // self-parent
	}
	got := Attribute(procs, []Root{{SessionID: "a", PID: 100}, {SessionID: "b", PID: 103}}, attrNCPU, attrMem)
	if a := bySession(t, got, "a"); a.Procs != 3 || a.Pcpu != 3 {
		t.Errorf("each process counted once: %+v", a)
	}
	if b := bySession(t, got, "b"); b.Procs != 1 {
		t.Errorf("self-parent counted once: %+v", b)
	}
}

func TestAttribute_Units(t *testing.T) {
	// pcpu 250 on 10 cpus -> cpu 25; rss 1.6e9 of 16e9 -> mem 10; use 25.
	procs := []Proc{{PID: 100, PPID: 1, Pcpu: 250, RSSBytes: 1_600_000_000}}
	got := Attribute(procs, []Root{{SessionID: "a", PID: 100}}, attrNCPU, attrMem)
	s := got[0]
	if !near(s.CPU, 25) || !near(s.Mem, 10) || s.Use != 25 {
		t.Errorf("units: %+v", s)
	}

	// Memory-bound: mem wins, and use rounds up.
	procs = []Proc{{PID: 100, PPID: 1, Pcpu: 10, RSSBytes: 3_000_000_000}}
	s = Attribute(procs, []Root{{SessionID: "a", PID: 100}}, attrNCPU, attrMem)[0]
	if !near(s.Mem, 18.75) || s.Use != 19 {
		t.Errorf("memory-bound: %+v", s)
	}

	// A zero ncpu or memsize must not divide by zero.
	s = Attribute(procs, []Root{{SessionID: "a", PID: 100}}, 0, 0)[0]
	if s.CPU != 0 || s.Mem != 0 || s.Use != 0 {
		t.Errorf("zero divisors: %+v", s)
	}
}

func TestAttribute_SortAndDuplicateRoots(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 1, Pcpu: 10},
		{PID: 200, PPID: 1, Pcpu: 100},
		{PID: 300, PPID: 1, Pcpu: 10},
	}
	roots := []Root{
		{SessionID: "z", PID: 100},
		{SessionID: "b", PID: 300},
		{SessionID: "a", PID: 100}, // same pid as "z": the first by session id wins
		{SessionID: "m", PID: 200},
	}
	got := Attribute(procs, roots, attrNCPU, attrMem)
	var ids []string
	for _, s := range got {
		ids = append(ids, s.SessionID)
	}
	if want := []string{"m", "a", "b"}; !slices.Equal(ids, want) {
		t.Errorf("order = %v, want %v (use desc, then session id; duplicate pid kept once)", ids, want)
	}
}
