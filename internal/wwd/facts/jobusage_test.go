package facts

import (
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func newJU() *JobUsageTracker { return NewJobUsageTracker(ot0, 5*time.Second) }

// sample feeds one sample n*5 s after ot0: the process table of the host, then the facts record of the same moment.
func sample(tr *JobUsageTracker, h *host, n int, zone wire.Zone, owner *wire.OwnerState) {
	snap, ctx := h.snapshot()
	now := ot0.Add(time.Duration(n) * 5 * time.Second)
	tr.Observe(now, snap, ctx)
	tr.Commit(wire.Facts{At: now, Owner: owner, Derived: wire.Derived{Zone: zone}})
}

func jobHost() *host {
	h := newHost()
	h.add(300, 100, "go", []string{"go", "test"}) // a job's root: carries the marker
	h.add(301, 300, "compile", []string{"compile"})
	h.jobs[300] = "j_a"
	return h
}

func TestJobUsageSumsTheTreeOfAJobEverySample(t *testing.T) {
	h, tr := jobHost(), newJU()
	for n := 0; n < 4; n++ {
		h.rec(300).CPUPct, h.rec(301).CPUPct = 50, 150 // 2 cores for the tree
		h.rec(300).FootprintBytes, h.rec(301).FootprintBytes = mb(300), mb(500)
		if n == 2 {
			h.rec(301).CPUPct, h.rec(301).FootprintBytes = 350, mb(900) // the peak, in the third sample
		}
		sample(tr, h, n, wire.ZoneNormal, nil)
	}
	got, ok := tr.Take("j_a")
	if !ok {
		t.Fatal("the job was seen")
	}
	// the child has no marker of its own: it is the job's (jobAbove)
	if got.PeakCores != 4 || got.MemPeakBytes != mb(1200) || got.Samples != 4 {
		t.Fatalf("%+v", got)
	}
	// 4 samples x 5 s: three at 2 cores and one at 4 cores = 10+10+20+10 = 50 CPU seconds
	if got.CPUSeconds < 49.9 || got.CPUSeconds > 50.1 {
		t.Fatalf("cpu seconds %v", got.CPUSeconds)
	}
	if !got.FirstSeen.Equal(ot0) || !got.LastSeen.Equal(ot0.Add(15*time.Second)) {
		t.Fatalf("seen %v .. %v", got.FirstSeen, got.LastSeen)
	}
}

func TestJobUsageKeepsJobsApartAndLeavesOutTheRest(t *testing.T) {
	h, tr := jobHost(), newJU()
	h.add(400, 100, "swift", []string{"swift", "build"})
	h.jobs[400] = "j_b"
	h.add(500, 100, "node", []string{"node", "dev"}) // a session's own command: no job
	h.rec(300).CPUPct, h.rec(301).CPUPct, h.rec(400).CPUPct, h.rec(500).CPUPct = 100, 0, 300, 900
	h.rec(300).FootprintBytes, h.rec(400).FootprintBytes, h.rec(500).FootprintBytes = mb(100), mb(700), mb(5000)
	sample(tr, h, 0, wire.ZoneNormal, nil)
	a, _ := tr.Take("j_a")
	b, _ := tr.Take("j_b")
	// every process starts at 5 MB (newHost), so j_a's tree is 100 + the child's 5
	if a.PeakCores != 1 || a.MemPeakBytes != mb(105) || b.PeakCores != 3 || b.MemPeakBytes != mb(700) {
		t.Fatalf("a %+v b %+v", a, b)
	}
}

func TestJobUsageCountsTheJobsRunningTogether(t *testing.T) {
	h, tr := jobHost(), newJU()
	h.rec(300).CPUPct = 100
	sample(tr, h, 0, wire.ZoneNormal, nil) // j_a alone
	h.add(400, 100, "swift", []string{"swift"})
	h.jobs[400] = "j_b"
	h.add(410, 100, "make", []string{"make"})
	h.jobs[410] = "j_c"
	sample(tr, h, 1, wire.ZoneNormal, nil) // three at once
	h.remove(410)
	sample(tr, h, 2, wire.ZoneNormal, nil)
	a, _ := tr.Take("j_a")
	b, _ := tr.Take("j_b")
	if a.JobsAtStart != 1 || a.JobsPeak != 3 {
		t.Errorf("a: %+v", a)
	}
	// j_b began when j_a, j_b and j_c were all there: itself and the others count
	if b.JobsAtStart != 3 || b.JobsPeak != 3 {
		t.Errorf("b: %+v", b)
	}
}

func TestJobUsageRecordsTheZoneAtTheStartAndTheWorst(t *testing.T) {
	h, tr := jobHost(), newJU()
	for n, z := range []wire.Zone{wire.ZoneWarning, wire.ZoneNormal, wire.ZoneDanger, wire.ZoneUnknown, wire.ZoneWarning} {
		sample(tr, h, n, z, nil)
	}
	got, _ := tr.Take("j_a")
	if got.ZoneAtStart != wire.ZoneWarning || got.ZoneWorst != wire.ZoneDanger {
		t.Fatalf("%+v", got)
	}
	// a zone that is never known stays unknown
	h2, tr2 := jobHost(), newJU()
	sample(tr2, h2, 0, wire.ZoneUnknown, nil)
	sample(tr2, h2, 1, wire.ZoneUnknown, nil)
	if got, _ := tr2.Take("j_a"); got.ZoneAtStart != wire.ZoneUnknown || got.ZoneWorst != wire.ZoneUnknown {
		t.Fatalf("%+v", got)
	}
	// and an unknown start followed by a known zone is worst of the known ones
	h3, tr3 := jobHost(), newJU()
	sample(tr3, h3, 0, wire.ZoneUnknown, nil)
	sample(tr3, h3, 1, wire.ZoneNormal, nil)
	if got, _ := tr3.Take("j_a"); got.ZoneAtStart != wire.ZoneUnknown || got.ZoneWorst != wire.ZoneNormal {
		t.Fatalf("%+v", got)
	}
}

func TestJobUsageOwnerAtTheStartAndDuringTheRun(t *testing.T) {
	away, here := &wire.OwnerState{Active: false, Reason: ""}, &wire.OwnerState{Active: true, Reason: "hid"}
	run := func(owners ...*wire.OwnerState) JobSample {
		h, tr := jobHost(), newJU()
		for n, o := range owners {
			sample(tr, h, n, wire.ZoneNormal, o)
		}
		got, _ := tr.Take("j_a")
		return got
	}
	if g := run(away, away, here, away); g.OwnerAtStart == nil || *g.OwnerAtStart || g.OwnerSeen == nil || !*g.OwnerSeen {
		t.Errorf("away at the start, back later: %+v %+v", g.OwnerAtStart, g.OwnerSeen)
	}
	if g := run(here, away, away); g.OwnerAtStart == nil || !*g.OwnerAtStart || !*g.OwnerSeen {
		t.Errorf("there at the start counts as seen: %+v %+v", g.OwnerAtStart, g.OwnerSeen)
	}
	if g := run(away, away); *g.OwnerAtStart || *g.OwnerSeen {
		t.Errorf("never there: %+v %+v", g.OwnerAtStart, g.OwnerSeen)
	}
	// a host with no owner rule: both null
	if g := run(nil, nil); g.OwnerAtStart != nil || g.OwnerSeen != nil {
		t.Errorf("no owner rule: %+v %+v", g.OwnerAtStart, g.OwnerSeen)
	}
	// a fault hook made the owner up (reason fault:...): not the owner, so not recorded as one
	faked := &wire.OwnerState{Active: true, Reason: "fault: owner active"}
	if g := run(faked, faked); g.OwnerAtStart != nil || g.OwnerSeen != nil {
		t.Errorf("faked owner: %+v %+v", g.OwnerAtStart, g.OwnerSeen)
	}
}

func TestJobUsageTakeHandsOutOnceAndOnlyWhatWasSeen(t *testing.T) {
	h, tr := jobHost(), newJU()
	sample(tr, h, 0, wire.ZoneNormal, nil)
	if _, ok := tr.Take("j_never"); ok {
		t.Fatal("a job that was never seen has no sample")
	}
	if _, ok := tr.Take("j_a"); !ok {
		t.Fatal("seen")
	}
	if _, ok := tr.Take("j_a"); ok {
		t.Fatal("taken once")
	}
	// a job ends once: its processes still in a later sample (a straggler) do not start a second row
	sample(tr, h, 1, wire.ZoneNormal, nil)
	if _, ok := tr.Take("j_a"); ok || tr.Len() != 0 {
		t.Fatalf("a taken job came back: len %d", tr.Len())
	}
}

// Take between a sample's Observe and its Commit (the job ended in that moment) leaves nothing behind for the job.
func TestJobUsageTakeBetweenObserveAndCommitLeavesNoOrphan(t *testing.T) {
	h, tr := jobHost(), newJU()
	sample(tr, h, 0, wire.ZoneNormal, nil)
	snap, ctx := h.snapshot()
	now := ot0.Add(5 * time.Second)
	tr.Observe(now, snap, ctx)
	if _, ok := tr.Take("j_a"); !ok {
		t.Fatal("seen")
	}
	tr.Commit(wire.Facts{At: now, Derived: wire.Derived{Zone: wire.ZoneNormal}})
	if tr.Len() != 0 {
		t.Fatalf("an orphan accumulator was made: %d", tr.Len())
	}
}

// A sample whose process read failed has no Observe: the facts record that follows adds nothing to any job.
func TestJobUsageCommitWithoutObserveChangesNothing(t *testing.T) {
	h, tr := jobHost(), newJU()
	sample(tr, h, 0, wire.ZoneNormal, nil)
	tr.Commit(wire.Facts{At: ot0.Add(5 * time.Second), Derived: wire.Derived{Zone: wire.ZoneDanger}})
	got, _ := tr.Take("j_a")
	if got.Samples != 1 || got.ZoneWorst != wire.ZoneNormal {
		t.Fatalf("%+v", got)
	}
}

func TestJobUsageAGapIsNotChargedAsCPU(t *testing.T) {
	h, tr := jobHost(), newJU()
	h.rec(300).CPUPct = 100
	sample(tr, h, 0, wire.ZoneNormal, nil)
	sample(tr, h, 1000, wire.ZoneNormal, nil) // 5000 s later: the machine slept
	got, _ := tr.Take("j_a")
	// the first sample is charged one interval, the second at most three
	if got.CPUSeconds > 20.1 {
		t.Fatalf("cpu seconds %v", got.CPUSeconds)
	}
}

func TestJobUsageKnowsWhenItStartedSoAJobBeforeItCanBeCalledPartial(t *testing.T) {
	if got := newJU().StartedAt(); !got.Equal(ot0) {
		t.Fatalf("%v", got)
	}
}

func TestJobUsageForgetsJobsNobodyTookAndBoundsWhatItKeeps(t *testing.T) {
	h, tr := jobHost(), newJU()
	sample(tr, h, 0, wire.ZoneNormal, nil)
	h.remove(300, 301) // the job is gone and nobody comes for its sample
	late := int(jobUsageStale/(5*time.Second)) + 2
	sample(tr, h, late, wire.ZoneNormal, nil)
	if _, ok := tr.Take("j_a"); ok {
		t.Fatal("a sample nobody took for hours is forgotten")
	}
	// more jobs at once than the bound: the ones seen longest ago go first
	h2, tr2 := newHost(), NewJobUsageTracker(ot0, 5*time.Second)
	for i := 0; i < jobUsageMaxJobs+20; i++ {
		pid := 1000 + i
		h2.add(pid, 100, "w", []string{"w"})
		h2.jobs[pid] = "j_" + time.Duration(i).String()
	}
	sample(tr2, h2, 0, wire.ZoneNormal, nil)
	if n := tr2.Len(); n > jobUsageMaxJobs {
		t.Fatalf("%d jobs kept", n)
	}
}

// The sampler's goroutine feeds it while the queue's event goroutine takes from it.
func TestJobUsageConcurrentTakeAndSample(t *testing.T) {
	h, tr := jobHost(), newJU()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			tr.Take("j_a")
		}
	}()
	for n := 0; n < 200; n++ {
		sample(tr, h, n, wire.ZoneNormal, nil)
	}
	wg.Wait()
}

