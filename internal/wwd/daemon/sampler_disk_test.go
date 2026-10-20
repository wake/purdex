package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// diskSampler is a sampler over fakeSources (whose facts list one disk, the root, with 50 GB free) whose work root has *freeGB free
// on a volume of its own, under a disk floor of floor GB. *readErr, when set, is what reading the work root's space answers.
func diskSampler(t *testing.T, freeGB *float64, readErr *error, workRoot string, floor float64) (*Sampler, *HostState) {
	t.Helper()
	level := 1
	st, err := NewHostState(HostStateOptions{Host: "mlab", Initial: wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}, DiskMinGB: &floor}})
	if err != nil {
		t.Fatal(err)
	}
	return NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mlab", OSVersion: "26.6.2", Sources: fakeSources(&level, nil), State: st, WorkRoot: workRoot,
		WorkFreeGB: func(path string) (float64, error) {
			if path != workRoot {
				t.Errorf("read %q, want the work root %q", path, workRoot)
			}
			if readErr != nil && *readErr != nil {
				return 0, *readErr
			}
			return *freeGB, nil
		},
	}), st
}

// spec §3.2 disk_low: a work volume under the worker's disk floor holds heavy and only heavy; the floor is read at every sample, so a PUT
// takes effect at the next one.
func TestSamplerHoldsHeavyOnALowWorkVolume(t *testing.T) {
	free := 8.0
	s, st := diskSampler(t, &free, nil, "/Volumes/Jobs/wwd", 10)
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

// The work root's own volume is measured, not the nearest one the facts' disk list holds: here the list says the root volume has 50 GB
// and the work root, on a volume of its own, has 5.
func TestSamplerMeasuresTheWorkRootsOwnVolume(t *testing.T) {
	free := 5.0
	s, _ := diskSampler(t, &free, nil, "/mnt/wwd", 10)
	s.Tick(s0)
	f, _ := s.Latest()
	if len(f.Disk) == 0 || f.Disk[0].FreeGB < 40 {
		t.Fatalf("the test needs a facts disk list with room: %+v", f.Disk)
	}
	if !f.Derived.DiskLow || f.Derived.Headroom.Heavy != 0 {
		t.Fatalf("a work volume with 5 GB hidden by the listed disk: derived %+v", f.Derived)
	}
}

// A work root whose space cannot be read is not a reason to hold work (the gate's rule), and it does not stop the sampler.
func TestSamplerDoesNotHoldWorkForAnUnreadableWorkRoot(t *testing.T) {
	free := 1.0
	readErr := errors.New("statfs: no such file")
	s, _ := diskSampler(t, &free, &readErr, "/mnt/gone", 10)
	s.Tick(s0)
	if f, _ := s.Latest(); f.Derived.DiskLow || f.Derived.Headroom.Heavy != 1 {
		t.Fatalf("derived %+v", f.Derived)
	}
	readErr = nil // readable again: the floor applies at once
	s.Tick(s0.Add(5 * time.Second))
	if f, _ := s.Latest(); !f.Derived.DiskLow {
		t.Fatalf("derived %+v", f.Derived)
	}
}

// Without a work root there is no volume to look at (a test sampler, a host that has none): no floor applies and nothing is read.
func TestSamplerWithoutAWorkRootChecksNoDisk(t *testing.T) {
	level := 1
	st, err := NewHostState(HostStateOptions{Host: "mlab", Initial: wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mlab", OSVersion: "26.6.2", Sources: fakeSources(&level, nil), State: st,
		WorkFreeGB: func(string) (float64, error) { t.Error("read the disk without a work root"); return 0, nil },
	})
	s.Tick(s0)
	if f, _ := s.Latest(); f.Derived.DiskLow || f.Derived.Headroom.Heavy != 1 {
		t.Fatalf("derived %+v", f.Derived)
	}
}
