package fleet

import (
	"math"
	"strings"
	"testing"
)

// The vm_stat block is the one internal/resources tests with (sample_test.go vmStatSample).
const darwinOut = `@@uname
Darwin
@@ncpu
10
@@memsize
17179869184
@@load
{ 2.50 2.10 1.90 }
@@mem
Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                    68039.
Pages active:                                 332323.
Pages inactive:                               318361.
Pages speculative:                             13661.
Pages throttled:                                   0.
Pages wired down:                             137688.
@@disk
Filesystem     1024-blocks      Used Available Capacity iused      ifree %iused  Mounted on
/dev/disk3s5     239362496 152410000  80000000    66%  600000 800000000    0%   /System/Volumes/Data
@@end
`

const linuxOut = `@@uname
Linux
@@ncpu
4
@@load
0.80 0.50 0.40 1/234 5678
@@mem
MemTotal:        8000000 kB
MemFree:          500000 kB
MemAvailable:    2000000 kB
@@disk
Filesystem     1K-blocks     Used Available Use% Mounted on
/dev/sda1       100000000 87000000  13000000  87% /
@@end
`

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestParse_Darwin(t *testing.T) {
	r, err := Parse(darwinOut)
	if err != nil {
		t.Fatal(err)
	}
	// 61.847: the figure resources.ComputeHost gives for this vm_stat block on a 16 GiB host.
	if r.OS != "Darwin" || r.NCPU != 10 || r.Load1 != 2.5 || !near(r.CPU, 25) || !near(r.Mem, 61.847) || r.Disk != 66 {
		t.Fatalf("got %+v", r)
	}
}

func TestParse_Linux(t *testing.T) {
	r, err := Parse(linuxOut)
	if err != nil {
		t.Fatal(err)
	}
	if r.OS != "Linux" || r.NCPU != 4 || r.Load1 != 0.8 || !near(r.CPU, 20) || !near(r.Mem, 75) || r.Disk != 87 {
		t.Fatalf("got %+v", r)
	}
}

func TestParse_Errors(t *testing.T) {
	cases := map[string]string{
		"no end marker": strings.Replace(darwinOut, "@@end\n", "", 1),
		"empty":         "",
		"unknown os":    strings.Replace(linuxOut, "Linux", "Plan9", 1),
		"no cpu":        strings.Replace(linuxOut, "@@ncpu\n4", "@@ncpu\n", 1),
		"zero cpu":      strings.Replace(linuxOut, "@@ncpu\n4", "@@ncpu\n0", 1),
		"bad load":      strings.Replace(linuxOut, "0.80", "x", 1),
		"no MemAvail":   strings.Replace(linuxOut, "MemAvailable:", "Nope:", 1),
		"no vm free":    strings.Replace(darwinOut, "Pages free:", "Pages gone:", 1),
		"no memsize":    strings.Replace(darwinOut, "17179869184", "", 1),
		"bad df":        strings.Replace(linuxOut, "87% /", "/", 1),
		"df over 100":   strings.Replace(linuxOut, "87%", "187%", 1),
		"disk missing":  strings.Replace(linuxOut, "@@disk", "@@dusk", 1),
	}
	for name, out := range cases {
		if _, err := Parse(out); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestScript_MountsMatchDiskMount(t *testing.T) {
	if !strings.Contains(Script, "df -k "+DiskMount("darwin")) || !strings.Contains(Script, "df -kP "+DiskMount("linux")) {
		t.Error("Script and DiskMount disagree on the disk mount")
	}
	if !strings.HasSuffix(strings.TrimSpace(Script), "echo '@@end'") {
		t.Error("Script must end with the end marker")
	}
}
