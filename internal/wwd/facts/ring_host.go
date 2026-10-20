package facts

import (
	"math"

	"github.com/wake/purdex/internal/wwd/wire"
)

// rollupHost sets the G-4 fields of a minute's record (host-metrics spec §2.4) from the samples in it. The base is the last
// sample, so boot_at is already the last one's; the rest are worked out here over the samples that have a value (a sample without
// one is skipped, never counted as 0; a minute with none is null). None of these are proto 2 fields: they are not gated on the
// last record's version.
func rollupHost(out *wire.Facts, recs []wire.Facts) {
	out.ProcsTotal = peakInt(recs, func(f wire.Facts) *int { return f.ProcsTotal })
	out.Mem.SwapTotalMB = peakInt(recs, func(f wire.Facts) *int { return f.Mem.SwapTotalMB })
	out.Mem.WiredMB = meanInt(recs, func(f wire.Facts) *int { return f.Mem.WiredMB })
	out.Mem.ActiveMB = meanInt(recs, func(f wire.Facts) *int { return f.Mem.ActiveMB })
	out.Mem.InactiveMB = meanInt(recs, func(f wire.Facts) *int { return f.Mem.InactiveMB })
	out.Mem.FileCacheMB = meanInt(recs, func(f wire.Facts) *int { return f.Mem.FileCacheMB })
	out.Self = nil
	var withSelf []wire.Facts
	for _, r := range recs {
		if r.Self != nil {
			withSelf = append(withSelf, r)
		}
	}
	if len(withSelf) == 0 {
		return
	}
	self := wire.Self{RSSMB: peakInt(withSelf, func(f wire.Facts) *int { return f.Self.RSSMB })}
	var sum float64
	var n int
	for _, r := range withSelf {
		if r.Self.CPUPct != nil {
			sum, n = sum+*r.Self.CPUPct, n+1
		}
	}
	if n > 0 {
		v := sum / float64(n)
		self.CPUPct = &v
	}
	out.Self = &self
}

func peakInt(recs []wire.Facts, get func(wire.Facts) *int) *int {
	var out *int
	for _, r := range recs {
		if p := get(r); p != nil && (out == nil || *p > *out) {
			v := *p
			out = &v
		}
	}
	return out
}

// meanInt is the mean of the samples that have a value, rounded to the nearest whole number.
func meanInt(recs []wire.Facts, get func(wire.Facts) *int) *int {
	var sum, n int
	for _, r := range recs {
		if p := get(r); p != nil {
			sum, n = sum+*p, n+1
		}
	}
	if n == 0 {
		return nil
	}
	v := int(math.Round(float64(sum) / float64(n)))
	return &v
}
