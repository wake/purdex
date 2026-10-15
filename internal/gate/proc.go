package gate

import (
	"syscall"
	"time"
)

// procInfo is what the gate needs of a process: its parent, and when it started (to tell a reused pid).
type procInfo struct {
	PPID  int
	Start int64
}

// Procs reads the process table; a seam so tests can describe a tree.
type Procs func() (map[int]procInfo, error)

// ancestors lists pid's parent chain (not pid itself), nearest first, stopping at pid 0/1 or a loop.
func ancestors(tab map[int]procInfo, pid int) []int {
	var out []int
	seen := map[int]bool{pid: true}
	for {
		p, ok := tab[pid]
		if !ok || p.PPID <= 1 || seen[p.PPID] {
			return out
		}
		out = append(out, p.PPID)
		seen[p.PPID] = true
		pid = p.PPID
	}
}

// descendants lists every process below root (not root itself).
func descendants(tab map[int]procInfo, root int) map[int]int64 {
	kids := map[int][]int{}
	for pid, p := range tab {
		kids[p.PPID] = append(kids[p.PPID], pid)
	}
	out := map[int]int64{}
	stack := []int{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, k := range kids[n] {
			if _, dup := out[k]; dup || k == root {
				continue
			}
			out[k] = tab[k].Start
			stack = append(stack, k)
		}
	}
	return out
}

// tree follows the processes that descend from a command that shares the gate's process group (a terminal's
// foreground job: a group of its own would stop at the first read of the terminal). A process whose parent exited
// is adopted by init and no longer found by walking down from the command, so the tree is walked while it runs and
// what was seen is remembered, with its start time.
type tree struct {
	root  int
	procs Procs
	seen  map[int]int64
}

func newTree(root int, procs Procs) *tree {
	return &tree{root: root, procs: procs, seen: map[int]int64{}}
}

// scan adds what is below the root now.
func (t *tree) scan() {
	tab, err := t.procs()
	if err != nil {
		return
	}
	for pid, start := range descendants(tab, t.root) {
		t.seen[pid] = start
	}
}

// alive lists the remembered processes that still run (the same pid with the same start time).
func (t *tree) alive() []int {
	tab, err := t.procs()
	if err != nil {
		return nil
	}
	var out []int
	for pid, start := range t.seen {
		if p, ok := tab[pid]; ok && p.Start == start {
			out = append(out, pid)
		}
	}
	return out
}

// reap ends what is left: SIGTERM, grace, SIGKILL.
func (t *tree) reap(grace time.Duration) {
	t.scan() // what the command started since the last look
	left := t.alive()
	if len(left) == 0 {
		return
	}
	for _, pid := range left {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if len(t.alive()) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, pid := range t.alive() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
