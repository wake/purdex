package facts

import (
	"os"
	"path/filepath"
	"testing"
)

// A /proc/diskstats as a Linux host prints it: an NVMe disk with two partitions, a SATA disk with one, a loop device, a zram swap, a
// device-mapper volume and an md array. The fields after the name are reads completed, merged, sectors read, ms, then the same for
// writes.
const diskstatsFixture = ` 259       0 nvme0n1 1000 5 20000 300 2000 7 40000 800 0 900 1100 0 0 0 0 0 0
 259       1 nvme0n1p1 100 0 2000 30 200 0 4000 80 0 90 110 0 0 0 0 0 0
 259       2 nvme0n1p2 900 5 18000 270 1800 7 36000 720 0 810 990 0 0 0 0 0 0
   8       0 sda 50 1 800 20 60 2 1200 30 0 40 50 0 0 0 0
   8       1 sda1 50 1 800 20 60 2 1200 30 0 40 50 0 0 0 0
   7       0 loop0 10 0 80 1 0 0 0 0 0 1 1 0 0 0 0
 251       0 zram0 5 0 40 0 5 0 40 0 0 0 0 0 0 0 0
 253       0 dm-0 900 0 17000 250 1700 0 34000 700 0 800 950 0 0 0 0
   9       0 md0 3 0 24 0 3 0 24 0 0 0 0 0 0 0 0
`

func TestParseDiskstats(t *testing.T) {
	got, err := ParseDiskstats(diskstatsFixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 9 {
		t.Fatalf("%d lines: %+v", len(got), got)
	}
	// sectors are 512 bytes whatever the device's own block size
	want := RawDiskIO{Device: "nvme0n1", ReadOps: 1000, ReadBytes: 20000 * 512, WriteOps: 2000, WriteBytes: 40000 * 512}
	if got[0] != want {
		t.Errorf("nvme0n1 %+v want %+v", got[0], want)
	}
	// the short line a 4.x kernel prints (fewer trailing fields) parses the same
	if got[3].Device != "sda" || got[3].ReadOps != 50 || got[3].WriteBytes != 1200*512 {
		t.Errorf("sda %+v", got[3])
	}
	for _, bad := range []string{"", "\n", " 8 0 sda 1 2\n"} {
		if got, err := ParseDiskstats(bad); err == nil && len(got) != 0 {
			t.Errorf("%q gave %+v", bad, got)
		}
	}
	if _, err := ParseDiskstats(" 8 0 sda x 1 2 3 4 5 6 7 8\n"); err == nil {
		t.Error("a counter that is not a number must fail")
	}
}

// fakeSysBlock is a /sys/block with the directories the kernel lists there: a disk has a device link, a loop, zram, dm or md volume
// does not, and a partition is not listed at all.
func fakeSysBlock(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"nvme0n1/device", "sda/device", "loop0", "zram0", "dm-0", "md0"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestPhysicalDiskIOKeepsOnlyWholeDisks(t *testing.T) {
	all, err := ParseDiskstats(diskstatsFixture)
	if err != nil {
		t.Fatal(err)
	}
	got, err := PhysicalDiskIO(all, fakeSysBlock(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Device != "nvme0n1" || got[1].Device != "sda" {
		t.Fatalf("partitions, loop, zram, dm and md must not be listed: %+v", got)
	}
}

// Without /sys/block the disks cannot be told from the volumes on them, and a guess would count a disk twice: an error, so disk_io is null.
func TestPhysicalDiskIONoSysBlockIsAnError(t *testing.T) {
	all, _ := ParseDiskstats(diskstatsFixture)
	if _, err := PhysicalDiskIO(all, filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("no /sys/block must be an error")
	}
}

// A host with no physical disk at all (a container, a VM with a virtual disk that has no device link) lists none: an empty answer, not
// an error and not the volumes.
func TestPhysicalDiskIONoPhysicalDisks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "loop0"), 0o755); err != nil {
		t.Fatal(err)
	}
	all, _ := ParseDiskstats(diskstatsFixture)
	got, err := PhysicalDiskIO(all, root)
	if err != nil || len(got) != 0 {
		t.Errorf("%+v %v", got, err)
	}
}
