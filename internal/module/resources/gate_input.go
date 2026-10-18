package resourcesmod

import (
	"math"
	"os"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/hostmem"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/wwd/wire"
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

// cachePressure gives the gate the reading the sampler just took (the pressure level and the paging counters, the two things
// the gate reads for itself with a sysctl and a vm_stat fork), by adding it to the gate's pressure.json under the gate's own
// lock. The gate reuses a reading younger than 5 s whoever wrote it, so while the daemon runs its commands fork less and see
// readings no older than before; without the daemon nothing changes. A reading that lacks the level or the counters is not
// written (a 0 level is "unknown"), and a module that was not wired to the host (a test's) writes nowhere.
func (m *Module) cachePressure(raw resources.HostRaw, sampledAt time.Time) {
	if m.gateConfig == nil || !raw.PageCounters || raw.Pressure <= 0 {
		return
	}
	hostmem.Cache{Dir: m.gateConfig().Dir}.Record(sampledAt, hostmem.Sample{
		At: sampledAt.UnixMilli(), Level: raw.Pressure, Pageouts: raw.Pageouts, Swapouts: raw.Swapouts})
}

// cachePressureFromFacts is cachePressure while wwd's facts are the host reading: the level is wwd's, and the counters the gate
// takes a rate from are the cache's own last counter plus wwd's page-out and swap-out rate over the time since that reading, so
// whoever wrote the last one (this daemon from its own sampler, another daemon, a gate) the sequence goes on from it and never
// goes back. wwd gives no rate (the platform has none) or no level, or the cache has no reading to go on from yet: the first
// reading of a run is a counter of 0 with the level, and nothing is written for a reading that lacks the level or the rate.
func (m *Module) cachePressureFromFacts(f wire.Facts, sampledAt time.Time) {
	if m.gateConfig == nil || f.Mem.Pressure <= 0 || (f.Mem.PageOutRate == nil && f.Mem.SwapOutRate == nil) {
		return
	}
	rate := 0.0
	if f.Mem.PageOutRate != nil {
		rate += *f.Mem.PageOutRate
	}
	if f.Mem.SwapOutRate != nil {
		rate += *f.Mem.SwapOutRate
	}
	cache := hostmem.Cache{Dir: m.gateConfig().Dir}
	var total uint64
	if have := cache.Load().Samples; len(have) > 0 {
		last := have[len(have)-1]
		total = last.Pageouts + last.Swapouts
		if dt := sampledAt.Sub(time.UnixMilli(last.At)); dt > 0 && dt <= factsCounterGap && rate > 0 {
			m.pageFrac += rate * dt.Seconds() // the fraction a slow rate adds per tick is kept, not dropped
			whole := math.Floor(m.pageFrac)
			total += uint64(whole)
			m.pageFrac -= whole
		}
	}
	cache.Record(sampledAt, hostmem.Sample{At: sampledAt.UnixMilli(), Level: f.Mem.Pressure, Pageouts: total})
}

// factsCounterGap is the longest gap between two cache readings over which the facts' rate is taken to have held; after a longer
// one the counter goes on from where it was (the rate window of the readers is shorter than this anyway).
const factsCounterGap = 15 * time.Second

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
	if _, err := os.Stat(cfg.Dir); err != nil {
		// no directory yet means no gate has run (zeros are right); one that cannot be looked at is no reading
		return 0, 0, 0, os.IsNotExist(err)
	}
	h := &gate.Pool{Dir: cfg.Dir, Class: gate.ClassHeavy, Size: cfg.Slots.Size(gate.ClassHeavy), Now: m.now}
	l := &gate.Pool{Dir: cfg.Dir, Class: gate.ClassLight, Size: cfg.Slots.Size(gate.ClassLight), Now: m.now}
	return len(h.Held()), len(l.Held()), h.PeekWaiting() + l.PeekWaiting(), true
}
