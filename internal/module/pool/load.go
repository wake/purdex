package poolmod

import (
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The host load score of the fleet ring, moved here from internal/fleet (WWD-9 9d-3a) so that the pool no longer depends on the fleet module,
// which goes in 9d-4. The declarations below are byte for byte the ones in internal/fleet/load.go and local.go; the copies there stay until
// that package goes.

// The lines of the composite load (internal/fleet/load.go).
const (
	diskScoreFrom  = 85.0 // disk counts toward the composite only from here
	pageRateCap    = 100.0
	memWarnLevel   = 2
	memDangerLevel = 4
)

type LocalFast struct {
	At       time.Time
	CPU      float64 // real CPU use of the whole host since the previous tick, 0–100
	Mem      float64 // used memory, 0–100
	Level    int     // kern.memorystatus_vm_pressure_level: 1, 2 or 4
	PageRate float64 // pageouts + swapouts per second over hostmem.RateWindow
	Load1    float64
	NCPU     int
	// Missing names what this reading could not have ("cpu", "pageouts") and so is not to be believed; empty when it is whole.
	// A figure that is missing is absent, not 0.
	Missing []string
}

// LoadScore is Composite and the reading that is the largest of its five (wire.LoadDriver*): cpu, mem, pressure, paging or disk.
// On a tie the first in that order is the driver, and a host with no reading at all is "cpu" at 0.
func LoadScore(f LocalFast, diskPct float64, diskOK bool) (float64, string) {
	score, driver := clamp100(f.CPU), wire.LoadDriverCPU
	for _, c := range []struct {
		v      float64
		driver string
	}{
		{clamp100(f.Mem), wire.LoadDriverMem},
		{levelScore(f.Level), wire.LoadDriverLevel},
		{min(max(f.PageRate, 0), pageRateCap), wire.LoadDriverPaging},
	} {
		if c.v > score {
			score, driver = c.v, c.driver
		}
	}
	if diskOK && diskPct >= diskScoreFrom && clamp100(diskPct) > score {
		score, driver = clamp100(diskPct), wire.LoadDriverDisk
	}
	return score, driver
}

func clamp100(v float64) float64 { return min(max(v, 0), 100) }

func levelScore(level int) float64 {
	switch {
	case level >= memDangerLevel:
		return 100
	case level >= memWarnLevel:
		return 75
	case level == 1:
		return 30
	}
	return 0
}
