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

// CPUSource says where cpu_seconds came from: the process's rusage at exit (macOS, a job wwd ran itself), the sandbox unit's
// CPUUsageNSec (Linux), or the integral of the 5 s samples (a fast-lane job, whose process wwd did not start).
type CPUSource string

const (
	CPUFromRusage CPUSource = "rusage"
	CPUFromUnit   CPUSource = "unit"
	CPUSampled    CPUSource = "sampled"
)

// Measure says whether a peak is the exact peak or the highest of the 5 s samples (which can miss a short spike).
type Measure string

const (
	MeasureExact   Measure = "exact"
	MeasureSampled Measure = "sampled"
)

// JobUsage is one finished job's usage and the host's state around it. One row per job; the host that ran it writes it.
type JobUsage struct {
	JobID  string      `json:"job_id"`
	Host   string      `json:"host"`
	Repo   string      `json:"repo"`
	Task   string      `json:"task"`
	Slot   Slot        `json:"slot"`
	Class  JobClass    `json:"class"`
	Source UsageSource `json:"source"`
	Result JobState    `json:"result"` // a final state: succeeded, failed, cancelled, superseded or expired
	// Reason is the job's own failure reason ("timeout", "oom", "infra"...), empty when it has none.
	Reason string `json:"reason"`

	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	DurationS float64   `json:"duration_s"`

	// CPUSeconds is user plus system time of the job's whole process tree. AvgCores is CPUSeconds over DurationS, worked out when the
	// row is written (not on read), 0 for a zero duration. PeakCores is the highest the tree's CPU use reached, in cores.
	CPUSeconds       float64   `json:"cpu_seconds"`
	CPUSource        CPUSource `json:"cpu_source"`
	AvgCores         float64   `json:"avg_cores"`
	PeakCores        float64   `json:"peak_cores"`
	PeakCoresMeasure Measure   `json:"peak_cores_measure"`
	// MemPeakMB is the most memory the whole tree held at once.
	MemPeakMB      int     `json:"mem_peak_mb"`
	MemPeakMeasure Measure `json:"mem_peak_measure"`

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
}

func finiteNonNegative(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return fmt.Errorf("usage: %s is %v", name, v)
	}
	return nil
}

func (u JobUsage) Validate() error {
	if u.JobID == "" || u.Host == "" {
		return fmt.Errorf("usage: missing job_id or host")
	}
	if u.Slot != SlotHeavy && u.Slot != SlotLight {
		return fmt.Errorf("usage: invalid slot %q", u.Slot)
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
	for name, v := range map[string]float64{"duration_s": u.DurationS, "cpu_seconds": u.CPUSeconds, "avg_cores": u.AvgCores, "peak_cores": u.PeakCores} {
		if err := finiteNonNegative(name, v); err != nil {
			return err
		}
	}
	if u.MemPeakMB < 0 || u.JobsAtStart < 0 || u.JobsPeak < u.JobsAtStart {
		return fmt.Errorf("usage: invalid memory or job counts")
	}
	switch u.CPUSource {
	case CPUFromRusage, CPUFromUnit, CPUSampled:
	default:
		return fmt.Errorf("usage: invalid cpu_source %q", u.CPUSource)
	}
	for name, m := range map[string]Measure{"peak_cores_measure": u.PeakCoresMeasure, "mem_peak_measure": u.MemPeakMeasure} {
		if m != MeasureExact && m != MeasureSampled {
			return fmt.Errorf("usage: invalid %s %q", name, m)
		}
	}
	if !u.ZoneAtStart.Valid() || !u.ZoneWorst.Valid() {
		return fmt.Errorf("usage: invalid zone")
	}
	if (u.OwnerAtStart == nil) != (u.OwnerSeen == nil) {
		return fmt.Errorf("usage: owner_at_start and owner_seen are both null or both set")
	}
	return nil
}

// UsageResponse is GET /v1/usage: the rows whose job ended at or after Since, oldest first, at most the asked limit. Truncated says
// there are more after the last one; NextSince is then the end time of the last row, to ask again with (inclusive, so a row can come
// twice: drop repeats by job_id).
type UsageResponse struct {
	Host      string     `json:"host"`
	Since     time.Time  `json:"since"`
	Records   []JobUsage `json:"records"`
	Truncated bool       `json:"truncated"`
	NextSince *time.Time `json:"next_since"`
}

func (r UsageResponse) Validate() error {
	if r.Host == "" {
		return fmt.Errorf("usage: missing host")
	}
	if r.Truncated != (r.NextSince != nil) {
		return fmt.Errorf("usage: truncated and next_since go together")
	}
	for i, u := range r.Records {
		if err := u.Validate(); err != nil {
			return fmt.Errorf("records[%d]: %w", i, err)
		}
	}
	return nil
}
