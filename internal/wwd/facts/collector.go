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
	// Load15 is nil where the platform did not give it.
	Load15 *float64
	// User (user + nice) and System (system; on Linux also irq and softirq) are the same ticks as Busy and Total, split; HaveSplit
	// says they were read. Cores are the per-core ticks in core order (nil: not read). Kinds is each core's kind, "p", "e" or ""
	// (unknown); it is nil, or the same length as Cores.
	User, System uint64
	HaveSplit    bool
	Cores        []RawCore
	Kinds        []string
}

// RawCore is one core's ticks since boot: User and System as in RawCPU, Idle (on Linux with iowait), and Total of all of them.
// ID is the core's number (Linux cpuN; macOS the index), so a change of which cores are online is told from a change of count.
type RawCore struct {
	ID                        int
	User, System, Idle, Total uint64
}

// RawMem is one reading of memory. Free and Inactive are page counts;
// Pageouts and Swapouts are cumulative pages. The pointer fields are nil where
// the platform has no such counter; NoPageouts / NoSwapouts mark one counter unreadable.
type RawMem struct {
	TotalBytes, PageSize uint64
	Free, Inactive       uint64
	Level                int // kernel pressure level: 1 normal, 2 warning, 4 critical; 0 unknown
	Pageouts, Swapouts   uint64
	NoPageouts           bool // Pageouts could not be read: page_out_rate is null
	NoSwapouts           bool // Swapouts could not be read: swap_out_rate is null
	// Decompressions is the compressor's running count of pages brought back since boot (macOS); HaveDecompressions says it was read.
	// Without it the decompress rate is absent (Linux has PSI instead).
	Decompressions     uint64
	HaveDecompressions bool
	SwapUsedBytes      *uint64
	CompressorBytes    *uint64
	// SwapTotalBytes and the breakdown are nil where the platform has no such figure or the read failed (Linux has no wired).
	SwapTotalBytes                                         *uint64
	ActiveBytes, InactiveBytes, WiredBytes, FileCacheBytes *uint64

	// Linux reads. AvailableBytes (MemAvailable) replaces Free + Inactive, and
	// PSI is the production slices' pressure (nil: unreadable). Linux marks the
	// reading so the level comes from the Linux mapping, not from Level.
	Linux          bool
	AvailableBytes uint64
	PSI            *wire.PSI
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
	if cur.Load15 != nil {
		v := *cur.Load15
		out.Load15 = &v
	}
	busy, total := cur.Busy, cur.Total
	if prev != nil && cur.Total > prev.Total && cur.Busy >= prev.Busy {
		busy, total = cur.Busy-prev.Busy, cur.Total-prev.Total
	}
	if total > 0 {
		out.IdlePct = clamp(100*(1-float64(busy)/float64(total)), 0, 100)
	}
	if cur.HaveSplit && prev != nil && prev.HaveSplit {
		out.UserPct, out.SystemPct = tickShares(prev.User, prev.System, prev.Total, cur.User, cur.System, cur.Total)
	}
	if cur.Cores != nil {
		out.Cores = buildCores(prev, cur)
	}
	return out
}

// tickShares is the share of the time between two readings spent in user and in system, 0 to 100. Both are nil when the readings
// cannot be compared: no time passed, or a counter went backwards (a restart of the counters, not a negative share).
func tickShares(pu, ps, pt, cu, cs, ct uint64) (user, system *float64) {
	if ct <= pt || cu < pu || cs < ps {
		return nil, nil
	}
	dt := float64(ct - pt)
	u, s := clamp(100*float64(cu-pu)/dt, 0, 100), clamp(100*float64(cs-ps)/dt, 0, 100)
	return &u, &s
}

