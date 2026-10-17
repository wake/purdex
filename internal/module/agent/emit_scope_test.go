package agent

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// #3092 A: the session-scoped read must equal the full-table read for every session name.

func newScopeModule(t *testing.T) (*Module, *batchCountingTmux) {
	t.Helper()
	m := newTestModule(t)
	fake := tmux.NewFakeExecutor()
	tx := &batchCountingTmux{FakeExecutor: fake}
	m.tmux = tx
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "c0", Name: "s0"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}
	return m, tx
}

func TestProjectionForSession_ScopedEqualsGlobal(t *testing.T) {
	const start = "Sun Apr 20 01:30:00 2026"
	for seed := int64(1); seed <= 25; seed++ {
		rng := rand.New(rand.NewSource(seed))
		m, tx := newScopeModule(t)
		dead := map[int]bool{}
		orig := pidAncestorIncludesFn
		pidAncestorIncludesFn = func(pid, ancestor int) bool { return !dead[pid] }
		t.Cleanup(func() { pidAncestorIncludesFn = orig })

		nPanes := 3 + rng.Intn(10)
		pid := 100
		for i := 0; i < nPanes; i++ {
			pane := "%" + strconv.Itoa(i)
			sess := "s" + strconv.Itoa(rng.Intn(4))
			switch rng.Intn(6) {
			case 0: // tmux no longer lists the pane
			case 1: // linked into several sessions: the listing cannot tell, the per-pane answer can
				tx.SetPaneSessionName(pane, sess)
				tx.SetActivePanePID(pane, strconv.Itoa(9000+i))
				tx.SetPaneAmbiguous(pane, true)
			default:
				tx.SetPaneSessionName(pane, sess)
				tx.SetActivePanePID(pane, strconv.Itoa(9000+i))
			}
			for k := rng.Intn(4); k > 0; k-- { // 0..3 frames in one pane
				pid++
				if rng.Intn(4) == 0 {
					dead[pid] = true // not the pane process's descendant: filtered out
				}
				seedIdentityFrame(t, m, pane, "cc", pid, start+" "+strconv.Itoa(pid), int64(10+pid), "sid-"+strconv.Itoa(pid), "/w")
			}
		}
		// a different tmux session whose name collides with nothing here, and a session with no pane at all
		for i := 0; i < 5; i++ {
			name := "s" + strconv.Itoa(i)
			got, gerr := m.projectionForSession(name)
			want, werr := m.projectionForSessionGlobal(name)
			if (gerr == nil) != (werr == nil) || !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d session %s: scoped = %+v (%v), global = %+v (%v)", seed, name, got, gerr, want, werr)
			}
		}
	}
}

func TestProjectionForSession_ScopedEqualsGlobal_FrameDeletedBetween(t *testing.T) {
	f := newProjFixture(t, 8, true)
	for _, name := range []string{"s0", "s1", "s2", "s3"} {
		before, _ := f.m.projectionForSessionGlobal(name)
		if before != nil && before.TopFrame != nil {
			if err := f.m.frames.Delete(before.TopFrame.FrameID); err != nil {
				t.Fatal(err)
			}
		}
		got, gerr := f.m.projectionForSession(name)
		want, werr := f.m.projectionForSessionGlobal(name)
		if gerr != nil || werr != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s after delete: scoped %+v (%v) global %+v (%v)", name, got, gerr, want, werr)
		}
	}
}

func TestProjectionForSession_ScopedReadsOnlyTheSessionsFrames(t *testing.T) {
	f := newProjFixture(t, 12, false)
	f.m.listFramesFn = nil
	got, err := f.m.projectionForSession("s0")
	if err != nil || got == nil {
		t.Fatalf("s0 = %+v, %v", got, err)
	}
	// 12 panes over s0..s3: s0 owns %0,%4,%8. Unknown session: no rows read, no projection.
	none, err := f.m.projectionForSession("nosuch")
	if err != nil || none != nil {
		t.Fatalf("nosuch = %+v, %v", none, err)
	}
}

func cachedFixture(t *testing.T, ttl time.Duration) *projFixture {
	f := newProjFixture(t, 4, false)
	f.m.paneSnapTTL = ttl
	return f
}

func TestPaneSnapCache_SharedWithinTTL(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	f.tx.reset()
	for i := 0; i < 5; i++ {
		if p, err := f.m.projectionForSession("s0"); err != nil || p == nil {
			t.Fatalf("read %d: %+v, %v", i, p, err)
		}
	}
	if b, _, _ := f.tx.counts(); b != 1 {
		t.Fatalf("5 reads inside the TTL listed %d times, want 1", b)
	}
}

func TestPaneSnapCache_RefreshesAfterTTL(t *testing.T) {
	f := cachedFixture(t, 20*time.Millisecond)
	f.tx.reset()
	f.m.projectionForSession("s0")
	time.Sleep(40 * time.Millisecond)
	f.m.projectionForSession("s0")
	if b, _, _ := f.tx.counts(); b != 2 {
		t.Fatalf("listings = %d, want 2 (TTL expired)", b)
	}
}

