package wire

import (
	"fmt"
	"math"
	"time"
)

// What wwd records of a finished job: how much it used and what the host was like while it ran (spec section on job usage). It is
// recorded only; nothing in admission reads it.

// UsageSource says how the job reached this host: through the queue (a worker ran it) or the local fast lane.
type UsageSource string

const (
	UsageQueue    UsageSource = "queue"
	UsageFastLane UsageSource = "fast_lane"
)

// Slot is the gate class a job took: heavy or light.
type Slot string

const (
	SlotHeavy Slot = "heavy"
	SlotLight Slot = "light"
)

// JobSlot is the slot a job is charged, which is a rule of the host's admission today and not a property of the job (a Job has no slot,
// nor does a manifest): a queue job the worker runs takes a heavy slot (worker/loop: it claims only while running < Slots.Heavy and the
// headroom has a heavy slot); a fast-lane job takes the slot its request named, a heavy one for an undeclared command (adhoc) and a
// light one for a declared task (spec §5.2). When the admission rule changes, this changes with it.
func JobSlot(j Job) Slot {
	if j.Local && !j.AdHoc {
		return SlotLight
	}
	return SlotHeavy
}

// CPUSource says where cpu_seconds came from: the process's rusage at exit (macOS, a job wwd ran itself), the sandbox unit's
// cgroup (Linux), the integral of the 5 s samples (a fast-lane job, whose process wwd did not start), or CPUNone: nothing was
// measured (a job shorter than a sample, one that never started, wwd restarted) and cpu_seconds is null: unknown, not 0.
type CPUSource string

const (
	CPUFromRusage CPUSource = "rusage"
	CPUFromUnit   CPUSource = "unit"
	CPUSampled    CPUSource = "sampled"
	CPUNone       CPUSource = "none"
)

// Measure says whether a peak is the exact peak or the highest of the 5 s samples (which can miss a short spike), or MeasureNone:
// no sample was taken and the figure is null: unknown, not 0.
type Measure string

const (
	MeasureExact   Measure = "exact"
	MeasureSampled Measure = "sampled"
	MeasureNone    Measure = "none"
)

