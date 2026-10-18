package watch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/resources"
)

var t0 = time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)

const tick = 5 * time.Second

// rig is a detector with a clock that moves by a tick per pass.
type rig struct {
	t   *testing.T
	d   *Detector
	mem *MemoryStore
	now time.Time
	set Settings
	// what every pass reads, set by the test
	procs   []resources.Proc
	raw     resources.HostRaw
	holders []gate.Holder
	roots   []resources.Root
	host    *HostSignals // wwd's pressure and paging, when the test wants E2 to read them
	argv    map[int][]string
	gates   map[int]bool // the pids the caller verified as a genuine pdx gate
	cwd     map[int]string
	started map[int]time.Time // when each pid started; absent = unknown
	lockPID []int
	locks   int
	// fp, when non-nil, backs the Footprint callback: a pid absent from it is "not ok"; fpCalls counts the asks.
	fp      map[int]uint64
	fpCalls int
}

func newRig(t *testing.T) *rig {
	mem := NewMemoryStore()
	return &rig{t: t, d: New(mem), mem: mem, now: t0, set: DefaultSettings(), argv: map[int][]string{}, gates: map[int]bool{}, cwd: map[int]string{}, started: map[int]time.Time{}}
}

// pass runs one pass at the current time and moves the clock on by a tick.
func (r *rig) pass() []Event {
	in := Input{Now: r.now, Procs: r.procs, Raw: r.raw, Host: r.host, Holders: r.holders, Roots: r.roots, Set: r.set,
		Argv:    func(pid int) []string { return r.argv[pid] },
		IsGate:  func(pid int) bool { return r.gates[pid] },
		Cwd:     func(pid int) string { return r.cwd[pid] },
		Started: func(pid int) time.Time { return r.started[pid] },
		LockPIDs: func(gate.Holder) []int {
			r.locks++
			return r.lockPID
		},
	}
	if r.fp != nil {
		in.Footprint = func(pid int) (uint64, bool) {
			r.fpCalls++
			v, ok := r.fp[pid]
			return v, ok
		}
	}
	ev := r.d.Pass(in)
	r.now = r.now.Add(tick)
	return ev
}

// run passes for a duration and returns every event, with the time offset of each pass that made one.
func (r *rig) run(d time.Duration) []Event {
	var all []Event
	for end := r.now.Add(d); r.now.Before(end); {
		all = append(all, r.pass()...)
	}
	return all
}

func p(pid, ppid int, comm string, pcpu float64, rssMB uint64) resources.Proc {
	return resources.Proc{PID: pid, PPID: ppid, Comm: comm, Pcpu: pcpu, RSSBytes: rssMB * mib}
}

// a shell (pid 10) with a `go test` below it that uses cores×100 percent and rssMB.
func goTest(cores float64, rssMB uint64) []resources.Proc {
	return []resources.Proc{
		p(1, 0, "launchd", 0, 10),
		p(10, 1, "zsh", 0, 5),
		p(100, 10, "go", cores*100/2, rssMB/2),
		p(101, 100, "compile", cores*100/2, rssMB/2),
	}
}

// ---- E1: the thresholds ----

func TestE1_NothingUnderTheThresholds(t *testing.T) {
	r := newRig(t)
	r.procs = goTest(1.4, 1000) // 1.4 cores (< 1.5), 1000 MB (< 1024)
	assert.Empty(t, r.run(10*time.Minute), "a tree under both thresholds is not an event however long it lives (D11)")
}

func TestE1_CoresAtTheThresholdFireAfterTheWindow(t *testing.T) {
	r := newRig(t)
	r.procs = goTest(1.5, 100) // exactly x_cores: the comparison is >=
	r.argv[100] = []string{"go", "test", "./..."}
	var at []time.Duration
	for i := 0; i < 20; i++ {
		if ev := r.pass(); len(ev) > 0 {
			at = append(at, r.now.Add(-tick).Sub(t0))
			assert.Equal(t, KindUnmanagedHeavy, ev[0].Kind)
		}
	}
	assert.Equal(t, []time.Duration{60 * time.Second}, at, "one event, when the 60 s window has been seen and not before")
}

func TestE1_MemoryAtTheThresholdFiresWhenItHeldThroughoutTheWindow(t *testing.T) {
	r := newRig(t)
	r.procs = goTest(0.1, 1024) // exactly y_mb
	r.argv[100] = []string{"go", "test", "./..."}
	ev := r.run(2 * time.Minute)
	require.Len(t, ev, 1)
	assert.Equal(t, uint64(1024), ev[0].PeakMB)

	r2 := newRig(t) // a dip below y_mb inside the window: no event while the dip is in it
	for i := 0; i < 20; i++ {
		mb := uint64(2000)
		if i == 8 {
			mb = 900
		}
		r2.procs = goTest(0.1, mb)
		assert.Empty(t, r2.pass(), "pass %d", i)
	}
	r2.procs = goTest(0.1, 2000)
	assert.Len(t, r2.run(10*time.Second), 1, "once the dip has left the window it has been over throughout")
}

func TestE1_MeanCoresNotAPeak(t *testing.T) {
	r := newRig(t)
	// 3 cores for 20 s, then 0.1: the mean over the window stays under 1.5 once the burst has been diluted
	for i := 0; i < 12*3; i++ {
		c := 0.1
		if i%12 < 4 {
			c = 3 // 20 s in every minute: mean 0.1*40/60+3*20/60 = 1.07
		}
		r.procs = goTest(c, 100)
		assert.Empty(t, r.pass(), "pass %d", i)
	}
}

