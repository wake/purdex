package facts

import "testing"

var fullStats = map[string]uint64{
	"Bytes (Read)": 16198458789888, "Bytes (Write)": 2494512979968, "Operations (Read)": 885403435, "Operations (Write)": 162267166,
	"Errors (Read)": 0, "Total Time (Read)": 161601143331559,
}

// The IOBlockStorageDriver's Statistics and its whole-disk IOMedia give one disk's counters under the name of the media.
func TestRawDiskIOFromProps(t *testing.T) {
	got, ok := rawDiskIOFromProps(fullStats, true, "disk0")
	want := RawDiskIO{Device: "disk0", ReadBytes: 16198458789888, WriteBytes: 2494512979968, ReadOps: 885403435, WriteOps: 162267166}
	if !ok || got != want {
		t.Fatalf("%+v %v want %+v", got, ok, want)
	}
}

// A driver without a whole-disk media (a partition's, or no BSD name), and one whose statistics lack any of the four figures, is
// left out: a missing figure is not a zero.
func TestRawDiskIOFromPropsRefusesWhatIsNotAWholeDisk(t *testing.T) {
	if _, ok := rawDiskIOFromProps(fullStats, false, "disk0s1"); ok {
		t.Error("a media that is not Whole is not a disk")
	}
	if _, ok := rawDiskIOFromProps(fullStats, true, ""); ok {
		t.Error("no BSD name: no device to call it")
	}
	for _, missing := range []string{"Bytes (Read)", "Bytes (Write)", "Operations (Read)", "Operations (Write)"} {
		s := map[string]uint64{}
		for k, v := range fullStats {
			if k != missing {
				s[k] = v
			}
		}
		if _, ok := rawDiskIOFromProps(s, true, "disk0"); ok {
			t.Errorf("without %q the counters are incomplete", missing)
		}
	}
	if _, ok := rawDiskIOFromProps(nil, true, "disk0"); ok {
		t.Error("no statistics")
	}
}
