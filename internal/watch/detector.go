package watch

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/resources"
)

const mib = 1 << 20

// orphanGrace is how many passes in a row a holder may read as not orphaned before its first-seen time is forgotten.
const orphanGrace = 3

// episode is the "one event when it crosses, none while it stays over" rule: fireAfter is how long the condition must hold
// (continuously) before the event, rearmAfter how long it must stay false before it can fire again.
type episode struct {
	since, underSince time.Time
	over              bool
}

// breakRun ends a run that has not been confirmed: the condition must hold for the whole fireAfter again, and a fired episode must
// see the condition go away again from the start. It leaves "fired" as it is. Used where nothing was observed (unknown) or the
// observer changed.
func (e *episode) breakRun() { e.since, e.underSince = time.Time{}, time.Time{} }

func (e *episode) step(now time.Time, cond bool, fireAfter, rearmAfter time.Duration) (fire bool) {
	if cond {
		e.underSince = time.Time{}
		if e.since.IsZero() {
			e.since = now
		}
		if !e.over && now.Sub(e.since) >= fireAfter {
			e.over = true
			return true
		}
		return false
	}
	e.since = time.Time{}
	if e.over {
		if e.underSince.IsZero() {
			e.underSince = now
		}
		if now.Sub(e.underSince) >= rearmAfter {
			e.over, e.underSince = false, time.Time{}
		}
	}
	return false
}

type treeKey struct {
	pid  int
	comm string
}

type sample struct {
	at    time.Time
	cores float64
	rss   uint64
}

type treeState struct {
	window  time.Duration // the t_s these samples were collected for
	since   time.Time     // where the unbroken run of samples starts
	samples []sample      // those of the last TS, and the one before them (the left edge's value)
	// resident: the tree was found to be a resident app (residentAndQuiet); from then on only its cores can make it an event
	resident bool
	simNoted bool // the "simulator" Diag was recorded for the memory that is over now
	ep       episode
}

// Detector holds what the passes share: the windows of the trees, the episodes of E2, and which holders have been reported.
type Detector struct {
	mem   Memory
	trees map[treeKey]*treeState

	orphanMiss map[string]int // consecutive passes a once-orphaned holder was not orphaned
	started    bool

	// Diag, when set, is told what the detector cannot use of what it read: the source and the problem ("" = fine again). It is called
	// every pass; the receiver keeps it to the changes.
	Diag func(source, problem string)

	pressure, pages episode
	havePages       bool
	e2Seen, e2Host  bool      // E2 has been run, and on wwd's signals (not Raw) the last time
	lastHostAt      time.Time // the stamp of the wwd facts E2 last took
	lastPass        time.Time
	lastPages       uint64
	lastPagesAt     time.Time
}

// New is a detector over a Memory.
func New(mem Memory) *Detector {
	return &Detector{mem: mem, trees: map[treeKey]*treeState{}, orphanMiss: map[string]int{}}
}

// Pass reads one tick and returns the events it makes, E1 first (by root pid), then E2, E3, E4. It is called on every sampler
// tick that sampled; a tick that failed is not passed, and the windows simply see the gap.
func (d *Detector) Pass(in Input) []Event {
	d.breakE2AfterAGap(in)
	events := d.unmanagedTrees(in)
	events = append(events, d.pressureEvents(in)...)
	events = append(events, d.slotEvents(in)...)
	return events
}

// breakE2AfterAGap starts the pressure and page-rate episodes again when the passes before this one were too far back (more than
// two and a half ticks: failed samples, or a watcher that was switched off): the time nobody looked is not time the host was
// under pressure. (E1's windows keep their own gap rule.)
func (d *Detector) breakE2AfterAGap(in Input) {
	interval := in.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if !d.lastPass.IsZero() && in.Now.Sub(d.lastPass) > interval*5/2 {
		d.pressure, d.pages = episode{}, episode{}
		d.havePages = false
	}
	d.lastPass = in.Now
}

// ---- E1 ----

