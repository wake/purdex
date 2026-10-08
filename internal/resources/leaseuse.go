package resources

import "math"

// BaselineEntry is one process a session-new lease does not own: it was
// running under the session's agent when the lease was granted. The start
// time is what tells it from a later process that was given the same pid.
type BaselineEntry struct {
	PID     int   `json:"pid"`
	StartMS int64 `json:"start_unix_ms"`
}

// LeaseTree names the processes one held lease is charged for.
type LeaseTree struct {
	ID    string
	Scope string // ScopeProcess or ScopeSessionNew
	// Root is the holder pid of a process-scope lease (the tree includes it),
	// or the session's agent pid of a session-new lease (the tree is what
	// started under it, never the agent itself).
	Root     int
	Baseline []BaselineEntry // session-new only
}

// LeaseUsage is what one lease's tree uses right now.
type LeaseUsage struct {
	Procs    int     // processes in the tree
	Pcpu     float64 // percent of one core, this lease's share
	RSSBytes uint64  // this lease's share
	// Use is the larger of CPU (Pcpu / ncpu) and memory (RSS / host memory),
	// in host percent and not rounded: the unit of D-1 and of
	// SessionUse.Use, as a float so that a share of a share keeps its size.
	Use float64
	// Empty means the tree has no process: a process-scope root that is not
	// in the table, or a session-new lease with nothing new under its agent.
	Empty bool
}

// ComputeLeaseUse measures every lease's tree in one pass over procs. It does
// no I/O and reads no clock.
//
// A process-scope tree is the root and every descendant. A session-new tree
// is the descendants of the root minus the baseline: a process is a baseline
// process only when its pid and its start time both match an entry, and then
// its whole subtree is left out. startMS gives the start time (unix
// milliseconds) of a pid in the same process table; when it cannot (nil, or
// ok false) the process does not match, so it is charged: an overcount only
// makes admission careful, an undercount would let work through.
//
// A process that several trees cover has its CPU and memory split evenly
// among those leases, so the leases' uses never add up to more than the
// processes really use (plan review #6). Two pids listed twice in procs are
// one process, the first row; a parent loop ends the walk.
//
// The result has an entry for every lease; it is never nil.
func ComputeLeaseUse(procs []Proc, leases []LeaseTree, startMS func(pid int) (int64, bool), ncpu int, memBytes uint64) map[string]LeaseUsage {
	out := make(map[string]LeaseUsage, len(leases))
	if len(leases) == 0 {
		return out
	}
	byPID := make(map[int]Proc, len(procs))
	children := make(map[int][]int, len(procs))
	for _, p := range procs {
		if _, dup := byPID[p.PID]; dup {
			continue // the first row is the process, as in Attribute
		}
		byPID[p.PID] = p
		children[p.PPID] = append(children[p.PPID], p.PID)
	}

	trees := make([][]int, len(leases))
	covers := make(map[int]int, len(byPID))
	for i, l := range leases {
		trees[i] = leaseTree(l, byPID, children, startMS)
		for _, pid := range trees[i] {
			covers[pid]++
		}
	}

	for i, l := range leases {
		u := LeaseUsage{Procs: len(trees[i]), Empty: len(trees[i]) == 0}
		var rss float64
		for _, pid := range trees[i] {
			share := float64(covers[pid])
			u.Pcpu += byPID[pid].Pcpu / share
			rss += float64(byPID[pid].RSSBytes) / share
		}
		u.RSSBytes = uint64(rss)
		var cpu, mem float64
		if ncpu > 0 {
			cpu = u.Pcpu / float64(ncpu)
		}
		if memBytes > 0 {
			mem = rss * 100 / float64(memBytes)
		}
		u.Use = math.Max(cpu, mem)
		out[l.ID] = u
	}
	return out
}

// leaseTree lists the pids of one lease's tree.
func leaseTree(l LeaseTree, byPID map[int]Proc, children map[int][]int, startMS func(int) (int64, bool)) []int {
	if _, ok := byPID[l.Root]; !ok {
		return nil
	}
	var baseline map[int]int64
	if l.Scope == ScopeSessionNew {
		baseline = make(map[int]int64, len(l.Baseline))
		for _, b := range l.Baseline {
			baseline[b.PID] = b.StartMS
		}
	}
	inBaseline := func(pid int) bool {
		want, listed := baseline[pid]
		if !listed || startMS == nil {
			return false
		}
		got, ok := startMS(pid)
		return ok && got == want
	}

	visited := map[int]bool{}
	var tree, queue []int
	if l.Scope == ScopeSessionNew {
		visited[l.Root] = true // a loop back to the agent does not charge it
		queue = append(queue, children[l.Root]...)
	} else {
		queue = append(queue, l.Root)
	}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if visited[pid] {
			continue
		}
		visited[pid] = true
		if l.Scope == ScopeSessionNew && inBaseline(pid) {
			continue // its subtree is not walked either
		}
		tree = append(tree, pid)
		queue = append(queue, children[pid]...)
	}
	return tree
}
