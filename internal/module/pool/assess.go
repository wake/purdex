package poolmod

import (
	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/wire"
)

// loadOf is a host's composite load from its facts record (capability pool.assess.v1): the one score the fleet ring uses
// (LoadScore), so the App only draws it. The CPU is 100 less the idle share, and not read at all where the record names no cpu
// count (a failed source leaves zeros); memory is the share in use; paging is page-outs plus swap-outs per second; the disk is the
// fullest mount.
func loadOf(f wire.Facts) wire.HostLoad {
	lf := LocalFast{Level: f.Mem.Pressure}
	if f.CPU.NCPU > 0 {
		lf.CPU = 100 - f.CPU.IdlePct
	}
	if f.Mem.TotalMB > 0 {
		lf.Mem = float64(f.Mem.TotalMB-f.Mem.AvailableMB) / float64(f.Mem.TotalMB) * 100
	}
	for _, r := range []*float64{f.Mem.PageOutRate, f.Mem.SwapOutRate} {
		if r != nil {
			lf.PageRate += *r // two huge readings add up to +Inf, which LoadScore holds to its ceiling (TestLoadOf)
		}
	}
	disk, diskOK := 0.0, false
	for _, d := range f.Disk {
		if !diskOK || d.UsedPct > disk {
			disk, diskOK = d.UsedPct, true
		}
	}
	score, driver := LoadScore(lf, disk, diskOK)
	return wire.HostLoad{Score: score, Driver: driver}
}

// assess fills a host's judgements from its facts: why its headroom is 0 (derived.Why) and its load. A host without facts has neither.
func assess(h *wire.PoolHost) {
	if h.Facts == nil {
		return
	}
	r := derived.Why(*h.Facts)
	l := loadOf(*h.Facts)
	h.HeadroomReasons, h.Load = &r, &l
}