func (d *Detector) unmanagedTrees(in Input) []Event {
	set := in.Set
	interval := in.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	// a window shorter than two ticks would be one sample standing for all of it: not a window that was observed
	if set.TS < 2*interval || (set.XCores <= 0 && set.YMB <= 0) {
		d.trees = map[treeKey]*treeState{}
		return nil
	}
	tab := newTable(in.Procs)
	type use struct {
		cores float64
		rss   uint64
	}
	uses := map[int]*use{}
	for _, p := range tab.byPID {
		r := tab.rootOf(p.PID)
		if r == 0 {
			continue
		}
		u := uses[r]
		if u == nil {
			u = &use{}
			uses[r] = u
		}
		u.cores += p.Pcpu / 100
		u.rss += p.RSSBytes
	}
	managedByHolder := map[int]bool{}
	for _, h := range in.Holders {
		for _, pid := range []int{h.PID, h.PGID} {
			if pid > 0 {
				if r := tab.rootOf(pid); r != 0 {
					managedByHolder[r] = true
				}
			}
		}
	}
	gateProc := map[int]bool{} // pid → is a `pdx gate`, asked once per pass
	isGate := func(p resources.Proc) bool {
		if p.Comm != "pdx" || in.IsGate == nil { // the name is only the cheap filter: the caller says who really is a gate
			return false
		}
		if v, ok := gateProc[p.PID]; ok {
			return v
		}
		v := in.IsGate(p.PID)
		gateProc[p.PID] = v
		return v
	}
	managed := func(root int) bool {
		if managedByHolder[root] {
			return true
		}
		found := false
		tab.ancestors(root, func(p resources.Proc) bool {
			found = isGate(p)
			return found
		})
		return found
	}

	roots := make([]int, 0, len(uses))
	for r := range uses {
		roots = append(roots, r)
	}
	sort.Ints(roots)

	var events []Event
	seen := map[treeKey]bool{}
	for _, root := range roots {
		if managed(root) {
			continue
		}
		key := treeKey{root, tab.byPID[root].Comm}
		seen[key] = true
		st := d.trees[key]
		if st == nil {
			st = &treeState{since: in.Now, window: set.TS}
			d.trees[key] = st
		}
		if set.TS > st.window {
			// the window was lengthened (settings are read at every pass): what was kept covers the old one only, so the
			// longer window is collected anew
			st.window, st.since, st.samples = set.TS, in.Now, nil
		}
		st.window = set.TS
		u := uses[root]
		sim := tab.byPID[root].Comm == simulatorComm
		if in.Footprint != nil && !sim && set.YMB > 0 && u.rss >= uint64(set.YMB)*mib {
			// over by RSS, which counts shared pages once per process: the tree's memory is what its processes really hold
			var sum uint64
			for _, p := range tab.byPID {
				if tab.rootOf(p.PID) != root {
					continue
				}
				if fp, ok := in.Footprint(p.PID); ok {
					sum += fp
				} else {
					sum += p.RSSBytes
				}
			}
			u.rss = sum
		}
		maxGap := interval*5/2 + time.Second
		if n := len(st.samples); n > 0 && in.Now.Sub(st.samples[n-1].at) > maxGap {
			// a pass was missed (a failed sample): what was not observed is not part of a window, so it starts again
			st.since, st.samples = in.Now, nil
		}
		st.samples = append(st.samples, sample{in.Now, u.cores, u.rss})
		// keep the samples inside the window and the one just before it
		cut := 0
		for cut+1 < len(st.samples) && !st.samples[cut+1].at.After(in.Now.Add(-set.TS)) {
			cut++
		}
		st.samples = st.samples[cut:]
		tset := set
		if st.resident || sim {
			tset.YMB = 0
		}
		if sim {
			// the simulator's memory alone is never an event (#3072); say so once per stretch of memory over y_mb
			if set.YMB > 0 && u.rss >= uint64(set.YMB)*mib {
				if !st.simNoted && d.Diag != nil {
					d.Diag(fmt.Sprintf("simulator %d", root), fmt.Sprintf("launchd_sim (pid %d) sums %d MB of RSS across its daemons, shared pages counted once per process: memory alone is not an event, its cores still count", root, u.rss/mib))
				}
				st.simNoted = true
			} else if st.simNoted {
				st.simNoted = false
				if d.Diag != nil {
					// fine again; one source per tree (several devices run at once), so one tree's recovery clears only its own line
					d.Diag(fmt.Sprintf("simulator %d", root), "")
				}
			}
		}
		over := d.overThreshold(st, in.Now, tset)
		if st.ep.step(in.Now, over, 0, set.TS) {
			if !st.resident && d.residentAndQuiet(in, tab, root, st, set) {
				st.resident = true // a long-lived app of /Applications whose memory is all that is over: no event (#2965); its cores still count
				st.ep = episode{}  // the episode that fired on memory is over: cores that are over right after start one of their own
				continue
			}
			events = append(events, d.treeEvent(in, tab, root, st))
		}
	}
	for key := range d.trees {
		if !seen[key] {
			delete(d.trees, key) // the tree ended (or turned managed): its episode with it
		}
	}
	return events
}