// ---- E1: episodes ----

func TestE1_OneEventWhileOverThenRearmedAfterAWindowUnder(t *testing.T) {
	r := newRig(t)
	r.argv[100] = []string{"go", "test", "./..."}
	r.procs = goTest(3, 100)
	require.Len(t, r.run(3*time.Minute), 1, "it stays over for minutes: one event")

	// under the threshold for a full window: re-armed, then over again: a second event
	r.procs = goTest(0.1, 100)
	assert.Empty(t, r.run(75*time.Second))
	r.procs = goTest(3, 100)
	assert.Len(t, r.run(90*time.Second), 1, "a new episode")
}

func TestE1_ADipShorterThanAWindowIsTheSameEpisode(t *testing.T) {
	r := newRig(t)
	r.argv[100] = []string{"go", "test", "./..."}
	r.procs = goTest(3, 100)
	require.Len(t, r.run(90*time.Second), 1)
	r.procs = goTest(0.1, 100)
	r.run(10 * time.Second)
	r.procs = goTest(3, 100)
	assert.Empty(t, r.run(3*time.Minute), "it did not stay under for t_s: no second event")
}

func TestE1_ATreeThatEndsStartsOverWhenItComesBack(t *testing.T) {
	r := newRig(t)
	r.argv[100] = []string{"go", "test", "./..."}
	r.procs = goTest(3, 100)
	require.Len(t, r.run(90*time.Second), 1)
	r.procs = []resources.Proc{p(1, 0, "launchd", 0, 10), p(10, 1, "zsh", 0, 5)} // the command ended
	assert.Empty(t, r.run(10*time.Second))
	r.procs = goTest(3, 100)
	assert.Empty(t, r.run(50*time.Second), "a new tree needs its own window")
	assert.Len(t, r.run(20*time.Second), 1)
}

// ---- E1: managed ----

func TestE1_ATreeUnderAGateIsManaged(t *testing.T) {
	r := newRig(t)
	r.procs = []resources.Proc{
		p(1, 0, "launchd", 0, 10), p(10, 1, "zsh", 0, 5),
		p(100, 10, "pdx", 1, 50), // pdx gate
		p(101, 100, "go", 200, 500),
		p(102, 101, "compile", 200, 500),
	}
	r.gates[100] = true
	assert.Empty(t, r.run(5*time.Minute))

	// a process that is called pdx and has `gate` for its argument is not a gate unless the caller verified it (its executable):
	// the name is a filter, not an identity
	r2 := newRig(t)
	r2.procs = r.procs
	r2.argv[100] = []string{"pdx", "gate", "--", "go", "test"}
	assert.Len(t, r2.run(2*time.Minute), 1, "a forged gate does not hide a tree")
	// a process that is not called pdx is never asked about
	r3 := newRig(t)
	r3.procs = []resources.Proc{p(1, 0, "launchd", 0, 10), p(10, 1, "zsh", 0, 5), p(100, 10, "node", 1, 50), p(101, 100, "go", 400, 500)}
	r3.gates[100] = true
	assert.Len(t, r3.run(2*time.Minute), 1)
}

func TestE1_AGateFurtherUpIsAnAncestor(t *testing.T) {
	r := newRig(t)
	r.procs = []resources.Proc{
		p(1, 0, "launchd", 0, 10), p(10, 1, "zsh", 0, 5),
		p(100, 10, "pdx", 1, 50),
		p(101, 100, "zsh", 0, 5), // the command's own shell: its children are roots, below the gate
		p(102, 101, "go", 300, 500),
	}
	r.gates[100] = true
	assert.Empty(t, r.run(5*time.Minute), "the gate is an ancestor of the tree's root")
}

func TestE1_ATreeThatHoldsASlotIsManagedEvenWithoutAGateAncestor(t *testing.T) {
	r := newRig(t)
	r.procs = goTest(3, 100) // the gate died and its command lives on (its pid is the holder's PGID)
	r.holders = []gate.Holder{{Class: gate.ClassHeavy, Slot: 1, PID: 9999, PGID: 101, Started: t0}}
	assert.Empty(t, r.run(5*time.Minute))
	r.holders = []gate.Holder{{Class: gate.ClassHeavy, Slot: 1, PID: 100, PGID: 9998, Started: t0}} // the gate's own pid in the tree
	r2 := newRig(t)
	r2.procs, r2.holders = r.procs, r.holders
	assert.Empty(t, r2.run(5*time.Minute))
	r3 := newRig(t) // a holder whose pids are in no tree
	r3.procs = r.procs
	r3.holders = []gate.Holder{{Class: gate.ClassHeavy, Slot: 1, PID: 7777, PGID: 7778, Started: t0}}
	assert.Len(t, r3.run(2*time.Minute), 1)
}

// ---- E1: trees, sessions, labels ----

