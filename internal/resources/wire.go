// Package resources measures how busy this host is and how much of that
// belongs to each agent session. It is a library: the module that owns the
// sampling ticker and the HTTP surface lives elsewhere.
//
// Units (spec D-1): 100 means the host fully busy. CPU is load1 / ncpu and
// memory is the share of RAM in use, both as percentages.
package resources

import (
	"encoding/json"
	"time"
)

const (
	// Capacity is what 100 means: the host fully busy.
	Capacity = 100
	// SampleInterval is how often the owning module takes a sample.
	SampleInterval = 5 * time.Second
	// PressureWarnFloor is the measured use a memory pressure warn (level 2)
	// counts as at least.
	PressureWarnFloor = 90
	// PressureCriticalFloor is the measured use a memory pressure critical
	// (level 4) counts as.
	PressureCriticalFloor = 100
	// MemFullPercent is the memory in use at which the host counts as full.
	MemFullPercent = 90
)

// Snapshot reasons for Available = false.
const (
	ReasonUnsupportedPlatform = "unsupported_platform"
	ReasonSampleFailed        = "sample_failed"
)

// Snapshot is one reading of the host. P1 adds Leases and Waiters; those
// field names are reserved so the App contract stays stable, and nothing
// emits them in P0.
type Snapshot struct {
	SampledAt time.Time    `json:"sampled_at"`
	Available bool         `json:"available"`
	Reason    string       `json:"reason,omitempty"`
	Capacity  int          `json:"capacity"`
	Host      HostUse      `json:"host"`
	Sessions  []SessionUse `json:"sessions"`
	Mode      string       `json:"mode"`
}

// MarshalJSON writes sampled_at in UTC whatever zone the time carries.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	type plain Snapshot
	p := plain(s)
	p.SampledAt = p.SampledAt.UTC()
	return json.Marshal(p)
}

// HostUse is the host-wide part of a Snapshot.
type HostUse struct {
	// Measured is the D-1 figure, 0 to 100.
	Measured int `json:"measured"`
	// CPU is load1 / ncpu as a percentage; it may exceed 100.
	CPU float64 `json:"cpu"`
	// Mem is the vm_stat in-use share as a percentage.
	Mem               float64 `json:"mem"`
	Load1             float64 `json:"load1"`
	NCPU              int     `json:"ncpu"`
	MemBytes          uint64  `json:"mem_bytes"`
	MemUsedBytes      uint64  `json:"mem_used_bytes"`
	Pressure          int     `json:"pressure"`           // 1, 2 or 4; 0 unknown
	MemorystatusLevel int     `json:"memorystatus_level"` // 0 to 100; -1 unknown
	// PcpuTotal is the sum of pcpu over all processes divided by ncpu, kept
	// for calibrating learned weights.
	PcpuTotal float64 `json:"pcpu_total"`
	// Full is R5's "host full": load1 >= ncpu, or Mem >= MemFullPercent, or
	// pressure >= 2.
	Full bool `json:"full"`
}

// SessionUse is what one agent session's process tree costs.
type SessionUse struct {
	SessionID string `json:"session_id"`
	PID       int    `json:"pid"`
	Tmux      string `json:"tmux,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	// CPU is the sum of pcpu over the tree divided by ncpu, in host percent.
	CPU float64 `json:"cpu"`
	// Mem is the sum of rss over the tree divided by memory size, in percent.
	Mem float64 `json:"mem"`
	// Use is ceil(max(CPU, Mem)).
	Use      int    `json:"use"`
	RSSBytes uint64 `json:"rss_bytes"`
	// Pcpu is the raw sum of pcpu; 100 is one core.
	Pcpu  float64 `json:"pcpu"`
	Procs int     `json:"procs"`
}
