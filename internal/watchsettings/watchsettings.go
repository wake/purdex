// Package watchsettings is the narrow read of the host setting `watch` (the load watcher, host-load-control spec §12.2), for
// modules that cannot import hostconfig (the resources module, which runs the watcher on its sampler tick).
package watchsettings

import "time"

// Key is the service-registry key under which the hostconfig module publishes itself as a Reader.
const Key = "hostconfig.watch-settings"

// Settings is the host setting `watch`: the switch and the thresholds. The delivery keys (targets, mode, …) come with W2a.
type Settings struct {
	Enabled bool `json:"enabled"`
	// E1: mean cores over TSeconds at or above XCores, or resident memory at or above YMB for that long.
	XCores   float64 `json:"x_cores"`
	YMB      int     `json:"y_mb"`
	TSeconds int     `json:"t_s"`
	// E2: pressure level at or above PressureLevel for PressureTSeconds, or page rate above PageRate for PageTSeconds.
	PressureLevel    int     `json:"pressure_level"`
	PressureTSeconds int     `json:"pressure_t_s"`
	PageRate         float64 `json:"page_rate"`
	PageTSeconds     int     `json:"page_t_s"`
}

// Default is what a never-written setting answers. The watcher is off until a host turns it on.
func Default() Settings {
	return Settings{Enabled: false, XCores: 1.5, YMB: 1024, TSeconds: 60, PressureLevel: 2, PressureTSeconds: 120, PageRate: 50, PageTSeconds: 60}
}

// Seconds is n seconds as a duration.
func Seconds(n int) time.Duration { return time.Duration(n) * time.Second }

// Reader is what a module type-asserts on the registry value. A stored value that no longer reads is an error: the caller
// decides what to do without a setting (the watcher stays off).
type Reader interface {
	WatchSettings() (Settings, error)
}
