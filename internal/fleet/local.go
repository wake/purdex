// Package fleet: the local source of spec rev 2 §7
// (docs/specs/2026-10-14-fleet-status-spec.md, branch docs/workbook-journal-spec).
//
// The interface is used by the fleet module; the implementation lives outside
// internal/fleet (the A line's resources sampler). Until it lands, FakeLocal
// stands in and reports nothing for the local host.
package fleet

import (
	"time"

	"github.com/wake/purdex/internal/gate"
)

// LocalSourceKey is the registry key under which the daemon's resources module offers its LocalSource (A line); the fleet
// module resolves it in Init and runs with an empty FakeLocal when it is missing.
const LocalSourceKey = "fleet.local-source"

// Proc is one process in a top list.
type Proc struct {
	PID   int     `json:"pid"`
	Name  string  `json:"name"`
	CPU   float64 `json:"cpu"`    // this process's CPU %, may exceed 100
	RSSMB float64 `json:"rss_mb"` // resident memory in MB
}

type LocalFast struct {
	At       time.Time
	CPU      float64 // real CPU use of the whole host since the previous tick, 0–100
	Mem      float64 // used memory, 0–100
	Level    int     // kern.memorystatus_vm_pressure_level: 1, 2 or 4
	PageRate float64 // pageouts + swapouts per second over hostmem.RateWindow
	Load1    float64
	NCPU     int
}

type LocalSlow struct {
	At     time.Time
	TopCPU []Proc       // 5, by CPU
	TopMem []Proc       // 5, by RSS
	Gate   *gate.Status // the local gate summary (watch pass HeldSlots); nil when no gate is installed
}

type LocalSource interface {
	Start()                   // begin the 1 s tick: called when this daemon becomes primary
	Stop()                    // end it: called when it steps down
	Fast() (LocalFast, bool)  // the newest 1 s reading; false before the first
	Slow() (LocalSlow, bool)  // the newest 5 s reading; false before the first
	Changed() <-chan struct{} // signalled after each new fast reading (coalesced, never blocks the sampler)
}
