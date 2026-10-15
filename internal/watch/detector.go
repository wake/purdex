package watch

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/resources"
)

const mib = 1 << 20

// episode is the "one event when it crosses, none while it stays over" rule: fireAfter is how long the condition must hold
// (continuously) before the event, rearmAfter how long it must stay false before it can fire again.
type episode struct {
	since, underSince time.Time
	over              bool
}

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
	since   time.Time
	samples []sample // those of the last TS
	ep      episode
}

// Detector holds what the passes share: the windows of the trees, the episodes of E2, and which holders have been reported.
type Detector struct {
	mem   Memory
	trees map[treeKey]*treeState

	pressure, pages episode
	havePages       bool
	lastPages       uint64
	lastPagesAt     time.Time

	orphanFired, longFired map[string]bool
}

// New is a detector over a Memory.
func New(mem Memory) *Detector {
	return &Detector{mem: mem, trees: map[treeKey]*treeState{}, orphanFired: map[string]bool{}, longFired: map[string]bool{}}
}

// Pass reads one tick and returns the events it makes, E1 first (by root pid), then E2, E3, E4. It is called on every sampler
// tick that sampled; a tick that failed is not passed, and the windows simply see the gap.
func (d *Detector) Pass(in Input) []Event {
	events := d.unmanagedTrees(in)
	events = append(events, d.pressureEvents(in)...)
	events = append(events, d.slotEvents(in)...)
	return events
}

// ---- E1 ----

func (d *Detector) unmanagedTrees(in Input) []Event {
	set := in.Set
	if set.TS <= 0 || (set.XCores <= 0 && set.YMB <= 0) {
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
		if p.Comm != "pdx" || in.Argv == nil {
			return false
		}
		if v, ok := gateProc[p.PID]; ok {
			return v
		}
		argv := in.Argv(p.PID)
		v := len(argv) >= 2 && argv[1] == "gate"
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
			st = &treeState{since: in.Now}
			d.trees[key] = st
		}
		u := uses[root]
		st.samples = append(st.samples, sample{in.Now, u.cores, u.rss})
		cut := 0
		for cut < len(st.samples) && !st.samples[cut].at.After(in.Now.Add(-set.TS)) {
			cut++
		}
		st.samples = st.samples[cut:]
		over := d.overThreshold(st, in.Now, set)
		if st.ep.step(in.Now, over, 0, set.TS) {
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

// overThreshold: the whole window has been seen, and its mean cores are at or above x_cores or its resident memory was at
// or above y_mb throughout.
func (d *Detector) overThreshold(st *treeState, now time.Time, set Settings) bool {
	if now.Sub(st.since) < set.TS || len(st.samples) == 0 {
		return false
	}
	var sum float64
	minRSS := st.samples[0].rss
	for _, s := range st.samples {
		sum += s.cores
		if s.rss < minRSS {
			minRSS = s.rss
		}
	}
	mean := sum / float64(len(st.samples))
	return (set.XCores > 0 && mean >= set.XCores) || (set.YMB > 0 && minRSS >= uint64(set.YMB)*mib)
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
	level := in.Raw.Pressure
	if d.pressure.step(in.Now, set.PressureLevel > 0 && level >= set.PressureLevel, set.PressureTS, set.PressureTS) {
		events = append(events, Event{Kind: KindPressureHigh, At: in.Now, Pressure: level,
			Detail: fmt.Sprintf("pressure level %d for %s", level, set.PressureTS)})
	}

	rate, over := 0.0, false
	if in.Raw.PageCounters {
		total := in.Raw.Pageouts + in.Raw.Swapouts
		if d.havePages && in.Now.After(d.lastPagesAt) && total >= d.lastPages {
			rate = float64(total-d.lastPages) / in.Now.Sub(d.lastPagesAt).Seconds()
			over = set.PageRate > 0 && rate > set.PageRate
		}
		d.havePages, d.lastPages, d.lastPagesAt = true, total, in.Now // a counter that went back (a reboot) is a new baseline
	} else {
		d.havePages = false
	}
	if d.pages.step(in.Now, over, set.PageTS, set.PageTS) {
		events = append(events, Event{Kind: KindPressureHigh, At: in.Now, Pressure: level, PageRate: rate,
			Detail: fmt.Sprintf("page rate %.0f/s for %s", rate, set.PageTS)})
	}
	return events
}

// ---- E3, E4 ----

func holderKey(h gate.Holder) string {
	return fmt.Sprintf("%s-%d:%d:%d", h.Class, h.Slot, h.PID, h.Started.Unix())
}

func (d *Detector) slotEvents(in Input) []Event {
	set := in.Set
	holders := slices.Clone(in.Holders)
	sort.Slice(holders, func(i, j int) bool { return holderKey(holders[i]) < holderKey(holders[j]) })

	var events []Event
	present := map[string]bool{}
	orphaned := map[string]bool{}
	lockLookups := 0
	for _, h := range holders {
		key := holderKey(h)
		present[key] = true
		base := Event{At: in.Now, Slot: fmt.Sprintf("%s-%d", h.Class, h.Slot), HolderPID: h.PID, Argv: h.Argv, Cwd: h.Cwd,
			Session: h.Session, Duration: in.Now.Sub(h.Started)}
		if h.Orphaned {
			orphaned[key] = true
			since := d.mem.OrphanSince(key, in.Now)
			if set.OrphanAfter > 0 && in.Now.Sub(since) >= set.OrphanAfter && !d.orphanFired[key] {
				d.orphanFired[key] = true
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
		if set.SlotLongAfter > 0 && in.Now.Sub(h.Started) >= set.SlotLongAfter && !d.longFired[key] {
			d.longFired[key] = true
			ev := base
			ev.Kind = KindSlotLong
			events = append(events, ev)
		}
	}
	d.mem.KeepOrphans(orphaned) // a holder no longer orphaned, or gone, is forgotten (also one from before a restart)
	for key := range d.longFired {
		if !present[key] {
			delete(d.longFired, key)
		}
	}
	for key := range d.orphanFired {
		if !present[key] {
			delete(d.orphanFired, key)
		}
	}
	// E3 before E4, each in key order
	sort.SliceStable(events, func(i, j int) bool { return events[i].Kind == KindSlotOrphaned && events[j].Kind == KindSlotLong })
	return events
}
