package facts

import (
	"sort"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// RawDiskIO is one physical disk's cumulative counters since boot (or since its driver started): bytes and operations, read and
// written. Device is the system's name for it (macOS disk0; Linux nvme0n1, sda).
type RawDiskIO struct {
	Device                string
	ReadBytes, WriteBytes uint64
	ReadOps, WriteOps     uint64
}

// DiskIOBuilder turns the counters of successive samples into rates per second, by the real time between the two samples (not the
// nominal 5 s). One value belongs to one goroutine.
type DiskIOBuilder struct {
	prev   map[string]RawDiskIO
	prevAt time.Time
}

func NewDiskIOBuilder() *DiskIOBuilder { return &DiskIOBuilder{} }

// Build turns the counters read at now into the wire records, sorted by device name. A device with nothing to compare with has null
// rates (it is still listed): the first sample, a device new to the list, and one that was missing from the previous sample and is back
// (its old counters are no baseline). Each of the four figures is judged by its own counter: one that went back, or a clock that did not
// move, makes that figure null and no other. Nothing to list is nil.
func (b *DiskIOBuilder) Build(now time.Time, raw []RawDiskIO) []wire.DiskIO {
	dt := now.Sub(b.prevAt).Seconds()
	cur := make(map[string]RawDiskIO, len(raw))
	out := make([]wire.DiskIO, 0, len(raw))
	for _, r := range raw {
		cur[r.Device] = r
		d := wire.DiskIO{Device: r.Device}
		if old, ok := b.prev[r.Device]; ok && dt > 0 {
			d.ReadBps, d.WriteBps = rate(old.ReadBytes, r.ReadBytes, dt), rate(old.WriteBytes, r.WriteBytes, dt)
			d.ReadIOPS, d.WriteIOPS = rate(old.ReadOps, r.ReadOps, dt), rate(old.WriteOps, r.WriteOps, dt)
		}
		out = append(out, d)
	}
	b.prev, b.prevAt = cur, now // devices missing from this sample are forgotten
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}

// rate is the counter's rise per second, nil when it went back.
func rate(old, cur uint64, seconds float64) *float64 {
	if cur < old {
		return nil
	}
	v := float64(cur-old) / seconds
	return &v
}
