package agent

import (
	"strconv"
	"testing"
	"time"
)

// #3316 C: projectPane (what the SPA shows of a pane, after a hook event) takes the pane's pid from the shared pane listing instead of
// forking a display-message for it. The fixture counts the batch listings and the per-pane pid reads.

// With the shared listing (a TTL) many events on several panes cost ONE listing and no per-pane read; the projection is the same as the
// one read live.
func TestProjectPane_SharedListingNoPerPaneRead(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	live := newProjFixture(t, 4, false) // paneSnapTTL = 0: the pane's pid is read live, as before
	var want []*SessionProjection
	for i := 0; i < 4; i++ {
		p, err := live.m.projectPane("%" + strconv.Itoa(i))
		if err != nil || p == nil {
			t.Fatalf("live %%%d: %+v, %v", i, p, err)
		}
		want = append(want, p)
	}
	if _, pid, _ := live.tx.counts(); pid != 4 {
		t.Fatalf("the live read made %d per-pane pid reads, want 4", pid)
	}

	f.tx.reset()
	for round := 0; round < 3; round++ {
		for i := 0; i < 4; i++ {
			p, err := f.m.projectPane("%" + strconv.Itoa(i))
			if err != nil || p == nil {
				t.Fatalf("round %d %%%d: %+v, %v", round, i, p, err)
			}
			if round == 0 && !projectionsEqual(p, want[i]) {
				t.Fatalf("%%%d: shared %+v, live %+v", i, p, want[i])
			}
		}
	}
	batch, pid, _ := f.tx.counts()
	if batch != 1 || pid != 0 {
		t.Fatalf("12 projections inside the TTL: %d listings and %d per-pane pid reads, want 1 and 0", batch, pid)
	}
}

// A pane the cached listing does not know may be new: it is listed once more, then used; a pane tmux does not list at all is answered
// as the live read answers (its frames unfiltered), after that one extra listing.
func TestProjectPane_UnknownPaneForcesOneRefresh(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	if _, err := f.m.projectPane("%0"); err != nil { // fills the cache
		t.Fatal(err)
	}
	f.tx.SetPaneSessionName("%new", "snew")
	f.tx.SetActivePanePID("%new", "2999")
	seedIdentityFrame(t, f.m, "%new", "cc", 1999, "Sun Apr 20 01:30:00 2026", 99, "sid-new", "/w")
	f.tx.reset()
	p, err := f.m.projectPane("%new")
	if err != nil || p == nil || p.TopFrame == nil {
		t.Fatalf("a new pane read as having no agent: %+v, %v", p, err)
	}
	if batch, pid, _ := f.tx.counts(); batch != 1 || pid != 0 {
		t.Fatalf("new pane: %d listings and %d pid reads, want 1 and 0", batch, pid)
	}

	// a pane with a frame that tmux does not list: one forced listing, no per-pane read, frames unfiltered (as the live read's error path)
	seedIdentityFrame(t, f.m, "%ghost", "cc", 1998, "Sun Apr 20 01:30:00 2026", 98, "sid-ghost", "/w")
	f.tx.reset()
	if p, err = f.m.projectPane("%ghost"); err != nil || p == nil {
		t.Fatalf("ghost: %+v, %v", p, err)
	}
	if batch, pid, _ := f.tx.counts(); batch != 1 || pid != 0 {
		t.Fatalf("ghost pane: %d listings and %d pid reads, want 1 and 0", batch, pid)
	}
}

