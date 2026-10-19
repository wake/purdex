// Package orphans watches the host's process table for what a developer's machine leaks (spec §13.3 "orphans"): processes the launcher
// adopted (ppid 1) that sit under a dev path and have outlived their work, and ppid-1 shell loops that keep creating children (#3416: twelve
// leaked test loops forked more than 500 times a second for days). It only looks. Nothing in this package can signal a process.
package orphans

import (
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/advisor"
	"github.com/wake/purdex/internal/wwd/facts"
)

// The numbers of the rule (spec §13.3).
const (
	// MinAge is how long a dev-path process must have lived to count as left behind.
	MinAge = 2 * time.Hour
	// LoopRate is the children per second above which a shell is a fork loop.
	LoopRate = 10.0
	// CheckEvery is how often the loop check is made. It reads the process table many times in a few hundred milliseconds, so it is not
	// made on every 5 s sample.
	CheckEvery = 30 * time.Second
	// maxCheckGap is how long may pass between two checks before the run is taken as interrupted.
	maxCheckGap = 3 * CheckEvery
	// rateHistory is how many of the latest bursts are added together to judge a shell. One burst is short (about 0.15 s), so its rate is
	// coarse: one more child is a jump of about 6.7 a second, and a loop a little over the limit shows one child in some bursts and two in
	// others. Judged one burst at a time it would be lost at the first low one, and its ten minutes would start over. Added up, the
	// quantisation averages out; the price is that a loop which stops is dropped only when its old bursts have aged out (about 3 minutes).
	rateHistory = 6

	burstReads = 16
	burstGap   = 10 * time.Millisecond
	// burstBudget is the wall-clock time a burst may take before it is given up: it runs on the sampler's goroutine, and a big or slow
	// process table (Linux reads each process) must not hold up the 5 s sampling. A normal burst takes about 0.3 s.
	burstBudget = 1500 * time.Millisecond
	// burstFloor keeps the measured window from being shorter than the reads that were really made, whatever the clock says.
	burstFloor = 100 * time.Millisecond
	maxListed  = 100
)

// Config is the scanner's settings.
type Config struct {
	// UID is the user whose processes are looked at: the one wwd runs as.
	UID int
}

// Scanner takes a process snapshot at a time (Observe, from the sampler's hook) and keeps the latest report (Report). Observe and Report
// may be called from different goroutines.
type Scanner struct {
	cfg Config

	// seams
	sleep func(time.Duration)
	clock func() time.Time

	mu        sync.Mutex
	report    *advisor.OrphanReport
	loops     map[loopKey]*loopState // the shells above the limit now
	bursts    map[loopKey][]burst    // the last rateHistory bursts of each ppid-1 shell
	lastCheck time.Time
}

// burst is one measurement of one shell: the new children seen, over the time the burst took.
type burst struct {
	children int
	window   time.Duration
}

// childKey identifies a child process: its pid and its start time.
type childKey struct {
	pid   int
	start time.Time
}

type loopKey struct {
	pid   int
	start time.Time
}

type loopState struct {
	name  string
	since time.Time
	rate  float64
}

// New returns a scanner.
func New(cfg Config) *Scanner {
	return &Scanner{cfg: cfg, sleep: time.Sleep, clock: time.Now, loops: map[loopKey]*loopState{}, bursts: map[loopKey][]burst{}}
}

// Report is the latest scan, or nil before the first one has run. The caller may keep it.
func (s *Scanner) Report() *advisor.OrphanReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.report == nil {
		return nil
	}
	r := *s.report
	r.Procs = append([]advisor.OrphanProc(nil), r.Procs...)
	r.Loops = append([]advisor.ForkLoop(nil), r.Loops...)
	return &r
}

