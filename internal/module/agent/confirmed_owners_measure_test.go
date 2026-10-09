package agent

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/wake/purdex/internal/tmux"
)

// TestMeasure_ConfirmedOwners times ConfirmedOwners against the machine's real tmux and process table. It is skipped
// unless PDX_MEASURE_OWNERS=1 (it reads the real tmux server: list-panes only, nothing is changed):
//
//	PDX_MEASURE_OWNERS=1 go test ./internal/module/agent -run TestMeasure_ConfirmedOwners -v -count=1
//
// A frame is seeded for this process in a real pane id, so the whole path runs (the candidate lookup, the pane
// listing, the owner pass with its process view and second listing); the owner is not found (the test process is not
// in that pane), which costs the same walk. It logs and asserts nothing: the numbers go in the PR.
func TestMeasure_ConfirmedOwners(t *testing.T) {
	if os.Getenv("PDX_MEASURE_OWNERS") != "1" {
		t.Skip("set PDX_MEASURE_OWNERS=1")
	}
	m, _, _ := newProvenanceQueryModule(t)
	real := tmux.NewRealExecutor()
	m.tmux = real
	ctx := context.Background()
	panes, err := real.ListAllPanes(ctx)
	if err != nil || len(panes) == 0 {
		t.Skipf("no tmux panes to measure against: %v", err)
	}
	start, err := processStartTimeFn(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	seedIdentityFrame(t, m, panes[0].PaneID, "cc", os.Getpid(), start, 1, "measure-sess", "/w")

	const runs = 30
	time1 := func(name string, f func()) {
		d := make([]time.Duration, runs)
		for i := range d {
			t0 := time.Now()
			f()
			d[i] = time.Since(t0)
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		t.Logf("%-28s p50 %v  p95 %v  max %v  (%d runs, %d panes)", name, d[runs/2], d[runs*95/100], d[runs-1], runs, len(panes))
	}
	time1("ListAllPanes", func() { _, _ = real.ListAllPanes(ctx) })
	time1("process view", func() { _, _ = takeProcSnapshotFn() })
	time1("ConfirmedOwners (live frame)", func() { _, _ = m.ConfirmedOwners(ctx, "measure-sess") })
	time1("ConfirmedOwners (no frame)", func() { _, _ = m.ConfirmedOwners(ctx, "nobody-here") })
}