// buildCores lists every core of cur. A core's shares are measured against the same core in prev, so they are nil when the number
// of cores or which cores are online changed (the index no longer names the same core) or when that core's own counters cannot be compared.
func buildCores(prev *RawCPU, cur RawCPU) []wire.Core {
	out := make([]wire.Core, len(cur.Cores))
	kinds := len(cur.Kinds) == len(cur.Cores) // a list that does not fit the cores is not trusted for any of them
	for i, c := range cur.Cores {
		if kinds && (cur.Kinds[i] == wire.CoreKindP || cur.Kinds[i] == wire.CoreKindE) {
			k := cur.Kinds[i]
			out[i].Kind = &k
		}
		if prev == nil || len(prev.Cores) != len(cur.Cores) {
			continue
		}
		p := prev.Cores[i]
		if p.ID != c.ID { // a core went offline and another came online: the index no longer names the same core
			continue
		}
		if c.Total <= p.Total || c.Idle < p.Idle {
			continue
		}
		if u, s := tickShares(p.User, p.System, p.Total, c.User, c.System, c.Total); u != nil {
			idle := clamp(100*float64(c.Idle-p.Idle)/float64(c.Total-p.Total), 0, 100)
			out[i].UserPct, out[i].SystemPct, out[i].IdlePct = u, s, &idle
		}
	}
	return out
}

// MemBuilder keeps the paging history the rates are measured over; one value
// belongs to one goroutine.
type MemBuilder struct {
	pageouts, swapouts []hostmem.PageSample
	decompressions     []hostmem.PageSample
	linux              LinuxPressure
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
	if raw.Linux {
		m.AvailableMB = int(raw.AvailableBytes / mib)
		m.PSI = raw.PSI
		swapMB := 0
		if raw.SwapUsedBytes != nil {
			swapMB = int(*raw.SwapUsedBytes / mib)
		}
		m.Pressure = b.linux.Update(now, m.TotalMB, m.AvailableMB, swapMB, raw.PSI)
	}
	if raw.SwapUsedBytes != nil {
		v := int(*raw.SwapUsedBytes / mib)
		m.SwapUsedMB = &v
	}
	if raw.CompressorBytes != nil {
		v := int(*raw.CompressorBytes / mib)
		m.CompressorMB = &v
	}
	m.SwapTotalMB, m.ActiveMB, m.InactiveMB = mbPtr(raw.SwapTotalBytes), mbPtr(raw.ActiveBytes), mbPtr(raw.InactiveBytes)
	m.WiredMB, m.FileCacheMB = mbPtr(raw.WiredBytes), mbPtr(raw.FileCacheBytes)
	if !raw.NoPageouts {
		po := b.rate(&b.pageouts, now, raw.Pageouts)
		m.PageOutRate = &po
	}
	if !raw.NoSwapouts {
		so := b.rate(&b.swapouts, now, raw.Swapouts)
		m.SwapOutRate = &so
	}
	if raw.HaveDecompressions {
		dr := b.rate(&b.decompressions, now, raw.Decompressions)
		m.DecompressRate = &dr
	}
	return m
}

func mbPtr(b *uint64) *int {
	if b == nil {
		return nil
	}
	v := int(*b / mib)
	return &v
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
		out = append(out, wire.Disk{Mount: d.Mount, FreeGB: float64(d.AvailBytes) / (1 << 30), UsedPct: pct, TotalBytes: d.TotalBytes, AvailBytes: d.AvailBytes})
	}
	return out
}

// Sources are the platform reads (seams for tests).
type Sources struct {
	Now   func() time.Time
	CPU   func() (RawCPU, error)
	Mem   func() (RawMem, error)
	Disks func() ([]RawDisk, error)
	// Procs reads the process table (nil: this platform has none yet; procs and
	// residents stay empty).
	Procs func(now time.Time) (ProcSnapshot, ProcContext, error)
	// Tools reads the toolchain versions (nil: tools stay all-null). It caches
	// internally; calling it every sample is fine.
	Tools func() wire.Tools
	// Owner is the "owner is active" rule (air26 only; nil elsewhere: owner stays
	// null). It sees the process table of the same sample.
	Owner func(ProcSnapshot, ProcContext) *wire.OwnerState
	// Observe gets the whole process table of every sample whose process read worked (nil: nobody watches the trees). It runs on
	// the sampling goroutine, after the other builders, with the context's table already built; it must not keep the snapshot.
	Observe func(now time.Time, snap ProcSnapshot, c ProcContext)
	// Boot is when the host booted (nil, or ok false: unknown, boot_at stays null). It is cheap: the platform caches what it reads once.
	Boot func() (time.Time, bool)
	// Self is wwd's own CPU time and memory (nil, or an error: self stays null and the sample does not fail).
	Self func() (RawSelf, error)
	// DiskIO reads the cumulative counters of every physical disk (nil, or an error: disk_io stays null and the sample does not fail).
	DiskIO func() ([]RawDiskIO, error)
}