func TestE1_TreesAreTheChildrenOfShellsAndAreNotCountedTwice(t *testing.T) {
	r := newRig(t)
	// A session: a terminal's zsh runs claude (a root), whose Bash tool shell runs `go test` (another root) and an idle MCP
	// server. The go tree is its own; claude's tree holds the rest.
	r.procs = []resources.Proc{
		p(1, 0, "launchd", 0, 10),
		p(10, 1, "zsh", 0, 5),
		p(50, 10, "claude", 20, 400),
		p(51, 50, "node", 5, 100), // an MCP server
		p(52, 50, "zsh", 0, 5),    // the tool's shell
		p(53, 52, "go", 150, 300), // root: the child of a shell
		p(54, 53, "compile", 150, 300),
	}
	r.argv[53] = []string{"go", "test", "./..."}
	r.roots = []resources.Root{{SessionID: "s1", PID: 50, Cwd: "/work/purdex"}}
	ev := r.run(2 * time.Minute)
	require.Len(t, ev, 1, "only the go tree is over; the session's own tree (0.25 cores) is not")
	assert.Equal(t, "go test", ev[0].Signature)
	assert.Equal(t, "s1", ev[0].Session)
	assert.Equal(t, "/work/purdex", ev[0].Cwd, "no cwd lookup answers: the session's")
	assert.InDelta(t, 3.0, ev[0].PeakCores, 0.001)
	assert.Equal(t, []string{"go", "test", "./..."}, ev[0].Argv)

	r.cwd[53] = "/work/purdex/subdir"
	r.procs[5].Pcpu = 0
	r.procs[6].Pcpu = 0
	r.run(80 * time.Second) // under for a window: re-armed
	r.procs[5].Pcpu, r.procs[6].Pcpu = 150, 150
	ev = r.run(2 * time.Minute)
	require.Len(t, ev, 1)
	assert.Equal(t, "/work/purdex/subdir", ev[0].Cwd, "the lookup's answer comes first")
}

func TestE1_NoShellNoRootExceptLaunchdAndTmux(t *testing.T) {
	r := newRig(t)
	// a process whose parent is neither a shell nor launchd/tmux belongs to no tree: nothing to report
	r.procs = []resources.Proc{p(1, 0, "launchd", 0, 10), p(20, 1, "daemon-x", 1, 10), p(21, 20, "worker", 400, 4000)}
	// 20 is a child of launchd: a root; 21 is in its tree
	r.argv[20] = []string{"daemon-x"}
	assert.Len(t, r.run(2*time.Minute), 1)
	r2 := newRig(t)
	r2.procs = []resources.Proc{p(30, 29, "orphan", 400, 4000)} // its parent is not in the table
	assert.Empty(t, r2.run(2*time.Minute))
	r3 := newRig(t)
	r3.procs = []resources.Proc{p(1, 0, "launchd", 0, 1), p(5, 1, "tmux", 0, 5), p(6, 5, "zsh", 0, 5), p(7, 6, "vitest", 300, 800)}
	r3.argv[7] = []string{"vitest", "run"}
	assert.Len(t, r3.run(2*time.Minute), 1, "a window's shell is a child of tmux, and its command a child of that shell")
}

func TestE1_ALoopInTheTableEndsTheWalk(t *testing.T) {
	r := newRig(t)
	r.procs = []resources.Proc{p(2, 3, "a", 500, 10), p(3, 2, "b", 500, 10), p(4, 4, "self", 500, 10)}
	assert.NotPanics(t, func() { r.run(2 * time.Minute) })
}

func TestE1_LabelsSayWhyTheGateDidNotTakeIt(t *testing.T) {
	for _, c := range []struct {
		argv  []string
		label Label
	}{
		{[]string{"go", "test", "./..."}, LabelGateGap},
		{[]string{"vendor/bin/phpunit", "--testsuite", "integration"}, LabelGateGap},
		{[]string{"composer", "test:integration"}, LabelGateGap},
		{[]string{"pnpm", "run", "electron:build"}, LabelAllowed},
		{[]string{"go", "build", "-o", "bin/pdx", "./cmd/pdx"}, LabelAllowed},
		{[]string{"vite", "build"}, LabelAllowed},
		{[]string{"pnpm", "install"}, LabelAllowed},
		{[]string{"rsync", "-a", "a", "b"}, LabelUnknown},
		{[]string{"pnpm", "my-script"}, LabelUnknown},
		{nil, LabelUnknown},
	} {
		r := newRig(t)
		r.procs = goTest(3, 100)
		r.argv[100] = c.argv
		ev := r.run(2 * time.Minute)
		require.Len(t, ev, 1, "%v", c.argv)
		assert.Equal(t, c.label, ev[0].Label, "%v", c.argv)
	}
}

func TestE1_NewIsTheSignaturesFirstSightingOnTheHost(t *testing.T) {
	r := newRig(t)
	r.argv[100] = []string{"composer", "test:integration"}
	r.procs = goTest(3, 100)
	ev := r.run(90 * time.Second)
	require.Len(t, ev, 1)
	assert.True(t, ev[0].New)
	assert.Equal(t, "composer test:integration", ev[0].Signature)

	r.procs = goTest(0.1, 100)
	r.run(80 * time.Second)
	r.argv[100] = []string{"composer", "test:integration", "--no-ansi"} // other arguments, the same signature
	r.procs = goTest(3, 100)
	ev = r.run(90 * time.Second)
	require.Len(t, ev, 1)
	assert.False(t, ev[0].New)
	assert.Equal(t, 2, r.mem.Count("composer test:integration"), "later sightings are counted")
}

// ---- E2 ----