func TestPaneSnapCache_RefreshesWhenGenBumps(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	f.tx.reset()
	f.m.projectionForSession("s0")
	f.m.bumpTmuxGen()
	f.m.projectionForSession("s0")
	if b, _, _ := f.tx.counts(); b != 2 {
		t.Fatalf("listings = %d, want 2 (gen bumped)", b)
	}
}

func TestPaneSnapCache_UnknownSessionForcesOneRefresh(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	f.m.projectionForSession("s0") // fills the cache
	f.tx.SetPaneSessionName("%new", "snew")
	f.tx.SetActivePanePID("%new", "2999")
	seedIdentityFrame(t, f.m, "%new", "cc", 1999, "Sun Apr 20 01:30:00 2026", 99, "sid-new", "/w")
	f.tx.reset()
	p, err := f.m.projectionForSession("snew")
	if err != nil || p == nil {
		t.Fatalf("new session read as absent: %+v, %v", p, err)
	}
	if b, _, _ := f.tx.counts(); b != 1 {
		t.Fatalf("listings = %d, want exactly 1 forced refresh", b)
	}
}

func TestPaneSnapCache_FailureNotCached(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	f.tx.SetListPanePlacementsError(fmt.Errorf("tmux hiccup"))
	f.tx.reset()
	f.m.projectionForSession("s0")
	f.m.projectionForSession("s0")
	if b, _, _ := f.tx.counts(); b != 2 {
		t.Fatalf("failed listings = %d, want 2 (not cached)", b)
	}
	f.tx.SetListPanePlacementsError(nil)
	f.tx.reset()
	f.m.projectionForSession("s0")
	f.m.projectionForSession("s0")
	if b, _, _ := f.tx.counts(); b != 1 {
		t.Fatalf("after recovery listings = %d, want 1", b)
	}
}

func TestPaneSnapCache_SingleFlight(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	f.tx.mu.Lock()
	f.tx.onCall = func() { time.Sleep(60 * time.Millisecond) }
	f.tx.mu.Unlock()
	f.tx.reset()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p, err := f.m.projectionForSession("s0"); err != nil || p == nil {
				t.Errorf("read: %+v, %v", p, err)
			}
		}()
	}
	wg.Wait()
	if b, _, _ := f.tx.counts(); b != 1 {
		t.Fatalf("8 concurrent reads forked %d times, want 1", b)
	}
}

// A session whose only pane is linked into several sessions is found through the per-pane answer, not the listing.
func TestProjectionForSession_AmbiguousOnlyPane(t *testing.T) {
	m, tx := newScopeModule(t)
	orig := pidAncestorIncludesFn
	pidAncestorIncludesFn = func(pid, ancestor int) bool { return true }
	t.Cleanup(func() { pidAncestorIncludesFn = orig })
	tx.SetPaneSessionName("%linked", "slinked")
	tx.SetActivePanePID("%linked", "9100")
	tx.SetPaneAmbiguous("%linked", true)
	seedIdentityFrame(t, m, "%linked", "cc", 500, "Sun Apr 20 01:30:00 2026", 7, "sid-l", "/w")
	got, err := m.projectionForSession("slinked")
	want, werr := m.projectionForSessionGlobal("slinked")
	if err != nil || werr != nil || got == nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("scoped = %+v (%v), global = %+v (%v)", got, err, want, werr)
	}
	if other, _ := m.projectionForSession("other"); other != nil {
		t.Fatalf("another session got the linked pane: %+v", other)
	}
}

