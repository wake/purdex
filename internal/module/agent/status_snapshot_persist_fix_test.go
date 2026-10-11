package agent

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// countingInstanceProvider counts TmuxInstance probes (each one can start a tmux process).
type countingInstanceProvider struct {
	*fakeSessionProvider
	probes atomic.Int32
}

func (p *countingInstanceProvider) TmuxInstance() string {
	p.probes.Add(1)
	return p.fakeSessionProvider.TmuxInstance()
}

// A clear whose DeleteAll keeps failing must not let a later flush write over the debt: the snapshot that arrived after
// the clear has to survive the eventual DeleteAll.
func TestStatusSnapshotPersist_NewSnapshotSurvivesOwedClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	a := moduleOn(t, path)
	a.sessions = sessionsOf("tm1", "c1")
	postSnap(a, "c1", `{"cwd":"/old"}`)
	a.flushStatusSnapshots()

	fail := true
	a.snapDeleteAllFn = func() error {
		if fail {
			return errors.New("disk busy")
		}
		return a.snaps.DeleteAll()
	}
	a.clearStatusSnapshots() // fails: owed
	postSnap(a, "c1", `{"cwd":"/new"}`)
	if n := a.flushStatusSnapshots(); n != 0 { // still failing: the flush must abort, keeping the row dirty
		t.Fatalf("flush wrote %d rows while the clear was still owed", n)
	}
	fail = false
	if n := a.flushStatusSnapshots(); n != 1 {
		t.Fatalf("recovery flush wrote %d, want the new snapshot", n)
	}

	b := moduleOn(t, path)
	b.sessions = sessionsOf("tm1", "c1")
	b.restoreStatusSnapshots(context.Background())
	got := replayOf(t, b)
	if got["c1"] == "" {
		t.Fatalf("the snapshot posted after the clear was lost: %v", got)
	}
}

// A flush with nothing to write must not reach tmux.
func TestStatusSnapshotPersist_IdleFlushDoesNotProbeTmux(t *testing.T) {
	a := moduleOn(t, filepath.Join(t.TempDir(), "agent.db"))
	p := &countingInstanceProvider{fakeSessionProvider: sessionsOf("tm1", "c1")}
	a.sessions = p
	a.flushStatusSnapshots()
	a.flushStatusSnapshots()
	if n := p.probes.Load(); n != 0 {
		t.Fatalf("idle flush probed tmux %d times", n)
	}
	postSnap(a, "c1", `{}`)
	a.flushStatusSnapshots()
	if p.probes.Load() == 0 {
		t.Fatal("a flush with a row to write should probe the generation")
	}
}
