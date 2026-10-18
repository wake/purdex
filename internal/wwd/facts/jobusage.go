package facts

import (
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// What the host knows of a job while it runs, kept from the 5 s samples the sampler already takes (no extra sampling, no extra
// process): the CPU and memory of the job's process tree (the processes that carry its WWD_JOB mark, or have an ancestor that does),
// and the host's state around it. When the job ends, the job-end hook takes the accumulated figures with Take and writes the usage
// row (wire.JobUsage). Recording only: nothing here decides anything about a job.
//
// It runs in two steps because the facts of a sample are made in two places: Observe gets the process table (from the collector,
// Sources.Observe) and Commit gets the finished facts record of the same moment (zone, owner), whose derived part is made after the
// collector returns. Both run on the sampler's goroutine; Take is called from another, so the state is behind a lock.

const (
	// jobUsageStale is how long a job that is no longer seen keeps its accumulated figures waiting for Take.
	jobUsageStale = 2 * time.Hour
	// jobUsageMaxJobs bounds the jobs kept; the ones seen longest ago go first.
	jobUsageMaxJobs = 512
	// jobUsageMaxTaken bounds the memory of jobs already taken (the oldest go first).
	jobUsageMaxTaken = 4096
	// jobUsageFaultPrefix starts the reason of an owner state a fault hook made up (kofi's C2 hooks): not the owner, so not recorded.
	jobUsageFaultPrefix = "fault:"
)

// JobSample is what the samples saw of one job.
type JobSample struct {
	JobID string
	// FirstSeen and LastSeen are the times of the first and the last sample the job's processes were in; Samples how many.
	FirstSeen, LastSeen time.Time
	Samples             int
	// CPUSeconds is the integral of the tree's CPU use over the samples (each charged one interval, at most three after a gap), the
	// sampled CPU time. PeakCores is the highest CPU use of the tree in one sample, in cores; MemPeakBytes the highest sum of the
	// footprints. Both are the highest of the samples, not the exact peaks.
	CPUSeconds   float64
	PeakCores    float64
	MemPeakBytes uint64
	// JobsAtStart is how many jobs had processes in the first sample the job was in (itself among them); JobsPeak the most in any
	// sample of its life.
	JobsAtStart, JobsPeak int
	// ZoneAtStart and ZoneWorst are the real pressure zone (not a fault hook's override) in the first sample and the worst in any.
	ZoneAtStart, ZoneWorst wire.Zone
	// OwnerAtStart and OwnerSeen are nil on a host with no owner rule, and while a fault hook makes the owner up.
	OwnerAtStart, OwnerSeen *bool
}

type jobAcc struct {
	JobSample
}

type jobNow struct {
	cpuPct float64
	mem    uint64
}

// JobUsageTracker accumulates JobSamples. One value per process; Observe and Commit belong to the sampler's goroutine, Take and Len
// may be called from any.
type JobUsageTracker struct {
	started  time.Time
	interval time.Duration

	mu         sync.Mutex
	jobs       map[string]*jobAcc
	taken      map[string]time.Time // jobs Take handed out: a pending sample made before the Take must not bring them back
	pending    map[string]*jobNow
	pendingAt  time.Time
	hasPending bool
	lastAt     time.Time
}

// NewJobUsageTracker starts a tracker at started (the time wwd began sampling) with the sampling interval.
func NewJobUsageTracker(started time.Time, interval time.Duration) *JobUsageTracker {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &JobUsageTracker{started: started, interval: interval, jobs: map[string]*jobAcc{}, taken: map[string]time.Time{}}
}

// StartedAt is when this tracker began: a job that began before it has a sample that misses its first part (the row says partial).
func (t *JobUsageTracker) StartedAt() time.Time { return t.started }

// Observe takes the process table of the sample made at now: for each job, the CPU and the memory of its processes.
func (t *JobUsageTracker) Observe(now time.Time, snap ProcSnapshot, c ProcContext) {
	c.ensureTable(snap)
	cur := map[string]*jobNow{}
	for _, p := range snap.Procs {
		id, ok := jobAbove(p, c)
		if !ok {
			continue
		}
		j := cur[id]
		if j == nil {
			j = &jobNow{}
			cur[id] = j
		}
		j.cpuPct += p.CPUPct
		j.mem += p.FootprintBytes
	}
	t.mu.Lock()
	t.pending, t.pendingAt, t.hasPending = cur, now, true
	t.mu.Unlock()
}

// worseZone is the worse of two zones; zoneRank (ring.go) puts unknown below all, so a known zone always beats it.
func worseZone(a, b wire.Zone) wire.Zone {
	if zoneRank(b) > zoneRank(a) {
		return b
	}
	return a
}

// Commit takes the finished facts record of the sample Observe just saw and adds it to every job in it. Without a sample before it
// (the process read failed) it changes nothing.
func (t *JobUsageTracker) Commit(f wire.Facts) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.hasPending {
		return
	}
	t.hasPending = false
	now := t.pendingAt
	dt := t.interval
	if !t.lastAt.IsZero() {
		if now.After(t.lastAt) {
			dt = min(now.Sub(t.lastAt), 3*t.interval) // a gap (sleep, wwd stopped) is not charged as CPU
			t.lastAt = now
		} else {
			dt = 0 // the clock went back: that stretch was charged already, and the base does not move back
		}
	} else {
		t.lastAt = now
	}

	zone := f.Derived.Zone // the real zone: a fault hook's override is in Derived.ZoneOverride
	var ownerActive *bool
	if o := f.Owner; o != nil && !strings.HasPrefix(o.Reason, jobUsageFaultPrefix) {
		a := o.Active
		ownerActive = &a
	}
	n := len(t.pending)
	for id, cur := range t.pending {
		if _, gone := t.taken[id]; gone {
			continue
		}
		acc := t.jobs[id]
		if acc == nil {
			acc = &jobAcc{JobSample: JobSample{
				JobID: id, FirstSeen: now, JobsAtStart: n, ZoneAtStart: zone, ZoneWorst: zone,
			}}
			if ownerActive != nil {
				start, seen := *ownerActive, false
				acc.OwnerAtStart, acc.OwnerSeen = &start, &seen
			}
			t.jobs[id] = acc
		}
		acc.LastSeen = now
		acc.Samples++
		cores := cur.cpuPct / 100
		acc.CPUSeconds += cores * dt.Seconds()
		acc.PeakCores = max(acc.PeakCores, cores)
		acc.MemPeakBytes = max(acc.MemPeakBytes, cur.mem)
		acc.JobsPeak = max(acc.JobsPeak, n)
		acc.ZoneWorst = worseZone(acc.ZoneWorst, zone)
		if ownerActive != nil {
			if acc.OwnerSeen == nil { // the owner was unknown (or made up) at the start and is known now
				seen := false
				acc.OwnerSeen = &seen
			}
			if *ownerActive {
				*acc.OwnerSeen = true
			}
		}
	}
	t.forget(now)
}

