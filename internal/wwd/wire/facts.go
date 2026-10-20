package wire

import (
	"fmt"
	"time"
)

// Facts is the one record per host (spec §3.1). A field a platform cannot read
// is a pointer (or nil slice) serialised as null, never omitted.
type Facts struct {
	V         int         `json:"v"`
	HostID    string      `json:"host_id"`
	Alias     string      `json:"alias"`
	At        time.Time   `json:"at"`
	Platform  string      `json:"platform"`
	Arch      string      `json:"arch"`
	OSVersion string      `json:"os_version"`
	CPU       CPU         `json:"cpu"`
	Mem       Mem         `json:"mem"`
	Disk      []Disk      `json:"disk"`
	Procs     []Proc      `json:"procs"`
	Residents []Resident  `json:"residents"`
	Tools     Tools       `json:"tools"`
	Owner     *OwnerState `json:"owner"` // air26 only; null elsewhere
	Exception []FactExc   `json:"exceptions"`
	Derived   Derived     `json:"derived"`
	Worker    FactsWorker `json:"worker"`
	// Stats is on a per-minute rollup only (a 5 s record has no "stats" key, and a minute stored before this existed
	// decodes with it nil): what the minute's 5 s samples add up to, which the rollup record itself cannot say.
	Stats *MinuteStats `json:"stats,omitempty"`
	// Queue is this host's queued jobs (spec §13.2); absent before proto 2 writers fill it.
	Queue *Queue `json:"queue,omitempty"`
	// ProcsTotal is how many processes the host has at the sample (the raw count of the process table, including those too short-lived
	// to be read). BootAt is when the host booted. Self is wwd's own use. Each is null where it was not read: an older wwd, an older
	// minute record, a platform without the source (host-metrics spec §2.1). They are not gated on FactsProto.
	ProcsTotal *int       `json:"procs_total"`
	BootAt     *time.Time `json:"boot_at"`
	Self       *Self      `json:"self"`
}

// Self is wwd's own use of the host. CPUPct is one core = 100 over the interval since the previous sample (null for the first sample);
// RSSMB is its memory in MB (macOS: the physical footprint, as for any process in the list; Linux: the resident set).
type Self struct {
	CPUPct *float64 `json:"cpu_pct"`
	RSSMB  *int     `json:"rss_mb"`
}

// SampleIntervalS is the seconds one facts sample stands for (spec §3.1: one record every 5 s).
const SampleIntervalS = 5

// MinuteStats is worked out per 5 s sample inside wwd and totalled per minute (internal/wwd/derived.MinuteAgg).
// SampledS counts the seconds that were really sampled (5 per sample), so a minute wwd started or restarted inside has
// fewer than 60. Full is the "host is full" flag with its 0.9 hysteresis (derived.FullLatch, the same definition as
// resources.FullLatch): FullTicks the samples it was on, FullStarts its off-to-on transitions in the minute,
// FullLongestS the longest run of it that was running in the minute (a run that began earlier counts, and may be longer
// than the minute). Normal/Warning/DangerS are the seconds sampled in each zone (an unknown zone is in SampledS and in
// none). The running maxima are the worker's. GateWaitingMax is null: wwd has no gate to count until the gate is folded
// into it (WWD-8/9).
type MinuteStats struct {
	SampledS        int  `json:"sampled_s"`
	FullTicks       int  `json:"full_ticks"`
	FullStarts      int  `json:"full_starts"`
	FullLongestS    int  `json:"full_longest_s"`
	NormalS         int  `json:"normal_s"`
	WarningS        int  `json:"warning_s"`
	DangerS         int  `json:"danger_s"`
	HeavyRunningMax int  `json:"heavy_running_max"`
	LightRunningMax int  `json:"light_running_max"`
	GateWaitingMax  *int `json:"gate_waiting_max"`
}

