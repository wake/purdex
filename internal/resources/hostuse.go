package resources

import (
	"math"
	"math/bits"
)

// HostRaw is what the sampler read from the host, before any arithmetic.
type HostRaw struct {
	Load1    float64
	NCPU     int
	MemBytes uint64
	// PageSize and the page counts come from vm_stat.
	PageSize, Free, Inactive, Speculative uint64
	Pressure                              int // kern.memorystatus_vm_pressure_level; 0 unknown
	MemorystatusLevel                     int // kern.memorystatus_level; -1 unknown
	PcpuSum                               float64
}

// Usable reports whether the reading has what D-1 divides by and whether its
// page counts fit in a uint64 of bytes: a count that wraps would read as an
// idle (or full) host. An unusable reading must be published as
// Available = false, Reason = "sample_failed".
func (r HostRaw) Usable() bool {
	if r.NCPU <= 0 || r.MemBytes == 0 {
		return false
	}
	_, ok := r.availableBytes()
	return ok
}

// availableBytes is (free + inactive + speculative) pages in bytes; ok is
// false when the sum or the product overflows a uint64.
func (r HostRaw) availableBytes() (uint64, bool) {
	pages, carry := bits.Add64(r.Free, r.Inactive, 0)
	if carry != 0 {
		return 0, false
	}
	if pages, carry = bits.Add64(pages, r.Speculative, 0); carry != 0 {
		return 0, false
	}
	hi, lo := bits.Mul64(pages, r.PageSize)
	return lo, hi == 0
}

// ceilPercent rounds up to a whole percentage, ignoring float noise so that
// an exact 40 does not become 41.
func ceilPercent(x float64) int { return int(math.Ceil(x - 1e-9)) }

func clampFloat(x, lo, hi float64) float64 { return math.Min(math.Max(x, lo), hi) }

// ComputeHost applies D-1: measured use is the larger of CPU (load1 / ncpu)
// and memory in use ((free + inactive + speculative) pages counted as
// available), then memory pressure warn counts as at least
// PressureWarnFloor and critical as PressureCriticalFloor. CPU above 100 is
// reported as is; Measured clamps to 100.
//
// An unusable reading (see HostRaw.Usable) yields zero for every derived
// figure and Full = false.
func ComputeHost(r HostRaw) HostUse {
	h := HostUse{
		Load1:             r.Load1,
		NCPU:              r.NCPU,
		MemBytes:          r.MemBytes,
		Pressure:          r.Pressure,
		MemorystatusLevel: r.MemorystatusLevel,
	}
	if !r.Usable() {
		return h
	}
	ncpu := float64(r.NCPU)
	h.CPU = 100 * r.Load1 / ncpu
	h.PcpuTotal = r.PcpuSum / ncpu

	available, _ := r.availableBytes() // Usable() above ruled out an overflow
	h.MemUsedBytes = r.MemBytes - min(available, r.MemBytes)
	h.Mem = clampFloat(float64(h.MemUsedBytes)*100/float64(r.MemBytes), 0, 100)

	measured := ceilPercent(math.Max(h.CPU, h.Mem))
	switch {
	case r.Pressure >= 4:
		measured = PressureCriticalFloor
	case r.Pressure >= 2:
		measured = max(measured, PressureWarnFloor)
	}
	h.Measured = min(max(measured, 0), Capacity)

	h.Full = r.Load1 >= ncpu || h.Mem >= MemFullPercent || r.Pressure >= 2
	return h
}
