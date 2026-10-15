package gate

import "fmt"

// autoPageRate is what max_page_rate "auto" stands for: the pageouts + swapouts per second (over 10 s) above which the
// host is swapping for real. Measured on mlab in G0 (2026-10-15): every full run stayed at or under 28 pages/s, clean
// ones under 3; the pressure level is the main condition and this one backs it up.
const autoPageRate = 50.0

// Pressure is the macOS memory state a heavy command is admitted on (spec §5): the kernel's pressure level (1 normal,
// 2 warn, 4 critical) and how fast pages are being pushed to disk. The free-memory percentage is not used: on
// 10-15 it said 55 % free while the host was swapping.
type Pressure struct {
	Level    int
	PageRate float64 // pageouts + swapouts per second over the last sampling window
}

// Module 1b, admission: whether new work may start now. Running work is never touched.
type AdmissionConfig struct {
	Enabled          bool
	MaxPressureLevel int     // the kernel's memory pressure level (1 normal, 2 warn, 4 critical)
	MaxPageRate      float64 // pages per second; meaningful when not Auto
	PageRateAuto     bool    // max_page_rate = "auto": autoPageRate
	DiskMinGB        float64 // free space a heavy command needs in its directory and in $TMPDIR; 0 = no check
	OnUnreadable     string  // "level-only": three failed reads fall back to the level alone; "admit": admit at once
}

func parseAdmission(r raw) AdmissionConfig {
	rate, auto := r.FloatOrAuto("max_page_rate", autoPageRate, true, 0, 1e9)
	return AdmissionConfig{
		Enabled:          r.Bool("enabled", true),
		MaxPressureLevel: r.Int("max_pressure_level", 1, 1, 4),
		MaxPageRate:      rate,
		PageRateAuto:     auto,
		DiskMinGB:        r.Float("disk_min_gb", 10, 0, 1e6),
		OnUnreadable:     r.Enum("on_unreadable", "level-only", "level-only", "admit"),
	}
}

// PageRateLimit is the rate at and above which nothing new starts.
func (a AdmissionConfig) PageRateLimit() float64 {
	if a.PageRateAuto {
		return autoPageRate
	}
	return a.MaxPageRate
}

// Admit says whether new work may start under p.
func (a AdmissionConfig) Admit(p Pressure) bool {
	if !a.Enabled {
		return true
	}
	return p.Level <= a.MaxPressureLevel && p.PageRate < a.PageRateLimit()
}

// DiskRefusal is the message of a refusal when the free space is below the floor, "" when it is enough.
func (a AdmissionConfig) DiskRefusal(path string, freeBytes int64) string {
	if !a.Enabled || a.DiskMinGB <= 0 {
		return ""
	}
	if float64(freeBytes) >= a.DiskMinGB*(1<<30) { // in floats: no conversion to overflow
		return ""
	}
	return fmt.Sprintf("%s has %.1f GB free, below the %.1f GB of [gate.admission] disk_min_gb: the command was not run (free some space, or lower the floor)",
		path, float64(freeBytes)/(1<<30), a.DiskMinGB)
}