// Validate checks that the figures can be a minute's.
func (s MinuteStats) Validate() error {
	for name, v := range map[string]int{
		"sampled_s": s.SampledS, "full_ticks": s.FullTicks, "full_starts": s.FullStarts, "full_longest_s": s.FullLongestS,
		"normal_s": s.NormalS, "warning_s": s.WarningS, "danger_s": s.DangerS,
		"heavy_running_max": s.HeavyRunningMax, "light_running_max": s.LightRunningMax,
	} {
		if v < 0 {
			return fmt.Errorf("stats: %s is negative", name)
		}
	}
	if s.GateWaitingMax != nil && *s.GateWaitingMax < 0 {
		return fmt.Errorf("stats: gate_waiting_max is negative")
	}
	if s.NormalS+s.WarningS+s.DangerS > s.SampledS {
		return fmt.Errorf("stats: the zone seconds add up to more than sampled_s")
	}
	if s.FullTicks*SampleIntervalS > s.SampledS {
		return fmt.Errorf("stats: full_ticks (%d samples of %d s) is more than sampled_s", s.FullTicks, SampleIntervalS)
	}
	if s.FullStarts > s.FullTicks {
		return fmt.Errorf("stats: full_starts is more than full_ticks")
	}
	return nil
}

type CPU struct {
	NCPU    int     `json:"ncpu"`
	Load1   float64 `json:"load1"`
	Load5   float64 `json:"load5"`
	IdlePct float64 `json:"idle_pct"`
}

// Mem: Pressure is 0 when unknown. PSI is Linux only; the other readings are
// null where the platform has no such counter.
type Mem struct {
	TotalMB      int      `json:"total_mb"`
	AvailableMB  int      `json:"available_mb"`
	Pressure     int      `json:"pressure"`
	PageOutRate  *float64 `json:"page_out_rate"`
	SwapOutRate  *float64 `json:"swap_out_rate"`
	SwapUsedMB   *int     `json:"swap_used_mb"`
	CompressorMB *int     `json:"compressor_mb"`
	PSI          *PSI     `json:"psi"`
	// DecompressRate is the compressor's decompressions per second (macOS; spec §13.2). Absent where it is not read, and
	// in every record until the writers are gated on proto 2 (§13.1); omitted rather than null so a proto 1 reader is
	// never shown it.
	DecompressRate *float64 `json:"decompress_rate,omitempty"`
	// SwapTotalMB is the swap the host can hold (vm.swapusage total; Linux SwapTotal). The breakdown below is of the same read as
	// AvailableMB: macOS host_statistics64 / Linux /proc/meminfo. All are null where the platform has no such figure (Linux has no
	// wired), in an older record, or when the read failed (host-metrics spec §2.1).
	SwapTotalMB *int `json:"swap_total_mb"`
	WiredMB     *int `json:"wired_mb"`
	ActiveMB    *int `json:"active_mb"`
	InactiveMB  *int `json:"inactive_mb"`
	FileCacheMB *int `json:"file_cache_mb"`
}

// Queue is this host's queue as facts show it (spec §13.2). Absent until the writers are gated on proto 2.
type Queue struct {
	Queued int `json:"queued"`
}

type PSI struct {
	Some10 float64 `json:"some10"`
	Full10 float64 `json:"full10"`
	Some60 float64 `json:"some60"`
}

type Disk struct {
	Mount   string  `json:"mount"`
	FreeGB  float64 `json:"free_gb"`
	UsedPct float64 `json:"used_pct"`
	// statfs as read: the size of the file system and what an unprivileged user
	// can still take, so a reader need not infer them from used_pct.
	TotalBytes uint64 `json:"total_bytes"`
	AvailBytes uint64 `json:"avail_bytes"`
}

type Proc struct {
	PID     int        `json:"pid"`
	Name    string     `json:"name"`
	CPUPct  float64    `json:"cpu_pct"`
	MemMB   int        `json:"mem_mb"`
	MemKind string     `json:"mem_kind"`
	Owner   *ProcOwner `json:"owner"` // null when attribution failed
}

// ProcOwner: ID and Session are omitted when not applicable (e.g. system).
type ProcOwner struct {
	Kind    OwnerKind `json:"kind"`
	ID      string    `json:"id,omitempty"`
	Session string    `json:"session,omitempty"`
}

