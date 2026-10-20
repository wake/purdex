package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/wire"
)

// S3-2a: the watcher's pressure rules read this host's own samples (LocalRecent), so a local sample carries what they judge by: the
// pressure level, the PSI of Linux, the swap in use and the RAM total. They are not behind the proto 2 gate (alerts are not), and a sample
// the sources could not read leaves the field absent rather than zero.

func pressureSampler(mem func() (facts.RawMem, error)) *Sampler {
	level := 1
	src := fakeSources(&level, nil)
	src.Mem = mem
	return NewSampler(SamplerOptions{HostID: "h1", Alias: "mlab", OSVersion: "26.6.2", Sources: src, Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true})
}

func TestLocalRecentCarriesPressureSwapAndTotalOnMacOS(t *testing.T) {
	swap := uint64(3000) << 20
	level := 2
	s := pressureSampler(func() (facts.RawMem, error) {
		return facts.RawMem{TotalBytes: 16 << 30, PageSize: 16384, Free: 100000, Inactive: 100000, Level: level, SwapUsedBytes: &swap}, nil
	})
	s.Tick(s0)
	level = 4
	swap = uint64(3100) << 20
	s.Tick(s0.Add(5 * time.Second))
	got := s.LocalRecent(s0)
	if len(got) != 2 {
		t.Fatalf("%d local samples", len(got))
	}
	if got[0].Mem.Pressure != 2 || got[1].Mem.Pressure != 4 {
		t.Errorf("pressure %d then %d, want 2 then 4", got[0].Mem.Pressure, got[1].Mem.Pressure)
	}
	if got[0].Mem.SwapUsedMB == nil || *got[0].Mem.SwapUsedMB != 3000 || *got[1].Mem.SwapUsedMB != 3100 {
		t.Errorf("swap %+v / %+v", got[0].Mem.SwapUsedMB, got[1].Mem.SwapUsedMB)
	}
	if got[0].Mem.TotalMB != 16384 {
		t.Errorf("total %d MB, want 16384", got[0].Mem.TotalMB)
	}
	if got[0].Mem.PSI != nil {
		t.Errorf("macOS has no PSI: %+v", got[0].Mem.PSI)
	}
}

func TestLocalRecentCarriesPSIOnLinuxAsACopy(t *testing.T) {
	psi := wire.PSI{Some10: 12, Full10: 0.5, Some60: 3}
	s := pressureSampler(func() (facts.RawMem, error) {
		return facts.RawMem{Linux: true, TotalBytes: 16 << 30, AvailableBytes: 8 << 30, PSI: &psi}, nil
	})
	s.Tick(s0)
	got := s.LocalRecent(s0)
	if len(got) != 1 || got[0].Mem.PSI == nil || *got[0].Mem.PSI != psi {
		t.Fatalf("local sample %+v", got)
	}
	// a copy: the sampler's own reading does not change when the caller's does, nor when the source's does
	got[0].Mem.PSI.Some10 = 99
	psi.Some10 = 77
	if again := s.LocalRecent(s0); again[0].Mem.PSI.Some10 != 12 {
		t.Fatalf("LocalRecent handed out (or kept) a shared PSI pointer: %+v", again[0].Mem.PSI)
	}
}

// A reading the source could not give is absent, not zero: a rule must not read "no swap figure" as "no swap used".
func TestLocalRecentLeavesAnUnreadFieldAbsent(t *testing.T) {
	s := pressureSampler(func() (facts.RawMem, error) {
		return facts.RawMem{TotalBytes: 16 << 30, PageSize: 16384, Free: 100000, Inactive: 100000, Level: 1}, nil // no SwapUsedBytes
	})
	s.Tick(s0)
	got := s.LocalRecent(s0)
	if len(got) != 1 || got[0].Mem.SwapUsedMB != nil {
		t.Fatalf("an unread swap figure came out as %+v", got)
	}
}

// A sample whose memory source failed has no memory reading: TotalMB 0, pressure 0 (unknown), no swap figure. That is how LocalRecent says
// "nothing was read here" (a successful read always has a total), so a rule can tell a hole from a calm sample and never counts it as
// pressure. A sample whose other sources failed still carries the memory it did read: the pressure there is evidence.
func TestLocalRecentMarksAFailedMemoryReadAsNoReading(t *testing.T) {
	level, memErr, cpuErr := 2, false, false
	swap := uint64(1) << 30
	src := fakeSources(&level, nil)
	src.Mem = func() (facts.RawMem, error) {
		if memErr {
			return facts.RawMem{}, errors.New("memory source failed")
		}
		return facts.RawMem{TotalBytes: 16 << 30, PageSize: 16384, Free: 100000, Inactive: 100000, Level: level, SwapUsedBytes: &swap}, nil
	}
	cpu := src.CPU
	src.CPU = func() (facts.RawCPU, error) {
		if cpuErr {
			return facts.RawCPU{}, errors.New("cpu source failed")
		}
		return cpu()
	}
	s := NewSampler(SamplerOptions{HostID: "h1", Alias: "mlab", OSVersion: "26.6.2", Sources: src, Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true})
	s.Tick(s0)
	cpuErr = true // the cpu source fails, memory is read
	s.Tick(s0.Add(5 * time.Second))
	cpuErr, memErr = false, true // the memory source fails
	s.Tick(s0.Add(10 * time.Second))
	got := s.LocalRecent(s0)
	if len(got) != 3 {
		t.Fatalf("%d samples, want all three (a hole is a sample with no reading, not a missing one)", len(got))
	}
	for i, wantRead := range []bool{true, true, false} {
		m := got[i].Mem
		if read := m.TotalMB > 0; read != wantRead {
			t.Errorf("sample %d: memory read = %v (total %d), want %v", i, read, m.TotalMB, wantRead)
		}
		if !wantRead && (m.Pressure != 0 || m.SwapUsedMB != nil || m.PSI != nil) {
			t.Errorf("sample %d: a failed memory read carries readings: %+v", i, m)
		}
		if wantRead && m.Pressure != 2 {
			t.Errorf("sample %d: pressure %d, want 2 (its memory was read)", i, m.Pressure)
		}
	}
}