// Observe is the sampler's hook: the dev-path scan is made on every call, the loop check at most every CheckEvery.
func (s *Scanner) Observe(now time.Time, snap facts.ProcSnapshot, c facts.ProcContext) {
	procs := s.devOrphans(now, snap, c)
	s.mu.Lock()
	// due when the last check is CheckEvery ago, or when the clock was set back past it (a negative gap): a check that waited for the clock
	// to catch up could be put off for hours
	gap := now.Round(0).Sub(s.lastCheck)
	check := s.lastCheck.IsZero() || gap >= CheckEvery || gap < 0
	s.mu.Unlock()
	if check {
		s.checkLoops(now, snap, c)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rep := &advisor.OrphanReport{At: now, Procs: procs}
	for k, l := range s.loops {
		rep.Loops = append(rep.Loops, advisor.ForkLoop{PID: k.pid, Name: l.name, Rate: l.rate, Since: l.since})
	}
	sort.Slice(rep.Loops, func(i, j int) bool { return rep.Loops[i].Rate > rep.Loops[j].Rate })
	s.report = rep
}

// devOrphans lists the user's ppid-1 processes older than MinAge that sit under a dev path.
func (s *Scanner) devOrphans(now time.Time, snap facts.ProcSnapshot, c facts.ProcContext) []advisor.OrphanProc {
	var out []advisor.OrphanProc
	for _, p := range snap.Procs {
		if p.PPID != 1 || p.UID != s.cfg.UID || p.Start.IsZero() || now.Sub(p.Start) < MinAge {
			continue // a start time that is not known cannot be called old
		}
		var argv []string
		if c.ArgsOf != nil {
			argv = c.ArgsOf(p.PID)
		}
		var kind string
		if c.KindOf != nil {
			kind = c.KindOf(p)
		}
		if k := devKind(p.Name, argv, kind); k != "" {
			out = append(out, advisor.OrphanProc{PID: p.PID, Name: p.Name, Kind: k, Age: now.Sub(p.Start)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Age > out[j].Age })
	if len(out) > maxListed {
		out = out[:maxListed]
	}
	return out
}

// devKind says which dev path a process belongs to ("" when none): the resident kinds the facts already know (a playwright cli, a codex
// broker), then the path in its argv.
func devKind(name string, argv []string, residentKind string) string {
	switch residentKind {
	case "playwright":
		return "playwright"
	case "codex-broker":
		return "codex-broker"
	}
	text := strings.ToLower(name + " " + strings.Join(argv, " "))
	switch {
	case strings.Contains(text, "/worktrees/"):
		return "worktree"
	case strings.Contains(text, "/.scratch/"):
		return "scratch"
	case strings.Contains(text, "/tmp/pdx"):
		return "pdx-tmp"
	}
	if len(argv) > 0 && path.Base(argv[0]) == "wwd" && (strings.Contains(argv[0], "/tmp/") || strings.Contains(argv[0], "/var/folders/")) {
		return "test-wwd"
	}
	return ""
}

// resetLoops forgets the run of every shell and its bursts: what was measured can no longer be joined to what comes next.
func (s *Scanner) resetLoops() {
	s.mu.Lock()
	s.loops = map[loopKey]*loopState{}
	s.bursts = map[loopKey][]burst{}
	s.mu.Unlock()
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

// checkLoops measures the user's ppid-1 shells: a burst of table reads a few milliseconds apart counts the distinct children each shell
// began during it. A shell whose last rateHistory bursts together are above LoopRate keeps the time it was first found so; one that falls
// back under it, or is gone, is dropped, so a new run starts over.
func (s *Scanner) checkLoops(now time.Time, snap facts.ProcSnapshot, c facts.ProcContext) {
	s.mu.Lock()
	prev := s.lastCheck
	// Round(0) drops the monotonic reading a time.Now() carries: Sub would otherwise use it, and the monotonic clock does not advance while
	// the machine sleeps, so a three-hour sleep would look like a few seconds. The gap is wall-clock time.
	s.lastCheck = now.Round(0)
	s.mu.Unlock()
	if gap := now.Round(0).Sub(prev); !prev.IsZero() && (gap > maxCheckGap || gap < 0) { // a clock set back is a break too
		s.resetLoops() // the checks were interrupted (a sleep, a stalled sampler): what came before is not continuous with what comes now
	}
	if c.Fresh == nil {
		s.resetLoops() // nothing here can measure a loop, so none is claimed
		return
	}
	// every ppid-1 shell of the user's, whether or not it has a child at this instant: the faster the loop, the shorter its child lives and the
	// less likely a snapshot is to catch one. The burst is one set of table reads for all of them.
	suspects := map[int]facts.ProcRec{}
	for _, p := range snap.Procs {
		if p.PPID == 1 && p.UID == s.cfg.UID && shells[p.Name] {
			suspects[p.PID] = p
		}
	}
	if len(suspects) == 0 {
		s.resetLoops() // no shell left to be a loop
		return
	}
	began := s.clock()
	// A child is new when it was not there at the first read. Not by its start time against the clock: on Linux a start time is the boot
	// time (whole seconds) plus ticks, so a child that began a moment ago can seem to have begun nearly a second before the burst.
	// A child is a process, not a pid: the key is the pair, so a pid handed to a new child is a new child (the start time need not be exact,
	// only the same each time the same process is read).
	before := map[int]map[childKey]bool{}
	seen := map[int]map[childKey]bool{}
	for pid := range suspects {
		before[pid], seen[pid] = map[childKey]bool{}, map[childKey]bool{}
	}
	for i := 0; i < burstReads; i++ {
		rows, err := c.Fresh()
		// a table that cannot be read, or a burst that has run past its budget (a big or slow table), says nothing about the loop, so the run
		// it was part of is over: kept, it would let ten minutes of failed reads count as ten minutes above the limit
		if err != nil || s.clock().Sub(began) > burstBudget {
			s.resetLoops()
			return
		}
		for _, r := range rows {
			if _, ok := seen[r.PPID]; !ok {
				continue
			}
			k := childKey{r.PID, r.Start}
			switch {
			case i == 0:
				before[r.PPID][k] = true // already there: not a fork of this burst
			case !before[r.PPID][k]:
				seen[r.PPID][k] = true
			}
		}
		if i < burstReads-1 {
			s.sleep(burstGap)
		}
	}
	window := max(s.clock().Sub(began), burstFloor)
	s.mu.Lock()
	defer s.mu.Unlock()
	// each shell's rate is its last rateHistory bursts added together; a shell that is gone leaves no history
	nextBursts := map[loopKey][]burst{}
	next := map[loopKey]*loopState{}
	for pid, p := range suspects {
		k := loopKey{pid: pid, start: p.Start}
		h := append(s.bursts[k], burst{children: len(seen[pid]), window: window})
		if len(h) > rateHistory {
			h = h[len(h)-rateHistory:]
		}
		nextBursts[k] = h
		var kids int
		var span time.Duration
		for _, b := range h {
			kids, span = kids+b.children, span+b.window
		}
		rate := float64(kids) / span.Seconds()
		if rate <= LoopRate {
			continue
		}
		st := &loopState{name: p.Name, since: now, rate: rate}
		if old, ok := s.loops[k]; ok {
			st.since = old.since
		}
		next[k] = st
	}
	s.bursts, s.loops = nextBursts, next
}
