// Package facts samples one host's own load (spec §3.1): cpu, mem, disk now,
// procs / residents / tools / owner in later steps. Platform reads sit behind
// Sources so every field has a fixture test; the darwin Sources use sysctl and
// Mach only (no fork per sample).
package facts

import (
	"errors"
	"time"

	"github.com/wake/purdex/internal/hostmem"
	"github.com/wake/purdex/internal/wwd/wire"
)

const mib = 1 << 20

// RawCPU is one reading of the host's CPU: load averages and tick counters
// summed over every core, cumulative since boot.
type RawCPU struct {
	Load1, Load5 float64
	NCPU         int
	Busy, Total  uint64
}

// RawMem is one reading of memory. Free and Inactive are page counts;
// Pageouts and Swapouts are cumulative pages. The pointer fields are nil where
// the platform has no such counter; NoPaging marks Pageouts/Swapouts unreadable.
type RawMem struct {
	TotalBytes, PageSize uint64
	Free, Inactive       uint64
	Level                int // kernel pressure level: 1 normal, 2 warning, 4 critical; 0 unknown
	Pageouts, Swapouts   uint64
	NoPaging             bool
	SwapUsedBytes        *uint64
	CompressorBytes      *uint64
}

// RawDisk is one mount: sizes in bytes. AvailBytes is what an unprivileged
// user can use, FreeBytes is every free block.
type RawDisk struct {
	Mount                             string
	TotalBytes, FreeBytes, AvailBytes uint64
}

// BuildCPU turns two readings into the wire record. With no earlier reading the
// idle share is that since boot.
func BuildCPU(prev *RawCPU, cur RawCPU) wire.CPU {
	out := wire.CPU{NCPU: cur.NCPU, Load1: cur.Load1, Load5: cur.Load5, IdlePct: 100}
	busy, total := cur.Busy, cur.Total
	if prev != nil && cur.Total > prev.Total && cur.Busy >= prev.Busy {
		busy, total = cur.Busy-prev.Busy, cur.Total-prev.Total
	}
	if total > 0 {
		out.IdlePct = clamp(100*(1-float64(busy)/float64(total)), 0, 100)
	}
	return out
}

// MemBuilder keeps the paging history the rates are measured over; one value
// belongs to one goroutine.
type MemBuilder struct {
	pageouts, swapouts []hostmem.PageSample
}

func NewMemBuilder() *MemBuilder { return &MemBuilder{} }

// Build turns a reading taken at now into the wire record. Rates are pages per
// second over hostmem.RateWindow (the same arithmetic the zone latch uses; a
// rate with no baseline yet is 0).
func (b *MemBuilder) Build(now time.Time, raw RawMem) wire.Mem {
	m := wire.Mem{
		TotalMB:     int(raw.TotalBytes / mib),
		AvailableMB: int((raw.Free + raw.Inactive) * raw.PageSize / mib),
		Pressure:    raw.Level,
	}
	if raw.SwapUsedBytes != nil {
		v := int(*raw.SwapUsedBytes / mib)
		m.SwapUsedMB = &v
	}
	if raw.CompressorBytes != nil {
		v := int(*raw.CompressorBytes / mib)
		m.CompressorMB = &v
	}
	if !raw.NoPaging {
		po := b.rate(&b.pageouts, now, raw.Pageouts)
		so := b.rate(&b.swapouts, now, raw.Swapouts)
		m.PageOutRate, m.SwapOutRate = &po, &so
	}
	return m
}

func (b *MemBuilder) rate(ring *[]hostmem.PageSample, now time.Time, total uint64) float64 {
	cur := hostmem.PageSample{At: now, Total: total}
	r := hostmem.RateOver(*ring, cur)
	keep := (*ring)[:0]
	for _, s := range *ring {
		if now.Sub(s.At) <= hostmem.RateWindow+2*time.Second && !s.At.After(now) {
			keep = append(keep, s)
		}
	}
	*ring = append(keep, cur)
	return r
}

// BuildDisk turns mounts into wire records; used_pct is df's: used over used
// plus what a user can still take.
func BuildDisk(raw []RawDisk) []wire.Disk {
	out := make([]wire.Disk, 0, len(raw))
	for _, d := range raw {
		used := d.TotalBytes - min(d.FreeBytes, d.TotalBytes)
		pct := 0.0
		if used+d.AvailBytes > 0 {
			pct = 100 * float64(used) / float64(used+d.AvailBytes)
		}
		out = append(out, wire.Disk{Mount: d.Mount, FreeGB: float64(d.AvailBytes) / (1 << 30), UsedPct: pct})
	}
	return out
}

// Sources are the platform reads (seams for tests).
type Sources struct {
	Now   func() time.Time
	CPU   func() (RawCPU, error)
	Mem   func() (RawMem, error)
	Disks func() ([]RawDisk, error)
}

// Reading is what one sample knows so far. A field whose read failed is its
// zero value; Sample reports the failure.
type Reading struct {
	At   time.Time
	CPU  wire.CPU
	Mem  wire.Mem
	Disk []wire.Disk
}

// Apply writes the reading into a facts record.
func (r Reading) Apply(f *wire.Facts) {
	f.At, f.CPU, f.Mem, f.Disk = r.At, r.CPU, r.Mem, r.Disk
}

// Collector takes Readings; one value belongs to one goroutine.
type Collector struct {
	src     Sources
	prevCPU *RawCPU
	mem     *MemBuilder
}

func NewCollector(src Sources) *Collector {
	return &Collector{src: src, mem: NewMemBuilder()}
}

// Sample takes one reading. A failing source leaves its fields at zero values
// (Disk empty) and is joined into the error; the other fields still come back.
func (c *Collector) Sample() (Reading, error) {
	now := c.src.Now()
	r := Reading{At: now, Disk: []wire.Disk{}}
	var errs []error
	if cpu, err := c.src.CPU(); err != nil {
		errs = append(errs, err)
	} else {
		r.CPU = BuildCPU(c.prevCPU, cpu)
		c.prevCPU = &cpu
	}
	if mem, err := c.src.Mem(); err != nil {
		errs = append(errs, err)
	} else {
		r.Mem = c.mem.Build(now, mem)
	}
	if d, err := c.src.Disks(); err != nil {
		errs = append(errs, err)
	} else {
		r.Disk = BuildDisk(d)
	}
	return r, errors.Join(errs...)
}

func clamp(v, lo, hi float64) float64 { return min(max(v, lo), hi) }
