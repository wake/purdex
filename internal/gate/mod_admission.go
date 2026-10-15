package gate

import "fmt"

// autoPageRate is what max_page_rate "auto" stands for: the pageouts + swapouts per second above which the host is
// swapping for real. Measured on mlab in G0 (host-load-control plan): quiet runs stay under 1 page/s; the 10-15 incident
// ran at several hundred.
const autoPageRate = 100.0

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
	rate, auto := r.FloatOrAuto("max_page_rate", autoPageRate, true, 0)
	return AdmissionConfig{
		Enabled:          r.Bool("enabled", true),
		MaxPressureLevel: r.Int("max_pressure_level", 1, 1, 4),
		MaxPageRate:      rate,
		PageRateAuto:     auto,
		DiskMinGB:        r.Float("disk_min_gb", 10, 0),
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
	if freeBytes >= int64(a.DiskMinGB*(1<<30)) {
		return ""
	}
	return fmt.Sprintf("%s has %.1f GB free, below the %.1f GB of [gate.admission] disk_min_gb: the command was not run (free some space, or lower the floor)",
		path, float64(freeBytes)/(1<<30), a.DiskMinGB)
}
