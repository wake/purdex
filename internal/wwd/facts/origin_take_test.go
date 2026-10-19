package facts

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
	wident "github.com/wake/purdex/internal/wwd/worker/ident"
)

// A sample made between Observe and Commit, with the job taken in between, must not make the job again after it ended: the job's own
// key (tag and id) is the one marked taken, also when the job was not there yet.
func TestJobUsageTakeMarkBeforeCommitDoesNotLetTheJobBackIn(t *testing.T) {
	a := wident.OriginTag("air26")
	h, tr := newHost(), newJU()
	h.add(300, 100, "go", []string{"go"})
	h.jobs[300], h.origins = "j_x", map[int]string{300: a}
	h.rec(300).CPUPct = 100
	snap, ctx := h.snapshot()
	now := ot0
	tr.Observe(now, snap, ctx)
	if _, ok := tr.TakeMark(a, "j_x"); ok { // nothing committed yet
		t.Fatal("nothing was committed yet")
	}
	tr.Commit(wire.Facts{At: now, Derived: wire.Derived{Zone: wire.ZoneNormal}})
	if tr.Len() != 0 {
		t.Fatalf("the sample of a job that was taken before its commit made the job again (%d kept)", tr.Len())
	}
}

// An old-mark command's samples are taken by the tagged call when nothing is under the tag, and then both keys are closed.
func TestJobUsageTakeMarkFallbackClosesBothKeys(t *testing.T) {
	a := wident.OriginTag("air26")
	h, tr := newHost(), newJU()
	h.add(300, 100, "go", []string{"go"})
	h.jobs[300] = "j_old" // no origin
	h.rec(300).CPUPct = 100
	sample(tr, h, 0, wire.ZoneNormal, nil)
	if _, ok := tr.TakeMark(a, "j_old"); !ok {
		t.Fatal("the old-mark samples must be found")
	}
	sample(tr, h, 1, wire.ZoneNormal, nil) // the same old process is sampled after the job ended
	if tr.Len() != 0 {
		t.Fatalf("a taken job came back under the old key (%d kept)", tr.Len())
	}
	// and the tagged key is closed too: a process that carries the tag now (the job went on under a new mark) is not taken in either
	h.origins = map[int]string{300: a}
	sample(tr, h, 2, wire.ZoneNormal, nil)
	if tr.Len() != 0 {
		t.Fatalf("a taken job came back under its own key (%d kept)", tr.Len())
	}
}
