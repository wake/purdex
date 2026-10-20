package facts

import (
	"sort"

	"github.com/wake/purdex/internal/wwd/wire"
)

// rollupDiskIO sets a minute's disk_io (host-metrics spec §2.4): every disk seen in the minute, each rate the mean of the samples in
// which that disk had one (a sample without a rate is skipped, never counted as 0). Sorted by device. Not a proto 2 field, so the
// version of the last record does not clear it.
func rollupDiskIO(out *wire.Facts, recs []wire.Facts) {
	type acc struct {
		sum [4]float64
		n   [4]int
	}
	by := map[string]*acc{}
	for _, r := range recs {
		for _, d := range r.DiskIO {
			a := by[d.Device]
			if a == nil {
				a = &acc{}
				by[d.Device] = a
			}
			for i, p := range [4]*float64{d.ReadBps, d.WriteBps, d.ReadIOPS, d.WriteIOPS} {
				if p != nil {
					a.sum[i], a.n[i] = a.sum[i]+*p, a.n[i]+1
				}
			}
		}
	}
	out.DiskIO = nil
	if len(by) == 0 {
		return
	}
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := by[n]
		mean := func(i int) *float64 {
			if a.n[i] == 0 {
				return nil
			}
			v := a.sum[i] / float64(a.n[i])
			return &v
		}
		out.DiskIO = append(out.DiskIO, wire.DiskIO{Device: n, ReadBps: mean(0), WriteBps: mean(1), ReadIOPS: mean(2), WriteIOPS: mean(3)})
	}
}