// JobUsage is one finished job's usage and the host's state around it. One row per job; the host that ran it writes it.
type JobUsage struct {
	JobID string `json:"job_id"`
	Host  string `json:"host"`
	Repo  string `json:"repo"`
	Task  string `json:"task"`
	// SlotCharged is the slot the host's admission charged the job, not a property of the job: a queue job the worker runs is heavy today,
	// a fast-lane job has the one its request named.
	SlotCharged Slot        `json:"slot_charged"`
	Class       JobClass    `json:"class"`
	Source      UsageSource `json:"source"`
	Result      JobState    `json:"result"` // a final state: succeeded, failed, cancelled, superseded or expired
	// Reason is the job's own failure reason ("timeout", "oom", "infra"...), empty when it has none.
	Reason string `json:"reason"`

	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`

	// CPUSeconds is user plus system time of the job's whole process tree. The duration and the average number of cores are not
	// stored: they are worked out on read from the two times and this (Duration, AvgCores), so a row cannot contradict itself.
	// PeakCores is the highest the tree's CPU use reached, in cores; MemPeakMB the most memory the whole tree held at once.
	// A figure nobody measured is null, never 0, and its source or measure says "none" (and only then): a reader that sums or
	// averages the column leaves the unknown ones out by itself instead of counting them as nothing.
	CPUSeconds       *float64  `json:"cpu_seconds"`
	CPUSource        CPUSource `json:"cpu_source"`
	PeakCores        *float64  `json:"peak_cores"`
	PeakCoresMeasure Measure   `json:"peak_cores_measure"`
	MemPeakMB        *int      `json:"mem_peak_mb"`
	MemPeakMeasure   Measure   `json:"mem_peak_measure"`

	// The host while it ran: jobs running here at the start and at most; the pressure zone at the start and the worst during.
	JobsAtStart int  `json:"jobs_at_start"`
	JobsPeak    int  `json:"jobs_peak"`
	ZoneAtStart Zone `json:"zone_at_start"`
	ZoneWorst   Zone `json:"zone_worst"`
	// OwnerAtStart and OwnerSeen: was the host's owner there at the start, and did the owner show up at any time during the run. Both
	// are null on a host with no owner rule (anything but air26), and one is never null without the other.
	OwnerAtStart *bool `json:"owner_at_start"`
	OwnerSeen    *bool `json:"owner_seen"`

	// Partial says the sampled figures miss the beginning of the run: wwd was restarted while the job ran, so the part before is not in
	// them. An exact figure (rusage, unit) is whole whatever Partial says.
	Partial bool `json:"partial"`
	// LowerBound says the run left processes behind when its command returned (a daemonized tmux, vite, playwright): what they used
	// afterwards is in no figure, so the exact figures (rusage, unit) are a floor and the true use is higher. It is about the figures
	// read at the end of the command; a sampled figure sees the leftovers while they run and is not what this flag is about.
	LowerBound bool `json:"lower_bound"`
}

// Duration is how long the job ran, from its two times.
func (u JobUsage) Duration() time.Duration { return u.EndedAt.Sub(u.StartedAt) }

// AvgCores is the CPU time over the run time: how many cores the job kept busy on average. ok is false for a run with no length or
// no CPU reading.
func (u JobUsage) AvgCores() (cores float64, ok bool) {
	if d := u.Duration().Seconds(); d > 0 && u.CPUSeconds != nil {
		return *u.CPUSeconds / d, true
	}
	return 0, false
}

func finiteNonNegative(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return fmt.Errorf("usage: %s is %v", name, v)
	}
	return nil
}

func (u JobUsage) Validate() error {
	if u.JobID == "" || u.Host == "" || u.Repo == "" || u.Task == "" { // a job always has a repo and a task (Job.Validate)
		return fmt.Errorf("usage: missing job_id, host, repo or task")
	}
	if u.SlotCharged != SlotHeavy && u.SlotCharged != SlotLight {
		return fmt.Errorf("usage: invalid slot_charged %q", u.SlotCharged)
	}
	if !u.Class.Valid() {
		return fmt.Errorf("usage: invalid class %q", u.Class)
	}
	if u.Source != UsageQueue && u.Source != UsageFastLane {
		return fmt.Errorf("usage: invalid source %q", u.Source)
	}
	switch u.Result {
	case StateSucceeded, StateFailed, StateCancelled, StateSuperseded, StateExpired:
	default:
		return fmt.Errorf("usage: result %q is not a final state", u.Result)
	}
	if u.StartedAt.IsZero() || u.EndedAt.IsZero() || u.EndedAt.Before(u.StartedAt) {
		return fmt.Errorf("usage: missing or reversed times")
	}
	for name, v := range map[string]*float64{"cpu_seconds": u.CPUSeconds, "peak_cores": u.PeakCores} {
		if v == nil {
			continue
		}
		if err := finiteNonNegative(name, *v); err != nil {
			return err
		}
	}
	if (u.MemPeakMB != nil && *u.MemPeakMB < 0) || u.JobsAtStart < 0 || u.JobsPeak < u.JobsAtStart {
		return fmt.Errorf("usage: invalid memory or job counts")
	}
	switch u.CPUSource {
	case CPUFromRusage, CPUFromUnit, CPUSampled, CPUNone:
	default:
		return fmt.Errorf("usage: invalid cpu_source %q", u.CPUSource)
	}
	for name, m := range map[string]Measure{"peak_cores_measure": u.PeakCoresMeasure, "mem_peak_measure": u.MemPeakMeasure} {
		if m != MeasureExact && m != MeasureSampled && m != MeasureNone {
			return fmt.Errorf("usage: invalid %s %q", name, m)
		}
	}
	// null goes with none and only with none: a value always says where it came from, and "none" always means null
	if (u.CPUSeconds == nil) != (u.CPUSource == CPUNone) || (u.PeakCores == nil) != (u.PeakCoresMeasure == MeasureNone) ||
		(u.MemPeakMB == nil) != (u.MemPeakMeasure == MeasureNone) {
		return fmt.Errorf("usage: a figure is null exactly when its source or measure is none")
	}
	if !u.ZoneAtStart.Valid() || !u.ZoneWorst.Valid() {
		return fmt.Errorf("usage: invalid zone")
	}
	if (u.OwnerAtStart == nil) != (u.OwnerSeen == nil) {
		return fmt.Errorf("usage: owner_at_start and owner_seen are both null or both set")
	}
	if u.OwnerAtStart != nil && *u.OwnerAtStart && !*u.OwnerSeen {
		return fmt.Errorf("usage: an owner there at the start was seen during the run")
	}
	return nil
}

// UsageResponse is GET /v1/usage: the rows whose job ended at or after Since, oldest first (ties by job id), at most the asked limit.
// Truncated says there are more after the last one; NextSince and NextAfter are then the end time and the job id of the last row: ask
// again with since=<NextSince>&after=<NextAfter> to get the rows after it, none twice and none skipped however many ended in the
// same millisecond. Send them back as they came (NextSince has a fraction of a second).
type UsageResponse struct {
	Host      string     `json:"host"`
	Since     time.Time  `json:"since"`
	Records   []JobUsage `json:"records"`
	Truncated bool       `json:"truncated"`
	NextSince *time.Time `json:"next_since"`
	NextAfter *string    `json:"next_after"`
}

func (r UsageResponse) Validate() error {
	if r.Host == "" {
		return fmt.Errorf("usage: missing host")
	}
	if r.Truncated != (r.NextSince != nil) || r.Truncated != (r.NextAfter != nil) {
		return fmt.Errorf("usage: truncated, next_since and next_after go together")
	}
	if r.NextAfter != nil && *r.NextAfter == "" {
		return fmt.Errorf("usage: empty next_after")
	}
	for i, u := range r.Records {
		if err := u.Validate(); err != nil {
			return fmt.Errorf("records[%d]: %w", i, err)
		}
	}
	return nil
}
