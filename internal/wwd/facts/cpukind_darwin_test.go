//go:build darwin

package facts

import (
	"os/exec"
	"regexp"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// The registry as `ioreg` prints it (the test file may start a process; the sampler may not) must give the same kinds as the
// in-process read.
func TestDarwinCoreKindsMatchIoreg(t *testing.T) {
	n, err := unix.SysctlUint32("hw.ncpu")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("ioreg", "-l", "-p", "IODeviceTree", "-n", "cpus").Output()
	if err != nil || len(out) == 0 {
		t.Skipf("ioreg not readable: %v", err)
	}
	// each cpuN node prints "cluster-type" = <"E"> and "logical-cpu-id" = N among its properties; a node starts at its "name" line
	ids := regexp.MustCompile(`"logical-cpu-id" = (\d+)`).FindAllStringSubmatch(string(out), -1)
	clusters := regexp.MustCompile(`"cluster-type" = <"([A-Za-z])">`).FindAllStringSubmatch(string(out), -1)
	if len(ids) != int(n) || len(clusters) != int(n) {
		t.Skipf("ioreg shows %d ids and %d cluster-types for %d cores; cannot pair them from the text", len(ids), len(clusters), n)
	}
	// pair by node: split the text at each node's opening line and read both properties from the same chunk
	chunks := regexp.MustCompile(`(?m)^\s*[|+ ]*-o cpu\d+@`).Split(string(out), -1)[1:]
	want := make([]string, n)
	for _, c := range chunks {
		id := regexp.MustCompile(`"logical-cpu-id" = (\d+)`).FindStringSubmatch(c)
		cl := regexp.MustCompile(`"cluster-type" = <"([A-Za-z])">`).FindStringSubmatch(c)
		if id == nil || cl == nil {
			t.Skip("ioreg text does not keep a node's properties together")
		}
		i, _ := strconv.Atoi(id[1])
		want[i] = map[string]string{"E": "e", "P": "p"}[cl[1]]
	}
	got := DarwinCoreKinds(int(n))
	if got == nil {
		t.Fatal("the in-process read gave no kinds on a machine whose ioreg lists them")
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("core %d: got %q, ioreg says %q", i, got[i], want[i])
		}
	}
	if DarwinCoreKinds(int(n)+1) != nil {
		t.Error("an answer for another core count must be nil")
	}
}
