//go:build darwin

package facts

import (
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// ioregWholeDisks lists the BSD names of the IOMedia that sit directly under an IOBlockStorageDriver in `ioreg -c IOBlockStorageDriver
// -r -l` and are Whole = Yes. The same output also shows, further down the tree, the disks macOS synthesises for APFS containers (disk1,
// disk3: class AppleAPFSMedia, also Whole = Yes): those are not under a driver and are not physical disks.
func ioregWholeDisks(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("ioreg", "-c", "IOBlockStorageDriver", "-r", "-l").Output()
	if err != nil {
		t.Skipf("ioreg: %v", err)
	}
	node := regexp.MustCompile(`^(.*?)\+-o .*<class (\w+)`)
	bsd := regexp.MustCompile(`"BSD Name" = "([^"]+)"`)
	classAt := map[int]string{} // the latest object seen at each depth of the tree
	var names []string
	var cur struct {
		directMedia, whole bool
		name               string
	}
	flush := func() {
		if cur.directMedia && cur.whole && cur.name != "" {
			names = append(names, cur.name)
		}
		cur.directMedia, cur.whole, cur.name = false, false, ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if m := node.FindStringSubmatch(line); m != nil {
			flush()
			depth := len(m[1])
			classAt[depth] = m[2]
			cur.directMedia = m[2] == "IOMedia" && classAt[depth-2] == "IOBlockStorageDriver"
			continue
		}
		if strings.Contains(line, `"Whole" = Yes`) {
			cur.whole = true
		}
		if m := bsd.FindStringSubmatch(line); m != nil {
			cur.name = m[1]
		}
	}
	flush()
	sort.Strings(names)
	return names
}

// The disks the IOKit read lists are the whole disks ioreg shows, and their counters only grow between two reads.
func TestDarwinDiskIOAgreesWithIoreg(t *testing.T) {
	read, err := newDarwinDiskIO()
	if err != nil {
		t.Fatal(err)
	}
	a, err := read()
	if err != nil {
		t.Fatal(err)
	}
	want := ioregWholeDisks(t)
	if len(want) == 0 {
		t.Skip("ioreg shows no whole disk on this host")
	}
	var got []string
	for _, d := range a {
		got = append(got, d.Device)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("devices %v, ioreg shows %v", got, want)
	}
	b, err := read()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != len(a) {
		t.Fatalf("%d then %d disks", len(a), len(b))
	}
	for i := range a {
		if a[i].Device != b[i].Device || b[i].ReadBytes < a[i].ReadBytes || b[i].WriteBytes < a[i].WriteBytes || b[i].ReadOps < a[i].ReadOps || b[i].WriteOps < a[i].WriteOps {
			t.Errorf("%+v then %+v", a[i], b[i])
		}
		if a[i].ReadOps == 0 && a[i].WriteOps == 0 {
			t.Errorf("%s has counters of zero since boot", a[i].Device)
		}
	}
}

// Reading again and again keeps working: a run of reads does not run out of IOKit iterators or Mach ports (what a read creates and does
// not release would pile up here).
func TestDarwinDiskIORepeatedReadsDoNotFail(t *testing.T) {
	read, err := newDarwinDiskIO()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2000; i++ {
		if _, err := read(); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
}

// The enumeration is on the sampling path, once per 5 s: its cost is logged, and a figure over a millisecond is to be reported
// (plan G-3 #7), so the test fails past 5 ms rather than let a wrong order of magnitude through.
func TestDarwinDiskIOCost(t *testing.T) {
	read, err := newDarwinDiskIO()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_, _ = read()
	}
	const n = 200
	start := time.Now()
	for i := 0; i < n; i++ {
		_, _ = read()
	}
	per := time.Since(start) / n
	t.Logf("IOKit disk enumeration: %v per read", per)
	if per > 5*time.Millisecond {
		t.Errorf("%v per read", per)
	}
}

func BenchmarkDarwinDiskIO(b *testing.B) {
	read, err := newDarwinDiskIO()
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = read()
	}
}