// A pane that tmux does not list (a frame left behind by a pane that is gone) forces ONE more listing, and is then remembered for the
// TTL: projecting it again and again does not list again each time. A layout change, or the pane turning up, ends the memory.
func TestProjectPane_AbsentPaneIsListedOnceNotEveryEvent(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	if _, err := f.m.projectPane("%0"); err != nil { // fills the cache
		t.Fatal(err)
	}
	seedIdentityFrame(t, f.m, "%ghost", "cc", 1998, "Sun Apr 20 01:30:00 2026", 98, "sid-ghost", "/w")
	f.tx.reset()
	for i := 0; i < 6; i++ {
		if p, err := f.m.projectPane("%ghost"); err != nil || p == nil {
			t.Fatalf("read %d: %+v, %v", i, p, err)
		}
	}
	if batch, pid, _ := f.tx.counts(); batch != 1 || pid != 0 {
		t.Fatalf("6 projections of a pane tmux does not list: %d listings and %d pid reads, want 1 and 0", batch, pid)
	}

	// the layout changed: the memory ends and the pane is looked for again
	f.m.bumpTmuxGen()
	f.tx.reset()
	if _, err := f.m.projectPane("%ghost"); err != nil {
		t.Fatal(err)
	}
	if batch, _, _ := f.tx.counts(); batch != 1 {
		t.Fatalf("after a layout change: %d listings, want 1 (a fresh one)", batch)
	}

	// the pane turns up (an external split the daemon was not told of): once the memory has expired it is found
	f.tx.SetPaneSessionName("%ghost", "s1")
	f.tx.SetActivePanePID("%ghost", "2998")
	f.m.snapCache.mu.Lock()
	f.m.snapCache.absent["%ghost"] = absentPane{at: time.Now().Add(-2 * time.Hour), gen: f.m.tmuxGen.Load()}
	f.m.snapCache.mu.Unlock()
	f.m.snapCache.mu.Lock()
	f.m.snapCache.taken = time.Now() // the cached listing is the old one, without %ghost
	f.m.snapCache.mu.Unlock()
	snap := f.m.projectionPaneSnapshot("%ghost")
	if snap == nil {
		t.Fatal("no snapshot")
	}
	if _, listed := snap.panes["%ghost"]; !listed {
		t.Fatal("an expired absent entry kept a pane that is there from being found")
	}
}

// The absent map is bounded.
func TestPaneSnapCache_AbsentIsBounded(t *testing.T) {
	var c paneSnapCache
	for i := 0; i < 3*maxAbsentPanes; i++ {
		c.noteAbsent("%"+strconv.Itoa(i), time.Hour, 1)
	}
	if n := len(c.absent); n > maxAbsentPanes {
		t.Fatalf("absent holds %d entries, want at most %d", n, maxAbsentPanes)
	}
	if !c.recentlyAbsent("%0", time.Hour, 1) || c.recentlyAbsent("%0", time.Hour, 2) || c.recentlyAbsent("%0", 0, 1) {
		t.Fatal("recentlyAbsent: want true for the same generation within the TTL, false for another generation or no TTL")
	}
}

// A pane with no frames needs no pid at all: nothing is listed for it.
func TestProjectPane_NoFramesNoTmuxRead(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	f.tx.reset()
	p, err := f.m.projectPane("%empty")
	if err != nil || p == nil {
		t.Fatalf("%+v, %v", p, err)
	}
	if batch, pid, session := f.tx.counts(); batch+pid+session != 0 {
		t.Fatalf("a pane without frames read tmux: %d %d %d", batch, pid, session)
	}
}

// A frame whose process is not a descendant of the pane's process is still left out, with the pid from the shared listing.
func TestProjectPane_SharedListingStillFiltersForeignFrames(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	f.tx.SetPaneSessionName("%foreign", "s1")
	f.tx.SetActivePanePID("%foreign", "9999") // pidAncestorIncludesFn: only pid+1000 == pane pid is an ancestor
	seedIdentityFrame(t, f.m, "%foreign", "cc", 500, "Sun Apr 20 01:30:00 2026", 600, "sid-foreign", "/w")
	f.m.bumpTmuxGen()
	p, err := f.m.projectPane("%foreign")
	if err != nil || p == nil {
		t.Fatalf("%+v, %v", p, err)
	}
	if p.TopFrame != nil {
		t.Fatalf("a foreign frame was projected: %+v", p.TopFrame)
	}
}

func projectionsEqual(a, b *SessionProjection) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Status == b.Status && ((a.TopFrame == nil) == (b.TopFrame == nil)) &&
		(a.TopFrame == nil || a.TopFrame.FrameID != "" && b.TopFrame.FrameID != "")
}