// Top-N sizes of the procs list (spec §3.1: the top 12 by memory and by CPU).
const (
	procsTopMem = 12
	procsTopCPU = 12
)

// Reading is what one sample knows so far. A field whose read failed is its
// zero value; Sample reports the failure.
type Reading struct {
	At        time.Time
	CPU       wire.CPU
	Mem       wire.Mem
	Disk      []wire.Disk
	Procs     []wire.Proc
	Residents []wire.Resident
	Tools     wire.Tools
	Owner     *wire.OwnerState
	// ProcsTotal, BootAt and Self are nil where the source is absent or failed.
	ProcsTotal *int
	BootAt     *time.Time
	Self       *wire.Self
	DiskIO     []wire.DiskIO // nil where the source is absent or failed
}

// Apply writes the reading into a facts record.
func (r Reading) Apply(f *wire.Facts) {
	f.At, f.CPU, f.Mem, f.Disk = r.At, r.CPU, r.Mem, r.Disk
	f.Procs, f.Residents, f.Tools, f.Owner = r.Procs, r.Residents, r.Tools, r.Owner
	f.ProcsTotal, f.BootAt, f.Self = r.ProcsTotal, r.BootAt, r.Self
	f.DiskIO = r.DiskIO
}

// Collector takes Readings; one value belongs to one goroutine.
type Collector struct {
	src     Sources
	prevCPU *RawCPU
	mem     *MemBuilder
	self    *SelfBuilder
	diskio  *DiskIOBuilder
}

func NewCollector(src Sources) *Collector {
	return &Collector{src: src, mem: NewMemBuilder(), self: NewSelfBuilder(), diskio: NewDiskIOBuilder()}
}

// Sample takes one reading. A failing source leaves its fields at zero values
// (Disk empty) and is joined into the error; the other fields still come back.
func (c *Collector) Sample() (Reading, error) {
	now := c.src.Now()
	r := Reading{At: now, Disk: []wire.Disk{}, Procs: []wire.Proc{}, Residents: []wire.Resident{}, Tools: wire.Tools{PHP: []string{}}}
	if c.src.Procs == nil {
		// no process source on this platform yet: null, not an empty list that says
		// "read, and nothing runs" (spec §3.1: a field a platform cannot read is null)
		r.Procs, r.Residents = nil, nil
	}
	if c.src.Tools != nil {
		r.Tools = c.src.Tools()
	}
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
	if c.src.DiskIO != nil {
		// a failed read is not a failed sample: the next one reads again, and the gap just makes that pair's interval longer
		// timed at its own read: the sources before it can be slow, and the counters cover the time since the last read, not since this
		// sample started
		readAt := c.src.Now()
		if raw, err := c.src.DiskIO(); err == nil {
			r.DiskIO = c.diskio.Build(readAt, raw)
		}
	}
	if c.src.Procs != nil {
		if snap, ctx, err := c.src.Procs(now); err != nil {
			errs = append(errs, err)
		} else {
			ctx.ensureTable(snap) // once, shared by the three builders below
			total := snap.Total
			r.ProcsTotal = &total
			r.Procs = BuildProcs(snap, ctx, procsTopMem, procsTopCPU)
			r.Residents = BuildResidents(snap, ctx)
			if c.src.Owner != nil {
				r.Owner = c.src.Owner(snap, ctx)
			}
			if c.src.Observe != nil {
				c.src.Observe(now, snap, ctx)
			}
		}
	}
	if c.src.Boot != nil {
		if at, ok := c.src.Boot(); ok {
			r.BootAt = &at
		}
	}
	if c.src.Self != nil {
		// a failed read is not a failed sample: self is one figure among many, and the next sample reads again
		// timed at its own read: the sources above can be slow, and the CPU time this reads covers the time since the last read, not since
		// this sample started
		readAt := c.src.Now()
		if raw, err := c.src.Self(); err == nil {
			self := c.self.Build(readAt, raw)
			r.Self = &self
		}
	}
	return r, errors.Join(errs...)
}

func clamp(v, lo, hi float64) float64 { return min(max(v, lo), hi) }
