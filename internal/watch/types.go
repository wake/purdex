// Package watch is the load watcher's detector (host-load-control spec §12): from the process table, the host's memory
// pressure and the gate's slot records of each sampler tick it says which commands run outside the gate and heavy (E1),
// when the host is under pressure (E2), and which slots are held by a gate that is gone (E3) or have been held a long
// time (E4). It is pure: the clock, the process table and everything else it reads come in as an Input, and what it
// remembers across restarts goes through a Memory. Recording, delivery and settings are other packages' work.
package watch

import (
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/resources"
)

// Kind is the kind of an event (spec §12.1).
type Kind string

const (
	KindUnmanagedHeavy Kind = "unmanaged_heavy" // E1: a tree outside the gate over the cores or memory threshold
	KindPressureHigh   Kind = "pressure_high"   // E2: pressure level or page rate high for a while
	KindSlotOrphaned   Kind = "slot_orphaned"   // E3: a slot held while its gate is gone
	KindSlotLong       Kind = "slot_long"       // E4: a slot held longer than lifetime.alert_after_min
)

// Label says why an E1 tree was not under the gate, from the gate's own classification of its command (ClassifyReason).
type Label string

const (
	LabelGateGap Label = "gate_gap" // the gate would have taken a slot for it (heavy or light) and it ran without one
	LabelUnknown Label = "unknown"  // a tool or script the gate's table does not know
	LabelAllowed Label = "allowed"  // the gate lets it through on purpose (packaging, a watch, a known cheap command)
)

// Settings are the thresholds (read at every pass by the caller; the defaults are the first values, tuned on the running
// host).
type Settings struct {
	XCores        float64       // E1: mean cores over TS at or above this…
	YMB           int           // …or resident memory at or above this for TS
	TS            time.Duration // E1: the window
	PressureLevel int           // E2: kern.memorystatus_vm_pressure_level at or above this…
	PressureTS    time.Duration // …for this long
	PageRate      float64       // E2: pages written out per second above this…
	PageTS        time.Duration // …for this long
	OrphanAfter   time.Duration // E3: a slot orphaned for this long
	SlotLongAfter time.Duration // E4: a slot held this long; 0 = never
}

// DefaultSettings are the first thresholds (plan W1b; W0 tuning replaces them).
func DefaultSettings() Settings {
	return Settings{
		XCores: 1.5, YMB: 1024, TS: 60 * time.Second,
		PressureLevel: 2, PressureTS: 120 * time.Second,
		PageRate: 50, PageTS: 60 * time.Second,
		OrphanAfter: 15 * time.Minute, SlotLongAfter: 60 * time.Minute,
	}
}

// Input is what one pass reads: one sampler tick.
type Input struct {
	Now   time.Time
	Procs []resources.Proc // the sampler's process table (with Comm)
	Raw   resources.HostRaw
	// Holders are the held slots of both gate pools (gate.Pool.Held), Orphaned set where the gate that took the slot is gone.
	Holders []gate.Holder
	// Roots are the sessions' root processes, already checked against a reused pid by the caller; the detector only uses them
	// to say which session a tree belongs to.
	Roots []resources.Root
	// Argv is the command line of a process, for the few the detector asks about (a `pdx` that may be a gate, the root of a
	// tree that crossed a threshold); nil, or a nil answer, is "unknown".
	Argv func(pid int) []string
	// Cwd is the working directory of a process, asked once for the root of a tree at the moment its event is made (one lookup
	// per episode, never per tick); nil, or "", falls back to the session's cwd.
	Cwd func(pid int) string
	// LockPIDs are the processes that hold a slot's lock file (lsof), for E3; called at most once per pass.
	LockPIDs func(h gate.Holder) []int
	Set      Settings
}

// Event is one detection.
type Event struct {
	Kind Kind
	At   time.Time
	// E1
	Label     Label
	Signature string
	Argv      []string // as read; the record scrubs it
	Cwd       string   // the session's cwd, when the tree belongs to one
	Session   string
	PeakCores float64
	PeakMB    uint64
	Duration  time.Duration
	New       bool // the signature's first sighting on this host
	// E2
	Pressure int
	PageRate float64
	Detail   string // what held: "pressure level 2 for 2m0s", "page rate 120/s for 1m0s"
	// E3, E4
	Slot      string // "heavy-1"
	HolderPID int    // the gate's pid in the slot record
	LockPIDs  []int  // E3: the processes that hold the lock
}

// Memory is what the detector keeps across passes and, in the persistent implementation, across restarts.
type Memory interface {
	// Sighting records one sighting of a command signature at a time and says whether it is the first on this host.
	Sighting(sig string, at time.Time) (first bool)
	// OrphanSince is when the holder was first seen orphaned: the time of the first call for a key, then the same time.
	OrphanSince(key string, now time.Time) time.Time
	// Reported says whether an E3 or E4 event was already made for a holder (its key), also before a restart: E3 is never folded,
	// so a restart must not send it again. MarkReported records it.
	Reported(kind Kind, key string) bool
	MarkReported(kind Kind, key string)
	// KeepReported forgets the reported marks of holders that are no longer there (every key but these).
	KeepReported(keys map[string]bool)
	// KeepOrphans forgets every orphan key but these: the holders that are orphaned now. A holder that is no longer orphaned,
	// or no longer there, drops out, also one remembered from before a restart.
	KeepOrphans(keys map[string]bool)
}
