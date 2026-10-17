//go:build darwin

package hostmem

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// BenchmarkFootprintsAllProcesses is the cost of the pass the local source makes every sampler tick (about 5 s): the footprint of every
// process of this host (#3057). ns/op is one whole pass; the pids are those `ps` lists now, the ones another user owns fail fast.
func BenchmarkFootprintsAllProcesses(b *testing.B) {
	out, err := exec.Command("/bin/ps", "-A", "-o", "pid=").Output()
	if err != nil {
		b.Skipf("no ps: %v", err)
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		if n, err := strconv.Atoi(f); err == nil {
			pids = append(pids, n)
		}
	}
	f, err := NewFootprinter()
	if err != nil {
		b.Fatal(err)
	}
	got := Footprints(f, pids)
	b.Logf("%d processes, %d with a footprint", len(pids), len(got))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Footprints(f, pids)
	}
}
