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
}

type Slots struct {
	Heavy int `json:"heavy"`
	Light int `json:"light"`
}

type Derived struct {
	Zone         Zone  `json:"zone"`
	Headroom     Slots `json:"headroom"`
	SessionsRoom int   `json:"sessions_room"`
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
	if f.V != Proto {
		return fmt.Errorf("facts: unsupported v %d", f.V)
	}
	if !f.Derived.Zone.Valid() {
		return fmt.Errorf("facts: invalid zone %q", f.Derived.Zone)
	}
	for i, p := range f.Procs {
		if p.Owner != nil && !p.Owner.Kind.Valid() {
			return fmt.Errorf("facts: procs[%d]: invalid owner kind %q", i, p.Owner.Kind)
		}
	}
	return nil
}