// overThreshold: the whole window has been seen without a gap, and its mean cores are at or above x_cores or its resident
// memory was at or above y_mb throughout. The mean is over time: each stretch between two samples counts for the lower of the
// two, so load that was seen at only one end of a stretch (a burst between two passes, a sample that jumped) is not taken for
// load that lasted the whole stretch, in either direction.
func (d *Detector) overThreshold(st *treeState, now time.Time, set Settings) bool {
	if now.Sub(st.since) < set.TS || len(st.samples) == 0 {
		return false
	}
	start := now.Add(-set.TS)
	var weighted, total float64
	var minRSS uint64
	first := true
	for i, s := range st.samples {
		if !s.at.Before(start) { // inside the window, its left edge too
			if first || s.rss < minRSS {
				minRSS, first = s.rss, false
			}
		}
		if i == 0 {
			continue
		}
		from := st.samples[i-1].at
		if from.Before(start) {
			from = start
		}
		if dt := s.at.Sub(from).Seconds(); dt > 0 {
			weighted += min(st.samples[i-1].cores, s.cores) * dt
			total += dt
		}
	}
	if total == 0 || first {
		return false
	}
	mean := weighted / total
	return (set.XCores > 0 && mean >= set.XCores) || (set.YMB > 0 && minRSS >= uint64(set.YMB)*mib)
}

// residentMinAge is how long a launchd child must have run, in windows, to count as resident: its memory is then what the app
// holds, not what a command grew.
const residentMinAge = 10

// simulatorComm is the root of an iOS simulator's process tree. CoreSimulator starts it (not launchd, not from /Applications), and
// its ~100 daemons share the dyld cache and runtime images, so the tree's memory is not a sum of anything: only its cores count.
const simulatorComm = "launchd_sim"

// residentAndQuiet says that the tree that crossed a threshold is a resident app and only its memory is over: its root is a child of
// launchd (pid 1), runs a program of /Applications, started more than residentMinAge windows ago, and its mean cores alone would not
// have crossed x_cores. Herd's php-fpm workers (1.0–1.3 GB of summed RSS, 0.2 cores, up for days) and a browser at rest are such
// trees (#2965; the spec says a long-lived low-usage process is not heavy). A resident app that burns CPU still raises its event.
// The Diag line is the record; nothing is learned about a tree whose start or command line cannot be read.
func (d *Detector) residentAndQuiet(in Input, tab *table, root int, st *treeState, set Settings) bool {
	if in.Started == nil || in.Argv == nil || tab.byPID[root].PPID != 1 {
		return false
	}
	argv := in.Argv(root)
	if len(argv) == 0 || !strings.HasPrefix(argv[0], "/Applications/") {
		return false
	}
	started := in.Started(root)
	if started.IsZero() || in.Now.Sub(started) < residentMinAge*set.TS {
		return false
	}
	cpuOnly := set
	cpuOnly.YMB = 0
	if d.overThreshold(st, in.Now, cpuOnly) {
		return false
	}
	if d.Diag != nil {
		d.Diag("resident app", fmt.Sprintf("%s (pid %d) holds %d MB, up %s: a resident app is not a heavy command, no event",
			filepath.Base(argv[0]), root, peakMB(st), in.Now.Sub(started).Round(time.Minute)))
	}
	return true
}

func peakMB(st *treeState) uint64 {
	var mb uint64
	for _, s := range st.samples {
		mb = max(mb, s.rss/mib)
	}
	return mb
}