func TestE2_PressureForALongEnoughWhile(t *testing.T) {
	r := newRig(t)
	r.raw = resources.HostRaw{Pressure: 2} // exactly the level: >=
	var at []time.Duration
	for i := 0; i < 40; i++ {
		if ev := r.pass(); len(ev) > 0 {
			at = append(at, r.now.Add(-tick).Sub(t0))
			assert.Equal(t, KindPressureHigh, ev[0].Kind)
			assert.Equal(t, 2, ev[0].Pressure)
			assert.Contains(t, ev[0].Detail, "pressure level 2")
		}
	}
	assert.Equal(t, []time.Duration{120 * time.Second}, at)

	r2 := newRig(t) // one pass of level 1 inside the window starts it over
	for i := 0; i < 40; i++ {
		r2.raw.Pressure = 2
		if i == 15 {
			r2.raw.Pressure = 1
		}
		if ev := r2.pass(); len(ev) > 0 {
			assert.Greater(t, i, 15+23, "the window restarted at the dip")
		}
	}
	r3 := newRig(t)
	r3.raw = resources.HostRaw{Pressure: 1}
	assert.Empty(t, r3.run(10*time.Minute))
}

func TestE2_PageRateAboveTheRateForAWhile(t *testing.T) {
	counters := func(r *rig, perSecond uint64) {
		r.raw.PageCounters = true
		r.raw.Pageouts += perSecond * uint64(tick/time.Second)
	}
	r := newRig(t)
	var events []Event
	for i := 0; i < 30; i++ {
		counters(r, 51) // above 50/s
		events = append(events, r.pass()...)
	}
	require.Len(t, events, 1)
	assert.InDelta(t, 51, events[0].PageRate, 0.001)
	assert.Contains(t, events[0].Detail, "page rate 51/s")

	r2 := newRig(t) // exactly the rate does not fire: >
	for i := 0; i < 40; i++ {
		counters(r2, 50)
		assert.Empty(t, r2.pass())
	}
	r3 := newRig(t) // a counter that goes back is a new baseline, not a huge rate and not a negative one
	r3.raw.PageCounters = true
	r3.raw.Pageouts = 1_000_000
	r3.pass()
	r3.raw.Pageouts = 10
	for i := 0; i < 40; i++ {
		r3.raw.Pageouts += 10
		assert.Empty(t, r3.pass())
	}
	r4 := newRig(t) // no counters: no page-rate events however the pressure is
	r4.raw.Pageouts = 1 << 40
	assert.Empty(t, r4.run(10*time.Minute))
}

// E2 from wwd: the pressure level and the paging rate come from Input.Host and Raw's are not read.
func TestE2_FromWWDSignals(t *testing.T) {
	observing := func(r *rig, h HostSignals, d time.Duration) []Event { // a new fact every pass
		var all []Event
		for end := r.now.Add(d); r.now.Before(end); {
			all = append(all, r.observe(h)...)
		}
		return all
	}
	r := newRig(t) // wwd says level 2 (the default limit) while Raw says normal
	r.raw = resources.HostRaw{Pressure: 1}
	events := observing(r, HostSignals{Pressure: 2}, 10*time.Minute)
	require.Len(t, events, 1)
	assert.Equal(t, KindPressureHigh, events[0].Kind)
	assert.Equal(t, 2, events[0].Pressure)

	r2 := newRig(t) // Raw says level 4, wwd says normal: wwd wins
	r2.raw = resources.HostRaw{Pressure: 4}
	assert.Empty(t, observing(r2, HostSignals{Pressure: 1}, 10*time.Minute))

	r3 := newRig(t) // the rate is wwd's own: above the limit fires, exactly the limit does not
	events = observing(r3, HostSignals{Pressure: 1, PageRate: 51, PageRateKnown: true}, 3*time.Minute)
	require.Len(t, events, 1)
	assert.InDelta(t, 51, events[0].PageRate, 0.001)
	assert.Contains(t, events[0].Detail, "page rate 51/s")
	r4 := newRig(t)
	assert.Empty(t, observing(r4, HostSignals{Pressure: 1, PageRate: 50, PageRateKnown: true}, 5*time.Minute))

	r5 := newRig(t) // a platform whose facts carry no rate: Raw's counters are not used instead
	r5.raw = resources.HostRaw{PageCounters: true}
	for i := 0; i < 60; i++ {
		r5.raw.Pageouts += 1_000_000
		assert.Empty(t, r5.observe(HostSignals{Pressure: 1, PageRate: 1000}), "an unknown rate is not read, whatever the field holds")
	}
}

// observe makes one pass with a new wwd observation (stamped now), the way a fresh fact arrives.
func (r *rig) observe(h HostSignals) []Event {
	h.At = r.now
	r.host = &h
	return r.pass()
}

// A fact that is only seen again is not a new observation: a stopped wwd (or a pool answer repeated within its 20 s) does not make
// one high sample last for the whole window.
func TestE2_ARepeatedFactIsNotANewObservation(t *testing.T) {
	r := newRig(t)
	r.host = &HostSignals{Pressure: 2, At: t0}
	var events []Event
	for i := 0; i < 60; i++ { // five minutes of passes over the one fact
		events = append(events, r.pass()...)
	}
	assert.Empty(t, events)

	r2 := newRig(t) // the same level, a new fact each pass: an event
	var events2 []Event
	for i := 0; i < 60; i++ {
		events2 = append(events2, r2.observe(HostSignals{Pressure: 2})...)
	}
	assert.Len(t, events2, 1)
}

