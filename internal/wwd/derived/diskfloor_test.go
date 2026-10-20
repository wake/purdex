package derived

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

var disks = []wire.Disk{{Mount: "/", FreeGB: 80}, {Mount: "/System/Volumes/Data", FreeGB: 8}, {Mount: "/Users/wake/work", FreeGB: 30}}

func TestWorkVolumeIsTheLongestMountAboveTheRoot(t *testing.T) {
	cases := []struct {
		root string
		want string
		ok   bool
	}{
		{"/Users/wake/work/wwd", "/Users/wake/work", true},
		{"/Users/wake/work", "/Users/wake/work", true},
		{"/Users/wake/work2/wwd", "/", true}, // whole path components: work2 is not under work
		{"/var/db", "/", true},
		{"/Volumes/other", "/", true},
	}
	for _, c := range cases {
		d, ok := WorkVolume(disks, c.root)
		if ok != c.ok || d.Mount != c.want {
			t.Errorf("%s: %q %v, want %q %v", c.root, d.Mount, ok, c.want, c.ok)
		}
	}
	if _, ok := WorkVolume([]wire.Disk{{Mount: "/data"}}, "/home/wwd"); ok {
		t.Error("a root no mount holds was found")
	}
	if _, ok := WorkVolume(nil, "/x"); ok {
		t.Error("no disks, a volume was found")
	}
}

func TestDiskLowComparesTheWorkVolumeWithTheFloor(t *testing.T) {
	cases := []struct {
		name  string
		disks []wire.Disk
		root  string
		min   float64
		want  bool
	}{
		{"below", disks, "/Users/wake/work/x", 40, true},
		{"above", disks, "/Users/wake/work/x", 10, false},
		{"exactly the floor is enough (the gate's rule)", disks, "/Users/wake/work/x", 30, false},
		{"floor 0 is none", disks, "/Users/wake/work/x", 0, false},
		{"negative is none", disks, "/Users/wake/work/x", -1, false},
		{"a volume that is not in the facts is not a reason", []wire.Disk{{Mount: "/data", FreeGB: 1}}, "/home/wwd", 10, false},
		{"no disks read is not a reason", nil, "/home/wwd", 10, false},
		{"the root volume", disks, "/var/wwd", 100, true},
	}
	for _, c := range cases {
		if got := DiskLow(c.disks, c.root, c.min); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
