// Package watchsettings is the narrow read of the host setting `watch` (the load watcher, host-load-control spec §12.2), for
// modules that cannot import hostconfig (the resources module, which runs the watcher on its sampler tick).
package watchsettings

import "time"

// Key is the service-registry key under which the hostconfig module publishes itself as a Reader.
const Key = "hostconfig.watch-settings"

// The values `mode` takes.
const (
	ModePatrol   = "patrol"   // the watcher rests when idle
	ModeResident = "resident" // it never rests
)

// Settings is the host setting `watch` (spec §12.2): the switch, the thresholds, and where the watcher is and how it is run.
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
	// Where events go and where a new watcher is started: host aliases, in order (this host's own alias included).
	Targets []string `json:"targets"`
	// Mode is ModePatrol or ModeResident; PatrolIdleMin is the rest countdown of patrol mode.
	Mode          string `json:"mode"`
	PatrolIdleMin int    `json:"patrol_idle_min"`
	// AutoWake: whether an event wakes or starts a watcher.
	AutoWake bool `json:"auto_wake"`
	// AgentCommand is the command run in the pdx-watch tmux session to start or resume the watcher (spec §12.2; it replaced
	// `model`). Workdir is its directory on this host (a checkout of the watch-desk branch); StartPrompt and WakePrompt replace
	// the built-in prompts when set.
	AgentCommand string `json:"agent_command"`
	Workdir      string `json:"workdir"`
	StartPrompt  string `json:"start_prompt"`
	WakePrompt   string `json:"wake_prompt"`
}

// Default is what a never-written setting answers: on (spec §12.2), with the first thresholds.
func Default() Settings {
	return Settings{Enabled: true, XCores: 1.5, YMB: 1024, TSeconds: 60, PressureLevel: 2, PressureTSeconds: 120, PageRate: 50, PageTSeconds: 60,
		Targets: []string{"air26", "mlab"}, Mode: ModePatrol, PatrolIdleMin: 120, AutoWake: true, AgentCommand: LegacyModelCommand("opus"),
		Workdir: "~/Workspace/wake/mlab-purdex-watch"}
}

// LegacyModelCommand is the agent_command a row written with `model` (741–744) stood for.
func LegacyModelCommand(model string) string {
	return "claude --dangerously-skip-permissions --model " + model
}

// Seconds is n seconds as a duration.
func Seconds(n int) time.Duration { return time.Duration(n) * time.Second }

// Reader is what a module type-asserts on the registry value. A stored value that no longer reads is an error: the caller
// decides what to do without a setting (the watcher stays off).
type Reader interface {
	WatchSettings() (Settings, error)
}