func (d *Detector) treeEvent(in Input, tab *table, root int, st *treeState) Event {
	ev := Event{Kind: KindUnmanagedHeavy, At: in.Now, Duration: in.Now.Sub(st.since)}
	for _, s := range st.samples {
		if s.cores > ev.PeakCores {
			ev.PeakCores = s.cores
		}
		if mb := s.rss / mib; mb > ev.PeakMB {
			ev.PeakMB = mb
		}
	}
	// the session it belongs to: the nearest ancestor that is a session's root process
	byPID := map[int]resources.Root{}
	for _, r := range in.Roots {
		if _, dup := byPID[r.PID]; !dup {
			byPID[r.PID] = r
		}
	}
	var session resources.Root
	tab.ancestors(root, func(p resources.Proc) bool {
		r, ok := byPID[p.PID]
		if ok {
			session = r
		}
		return ok
	})
	ev.Session, ev.Cwd = session.SessionID, session.Cwd
	if in.Cwd != nil {
		if c := in.Cwd(root); c != "" {
			ev.Cwd = c
		}
	}
	var argv []string
	if in.Argv != nil {
		argv = in.Argv(root)
	}
	ev.Argv = argv
	ev.Signature = Signature(argv)
	ev.Label = labelOf(argv, ev.Session != "")
	if tab.hasJobMark(root, in.JobMark) {
		ev.Label = LabelWWDJob // whatever the gate would have said of its command: it is the worker's own job
	}
	if ev.Signature != "" {
		ev.New = d.mem.Sighting(ev.Signature, in.Now)
	}
	return ev
}

// labelOf says why a command ran outside the gate, from the gate's own classification.
func labelOf(argv []string, inSession bool) Label {
	if len(argv) == 0 {
		return LabelUnknown
	}
	class, reason := gate.ClassifyReason(argv, gate.Env{Agent: inSession})
	switch {
	case class.Valid():
		return LabelGateGap
	case reason == gate.ReasonUnknown:
		return LabelUnknown
	}
	return LabelAllowed
}

// ---- E2 ----

func (d *Detector) pressureEvents(in Input) []Event {
	set := in.Set
	var events []Event
	// A run belongs to one source: when the signals change hands, a run not yet confirmed starts over (a window is never made of two
	// observers), and the Raw counters' baseline with it. A run that has fired stays fired.
	if fromHost := in.Host != nil; d.e2Seen && d.e2Host != fromHost {
		d.pressure.breakRun()
		d.pages.breakRun()
		d.havePages = false
	}
	d.e2Seen, d.e2Host = true, in.Host != nil
	if in.Host != nil {
		if in.Host.At.Equal(d.lastHostAt) {
			return nil // the facts seen before: nothing new was observed
		}
		d.lastHostAt = in.Host.At
	}
	level := in.Raw.Pressure
	if in.Host != nil {
		level = in.Host.Pressure
	}
	switch {
	case in.Host != nil && level == 0: // wwd does not know the level: not a low one
		d.pressure.breakRun()
	default:
		if d.pressure.step(in.Now, set.PressureLevel > 0 && level >= set.PressureLevel, set.PressureTS, set.PressureTS) {
			events = append(events, Event{Kind: KindPressureHigh, At: in.Now, Pressure: level,
				Detail: fmt.Sprintf("pressure level %d for %s", level, set.PressureTS)})
		}
	}

	rate, over := 0.0, false
	switch {
	case in.Host != nil: // wwd's rate: no counters to take a difference of, and the running baseline starts over when Raw is read again
		d.havePages = false
		if !in.Host.PageRateKnown {
			d.pages.breakRun() // not known is not low
			return events
		}
		rate = in.Host.PageRate
		over = set.PageRate > 0 && rate > set.PageRate
	case in.Raw.PageCounters:
		total := in.Raw.Pageouts + in.Raw.Swapouts
		if d.havePages && in.Now.After(d.lastPagesAt) && total >= d.lastPages {
			rate = float64(total-d.lastPages) / in.Now.Sub(d.lastPagesAt).Seconds()
			over = set.PageRate > 0 && rate > set.PageRate
		}
		d.havePages, d.lastPages, d.lastPagesAt = true, total, in.Now // a counter that went back (a reboot) is a new baseline
	default:
		d.havePages = false
	}
	if d.pages.step(in.Now, over, set.PageTS, set.PageTS) {
		events = append(events, Event{Kind: KindPressureHigh, At: in.Now, Pressure: level, PageRate: rate,
			Detail: fmt.Sprintf("page rate %.0f/s for %s", rate, set.PageTS)})
	}
	return events
}

