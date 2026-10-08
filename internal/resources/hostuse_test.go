package resources

import (
	"math"
	"testing"
)

// 1e6 pages of 16 KiB, so a page count maps to a whole percentage:
// 10,000 pages is 1 % of memory.
const (
	testPageSize = 16384
	testMemBytes = 16_384_000_000
)

func rawWith(load1 float64, ncpu int, free, inactive, spec uint64, pressure int) HostRaw {
	return HostRaw{
		Load1: load1, NCPU: ncpu, MemBytes: testMemBytes,
		PageSize: testPageSize, Free: free, Inactive: inactive, Speculative: spec,
		Pressure: pressure, MemorystatusLevel: 50,
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestComputeHost_Table(t *testing.T) {
	cases := []struct {
		name     string
		raw      HostRaw
		measured int
		cpu, mem float64
		full     bool
	}{
		// avail = 100k + 400k + 100k = 600k pages -> mem 40 %.
		{"idle", rawWith(1, 10, 100_000, 400_000, 100_000, 1), 40, 10, 40, false},
		{"cpu-bound", rawWith(12, 10, 100_000, 400_000, 100_000, 1), 100, 120, 40, true},
		{"load equals cores is full", rawWith(10, 10, 100_000, 400_000, 100_000, 1), 100, 100, 40, true},
		// avail 90k pages -> mem 91 %.
		{"memory 91", rawWith(1, 10, 30_000, 30_000, 30_000, 1), 91, 10, 91, true},
		// M-R1's real reading: pressure warn at 76 % in use.
		{"pressure 2 at mem 76", rawWith(1, 10, 80_000, 80_000, 80_000, 2), 90, 10, 76, true},
		{"pressure 2 does not lower a higher reading", rawWith(1, 10, 10_000, 10_000, 10_000, 2), 97, 10, 97, true},
		{"pressure 4", rawWith(1, 10, 100_000, 400_000, 100_000, 4), 100, 10, 40, true},
		{"fractional use rounds up", rawWith(1.01, 10, 300_000, 300_000, 300_000, 1), 11, 10.1, 10, false},
		// More available than installed: mem clamps to 0.
		{"available above memsize", rawWith(1, 10, 2_000_000, 0, 0, 1), 10, 10, 0, false},
		// Unusable: derived figures are all zero.
		{"ncpu 0", rawWith(5, 0, 100_000, 400_000, 100_000, 2), 0, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeHost(tc.raw)
			if got.Measured != tc.measured {
				t.Errorf("Measured = %d, want %d", got.Measured, tc.measured)
			}
			if !near(got.CPU, tc.cpu) {
				t.Errorf("CPU = %v, want %v", got.CPU, tc.cpu)
			}
			if !near(got.Mem, tc.mem) {
				t.Errorf("Mem = %v, want %v", got.Mem, tc.mem)
			}
			if got.Full != tc.full {
				t.Errorf("Full = %v, want %v", got.Full, tc.full)
			}
		})
	}
}

func TestComputeHost_PassThroughAndBytes(t *testing.T) {
	raw := rawWith(2.5, 10, 100_000, 400_000, 100_000, 2)
	raw.PcpuSum = 250
	raw.MemorystatusLevel = 48
	got := ComputeHost(raw)
	if got.Load1 != 2.5 || got.NCPU != 10 || got.MemBytes != testMemBytes ||
		got.Pressure != 2 || got.MemorystatusLevel != 48 {
		t.Errorf("raw fields not passed through: %+v", got)
	}
	if !near(got.PcpuTotal, 25) {
		t.Errorf("PcpuTotal = %v, want 25 (250 / 10 cpus)", got.PcpuTotal)
	}
	if want := uint64(400_000 * testPageSize); got.MemUsedBytes != want {
		t.Errorf("MemUsedBytes = %d, want %d", got.MemUsedBytes, want)
	}
}

// Page counts whose byte total wraps a uint64 make the reading unusable
// instead of reading as an idle (or full) host (codex attack finding).
func TestHostRaw_UsableRejectsOverflow(t *testing.T) {
	cases := map[string]HostRaw{
		"sum wraps":     {NCPU: 1, MemBytes: 1024, PageSize: 2, Free: 1 << 63, Inactive: 1 << 63},
		"product wraps": {NCPU: 1, MemBytes: 1024, PageSize: 1 << 40, Free: 1 << 40},
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if r.Usable() {
				t.Fatal("overflowing page counts must be unusable")
			}
			if h := ComputeHost(r); h.Measured != 0 || h.Full {
				t.Fatalf("unusable reading must derive nothing: %+v", h)
			}
		})
	}
	if !rawWith(1, 10, 1, 1, 1, 1).Usable() {
		t.Fatal("a plain reading stays usable")
	}
}

func TestHostRaw_Usable(t *testing.T) {
	if !rawWith(1, 10, 1, 1, 1, 1).Usable() {
		t.Error("a complete reading must be usable")
	}
	if rawWith(1, 0, 1, 1, 1, 1).Usable() {
		t.Error("ncpu 0 must be unusable")
	}
	r := rawWith(1, 10, 1, 1, 1, 1)
	r.MemBytes = 0
	if r.Usable() {
		t.Error("memsize 0 must be unusable")
	}
}
