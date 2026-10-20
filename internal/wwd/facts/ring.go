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
	return out
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