// ---- E3, E4 ----

// holderKey names one holder of a slot for its lifetime: class-slot, the gate's pid and when it took the slot, to the nanosecond.
func holderKey(h gate.Holder) string {
	return fmt.Sprintf("%s-%d:%d:%d", h.Class, h.Slot, h.PID, h.Started.UnixNano())
}

func (d *Detector) slotEvents(in Input) []Event {
	set := in.Set
	holders := slices.Clone(in.Holders)
	sort.Slice(holders, func(i, j int) bool { return holderKey(holders[i]) < holderKey(holders[j]) })

	var events []Event
	present := map[string]bool{}
	orphaned := map[string]bool{}
	lockLookups := 0
	var unmeasured []string
	for _, h := range holders {
		key := holderKey(h)
		present[key] = true
		// A slot held with no record, or one whose record has no start or no pid, has nothing to measure: Now minus a zero time is 292 years
		// (#3049). It is no event; the diagnostic below says it is there.
		if h.NoRecord || h.Started.IsZero() || h.PID <= 0 {
			unmeasured = append(unmeasured, fmt.Sprintf("%s-%d", h.Class, h.Slot))
			continue
		}
		base := Event{At: in.Now, Slot: fmt.Sprintf("%s-%d", h.Class, h.Slot), HolderPID: h.PID, Argv: h.Argv, Cwd: h.Cwd,
			Session: h.Session, Duration: in.Now.Sub(h.Started)}
		if h.Orphaned {
			orphaned[key] = true
			since := d.mem.OrphanSince(key, in.Now)
			if set.OrphanAfter > 0 && in.Now.Sub(since) >= set.OrphanAfter && !d.mem.Reported(KindSlotOrphaned, key) {
				d.mem.MarkReported(KindSlotOrphaned, key)
				ev := base
				ev.Kind = KindSlotOrphaned
				ev.Duration = in.Now.Sub(since)
				if in.LockPIDs != nil && lockLookups == 0 { // lsof runs at most once per pass
					lockLookups++
					ev.LockPIDs = in.LockPIDs(h)
				}
				events = append(events, ev)
			}
		}
		if set.SlotLongAfter > 0 && in.Now.Sub(h.Started) >= set.SlotLongAfter && !d.mem.Reported(KindSlotLong, key) {
			d.mem.MarkReported(KindSlotLong, key)
			ev := base
			ev.Kind = KindSlotLong
			events = append(events, ev)
		}
	}
	if d.Diag != nil {
		problem := ""
		if len(unmeasured) > 0 {
			problem = "held with no readable start or pid, not measured: " + strings.Join(unmeasured, ", ")
		}
		d.Diag("slot records", problem)
	}
	// A holder that is not orphaned in one pass keeps its first-seen time for orphanGrace passes (the reading may flicker); one
	// that is gone altogether, or that stays not orphaned, is forgotten, also one remembered from before a restart.
	if !d.started {
		// after a restart the memory may hold first-seen times this detector has no miss count for: a holder that is there and
		// reads as not orphaned in the first pass gets the same grace as any other
		d.started = true
		for key := range present {
			if !orphaned[key] {
				d.orphanMiss[key] = 0
			}
		}
	}
	keep := map[string]bool{}
	for key := range orphaned {
		keep[key] = true
		d.orphanMiss[key] = 0
	}
	for key, miss := range d.orphanMiss {
		if orphaned[key] {
			continue
		}
		if !present[key] || miss+1 > orphanGrace {
			delete(d.orphanMiss, key)
			continue
		}
		d.orphanMiss[key] = miss + 1
		keep[key] = true
	}
	d.mem.KeepOrphans(keep)
	d.mem.KeepReported(present) // a holder that is gone is forgotten, so a new one of the same slot is reported
	// E3 before E4, each in key order
	sort.SliceStable(events, func(i, j int) bool { return events[i].Kind == KindSlotOrphaned && events[j].Kind == KindSlotLong })
	return events
}
