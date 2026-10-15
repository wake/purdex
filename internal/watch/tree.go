package watch

import (
	"strings"

	"github.com/wake/purdex/internal/resources"
)

// shells are the command names whose children are the roots of what a person or an agent ran.
var shells = map[string]bool{"zsh": true, "bash": true, "sh": true, "fish": true, "dash": true, "ksh": true, "tcsh": true, "csh": true}

func isShell(comm string) bool { return shells[strings.TrimPrefix(comm, "-")] }

// startsTrees says whether a process's children are tree roots: a shell, or (when there is no shell: a program started by
// launchd, a tmux server's window) launchd and tmux themselves.
func startsTrees(p resources.Proc) bool {
	return isShell(p.Comm) || p.PID == 1 || p.Comm == "launchd" || p.Comm == "tmux"
}

// table is the process table with the lookups a pass needs.
type table struct {
	byPID  map[int]resources.Proc
	isRoot map[int]bool
	memo   map[int]int // pid → root pid, 0 = none
}

// maxWalk bounds every walk up the parent chain (a corrupt table can loop).
const maxWalk = 256

func newTable(procs []resources.Proc) *table {
	t := &table{byPID: make(map[int]resources.Proc, len(procs)), isRoot: map[int]bool{}, memo: map[int]int{}}
	for _, p := range procs {
		if _, dup := t.byPID[p.PID]; !dup {
			t.byPID[p.PID] = p // the first row is the process, as resources.Attribute does
		}
	}
	for pid, p := range t.byPID {
		if parent, ok := t.byPID[p.PPID]; ok && parent.PID != pid && startsTrees(parent) {
			t.isRoot[pid] = true
		}
	}
	return t
}

// rootOf is the root of the tree a process belongs to: the nearest ancestor (or itself) that is the child of a shell. 0 when
// there is none (the process is not under any shell, tmux or launchd).
func (t *table) rootOf(pid int) int {
	if r, ok := t.memo[pid]; ok {
		return r
	}
	var path []int
	cur, root := pid, 0
	for i := 0; i < maxWalk; i++ {
		if r, ok := t.memo[cur]; ok {
			root = r
			break
		}
		path = append(path, cur)
		if t.isRoot[cur] {
			root = cur
			break
		}
		p, ok := t.byPID[cur]
		if !ok || p.PPID == cur {
			break
		}
		cur = p.PPID
	}
	for _, q := range path {
		t.memo[q] = root
	}
	return root
}

// ancestors calls f on a process and then each of its ancestors, until f says stop, the chain ends or loops.
func (t *table) ancestors(pid int, f func(resources.Proc) (stop bool)) {
	cur := pid
	for i := 0; i < maxWalk; i++ {
		p, ok := t.byPID[cur]
		if !ok || f(p) {
			return
		}
		if p.PPID == cur {
			return
		}
		cur = p.PPID
	}
}
