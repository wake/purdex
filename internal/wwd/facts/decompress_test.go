package facts

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func rawWithDecompress(n uint64) RawMem {
	return RawMem{TotalBytes: 16 << 30, PageSize: 16384, Free: 1000, Inactive: 1000, Level: 1, Decompressions: n, HaveDecompressions: true}
}

// Spec §13.2: the decompression rate is the per-second delta of the compressor's counter, over the same window as the other rates.
func TestBuildMemDecompressRate(t *testing.T) {
	c := NewMemBuilder()
	m := c.Build(t0, rawWithDecompress(1_000_000))
	if m.DecompressRate == nil || *m.DecompressRate != 0 {
		t.Fatalf("no baseline yet: taken as 0 (hostmem.RateOver): %v", m.DecompressRate)
	}
	m = c.Build(t0.Add(5*time.Second), rawWithDecompress(1_400_000))
	if m.DecompressRate == nil || *m.DecompressRate != 80_000 { // 400000 pages / 5 s
		t.Fatalf("decompress_rate %v want 80000", m.DecompressRate)
	}
	m = c.Build(t0.Add(10*time.Second), rawWithDecompress(1_400_000))
	if *m.DecompressRate != 40_000 { // the window baseline is t0: 400000 / 10 s
		t.Fatalf("decompress_rate %v want 40000", *m.DecompressRate)
	}
}

// Where the counter is not read (Linux, or a failed read) the rate is absent, never 0.
func TestBuildMemWithoutDecompressions(t *testing.T) {
	raw := rawWithDecompress(5)
	raw.HaveDecompressions = false
	if m := NewMemBuilder().Build(t0, raw); m.DecompressRate != nil {
		t.Fatalf("an unread counter must be absent: %v", *m.DecompressRate)
	}
	lin := RawMem{Linux: true, TotalBytes: 4 << 30, AvailableBytes: 2 << 30}
	if m := NewMemBuilder().Build(t0, lin); m.DecompressRate != nil {
		t.Fatalf("Linux has PSI instead: %v", *m.DecompressRate)
	}
}

func TestCloneKeepsDecompressRateAndQueueIndependent(t *testing.T) {
	f := wire.Facts{V: wire.Proto, Mem: wire.Mem{DecompressRate: fp(7)}, Queue: &wire.Queue{Queued: 3}, Derived: wire.Derived{Zone: wire.ZoneNormal}}
	c := Clone(f)
	*c.Mem.DecompressRate = 99
	c.Queue.Queued = 99
	if *f.Mem.DecompressRate != 7 || f.Queue.Queued != 3 {
		t.Fatalf("the clone shares its pointers with the original: %v %v", *f.Mem.DecompressRate, f.Queue.Queued)
	}
}

// A minute keeps the worst of its samples, like the other rates; a minute with no reading has none; the queue keeps its peak.
func TestRollupKeepsTheWorstDecompressRateAndTheQueuePeak(t *testing.T) {
	a := rec(t0, 1, 1000, 1, wire.ZoneNormal)
	b := rec(t0.Add(5*time.Second), 1, 1000, 1, wire.ZoneNormal)
	c := rec(t0.Add(10*time.Second), 1, 1000, 1, wire.ZoneNormal)
	a.Mem.DecompressRate, b.Mem.DecompressRate, c.Mem.DecompressRate = fp(10), fp(90), fp(30)
	a.Queue, b.Queue, c.Queue = &wire.Queue{Queued: 1}, &wire.Queue{Queued: 5}, &wire.Queue{Queued: 2}
	got := Rollup([]wire.Facts{a, b, c})
	if got.Mem.DecompressRate == nil || *got.Mem.DecompressRate != 90 {
		t.Errorf("decompress_rate %v want 90", got.Mem.DecompressRate)
	}
	if got.Queue == nil || got.Queue.Queued != 5 {
		t.Errorf("queue %v want a peak of 5", got.Queue)
	}
	none := Rollup([]wire.Facts{rec(t0, 1, 1000, 1, wire.ZoneNormal), rec(t0.Add(5*time.Second), 1, 1000, 1, wire.ZoneNormal)})
	if none.Mem.DecompressRate != nil || none.Queue != nil {
		t.Errorf("a minute without the readings has none: %v %v", none.Mem.DecompressRate, none.Queue)
	}
}