// Unknown is not low: it breaks a run that has not been confirmed, and it does not count towards re-arming a fired one.
func TestE2_UnknownBreaksARunAndDoesNotRearm(t *testing.T) {
	r := newRig(t) // 100 s of level 2, one unknown, then 100 s more: no run of 120 s, so no event
	for i := 0; i < 20; i++ {
		assert.Empty(t, r.observe(HostSignals{Pressure: 2}))
	}
	assert.Empty(t, r.observe(HostSignals{Pressure: 0}))
	for i := 0; i < 20; i++ {
		assert.Empty(t, r.observe(HostSignals{Pressure: 2}))
	}

	r2 := newRig(t) // fired, then a long unknown, then high again: no second event (it never saw the pressure go down)
	var fired []Event
	for i := 0; i < 30; i++ {
		fired = append(fired, r2.observe(HostSignals{Pressure: 2})...)
	}
	require.Len(t, fired, 1)
	for i := 0; i < 120; i++ {
		assert.Empty(t, r2.observe(HostSignals{Pressure: 0}))
	}
	for i := 0; i < 60; i++ {
		assert.Empty(t, r2.observe(HostSignals{Pressure: 2}), "no event for a pressure that was never seen to end")
	}

	r3 := newRig(t) // the same with a real low spell in between: a second event
	for i := 0; i < 30; i++ {
		r3.observe(HostSignals{Pressure: 2})
	}
	for i := 0; i < 120; i++ {
		r3.observe(HostSignals{Pressure: 1})
	}
	var again []Event
	for i := 0; i < 60; i++ {
		again = append(again, r3.observe(HostSignals{Pressure: 2})...)
	}
	assert.Len(t, again, 1)

	r4 := newRig(t) // the page rate is the same: an unknown rate breaks the run
	r4.set.PageTS = time.Minute
	for i := 0; i < 8; i++ {
		assert.Empty(t, r4.observe(HostSignals{PageRate: 99, PageRateKnown: true}))
	}
	assert.Empty(t, r4.observe(HostSignals{}))
	for i := 0; i < 8; i++ {
		assert.Empty(t, r4.observe(HostSignals{PageRate: 99, PageRateKnown: true}))
	}
}

// The unknown in the middle of a re-arming spell starts the spell again (underSince is cleared too): 100 s low, an unknown, 40 s
// low is not 120 s of low.
func TestE2_UnknownRestartsTheRearmingSpell(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 30; i++ {
		r.observe(HostSignals{Pressure: 2}) // fires
	}
	for i := 0; i < 20; i++ {
		r.observe(HostSignals{Pressure: 1}) // 100 s of the 120 s to re-arm
	}
	r.observe(HostSignals{Pressure: 0})
	for i := 0; i < 8; i++ {
		r.observe(HostSignals{Pressure: 1}) // 40 s more
	}
	var again []Event
	for i := 0; i < 60; i++ {
		again = append(again, r.observe(HostSignals{Pressure: 2})...)
	}
	assert.Empty(t, again, "never re-armed: the low spells were 100 s and 40 s")
}

// The page rate's run breaks on a source flip too: 40 s over on the counters and 40 s over on wwd are not 60 s of either.
func TestE2_APageRunDoesNotCrossASourceFlip(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 8; i++ {
		r.raw.PageCounters = true
		r.raw.Pageouts += 99 * uint64(tick/time.Second)
		assert.Empty(t, r.pass())
	}
	for i := 0; i < 8; i++ {
		assert.Empty(t, r.observe(HostSignals{Pressure: 1, PageRate: 99, PageRateKnown: true}))
	}
}

// A run is one source's: a source flip breaks a run that has not fired, so no window is made of two sources; a run that has fired
// stays fired (no second event for the same pressure).
func TestE2_ASourceFlipBreaksAPendingRunButKeepsAFiredOne(t *testing.T) {
	r := newRig(t) // 100 s of Raw level 2, then wwd level 2 for 100 s: neither source has a full 120 s
	r.raw = resources.HostRaw{Pressure: 2}
	for i := 0; i < 20; i++ {
		assert.Empty(t, r.pass())
	}
	for i := 0; i < 20; i++ {
		assert.Empty(t, r.observe(HostSignals{Pressure: 2}))
	}

	r2 := newRig(t) // fired on Raw, then the source flips while it is still high: no second event
	r2.raw = resources.HostRaw{Pressure: 2}
	var fired []Event
	for i := 0; i < 30; i++ {
		fired = append(fired, r2.pass()...)
	}
	require.Len(t, fired, 1)
	for i := 0; i < 60; i++ {
		assert.Empty(t, r2.observe(HostSignals{Pressure: 2}))
	}
}

// Back to Raw after wwd: the counters' baseline starts over, so the whole jump of the counters in between is not a rate.
func TestE2_RawBaselineRestartsAfterWWD(t *testing.T) {
	r := newRig(t)
	r.set.PageTS = 0 // a single pass over the rate is an event, so the gap's jump would show at once
	r.raw = resources.HostRaw{PageCounters: true, Pageouts: 100}
	r.pass() // baseline at 100
	r.host = &HostSignals{PageRate: 1, PageRateKnown: true}
	r.pass()
	r.host = nil
	r.raw.Pageouts = 100 + 10_000_000 // a long time of paging happened while wwd was the source
	assert.Empty(t, r.pass(), "the first Raw pass after wwd is a new baseline, not a rate over the gap")
	for i := 0; i < 40; i++ {
		assert.Empty(t, r.pass(), "and no paging since")
	}
}

