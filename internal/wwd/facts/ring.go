package facts

import (
	"sort"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Ring keeps the newest N records (an hour of 5 s records is 720). Safe for use
// from one writer with readers only if the caller serialises access.
type Ring struct {
	buf  []wire.Facts
	next int
	n    int
}

func NewRing(capacity int) *Ring { return &Ring{buf: make([]wire.Facts, capacity)} }

func (r *Ring) Add(f wire.Facts) {
	r.buf[r.next] = Clone(f)
	r.next = (r.next + 1) % len(r.buf)
	if r.n < len(r.buf) {
		r.n++
	}
}

// Since returns the records at or after t, oldest first.
func (r *Ring) Since(t time.Time) []wire.Facts {
	out := make([]wire.Facts, 0, r.n)
	start := (r.next - r.n + len(r.buf)) % len(r.buf)
	for i := 0; i < r.n; i++ {
		f := r.buf[(start+i)%len(r.buf)]
		if !f.At.Before(t) {
			out = append(out, Clone(f))
		}
	}
	return out
}

// Last is the newest record.
func (r *Ring) Last() (wire.Facts, bool) {
	if r.n == 0 {
		return wire.Facts{}, false
	}
	return Clone(r.buf[(r.next-1+len(r.buf))%len(r.buf)]), true
}

func zoneRank(z wire.Zone) int {
	switch z {
	case wire.ZoneDanger:
		return 3
	case wire.ZoneWarning:
		return 2
	case wire.ZoneNormal:
		return 1
	}
	return 0 // unknown never masks a real zone
}

// Rollup condenses the records of one minute into one record: the last record
// is the base; CPU figures are averaged; the worst case is kept for memory
// (lowest available, highest pressure and rates) and for the zone; the stamp is
// the start of the minute. An empty slice gives the zero value.
func Rollup(recs []wire.Facts) wire.Facts {
	if len(recs) == 0 {
		return wire.Facts{}
	}
	out := Clone(recs[len(recs)-1])
	out.At = recs[0].At.Truncate(time.Minute)
	var l1, l5, idle float64
	out.Mem.AvailableMB = recs[0].Mem.AvailableMB
	out.Mem.Pressure = 0
	out.Mem.PageOutRate, out.Mem.SwapOutRate, out.Mem.DecompressRate = nil, nil, nil
	out.Queue = nil
	out.Worker.Waiting = nil // a moment of the worker's reading, not a figure of the minute
	out.Derived.Zone = wire.ZoneUnknown
	out.Worker.Running = wire.Slots{}
	for _, r := range recs {
		l1 += r.CPU.Load1
		l5 += r.CPU.Load5
		idle += r.CPU.IdlePct
		out.Worker.Running.Heavy = max(out.Worker.Running.Heavy, r.Worker.Running.Heavy)
		out.Worker.Running.Light = max(out.Worker.Running.Light, r.Worker.Running.Light)
		out.Mem.AvailableMB = min(out.Mem.AvailableMB, r.Mem.AvailableMB)
		out.Mem.Pressure = max(out.Mem.Pressure, r.Mem.Pressure)
		out.Mem.PageOutRate = maxPtr(out.Mem.PageOutRate, r.Mem.PageOutRate)
		out.Mem.SwapOutRate = maxPtr(out.Mem.SwapOutRate, r.Mem.SwapOutRate)
		if r.V >= wire.FactsProto { // only v 2 samples carry the proto 2 fields
			out.Mem.DecompressRate = maxPtr(out.Mem.DecompressRate, r.Mem.DecompressRate)
			if r.Queue != nil { // the peak of the minute
				if out.Queue == nil {
					out.Queue = &wire.Queue{}
				}
				out.Queue.Queued = max(out.Queue.Queued, r.Queue.Queued)
			}
		}
		if zoneRank(r.Derived.Zone) > zoneRank(out.Derived.Zone) {
			out.Derived.Zone = r.Derived.Zone
		}
	}
	if out.V < wire.FactsProto { // the rollup is the last record's version: a v1 minute carries none of the v 2 fields
		out.Mem.DecompressRate, out.Queue = nil, nil
	}
	n := float64(len(recs))
	out.CPU.Load1, out.CPU.Load5, out.CPU.IdlePct = l1/n, l5/n, idle/n
	rollupHost(&out, recs)
	rollupCPUSplit(&out.CPU, recs)
	rollupDiskIO(&out, recs)
	return out
}

// rollupCPUSplit averages load15, the user and system shares and each core's shares over the records that have a value (spec
// §2.4). None of them is a proto 2 field, so the version of the last record does not clear them. out.Cores is the last record's
// list, so its length and kinds stand; a core is averaged over the records that have a core at the same index.
func rollupCPUSplit(out *wire.CPU, recs []wire.Facts) {
	out.Load15 = meanOf(recs, func(f wire.Facts) *float64 { return f.CPU.Load15 })
	out.UserPct = meanOf(recs, func(f wire.Facts) *float64 { return f.CPU.UserPct })
	out.SystemPct = meanOf(recs, func(f wire.Facts) *float64 { return f.CPU.SystemPct })
	for i := range out.Cores {
		out.Cores[i].UserPct = coreMean(recs, i, func(c wire.Core) *float64 { return c.UserPct })
		out.Cores[i].SystemPct = coreMean(recs, i, func(c wire.Core) *float64 { return c.SystemPct })
		out.Cores[i].IdlePct = coreMean(recs, i, func(c wire.Core) *float64 { return c.IdlePct })
	}
}

// coreMean is the mean of one share of the core at index i over the records that have one. A record that has the core but no
// value for it ends what came before: the sampler leaves a core's share null when its counters cannot be compared with the sample
// before, which includes the sample at which a different core took over the index (cpu1 offline, cpu2 online). The values from
// before that sample belong to another core and are dropped, so a minute never averages two cores into one entry.
func coreMean(recs []wire.Facts, i int, get func(wire.Core) *float64) *float64 {
	var sum float64
	var n int
	for _, r := range recs {
		if i >= len(r.CPU.Cores) {
			continue
		}
		v := get(r.CPU.Cores[i])
		if v == nil {
			sum, n = 0, 0
			continue
		}
		sum += *v
		n++
	}
	if n == 0 {
		return nil
	}
	m := sum / float64(n)
	return &m
}

// meanOf is the mean of the values get finds, nil when it finds none.
func meanOf(recs []wire.Facts, get func(wire.Facts) *float64) *float64 {
	var sum float64
	var n int
	for _, r := range recs {
		if v := get(r); v != nil {
			sum += *v
			n++
		}
	}
	if n == 0 {
		return nil
	}
	m := sum / float64(n)
	return &m
}

func maxPtr(a, b *float64) *float64 {
	switch {
	case a == nil && b == nil:
		return nil
	case a == nil:
		v := *b
		return &v
	case b == nil:
		return a
	}
	v := max(*a, *b)
	return &v
}

// RollupAll groups records by minute (oldest first) and rolls each group up.
func RollupAll(recs []wire.Facts) []wire.Facts {
	sorted := append([]wire.Facts(nil), recs...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.Before(sorted[j].At) })
	var out []wire.Facts
	for i := 0; i < len(sorted); {
		m := sorted[i].At.Truncate(time.Minute)
		j := i
		for j < len(sorted) && sorted[j].At.Truncate(time.Minute).Equal(m) {
			j++
		}
		out = append(out, Rollup(sorted[i:j]))
		i = j
	}
	return out
}
