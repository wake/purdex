package facts

import (
	"errors"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func byDevice(t *testing.T, got []wire.DiskIO, dev string) wire.DiskIO {
	t.Helper()
	for _, d := range got {
		if d.Device == dev {
			return d
		}
	}
	t.Fatalf("no device %q in %+v", dev, got)
	return wire.DiskIO{}
}

func near(p *float64, want float64) bool { return p != nil && *p > want-1e-6 && *p < want+1e-6 }

// A device is listed from its first sight, with no rates (nothing to compare with); from the second sample the rates are the
// counters' difference over the real time between the two, which is not always 5 s.
func TestDiskIOBuilderRates(t *testing.T) {
	b := NewDiskIOBuilder()
	first := b.Build(t0, []RawDiskIO{{Device: "disk0", ReadBytes: 1000, WriteBytes: 2000, ReadOps: 10, WriteOps: 20}})
	d := byDevice(t, first, "disk0")
	if d.ReadBps != nil || d.WriteBps != nil || d.ReadIOPS != nil || d.WriteIOPS != nil {
		t.Fatalf("first sight: the device is listed, its rates are null: %+v", d)
	}
	// 5 s
	got := b.Build(t0.Add(5*time.Second), []RawDiskIO{{Device: "disk0", ReadBytes: 6000, WriteBytes: 2000, ReadOps: 60, WriteOps: 25}})
	d = byDevice(t, got, "disk0")
	if !near(d.ReadBps, 1000) || !near(d.WriteBps, 0) || !near(d.ReadIOPS, 10) || !near(d.WriteIOPS, 1) {
		t.Errorf("5 s: %+v", d)
	}
	// 7.3 s, not 5: the rate is per second of the real interval
	got = b.Build(t0.Add(12300*time.Millisecond), []RawDiskIO{{Device: "disk0", ReadBytes: 6000 + 7300, WriteBytes: 2000 + 14600, ReadOps: 60 + 73, WriteOps: 25 + 146}})
	d = byDevice(t, got, "disk0")
	if !near(d.ReadBps, 1000) || !near(d.WriteBps, 2000) || !near(d.ReadIOPS, 10) || !near(d.WriteIOPS, 20) {
		t.Errorf("7.3 s: %+v", d)
	}
}

// A counter that went back (a driver reset), or a clock that did not move, gives null for that pair and the next pair is good again.
func TestDiskIOBuilderCounterBackwardsAndNoTime(t *testing.T) {
	b := NewDiskIOBuilder()
	b.Build(t0, []RawDiskIO{{Device: "disk0", ReadBytes: 5000, WriteBytes: 5000, ReadOps: 50, WriteOps: 50}})
	got := b.Build(t0.Add(5*time.Second), []RawDiskIO{{Device: "disk0", ReadBytes: 100, WriteBytes: 5500, ReadOps: 60, WriteOps: 55}})
	d := byDevice(t, got, "disk0")
	// each figure is judged by its own counter: the one that went back is null, the others are good
	if d.ReadBps != nil || !near(d.WriteBps, 100) || !near(d.ReadIOPS, 2) || !near(d.WriteIOPS, 1) {
		t.Errorf("read bytes went back: %+v", d)
	}
	got = b.Build(t0.Add(5*time.Second), []RawDiskIO{{Device: "disk0", ReadBytes: 200, WriteBytes: 5600, ReadOps: 61, WriteOps: 56}})
	d = byDevice(t, got, "disk0")
	if d.ReadBps != nil || d.WriteBps != nil || d.ReadIOPS != nil || d.WriteIOPS != nil {
		t.Errorf("no time passed: %+v", d)
	}
	got = b.Build(t0.Add(10*time.Second), []RawDiskIO{{Device: "disk0", ReadBytes: 5200, WriteBytes: 5600, ReadOps: 61, WriteOps: 56}})
	if d = byDevice(t, got, "disk0"); !near(d.ReadBps, 1000) {
		t.Errorf("the baseline is the latest sample, so the next pair is good: %+v", d)
	}
}

// A device that goes away and comes back under the same name starts again: its old counters are not a baseline.
func TestDiskIOBuilderReappearingDeviceStartsOver(t *testing.T) {
	b := NewDiskIOBuilder()
	b.Build(t0, []RawDiskIO{{Device: "disk0", ReadBytes: 100}, {Device: "disk2", ReadBytes: 100}})
	b.Build(t0.Add(5*time.Second), []RawDiskIO{{Device: "disk0", ReadBytes: 600}}) // disk2 is gone
	got := b.Build(t0.Add(10*time.Second), []RawDiskIO{{Device: "disk0", ReadBytes: 1100}, {Device: "disk2", ReadBytes: 5000}})
	if d := byDevice(t, got, "disk2"); d.ReadBps != nil {
		t.Errorf("disk2 is back after a sample without it: its rates are null, got %v", *d.ReadBps)
	}
	if d := byDevice(t, got, "disk0"); !near(d.ReadBps, 100) {
		t.Errorf("disk0 carried on: %+v", d)
	}
	// and a device new to the list is null the first time too, however big its counters
	if len(got) != 2 || got[0].Device != "disk0" || got[1].Device != "disk2" {
		t.Errorf("devices come sorted by name: %+v", got)
	}
}

// Nothing to list is not an error and not a list: nil.
func TestDiskIOBuilderEmpty(t *testing.T) {
	if got := NewDiskIOBuilder().Build(t0, nil); got != nil {
		t.Errorf("%+v", got)
	}
}

// The rates are over the time between the two counter reads, not between the two sample starts: when a source read before the disk
// counters is slow in one sample, the counters cover more than the nominal 5 s (R2 attack).
func TestCollectorDiskIOUsesTheTimeOfItsOwnRead(t *testing.T) {
	now := t0
	var disksTake time.Duration
	ops := uint64(0)
	src := Sources{
		Now: func() time.Time { return now },
		CPU: func() (RawCPU, error) { return RawCPU{NCPU: 2, Busy: 1, Total: 2}, nil },
		Mem: func() (RawMem, error) { return fixtureMem(1, 1), nil },
		Disks: func() ([]RawDisk, error) {
			now = now.Add(disksTake) // a slow mount point before the disk counters are read
			return nil, nil
		},
		DiskIO: func() ([]RawDiskIO, error) { return []RawDiskIO{{Device: "disk0", ReadOps: ops}}, nil },
	}
	c := NewCollector(src)
	if _, err := c.Sample(); err != nil { // counters read at t0
		t.Fatal(err)
	}
	// the second sample starts 5 s later, its disks call takes 30 s, so its counters are read at t0+35 s: 350 operations in 35 s
	now, ops, disksTake = t0.Add(5*time.Second), 350, 30*time.Second
	r, _ := c.Sample()
	if len(r.DiskIO) != 1 || !near(r.DiskIO[0].ReadIOPS, 10) {
		t.Fatalf("%+v want 10 iops (350 operations over the 35 s between the reads)", r.DiskIO)
	}
}

// The collector hands the source's devices through the builder; a failing source leaves disk_io null without failing the sample.
func TestCollectorDiskIO(t *testing.T) {
	now := t0
	ops := uint64(10)
	var fail bool
	src := Sources{
		Now:   func() time.Time { return now },
		CPU:   func() (RawCPU, error) { return RawCPU{NCPU: 2, Busy: 1, Total: 2}, nil },
		Mem:   func() (RawMem, error) { return fixtureMem(1, 1), nil },
		Disks: func() ([]RawDisk, error) { return nil, nil },
		DiskIO: func() ([]RawDiskIO, error) {
			if fail {
				return nil, errTestDiskIO
			}
			return []RawDiskIO{{Device: "disk0", ReadOps: ops}}, nil
		},
	}
	c := NewCollector(src)
	r, err := c.Sample()
	if err != nil || len(r.DiskIO) != 1 || r.DiskIO[0].ReadIOPS != nil {
		t.Fatalf("first: %+v %v", r.DiskIO, err)
	}
	now, ops = t0.Add(5*time.Second), 60
	r, _ = c.Sample()
	if len(r.DiskIO) != 1 || !near(r.DiskIO[0].ReadIOPS, 10) {
		t.Fatalf("second: %+v", r.DiskIO)
	}
	fail = true
	r, err = c.Sample()
	if err != nil || r.DiskIO != nil {
		t.Errorf("a failed read is null, not a failed sample: %+v %v", r.DiskIO, err)
	}
	var f wire.Facts
	r.Apply(&f)
}

var errTestDiskIO = errors.New("test: disk io unreadable")