// ---- E3, E4 ----

func holder(slot, pid int, started time.Time, orphaned bool) gate.Holder {
	return gate.Holder{Class: gate.ClassHeavy, Slot: slot, PID: pid, PGID: pid + 1, Argv: []string{"go", "test"}, Cwd: "/w",
		Started: started, Session: "s1", Orphaned: orphaned}
}

func TestE3_AnOrphanedSlotAfterFifteenMinutes(t *testing.T) {
	r := newRig(t)
	r.lockPID = []int{4242, 4243}
	r.holders = []gate.Holder{holder(1, 500, t0.Add(-time.Hour), true)}
	var at []time.Duration
	var ev Event
	for i := 0; i < 250; i++ {
		if got := r.pass(); len(got) > 0 {
			for _, e := range got {
				if e.Kind == KindSlotOrphaned {
					at = append(at, r.now.Add(-tick).Sub(t0))
					ev = e
				}
			}
		}
	}
	assert.Equal(t, []time.Duration{15 * time.Minute}, at, "once, 15 min after it was first seen orphaned (not after it was taken)")
	assert.Equal(t, "heavy-1", ev.Slot)
	assert.Equal(t, 500, ev.HolderPID)
	assert.Equal(t, []int{4242, 4243}, ev.LockPIDs)
	assert.Equal(t, 1, r.locks, "one lsof, for the one orphaned slot")
}

func TestE3_TheFirstSeenTimeSurvivesARestartAndAHolderThatIsNotOrphanedAnymoreIsForgotten(t *testing.T) {
	r := newRig(t)
	r.holders = []gate.Holder{holder(1, 500, t0.Add(-time.Hour), true)}
	r.run(10 * time.Minute)
	r.d = New(r.mem) // the daemon restarts; the memory is the state file
	assert.Empty(t, filterKind(r.run(4*time.Minute), KindSlotOrphaned), "not yet: 10 + 4 min since it was first seen")
	assert.Len(t, filterKind(r.run(2*time.Minute), KindSlotOrphaned), 1, "15 min from the first sighting, counted across the restart")

	r.holders = nil // the slot is free
	r.pass()
	assert.Zero(t, r.mem.Orphans(), "the orphan is forgotten when its holder is gone")
	// and one remembered from before a restart that is not there any more
	r.mem.OrphanSince("heavy-9:1:1", r.now)
	r.d = New(r.mem)
	r.pass()
	assert.Zero(t, r.mem.Orphans())
}

func TestE3_LsofRunsAtMostOncePerPass(t *testing.T) {
	r := newRig(t)
	r.lockPID = []int{1}
	r.holders = []gate.Holder{holder(1, 500, t0, true), holder(2, 600, t0, true)}
	got := filterKind(r.run(16*time.Minute), KindSlotOrphaned)
	require.Len(t, got, 2)
	assert.Equal(t, 1, r.locks)
	assert.Equal(t, []int{1}, got[0].LockPIDs)
	assert.Empty(t, got[1].LockPIDs, "the second one in the same pass has none: the lookup is once per pass")
}

func TestE4_ASlotHeldLongerThanTheAlertTimeOnceAndAgainForANewHolder(t *testing.T) {
	r := newRig(t)
	r.holders = []gate.Holder{holder(1, 500, t0, false)}
	var at []time.Duration
	for i := 0; i < 800; i++ {
		for _, e := range r.pass() {
			if e.Kind == KindSlotLong {
				at = append(at, r.now.Add(-tick).Sub(t0))
			}
		}
	}
	assert.Equal(t, []time.Duration{60 * time.Minute}, at, "exactly alert_after_min, once")

	r.holders = nil
	r.pass()
	r.holders = []gate.Holder{holder(1, 501, r.now, false)} // another holder of the same slot
	assert.Len(t, filterKind(r.run(61*time.Minute), KindSlotLong), 1)

	r2 := newRig(t)
	r2.set.SlotLongAfter = 0 // never
	r2.holders = []gate.Holder{holder(1, 500, t0.Add(-100*time.Hour), false)}
	assert.Empty(t, r2.run(10*time.Minute))
}

func TestE3E4_TheOrderIsE1ThenE2ThenE3ThenE4(t *testing.T) {
	r := newRig(t)
	r.set.OrphanAfter, r.set.PressureTS, r.set.TS = 10*time.Second, 10*time.Second, 10*time.Second
	r.procs = goTest(3, 100)
	r.argv[100] = []string{"go", "test", "./..."}
	r.raw.Pressure = 4
	// held 60 min at the third pass (t0+10s): E4 is due then too
	r.holders = []gate.Holder{holder(1, 500, t0.Add(10*time.Second-time.Hour), true)}
	assert.Empty(t, r.pass(), "the first passes only start the windows")
	assert.Empty(t, r.pass())
	var kinds []Kind
	for _, e := range r.pass() {
		kinds = append(kinds, e.Kind)
	}
	assert.Equal(t, []Kind{KindUnmanagedHeavy, KindPressureHigh, KindSlotOrphaned, KindSlotLong}, kinds)
}

