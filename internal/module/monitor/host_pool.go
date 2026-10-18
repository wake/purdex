package monitor

import (
	"context"
	"errors"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The pool-backed host collector (WWD-2c-2): while the fleet row's source is "pool", the host part of the snapshot (CPU, memory,
// disk) is wwd's facts of this host, not the monitor's own sampling; whenever wwd has no fresh facts, or the source is not the
// pool, every call goes to the collector the monitor always had. The per-session part of the snapshot is not touched.

// localFacts returns this host's fresh facts from wwd; ok is false when the source is not the pool or wwd has none.
type localFacts func() (wire.Facts, bool)

type poolHostCollector struct {
	fallback HostCollector
	facts    localFacts
}

// newPoolHostCollector wraps fallback. A fallback that reports the CPU percentage itself (darwin) keeps doing so through the
// wrapper, so the CPU still reads when wwd does not.
func newPoolHostCollector(fallback HostCollector, facts localFacts) HostCollector {
	c := &poolHostCollector{fallback: fallback, facts: facts}
	if _, ok := fallback.(hostCPUPercentSource); ok {
		return &poolPercentCollector{c}
	}
	return c
}

// pinned is this collector with the source decided once: the facts as they are now, for every figure of one snapshot.
func (c *poolHostCollector) pinned() HostCollector {
	f, ok := c.facts()
	if ok && !usable(f) {
		ok = false // facts that cannot give every figure give none: one snapshot, one source
	}
	return newPoolHostCollector(c.fallback, func() (wire.Facts, bool) { return f, ok })
}

func (c *poolHostCollector) CollectCPU(ctx context.Context) (HostCPUSample, error) {
	return c.fallback.CollectCPU(ctx)
}

// poolCPU is the CPU use of the whole host, 0 to 100: what is not idle.
func (c *poolHostCollector) poolCPU() (float64, bool) {
	f, ok := c.facts()
	if !ok {
		return 0, false
	}
	return min(max(100-f.CPU.IdlePct, 0), 100), true
}

func (c *poolHostCollector) CollectMemory(ctx context.Context) (HostMemorySample, error) {
	if f, ok := c.facts(); ok && f.Mem.TotalMB > 0 {
		used := max(f.Mem.TotalMB-f.Mem.AvailableMB, 0)
		return HostMemorySample{TotalBytes: uint64(f.Mem.TotalMB) << 20, UsedBytes: uint64(used) << 20}, nil
	}
	return c.fallback.CollectMemory(ctx)
}

// CollectDisk reads the root mount's disk from the facts; a disk the facts cannot give (a wwd that does not send the sizes yet) is
// left to the fallback.
func (c *poolHostCollector) CollectDisk(ctx context.Context) (HostDiskSample, error) {
	if f, ok := c.facts(); ok {
		if s, ok := diskSampleOf(f.Disk); ok {
			return s, nil
		}
	}
	return c.fallback.CollectDisk(ctx)
}

// usable says whether facts can give all three figures of a snapshot: the memory total and the disk sizes (a wwd older than the
// sizes, or a host that reports no disk, cannot).
func usable(f wire.Facts) bool {
	if f.Mem.TotalMB <= 0 {
		return false
	}
	_, ok := diskSampleOf(f.Disk)
	return ok
}

// diskSampleOf is the root mount's disk, the statfs figures the facts carry: total is Blocks x Bsize and avail is Bavail x Bsize,
// so used is total less avail, the same arithmetic as the monitor's own collector, which reads "/". Facts without that mount (or
// without sizes) give nothing: another filesystem's figures are not the root's.
func diskSampleOf(ds []wire.Disk) (HostDiskSample, bool) {
	for _, d := range ds {
		if d.Mount != "/" {
			continue
		}
		if d.TotalBytes == 0 || d.AvailBytes > d.TotalBytes {
			return HostDiskSample{}, false
		}
		return HostDiskSample{TotalBytes: d.TotalBytes, UsedBytes: d.TotalBytes - d.AvailBytes}, true
	}
	return HostDiskSample{}, false
}

// Close ends what the fallback keeps running (the darwin CPU sampler).
func (c *poolHostCollector) Close() {
	if cl, ok := c.fallback.(interface{ Close() }); ok {
		cl.Close()
	}
}

// poolPercentCollector is poolHostCollector for a fallback that reports the CPU percentage itself.
type poolPercentCollector struct{ *poolHostCollector }

// CPUPercent is the fallback's own reading; collectHostCPU asks poolCPU first, so this is reached only without wwd's.
func (c *poolPercentCollector) CPUPercent(interval time.Duration) (float64, error) {
	src, ok := c.fallback.(hostCPUPercentSource)
	if !ok {
		return 0, errors.New("no cpu source")
	}
	return src.CPUPercent(interval)
}