// A batch that fails after most of its budget leaves the per-pane fallback only what is left, cached path included.
func TestPaneSnapCache_FallbackKeepsTheReadDeadline(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	f.tx.SetListPanePlacementsError(fmt.Errorf("tmux hiccup"))
	f.tx.mu.Lock()
	f.tx.onCall = func() { time.Sleep(50 * time.Millisecond) }
	f.tx.mu.Unlock()
	before := time.Now()
	snap, _, err := f.m.cachedPaneSnapshot(false)
	if err != nil || snap == nil || !snap.fallback {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
	if limit := before.Add(paneSnapshotTimeout + 20*time.Millisecond); snap.deadline.After(limit) {
		t.Fatalf("fallback deadline %v is later than the read's own budget %v", snap.deadline, limit)
	}
	if got := before.Add(paneSnapshotTimeout - 10*time.Millisecond); !snap.deadline.Before(got.Add(60 * time.Millisecond)) {
		t.Fatalf("deadline %v was reset after the batch call", snap.deadline)
	}
}

// lateTmux answers the listing first and only then parks, so the answer predates whatever the test changes meanwhile.
type lateTmux struct {
	*batchCountingTmux
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (l *lateTmux) ListPanePlacements(ctx context.Context) (map[string]tmux.PanePlacement, error) {
	out, err := l.batchCountingTmux.ListPanePlacements(ctx)
	l.once.Do(func() { close(l.entered); <-l.release })
	return out, err
}

// A reader that joins a listing already in flight must not accept it if the layout changed after the fork began.
func TestPaneSnapCache_WaiterRejectsFlightOlderThanGenBump(t *testing.T) {
	f := cachedFixture(t, time.Hour)
	l := &lateTmux{batchCountingTmux: f.tx, entered: make(chan struct{}), release: make(chan struct{})}
	f.m.tmux = l

	var first sync.WaitGroup
	first.Add(1)
	go func() { defer first.Done(); f.m.cachedPaneSnapshot(false) }()
	<-l.entered // flight 1 has its answer and is parked

	f.tx.SetPaneSessionName("%late", "slate")
	f.tx.SetActivePanePID("%late", "2998")
	f.m.bumpTmuxGen()

	got := make(chan *paneSnapshot, 1)
	go func() { s, _, _ := f.m.cachedPaneSnapshot(false); got <- s }()
	time.Sleep(50 * time.Millisecond) // the waiter is parked on flight 1
	close(l.release)
	first.Wait()
	s := <-got
	if s == nil || s.panes == nil {
		t.Fatalf("waiter got %+v", s)
	}
	if _, ok := s.panes["%late"]; !ok {
		t.Fatalf("waiter accepted a listing from before the gen bump (no %%late pane)")
	}
}

// The owner of a listing re-lists if the layout changed while it was forking (plain and forced).
func TestPaneSnapCache_OwnerRelistsAfterGenBump(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprintf("force=%v", force), func(t *testing.T) {
			f := cachedFixture(t, time.Hour)
			l := &lateTmux{batchCountingTmux: f.tx, entered: make(chan struct{}), release: make(chan struct{})}
			f.m.tmux = l
			got := make(chan *paneSnapshot, 1)
			go func() { s, _, _ := f.m.cachedPaneSnapshot(force); got <- s }()
			<-l.entered
			f.tx.SetPaneSessionName("%late", "slate")
			f.tx.SetActivePanePID("%late", "2998")
			f.m.bumpTmuxGen()
			close(l.release)
			s := <-got
			if s == nil || s.panes == nil {
				t.Fatalf("owner got %+v", s)
			}
			if _, ok := s.panes["%late"]; !ok {
				t.Fatalf("owner returned a listing from before the gen bump")
			}
		})
	}
}

// BenchmarkEmitSlotRead is what a hook pays inside the emit slot to read its session's projection: 200 frames over 18
// sessions. "global" is the full-table read before #3092 A, "scoped" the session-scoped read with the 2 s shared listing.
// tmux-lists/op counts the `list-panes` forks (the fake costs nothing; a real one is ~27 ms).
func BenchmarkEmitSlotRead(b *testing.B) {
	for _, mode := range []string{"global", "scoped"} {
		b.Run(mode, func(b *testing.B) {
			m, tx := newScopeModuleB(b)
			orig := pidAncestorIncludesFn
			pidAncestorIncludesFn = func(pid, ancestor int) bool { return true }
			defer func() { pidAncestorIncludesFn = orig }()
			for i := 0; i < 200; i++ {
				pane := "%" + strconv.Itoa(i)
				tx.SetPaneSessionName(pane, "s"+strconv.Itoa(i%18))
				tx.SetActivePanePID(pane, strconv.Itoa(9000+i))
				if _, err := m.frames.Upsert(store.Frame{PaneID: pane, AgentType: "cc", PID: 1000 + i, PPID: 1, ProcessStartTime: "t" + strconv.Itoa(i),
					Status: agentpkg.StatusIdle, StartedAt: int64(10 + i), LastSeenAt: int64(10 + i), Verified: true, SessionID: "sid-" + strconv.Itoa(i), Cwd: "/w"}); err != nil {
					b.Fatal(err)
				}
			}
			if mode == "scoped" {
				m.paneSnapTTL = defaultPaneSnapTTL
			}
			tx.reset()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				name := "s" + strconv.Itoa(i%18)
				var p *SessionProjection
				var err error
				if mode == "global" {
					p, err = m.projectionForSessionGlobal(name)
				} else {
					p, err = m.projectionForSession(name)
				}
				if err != nil || p == nil {
					b.Fatalf("%s: %+v, %v", name, p, err)
				}
			}
			batch, _, _ := tx.counts()
			b.ReportMetric(float64(batch)/float64(b.N), "tmux-lists/op")
		})
	}
}

func newScopeModuleB(b *testing.B) (*Module, *batchCountingTmux) {
	events, err := store.OpenAgentEvent(":memory:")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { events.Close() })
	m, err := New(events)
	if err != nil {
		b.Fatal(err)
	}
	m.registry = agentpkg.NewRegistry()
	fake := tmux.NewFakeExecutor()
	tx := &batchCountingTmux{FakeExecutor: fake}
	m.tmux = tx
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{{Code: "c0", Name: "s0"}}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}
	return m, tx
}
