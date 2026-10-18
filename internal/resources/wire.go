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
	// ReasonWarmingUp is what a snapshot says before the first sample lands.
	ReasonWarmingUp = "warming_up"
	// ReasonOff is what a snapshot says while the host setting has mode off:
	// the sampler skips its reads and the figures, if any, are stale.
	ReasonOff = "off"
)

// ModeMeasure is sampling without admission: the mode P0 always reported, and
// what the module falls back to when it cannot read or store its settings.
const ModeMeasure = "measure"

// Snapshot is one reading of the host. Mode is the host setting's mode. (The lease lists it once carried - leases, waiters,
// recent, positions, gap_until - went with the lease routes, WWD-0a.)
type Snapshot struct {
	SampledAt time.Time    `json:"sampled_at"`
	Available bool         `json:"available"`
	Reason    string       `json:"reason,omitempty"`
	Capacity  int          `json:"capacity"`
	Host      HostUse      `json:"host"`
	Sessions  []SessionUse `json:"sessions"`
	Mode      string       `json:"mode"`
	// Samples is the ring of the last 10 minutes, oldest first; only when
	// asked for with ?samples=1.
	Samples []TrendPoint `json:"samples,omitempty"`
	// SampleMS is how long the tick that produced this snapshot took, for
	// seeing the cost of sampling; a debug field.
	SampleMS int64 `json:"sample_ms,omitempty"`
}

// MarshalJSON writes sampled_at in UTC whatever zone the time carries.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	type plain Snapshot
	p := plain(s)
	p.SampledAt = p.SampledAt.UTC()
	return json.Marshal(p)
}

// HostReaderKey is the registry key of the HostReader the resources module offers: the latest host sample, for a reader that must
// not sample (or fork) itself, such as a failure's detail naming the load it happened under.
const HostReaderKey = "resources.host-reader"

// HostReader is the latest published host reading; ok is false until a tick has published a usable one.
type HostReader interface {
	LatestHost() (host HostUse, ok bool)
}

// HostDetailReader is what a reader that needs the situation behind the zone asks for (the spawn resource card, RT-5a): the
// latest host reading with its sample time. The object registered under HostReaderKey offers it too, so there is one key; a
// reader type-asserts for it and treats its absence as "unknown".
type HostDetailReader interface {
	LatestHostDetail() (HostDetail, bool)
}

// HostDetail is the host part of the latest snapshot. (It carried the held leases and the queue until WWD-0a removed them.)
type HostDetail struct {
	Host HostUse `json:"host"`
	// SampledAt is when the reading was taken, unix ms.
	SampledAt int64 `json:"sampled_at"`
	// SessionsRoom is wwd's derived "how many more sessions this host can take" (facts.derived.sessions_room); nil when the
	// reading is not wwd's. It carries no judgement of this package's: the reader that decides whether another member may be
	// opened asks for it.
	SessionsRoom *int `json:"sessions_room,omitempty"`
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
	Load5             float64 `json:"load5,omitempty"`
	Load15            float64 `json:"load15,omitempty"`
	NCPU              int     `json:"ncpu"`
	MemBytes          uint64  `json:"mem_bytes"`
	MemUsedBytes      uint64  `json:"mem_used_bytes"`
	Pressure          int     `json:"pressure"`           // 1, 2 or 4; 0 unknown
	MemorystatusLevel int     `json:"memorystatus_level"` // 0 to 100; -1 unknown
	// PageRate is the pages written out (pageouts + swapouts) per second over the last seconds, the second input of the zone
	// beside Pressure; the gate admits by the same two figures. Absent while there is no baseline yet.
	PageRate float64 `json:"page_rate,omitempty"`
	// PcpuTotal is the sum of pcpu over all processes divided by ncpu, kept
	// for calibrating learned weights.
	PcpuTotal float64 `json:"pcpu_total"`
	// Full is R5's "host full": load1 >= ncpu, or Mem >= MemFullPercent, or
	// pressure >= 2.
	Full bool `json:"full"`
	// Zone is normal, warning, danger or unknown (lease v2 spec D-1′); Trend
	// is rising, flat, falling or unknown (D-2′). ZoneSince is when the zone
	// last changed, unix ms.
	Zone      string `json:"zone"`
	Trend     string `json:"trend"`
	ZoneSince int64  `json:"zone_since"`
}

// TrendPoint is one sample of the sampler's ring, for GET
// /api/resources?samples=1 (debugging and the G1 measurement).
type TrendPoint struct {
	At       int64   `json:"at"` // unix ms
	Mem      float64 `json:"mem"`
	CPU      float64 `json:"cpu"`
	Pressure int     `json:"pressure"`
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

// EventType is the WS host event that carries the snapshot.
const EventType = "resources.changed"

// Error codes of the resources routes, in APIError.Error.
const (
	ErrBadRequest = "bad_request"
	ErrNotReady   = "not_ready" // the daemon is stopping, or resources.db is not open
)

// APIError is the body of a refused request: {"error": code, "detail": text}.
type APIError struct {
	Error  string `json:"error"`
	Detail string `json:"detail,omitempty"`
}