func filterKind(ev []Event, k Kind) []Event {
	var out []Event
	for _, e := range ev {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

// ---- settings that switch parts off ----

func TestSettingsThatSwitchPartsOff(t *testing.T) {
	r := newRig(t)
	r.set.TS = 0
	r.procs = goTest(5, 5000)
	assert.Empty(t, r.run(5*time.Minute), "no window: no E1")
	r2 := newRig(t)
	r2.set.XCores = 0 // cores off, memory still on
	r2.procs = goTest(5, 100)
	assert.Empty(t, r2.run(5*time.Minute))
	r2.procs = goTest(0.1, 2000)
	assert.Len(t, r2.run(5*time.Minute), 1)
}

// ---- restarts and settings changes ----

// E3 is never folded: a daemon that restarts must not send it again, nor E4, for a holder that was already reported.
func TestE3E4_AReportedHolderIsNotReportedAgainAfterARestart(t *testing.T) {
	r := newRig(t)
	r.holders = []gate.Holder{holder(1, 500, t0.Add(-2*time.Hour), true)}
	r.run(20 * time.Minute)
	require.Equal(t, 2, r.mem.ReportedCount(), "E3 and E4 were reported")
	r.d = New(r.mem) // the daemon restarts; the memory is the state file
	assert.Empty(t, r.run(10*time.Minute), "the holder is still there and still past due: nothing to say again")
	// a different holder of the same slot is a new one
	r.holders = []gate.Holder{holder(1, 501, r.now, true)}
	assert.Empty(t, r.run(10*time.Minute), "its orphan time starts now")
	assert.Len(t, filterKind(r.run(10*time.Minute), KindSlotOrphaned), 1)
	// and when the holder goes the marks go
	r.holders = nil
	r.pass()
	assert.Zero(t, r.mem.ReportedCount())
}

// The settings are read at every pass: a window made longer must be collected anew, not trusted on the old one's samples.
func TestE1_ALongerWindowIsCollectedAnew(t *testing.T) {
	r := newRig(t)
	r.set.TS = 30 * time.Second
	r.argv[100] = []string{"go", "test", "./..."}
	r.procs = goTest(0.1, 100)
	assert.Empty(t, r.run(70*time.Second))
	r.set.TS = 60 * time.Second
	r.procs = goTest(3, 100)
	assert.Empty(t, r.run(55*time.Second), "15 s of load is not a 60 s window, whatever the tree's age")
	assert.Len(t, r.run(15*time.Second), 1, "a whole new window of it is")

	// a window made shorter just applies
	r2 := newRig(t)
	r2.procs = goTest(3, 100)
	r2.run(20 * time.Second)
	r2.set.TS = 30 * time.Second
	assert.Len(t, r2.run(15*time.Second), 1)
}

// ---- the window is what was observed ----

// A pass that did not happen (a failed sample) breaks the window: the time nobody saw is not 60 seconds of load.
func TestE1_AGapBreaksTheWindow(t *testing.T) {
	r := newRig(t)
	r.argv[100] = []string{"go", "test", "./..."}
	r.procs = goTest(0.1, 100)
	r.pass() // t0: one quiet sample, then the sampler fails for 55 s
	r.now = r.now.Add(55 * time.Second)
	r.procs = goTest(3, 100)
	assert.Empty(t, r.pass(), "t=55")
	assert.Empty(t, r.pass(), "t=60: only the last 5 s have been seen, not a window of it")
	assert.Empty(t, r.run(45*time.Second))
	assert.Len(t, r.run(15*time.Second), 1, "a window of load seen without a gap fires")
}

// Passes at uneven times: the mean is over time, and a stretch counts for the lower of its two ends.
func TestE1_TheMeanIsOverTimeAndAStretchCountsForTheLowerEnd(t *testing.T) {
	run := func(samples [][2]float64) []Event {
		r := newRig(t)
		r.set.TS = 20 * time.Second
		r.set.XCores = 1.5
		var last []Event
		for _, sm := range samples {
			last = r.d.Pass(Input{Now: t0.Add(time.Duration(sm[0]) * time.Second), Procs: goTest(sm[1], 100), Set: r.set, Interval: 10 * time.Second,
				Argv: func(int) []string { return []string{"go", "test", "./..."} }})
		}
		return last
	}
	// 2 cores for 18 s then nothing at 20 s: time-weighted 1.8 (the plain mean of the three samples, 1.33, would not fire)
	assert.Len(t, run([][2]float64{{0, 2}, {18, 2}, {20, 0}}), 1)
	// 1 core for 18 s then 5 at 20 s: 1.0 over time (the plain mean, 2.33, would fire)
	assert.Empty(t, run([][2]float64{{0, 1}, {18, 1}, {20, 5}}))
	// load seen only at the end: 18 s of nothing, then 3 cores at 18 s and 20 s: 2 s of load, not a window of it
	assert.Empty(t, run([][2]float64{{0, 0.1}, {18, 3}, {20, 3}}))
	// and the mirror: load at the start, gone by the end
	assert.Empty(t, run([][2]float64{{0, 3}, {2, 3}, {20, 0.1}}))
	// steady load fires however the passes are spaced
	assert.Len(t, run([][2]float64{{0, 3}, {7, 3}, {15, 3}, {20, 3}}), 1)
}

// ---- holders, signatures: identity and secrets ----

func TestE3E4_HoldersOfTheSameSlotAndPidInTheSameSecondAreNotTheSame(t *testing.T) {
	a := gate.Holder{Class: gate.ClassHeavy, Slot: 1, PID: 500, Started: t0.Add(100 * time.Millisecond)}
	b := gate.Holder{Class: gate.ClassHeavy, Slot: 1, PID: 500, Started: t0.Add(700 * time.Millisecond)}
	assert.NotEqual(t, holderKey(a), holderKey(b))
}

// After a restart the first pass may read the holder as not orphaned: the remembered first-seen time gets the same grace.
func TestE3_TheGraceAlsoCoversTheFirstPassAfterARestart(t *testing.T) {
	r := newRig(t)
	orphan := holder(1, 500, t0.Add(-time.Hour), true)
	r.holders = []gate.Holder{orphan}
	r.run(10 * time.Minute)
	r.d = New(r.mem) // restart
	r.holders = []gate.Holder{holder(1, 500, orphan.Started, false)}
	r.pass() // the first pass reads it as alive
	r.holders = []gate.Holder{orphan}
	assert.Len(t, filterKind(r.run(6*time.Minute), KindSlotOrphaned), 1, "the first-seen time from before the restart is kept")
	assert.Equal(t, 1, r.mem.Orphans())
}

// One reading of "not orphaned" does not throw away the time already counted; staying not orphaned does.
func TestE3_AFlickerInTheOrphanReadingKeepsTheFirstSeenTime(t *testing.T) {
	r := newRig(t)
	orphan := holder(1, 500, t0.Add(-time.Hour), true)
	r.holders = []gate.Holder{orphan}
	r.run(10 * time.Minute)
	r.holders = []gate.Holder{holder(1, 500, orphan.Started, false)} // one pass reads it as alive
	r.pass()
	r.holders = []gate.Holder{orphan}
	assert.Len(t, filterKind(r.run(6*time.Minute), KindSlotOrphaned), 1, "15 min from the first sighting, the flicker did not restart it")

	r2 := newRig(t) // it stays not orphaned for good: forgotten
	r2.holders = []gate.Holder{orphan}
	r2.run(10 * time.Minute)
	r2.holders = []gate.Holder{holder(1, 500, orphan.Started, false)}
	r2.run(30 * time.Second)
	assert.Zero(t, r2.mem.Orphans())
}

// A caller that cannot verify gates (IsGate nil) treats none as one, and does not crash on a process called pdx.
func TestE1_WithoutAGateCheckNoProcessIsAGate(t *testing.T) {
	procs := []resources.Proc{p(1, 0, "launchd", 0, 10), p(10, 1, "zsh", 0, 5), p(100, 10, "pdx", 1, 50), p(101, 100, "go", 400, 500)}
	d := New(NewMemoryStore())
	var n int
	for i := 0; i < 30; i++ {
		n += len(d.Pass(Input{Now: t0.Add(time.Duration(i) * tick), Procs: procs, Set: DefaultSettings()}))
	}
	assert.Equal(t, 1, n)
}

// A window shorter than two ticks is one sample standing for all of it: E1 is off then, and a window's left edge counts.
func TestE1_AWindowShorterThanTwoTicksIsNotMade(t *testing.T) {
	r := newRig(t)
	r.set.TS = 5 * time.Second // one tick
	r.procs = goTest(3, 100)
	assert.Empty(t, r.run(5*time.Minute))
	r.set.TS = 10 * time.Second // two ticks: a window
	assert.Len(t, r.run(30*time.Second), 1)
}

func TestE1_MemoryAtTheLeftEdgeOfTheWindowCounts(t *testing.T) {
	r := newRig(t)
	r.set.TS = 20 * time.Second
	for i := 0; i < 10; i++ { // memory dips at the pass exactly TS before the fire
		mb := uint64(2000)
		if i == 0 {
			mb = 900
		}
		r.procs = goTest(0.1, mb)
		ev := r.pass()
		if i == 4 { // t=20: the window [0, 20] has the dip at its left edge
			assert.Empty(t, ev, "the sample at the left edge is in the window")
		}
	}
}

// Time nobody looked (failed samples, a watcher that was off) is not time the host was under pressure.
func TestE2_AGapBetweenPassesStartsTheEpisodeAgain(t *testing.T) {
	r := newRig(t)
	r.raw = resources.HostRaw{Pressure: 2}
	assert.Empty(t, r.run(100*time.Second), "100 s of 120: not yet")
	r.now = r.now.Add(10 * time.Minute) // the watcher was off
	assert.Empty(t, r.run(60*time.Second), "the gap does not count: 60 s since the restart")
	assert.NotEmpty(t, r.run(90*time.Second), "and the episode completes 120 s after the restart")

	r2 := newRig(t) // the control: without the gap the same 160 s fire
	r2.raw = resources.HostRaw{Pressure: 2}
	assert.NotEmpty(t, r2.run(160*time.Second))
}

func TestE2_AGapAlsoStartsThePageRateAgain(t *testing.T) {
	r := newRig(t)
	r.set.PageTS = 60 * time.Second
	pages := uint64(0)
	step := func(d time.Duration) []Event {
		var all []Event
		for end := r.now.Add(d); r.now.Before(end); {
			pages += 1000 // 200/s at a 5 s tick, over the default 50
			r.raw = resources.HostRaw{PageCounters: true, Pageouts: pages}
			all = append(all, r.pass()...)
		}
		return all
	}
	assert.Empty(t, step(40*time.Second))
	r.now = r.now.Add(10 * time.Minute)
	assert.Empty(t, step(40*time.Second), "40 s since the restart, under the 60 s window")
	assert.NotEmpty(t, step(60*time.Second))
}
