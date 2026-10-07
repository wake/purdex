// Package execstat counts the tmux and ps child processes the daemon forks and
// how long they take (#1767). Pure observation: counters are atomic and
// lock-free, and nothing about a command's arguments or output is recorded.
package execstat

import (
	"fmt"
	"sync/atomic"
	"time"
)

// Counter is a fork count and the summed wall time of those forks.
type Counter struct {
	n  atomic.Int64
	ns atomic.Int64
}

// Tmux and PS are the process-wide counters.
var Tmux, PS Counter

// Observe records one fork that took d.
func (c *Counter) Observe(d time.Duration) {
	c.n.Add(1)
	c.ns.Add(int64(d))
}

// Snapshot returns the fork count and total time so far.
func (c *Counter) Snapshot() (int64, time.Duration) {
	return c.n.Load(), time.Duration(c.ns.Load())
}

// Reset zeroes the counter; for tests only.
func (c *Counter) Reset() {
	c.n.Store(0)
	c.ns.Store(0)
}

// Stats is a point-in-time reading of both counters.
type Stats struct {
	TmuxN int64
	TmuxD time.Duration
	PSN   int64
	PSD   time.Duration
}

// Take reads both counters.
func Take() Stats {
	var s Stats
	s.TmuxN, s.TmuxD = Tmux.Snapshot()
	s.PSN, s.PSD = PS.Snapshot()
	return s
}

// Sub returns s - base: what happened between two Take calls.
func (s Stats) Sub(base Stats) Stats {
	return Stats{s.TmuxN - base.TmuxN, s.TmuxD - base.TmuxD, s.PSN - base.PSN, s.PSD - base.PSD}
}

// String renders "tmux=N(Xms) ps=M(Yms)".
func (s Stats) String() string {
	return fmt.Sprintf("tmux=%d(%dms) ps=%d(%dms)", s.TmuxN, s.TmuxD.Milliseconds(), s.PSN, s.PSD.Milliseconds())
}
