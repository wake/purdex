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

// CollectDisk reads the root mount's disk from the facts. They carry the free space and the used share, not the size, so the
// size is worked out from the two; a disk that does not add up is left to the fallback.
func (c *poolHostCollector) CollectDisk(ctx context.Context) (HostDiskSample, error) {
	if f, ok := c.facts(); ok {
		if s, ok := diskSampleOf(f.Disk); ok {
			return s, nil
		}
	}
	return c.fallback.CollectDisk(ctx)
}

func diskSampleOf(ds []wire.Disk) (HostDiskSample, bool) {
	if len(ds) == 0 {
		return HostDiskSample{}, false
	}
	d := ds[0]
	for _, x := range ds {
		if x.Mount == "/" {
			d = x
			break
		}
	}
	if d.UsedPct <= 0 || d.UsedPct >= 100 || d.FreeGB < 0 {
		return HostDiskSample{}, false
	}
	free := d.FreeGB * (1 << 30)
	total := free / (1 - d.UsedPct/100)
	return HostDiskSample{TotalBytes: uint64(total), UsedBytes: uint64(total - free)}, true
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
