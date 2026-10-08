package resources

import (
	"math"
	"slices"
	"strings"
)

// Proc is one row of the process table.
type Proc struct {
	PID, PPID int
	Pcpu      float64 // percent of one core, as ps reports it
	RSSBytes  uint64
}

// Root is where a session's process tree starts: the agent process itself.
//
// ProcStart is carried for the caller and is not compared here: a Proc has
// no start time (ps would give it as locale-dependent text), so Attribute
// matches a root by pid alone. The root source is what rules out a reused
// pid, by checking the pid's start time against the process snapshot it
// takes in the same tick, a few milliseconds before the ps fork
// (P0-2, ProcessRoots).
type Root struct {
	SessionID string
	PID       int
	ProcStart string
	Tmux, Cwd string
}

// Attribute charges every process to the session whose root it descends
// from and returns one SessionUse per root found in procs, largest Use first
// (ties by session id).
//
// A descendant that is itself another root belongs to that root and is not
// walked into, so a session started from inside another session's shell is
// not counted twice. Two roots with the same pid are one process: the first
// by session id is kept. A parent loop in a corrupt table ends the walk.
// The result is never nil.
func Attribute(procs []Proc, roots []Root, ncpu int, memBytes uint64) []SessionUse {
	out := []SessionUse{}
	if len(procs) == 0 || len(roots) == 0 {
		return out
	}

	byPID := make(map[int]Proc, len(procs))
	children := make(map[int][]int, len(procs))
	for _, p := range procs {
		if _, dup := byPID[p.PID]; dup {
			continue // one policy for a table that lists a pid twice: the first row is the process
		}
		byPID[p.PID] = p
		children[p.PPID] = append(children[p.PPID], p.PID)
	}

	ordered := slices.Clone(roots)
	slices.SortStableFunc(ordered, func(a, b Root) int { return strings.Compare(a.SessionID, b.SessionID) })
	isRoot := make(map[int]bool, len(ordered))
	var kept []Root
	for _, r := range ordered {
		if isRoot[r.PID] {
			continue
		}
		isRoot[r.PID] = true
		kept = append(kept, r)
	}

	for _, r := range kept {
		rootProc, ok := byPID[r.PID]
		if !ok {
			continue
		}
		u := SessionUse{SessionID: r.SessionID, PID: r.PID, Tmux: r.Tmux, Cwd: r.Cwd}
		visited := map[int]bool{r.PID: true}
		queue := []Proc{rootProc}
		for len(queue) > 0 {
			p := queue[0]
			queue = queue[1:]
			u.Procs++
			u.Pcpu += p.Pcpu
			u.RSSBytes += p.RSSBytes
			for _, c := range children[p.PID] {
				if visited[c] || isRoot[c] {
					continue
				}
				visited[c] = true
				queue = append(queue, byPID[c])
			}
		}
		if ncpu > 0 {
			u.CPU = u.Pcpu / float64(ncpu)
		}
		if memBytes > 0 {
			u.Mem = float64(u.RSSBytes) * 100 / float64(memBytes)
		}
		u.Use = ceilPercent(math.Max(u.CPU, u.Mem))
		out = append(out, u)
	}

	slices.SortStableFunc(out, func(a, b SessionUse) int {
		if a.Use != b.Use {
			return b.Use - a.Use
		}
		return strings.Compare(a.SessionID, b.SessionID)
	})
	return out
}