type Resident struct {
	Session string `json:"session"`
	Kind    string `json:"kind"`
	Count   int    `json:"count"`
	MemMB   int    `json:"mem_mb"`
}

// Tools lists toolchain versions; unavailable tools are null.
type Tools struct {
	Go    *string  `json:"go"`
	Node  *string  `json:"node"`
	Pnpm  *string  `json:"pnpm"`
	PHP   []string `json:"php"`
	Xcode *string  `json:"xcode"`
}

type OwnerState struct {
	Active bool   `json:"active"`
	IdleS  int    `json:"idle_s"`
	Reason string `json:"reason"`
}

// FactExc is an exception as shown inside facts.
type FactExc struct {
	Name  string     `json:"name"`
	Since time.Time  `json:"since"`
	Until *time.Time `json:"until"` // null for pid-bound exceptions
	// Allow and Flags are the exception's hold fields (spec §13.4), shown so every reader sees a hold from the pool facts.
	Allow *HoldAllow `json:"allow,omitempty"`
	Flags []HoldFlag `json:"flags,omitempty"`
}

type Slots struct {
	Heavy int `json:"heavy"`
	Light int `json:"light"`
}

type Derived struct {
	Zone Zone `json:"zone"`
	// ZoneOverride is set only while a fault hook is on (fault.zone): the zone peers score this host's jobs by. Zone stays the real one,
	// so every other reader (the Purdex gate feed, the app's host cell, headroom) is unaffected. Absent when no hook is on.
	ZoneOverride Zone  `json:"zone_override,omitempty"`
	Headroom     Slots `json:"headroom"`
	SessionsRoom int   `json:"sessions_room"`
	// DiskLow is true only while the work volume has less free space than the worker's disk floor: headroom.heavy is then 0 with the
	// reason disk_low (spec §3.2). Absent otherwise, so a record with room reads as it always did.
	DiskLow bool `json:"disk_low,omitempty"`
}

// PickZone is the zone a worker scores this origin's jobs by (spec 4.6 origin_bonus): the fault override when one is set, else Zone.
// Nothing but a worker's pick reads it; every other reader uses Zone.
func (d Derived) PickZone() Zone {
	if d.ZoneOverride != "" {
		return d.ZoneOverride
	}
	return d.Zone
}

// FactsWorker is a host's worker state as every reader sees it. OwnerRule is
// the same free text as WorkerSettings.owner_rule ("" when the host has no
// owner rule).
type FactsWorker struct {
	Enabled   bool   `json:"enabled"`
	Slots     Slots  `json:"slots"`
	Running   Slots  `json:"running"`
	OwnerRule string `json:"owner_rule"`
}

// Validate checks the version and enumerations.
func (f Facts) Validate() error {
	if f.V < MinProto || f.V > FactsProto {
		return fmt.Errorf("facts: unsupported v %d", f.V)
	}
	if f.Mem.DecompressRate != nil && *f.Mem.DecompressRate < 0 {
		return fmt.Errorf("facts: negative decompress_rate")
	}
	if f.Queue != nil && f.Queue.Queued < 0 {
		return fmt.Errorf("facts: negative queue.queued")
	}
	for i, e := range f.Exception {
		if err := validateHoldFields(e.Allow, e.Flags); err != nil {
			return fmt.Errorf("facts: exceptions[%d]: %w", i, err)
		}
	}
	if f.Derived.ZoneOverride != "" && !f.Derived.ZoneOverride.Valid() {
		return fmt.Errorf("facts: invalid zone_override %q", f.Derived.ZoneOverride)
	}
	if !f.Derived.Zone.Valid() {
		return fmt.Errorf("facts: invalid zone %q", f.Derived.Zone)
	}
	for i, p := range f.Procs {
		if p.Owner != nil && !p.Owner.Kind.Valid() {
			return fmt.Errorf("facts: procs[%d]: invalid owner kind %q", i, p.Owner.Kind)
		}
	}
	if f.Stats != nil {
		if err := f.Stats.Validate(); err != nil {
			return fmt.Errorf("facts: %w", err)
		}
	}
	return nil
}
