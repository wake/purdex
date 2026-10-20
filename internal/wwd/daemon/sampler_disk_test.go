package daemon

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/wire"
)

// diskReader reads one volume, the root, with *freeGB free (read at every sample).
func diskReader(freeGB *float64) func() ([]facts.RawDisk, error) {
	return func() ([]facts.RawDisk, error) {
		avail := uint64(*freeGB * (1 << 30))
		return []facts.RawDisk{{Mount: "/", TotalBytes: 100 << 30, AvailBytes: avail, FreeBytes: avail}}, nil
	}
}

// diskSampler is a sampler over fakeSources whose work volume (the root) has *freeGB free, under a disk floor of floor GB.
func diskSampler(t *testing.T, freeGB *float64, workRoot string, floor float64) (*Sampler, *HostState) {
	t.Helper()
	level := 1
	src := fakeSources(&level, nil)
	src.Disks = nil
	st, err := NewHostState(HostStateOptions{Host: "mlab", Initial: wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, DiskMinGB: &floor}})
	if err != nil {
		t.Fatal(err)
	}
	src.Disks = diskReader(freeGB)
	return NewSampler(SamplerOptions{HostID: "h1", Alias: "mlab", OSVersion: "26.6.2", Sources: src, State: st, WorkRoot: workRoot}), st
}

// spec §3.2 disk_low: a work volume under the worker's disk floor holds heavy and only heavy; the floor is read at every sample, so a PUT
// takes effect at the next one.
func TestSamplerHoldsHeavyOnALowWorkVolume(t *testing.T) {
	free := 8.0
	s, st := diskSampler(t, &free, "/work/wwd", 10)
	s.Tick(s0)
	f, _ := s.Latest()
	if f.Derived.Headroom != (wire.Slots{Heavy: 0, Light: 2}) || !f.Derived.DiskLow {
		t.Fatalf("8 GB under a floor of 10: derived %+v", f.Derived)
	}
	free = 12 // room again: the next sample shows it
	s.Tick(s0.Add(5 * time.Second))
	if f, _ = s.Latest(); f.Derived.Headroom != (wire.Slots{Heavy: 1, Light: 2}) || f.Derived.DiskLow {
		t.Fatalf("12 GB: derived %+v", f.Derived)
	}
	floor := 20.0 // a PUT raises the floor
	if _, err := st.SetWorker(wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, DiskMinGB: &floor}); err != nil {
		t.Fatal(err)
	}
	s.Tick(s0.Add(10 * time.Second))
	if f, _ = s.Latest(); !f.Derived.DiskLow || f.Derived.Headroom.Heavy != 0 {
		t.Fatalf("floor 20 with 12 free: derived %+v", f.Derived)
	}
	off := 0.0 // and 0 switches it off
	if _, err := st.SetWorker(wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, DiskMinGB: &off}); err != nil {
		t.Fatal(err)
	}
	s.Tick(s0.Add(15 * time.Second))
	if f, _ = s.Latest(); f.Derived.DiskLow || f.Derived.Headroom.Heavy != 1 {
		t.Fatalf("floor 0: derived %+v", f.Derived)
	}
}

// Without a work root there is no volume to look at (a test sampler, a host that has none): no floor applies.
func TestSamplerWithoutAWorkRootChecksNoDisk(t *testing.T) {
	free := 1.0
	s, _ := diskSampler(t, &free, "", 10)
	s.Tick(s0)
	if f, _ := s.Latest(); f.Derived.DiskLow || f.Derived.Headroom.Heavy != 1 {
		t.Fatalf("derived %+v", f.Derived)
	}
}
