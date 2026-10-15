package resourcesmod

import (
	"github.com/wake/purdex/internal/gate"
)

// What the daemon reads of the gate (host-load-control spec §6, L2): the gate's config (the page rate at which it holds work
// is the limit of the zone) and the slot records (how many heavy and light commands run, how many wait: the host timeline's
// heavy_running / light_running / gate_waiting). Both are files the gate itself keeps, read the way `pdx gate status` reads
// them; the daemon asks nothing of a gate and takes nothing.

// gateCfg is the gate's config now. A module that was not wired to the host (a test's) gets the defaults, and never reads the
// home directory it runs in.
func (m *Module) gateCfg() gate.Config {
	if m.gateConfig == nil {
		return gate.Defaults()
	}
	return m.gateConfig()
}

// hostGateConfig reads the gate's config file again each time it is asked (it is small, and a change applies to the next
// tick); a file that cannot be read keeps the last good one.
func hostGateConfig() func() gate.Config {
	path := gate.DefaultConfigPath()
	last := gate.Defaults()
	return func() gate.Config {
		cfg, err := gate.Load(path)
		if err != nil {
			return last
		}
		last = cfg
		return cfg
	}
}

// slotCounts is how many heavy and light commands hold a slot right now, and how many wait. ok is false when the module has
// no way to read them (a test's): the minute then records zeros, not a reading.
func (m *Module) slotCounts() (heavy, light, waiting int, ok bool) {
	if m.gateCounts != nil {
		return m.gateCounts()
	}
	if m.gateConfig == nil {
		return 0, 0, 0, false
	}
	cfg := m.gateConfig()
	h := &gate.Pool{Dir: cfg.Dir, Class: gate.ClassHeavy, Size: cfg.Slots.Size(gate.ClassHeavy), Now: m.now}
	l := &gate.Pool{Dir: cfg.Dir, Class: gate.ClassLight, Size: cfg.Slots.Size(gate.ClassLight), Now: m.now}
	return len(h.Held()), len(l.Held()), h.PeekWaiting() + l.PeekWaiting(), true
}
