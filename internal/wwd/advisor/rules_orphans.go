package advisor

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// OrphanProc is a ppid-1 process of the user's that sits under a dev path and has outlived its work. Kind says which path it matched
// ("worktree", "scratch", "pdx-tmp", "test-wwd", "playwright", "codex-broker").
type OrphanProc struct {
	PID  int
	Name string
	Kind string
	Age  time.Duration
}

// ForkLoop is a ppid-1 shell that keeps creating children: its children per second as measured, and when it was first measured above the
// limit without a break.
type ForkLoop struct {
	PID   int
	Name  string
	Rate  float64
	Since time.Time
}

// OrphanReport is what the host's process scan last found (internal/wwd/orphans). It is local to the process: nothing here is a wire
// payload.
type OrphanReport struct {
	At    time.Time
	Procs []OrphanProc
	Loops []ForkLoop
}

// forkLoopHold is how long a loop must have been seen above the limit before the alert is pushed (spec §13.3: for ≥ 10 min).
const forkLoopHold = 10 * time.Minute

// Orphans is "orphans" (digest; push when the fork rate holds): the ppid-1 processes under dev paths older than 2 h, and the ppid-1 shell
// loops that keep creating children (> 10 per second, for ≥ 10 minutes: #3416). It only lists; nothing here can kill a process. The scan
// is the host's own process table, taken live: there is none to replay, so without a report the rule is unjudged, not "no orphans".
type Orphans struct{}

func (Orphans) Kind() wire.AlertKind { return wire.AlertOrphans }

func (Orphans) Eval(in Input) (Verdict, error) {
	if in.Orphans == nil {
		return Verdict{}, errors.New("no process scan is available")
	}
	r := in.Orphans()
	if r == nil {
		return Verdict{}, errors.New("the process scan has not run yet")
	}
	v := Verdict{Key: "orphans", Level: wire.AlertDigest}
	if len(r.Procs) == 0 && len(r.Loops) == 0 {
		return v, nil
	}
	var worst float64
	for _, l := range r.Loops {
		worst = max(worst, l.Rate)
		if in.Now.Sub(l.Since) >= forkLoopHold {
			v.Level = wire.AlertPush
		}
	}
	var parts []string
	if n := len(r.Loops); n > 0 {
		longest := time.Duration(0)
		for _, l := range r.Loops {
			longest = max(longest, in.Now.Sub(l.Since))
		}
		parts = append(parts, fmt.Sprintf("fork 迴圈 %d 個（最高 %.0f/s，已 %d 分鐘）", n, worst, int(longest.Minutes())))
	}
	if n := len(r.Procs); n > 0 {
		parts = append(parts, fmt.Sprintf("dev 孤兒 %d 個（%s）", n, kindCounts(r.Procs)))
	}
	v.Holds, v.Detail = true, strings.Join(parts, "；")
	return v, nil
}

// kindCounts is "worktree 2、playwright 1", the most common first.
func kindCounts(ps []OrphanProc) string {
	n := map[string]int{}
	for _, p := range ps {
		n[p.Kind]++
	}
	kinds := make([]string, 0, len(n))
	for k := range n {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if n[kinds[i]] != n[kinds[j]] {
			return n[kinds[i]] > n[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = fmt.Sprintf("%s %d", k, n[k])
	}
	return strings.Join(out, "、")
}