// The owner unknown (no rule yet, or a fault hook) in the first sample does not hide what it shows later.
func TestJobUsageOwnerKnownOnlyAfterTheStart(t *testing.T) {
	here, away := &wire.OwnerState{Active: true, Reason: "hid"}, &wire.OwnerState{Active: false}
	faked := &wire.OwnerState{Active: true, Reason: "fault: owner active"}
	run := func(owners ...*wire.OwnerState) JobSample {
		h, tr := jobHost(), newJU()
		for n, o := range owners {
			sample(tr, h, n, wire.ZoneNormal, o)
		}
		got, _ := tr.Take("j_a")
		return got
	}
	for name, g := range map[string]JobSample{"nil then there": run(nil, here), "fault then there": run(faked, here, away)} {
		if g.OwnerAtStart != nil || g.OwnerSeen == nil || !*g.OwnerSeen {
			t.Errorf("%s: %+v %+v", name, g.OwnerAtStart, g.OwnerSeen)
		}
	}
	if g := run(nil, away); g.OwnerAtStart != nil || g.OwnerSeen == nil || *g.OwnerSeen {
		t.Errorf("nil then away: %+v %+v", g.OwnerAtStart, g.OwnerSeen)
	}
}

// A clock that goes back is not charged twice.
func TestJobUsageAClockGoingBackIsNotChargedTwice(t *testing.T) {
	h, tr := jobHost(), newJU()
	h.rec(300).CPUPct = 100
	for _, n := range []int{0, 1, 2, 1, 3} {
		sample(tr, h, n, wire.ZoneNormal, nil)
	}
	got, _ := tr.Take("j_a")
	// at 0 s (the first, 5 s), 5 s (5 s), 10 s (5 s), back at 5 s (0 s), 15 s (5 s from 10 s) = 20 s of one core
	if got.CPUSeconds < 19.9 || got.CPUSeconds > 20.1 {
		t.Fatalf("cpu seconds %v", got.CPUSeconds)
	}
}

// Jobs taken with no sample coming to forget them stay under a bound.
func TestJobUsageRemembersOnlyABoundedNumberOfTakenJobs(t *testing.T) {
	tr := newJU()
	for i := 0; i < jobUsageMaxTaken+50; i++ {
		tr.Take("j_" + time.Duration(i).String())
	}
	tr.mu.Lock()
	n := len(tr.taken)
	tr.mu.Unlock()
	if n > jobUsageMaxTaken {
		t.Fatalf("%d taken kept", n)
	}
}