// forget drops the jobs nobody took for a long time and keeps the number of jobs under the bound (the longest unseen first).
func (t *JobUsageTracker) forget(now time.Time) {
	for id, acc := range t.jobs {
		if now.Sub(acc.LastSeen) > jobUsageStale {
			delete(t.jobs, id)
		}
	}
	for id, at := range t.taken {
		if now.Sub(at) > jobUsageStale {
			delete(t.taken, id)
		}
	}
	for len(t.jobs) > jobUsageMaxJobs {
		var oldest string
		var at time.Time
		for id, acc := range t.jobs {
			if oldest == "" || acc.LastSeen.Before(at) {
				oldest, at = id, acc.LastSeen
			}
		}
		delete(t.jobs, oldest)
	}
}

// Take hands out the sample of a job and forgets it. ok is false for a job no sample saw (it ended between two samples, or its
// processes were never seen: the row then says none for every figure).
func (t *JobUsageTracker) Take(jobID string) (JobSample, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	at := t.lastAt
	if at.IsZero() {
		at = time.Now()
	}
	t.taken[jobID] = at                   // a job ends once: a sample of it made just before this Take is not its start
	for len(t.taken) > jobUsageMaxTaken { // bounded even when no sample comes to forget them
		var oldest string
		var oat time.Time
		for id, a := range t.taken {
			if oldest == "" || a.Before(oat) {
				oldest, oat = id, a
			}
		}
		delete(t.taken, oldest)
	}
	acc := t.jobs[jobID]
	if acc == nil {
		return JobSample{}, false
	}
	delete(t.jobs, jobID)
	return acc.JobSample, true
}

// Discard drops what was seen of a job without marking it taken: for a run that is not the job's end (the job goes back to the queue and
// runs again), whose next run must collect its own samples. Take is for the end of the job.
func (t *JobUsageTracker) Discard(jobID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.jobs, jobID)
}

// Len is how many jobs are being kept.
func (t *JobUsageTracker) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.jobs)
}
