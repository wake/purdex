package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// batchCountingTmux counts the tmux round trips a projection read makes, split by
// kind, and runs onCall for each (a test's stand-in for the ~27 ms a tmux
// child costs).
type batchCountingTmux struct {
	*tmux.FakeExecutor
	mu                  sync.Mutex
	batch, pid, session int
	onCall              func()
}

func (c *batchCountingTmux) note(kind *int) {
	c.mu.Lock()
	*kind++
	f := c.onCall
	c.mu.Unlock()
	if f != nil {
		f()
	}
}

func (c *batchCountingTmux) ListPanePlacements(ctx context.Context) (map[string]tmux.PanePlacement, error) {
	c.note(&c.batch)
	return c.FakeExecutor.ListPanePlacements(ctx)
}

func (c *batchCountingTmux) ActivePanePID(target string) (string, error) {
	c.note(&c.pid)
	return c.FakeExecutor.ActivePanePID(target)
}

func (c *batchCountingTmux) PaneSessionName(target string) (string, error) {
	c.note(&c.session)
	return c.FakeExecutor.PaneSessionName(target)
}

func (c *batchCountingTmux) counts() (batch, pid, session int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.batch, c.pid, c.session
}

func (c *batchCountingTmux) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batch, c.pid, c.session = 0, 0, 0
}

type projFixture struct {
	m  *Module
	tx *batchCountingTmux
}

// newProjFixture seeds n panes "%0".."%n-1", each with one live frame, spread
// over the sessions s0..s3. Pane i's frame belongs to pid 1000+i and the
// pane's own process is pid 2000+i, which pidAncestorIncludesFn (stubbed here)
// accepts as an ancestor. extras adds the cases a projection read has to get
// right: a pane whose process is not the frame's ancestor (its frame is
// filtered out), a pane tmux no longer knows (nothing configured: unfiltered
// and nameless, exactly as a failed per-pane lookup is), and a pane linked
// into two sessions the listing cannot tell apart (Ambiguous).
func newProjFixture(t *testing.T, n int, extras bool) *projFixture {
	t.Helper()
	m := newTestModule(t)
	fake := tmux.NewFakeExecutor()
	tx := &batchCountingTmux{FakeExecutor: fake}
	m.tmux = tx
	m.sessions = &fakeSessionProvider{sessions: []session.SessionInfo{
		{Code: "c0", Name: "s0"}, {Code: "c1", Name: "s1"}, {Code: "c2", Name: "s2"}, {Code: "c3", Name: "s3"},
	}}
	m.core = &core.Core{Events: core.NewEventsBroadcaster(), Tmux: fake}
	sub := m.core.Events.AddTestSubscriber()
	t.Cleanup(func() { m.core.Events.RemoveTestSubscriber(sub) })

	orig := pidAncestorIncludesFn
	pidAncestorIncludesFn = func(pid, ancestor int) bool { return ancestor == pid+1000 }
	t.Cleanup(func() { pidAncestorIncludesFn = orig })

	const start = "Sun Apr 20 01:30:00 2026"
	seed := func(i int, pane string) {
		seedIdentityFrame(t, m, pane, "cc", 1000+i, start, int64(10+i), "sid-"+strconv.Itoa(i), "/w")
	}
	for i := 0; i < n; i++ {
		pane := "%" + strconv.Itoa(i)
		fake.SetPaneSessionName(pane, "s"+strconv.Itoa(i%4))
		fake.SetActivePanePID(pane, strconv.Itoa(2000+i))
		seed(i, pane)
	}
	if extras {
		// %foreign: its process is not the frame's ancestor.
		fake.SetPaneSessionName("%foreign", "s1")
		fake.SetActivePanePID("%foreign", "9999")
		seed(500, "%foreign")
		// %gone: tmux knows nothing about it.
		seed(501, "%gone")
		// %linked: a linked window; the listing cannot say s2 or s3 owns it,
		// the per-pane answer (s3) is what must be used.
		fake.SetPaneSessionName("%linked", "s3")
		fake.SetActivePanePID("%linked", strconv.Itoa(2000+502))
		fake.SetPaneAmbiguous("%linked", true)
		seed(502, "%linked")
	}
	return &projFixture{m: m, tx: tx}
}

func TestProjectionRead_OneTmuxCallRegardlessOfPaneCount(t *testing.T) {
	for _, n := range []int{1, 5, 30} {
		t.Run(fmt.Sprintf("%d panes", n), func(t *testing.T) {
			f := newProjFixture(t, n, false)

			f.tx.reset()
			p, err := f.m.projectionForSession("s0")
			if err != nil || p == nil || p.TopFrame == nil {
				t.Fatalf("projectionForSession(s0) = %+v, %v", p, err)
			}
			if b, pid, sess := f.tx.counts(); b != 1 || pid != 0 || sess != 0 {
				t.Fatalf("projectionForSession: tmux calls batch=%d per-pane pid=%d per-pane name=%d, want 1/0/0", b, pid, sess)
			}

			f.tx.reset()
			named, err := f.m.liveSessionProjections()
			if err != nil || len(named) == 0 {
				t.Fatalf("liveSessionProjections = %+v, %v", named, err)
			}
			if b, pid, sess := f.tx.counts(); b != 1 || pid != 0 || sess != 0 {
				t.Fatalf("liveSessionProjections: tmux calls batch=%d per-pane pid=%d per-pane name=%d, want 1/0/0", b, pid, sess)
			}
		})
	}
}

func TestProjectionRead_FallsBackPerPaneWhenBatchFails(t *testing.T) {
	const n = 6
	f := newProjFixture(t, n, false)
	want, err := f.m.projectionForSession("s0")
	if err != nil || want == nil {
		t.Fatalf("batch read: %+v, %v", want, err)
	}
	wantNamed, err := f.m.liveSessionProjections()
	if err != nil {
		t.Fatal(err)
	}

	f.tx.SetListPanePlacementsError(errors.New("tmux gone"))
	out := captureSlotLog(t)
	batchFailLastLog.Store(0)

	f.tx.reset()
	got, err := f.m.projectionForSession("s0")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback projection = %+v, want %+v", got, want)
	}
	if b, pid, sess := f.tx.counts(); b != 1 || pid != n || sess == 0 {
		t.Fatalf("fallback calls batch=%d pid=%d name=%d, want 1 batch, %d per-pane pid lookups and per-pane names", b, pid, sess, n)
	}
	gotNamed, err := f.m.liveSessionProjections()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotNamed, wantNamed) {
		t.Fatalf("fallback liveSessionProjections = %+v, want %+v", gotNamed, wantNamed)
	}
	// Logged once, not once per read: the next reads are inside the window.
	if _, err := f.m.projectionForSession("s1"); err != nil {
		t.Fatal(err)
	}
	if got := lines(out.String(), "pane snapshot unavailable"); got != 1 {
		t.Fatalf("%d fallback log lines over three failing reads, want 1:\n%s", got, out.String())
	}
}

// The batch path and the per-pane path must agree on everything a read
// produces, including the awkward panes.
func TestProjectionRead_SameResultAsPerPaneLookup(t *testing.T) {
	f := newProjFixture(t, 12, true)
	sessions := []string{"s0", "s1", "s2", "s3", "nope"}

	read := func() (map[string]*SessionProjection, []namedProjection) {
		single := make(map[string]*SessionProjection)
		for _, s := range sessions {
			p, err := f.m.projectionForSession(s)
			if err != nil {
				t.Fatal(err)
			}
			single[s] = p
		}
		all, err := f.m.liveSessionProjections()
		if err != nil {
			t.Fatal(err)
		}
		return single, all
	}

	f.tx.reset()
	batchSingle, batchAll := read()
	if b, _, _ := f.tx.counts(); b != len(sessions)+1 {
		t.Fatalf("batch path made %d batch calls for %d reads", b, len(sessions)+1)
	}

	f.tx.SetListPanePlacementsError(errors.New("tmux gone"))
	perPaneSingle, perPaneAll := read()

	for _, s := range sessions {
		if !reflect.DeepEqual(batchSingle[s], perPaneSingle[s]) {
			t.Errorf("projectionForSession(%s): batch %+v, per-pane %+v", s, batchSingle[s], perPaneSingle[s])
		}
	}
	if !reflect.DeepEqual(batchAll, perPaneAll) {
		t.Errorf("liveSessionProjections: batch %+v, per-pane %+v", batchAll, perPaneAll)
	}
	// The read has to have seen the awkward panes, or the comparison proves nothing.
	if batchSingle["s3"] == nil || batchSingle["s3"].PaneID == "" {
		t.Fatalf("s3 has no projection: %+v", batchSingle["s3"])
	}
	for _, np := range batchAll {
		if np.Projection.PaneID == "%foreign" || np.Projection.PaneID == "%gone" {
			t.Errorf("%s represents %s", np.SessionName, np.Projection.PaneID)
		}
	}
}

// A linked pane the listing cannot place costs one per-pane name lookup, for
// that pane only.
func TestProjectionRead_AmbiguousPaneAsksTmuxForThatPaneOnly(t *testing.T) {
	f := newProjFixture(t, 8, true)
	f.tx.reset()
	if _, err := f.m.liveSessionProjections(); err != nil {
		t.Fatal(err)
	}
	if b, pid, sess := f.tx.counts(); b != 1 || pid != 0 || sess != 1 {
		t.Fatalf("calls batch=%d pid=%d name=%d, want 1/0/1 (the one ambiguous pane)", b, pid, sess)
	}
}

// tickClock is a hold clock the fake tmux advances: now never moves by itself.
type tickClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *tickClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *tickClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// TestEmitSlotHold_ThirtyPanesStaysUnder50ms: 610 held the emit slot for
// 0.6-1.5 s because a projection read cost two tmux children per pane. With
// each tmux call costing 27 ms (a fake clock the fake tmux advances), 30 panes
// cost one call in the slot now, and the old per-pane path costs well over a
// second.
func TestEmitSlotHold_ThirtyPanesStaysUnder50ms(t *testing.T) {
	const perCall = 27 * time.Millisecond
	f := newProjFixture(t, 30, false)
	clk := &tickClock{t: time.Unix(1_000_000, 0)}
	f.m.emit.hold.clock = clk.now
	f.tx.onCall = func() { clk.advance(perCall) }
	captureSlotLog(t)

	if !f.m.emitSession(kindHook, "c0", "s0", plainBuild) {
		t.Fatal("emit did not go out")
	}
	if got := holdBuckets(f.m); got != [4]uint64{0, 1, 0, 0} {
		t.Fatalf("buckets = %v, want the one 27ms hold in the <50ms bucket", got)
	}
	if mx := time.Duration(f.m.emit.hold.maxNs.Load()); mx != perCall {
		t.Fatalf("hold = %s, want one tmux call (%s)", mx, perCall)
	}

	// Control: the per-pane path (batch unavailable) is the >1 s hold of 610,
	// so the assertion above is not satisfied by a clock that never moves.
	f.tx.SetListPanePlacementsError(errors.New("tmux gone"))
	if !f.m.emitSession(kindHook, "c0", "s0", plainBuild) {
		t.Fatal("emit did not go out")
	}
	if got := holdBuckets(f.m); got != [4]uint64{0, 1, 0, 1} {
		t.Fatalf("buckets after the per-pane read = %v, want a hold in the >=250ms bucket", got)
	}
	if mx := time.Duration(f.m.emit.hold.maxNs.Load()); mx < time.Second {
		t.Fatalf("per-pane hold = %s, want over a second for 30 panes", mx)
	}
}

// stuckBatchTmux's batch call blocks until its context ends, like a hung tmux.
type stuckBatchTmux struct{ *batchCountingTmux }

func (s stuckBatchTmux) ListPanePlacements(ctx context.Context) (map[string]tmux.PanePlacement, error) {
	s.note(&s.batch)
	<-ctx.Done()
	return nil, ctx.Err()
}

// A batch call that times out must not send the read on to per-pane calls: they
// have no deadline, so a stuck tmux would hold the emit slot indefinitely.
func TestProjectionRead_BatchTimeoutDoesNotFallBackPerPane(t *testing.T) {
	f := newProjFixture(t, 30, false)
	orig := paneSnapshotTimeout
	paneSnapshotTimeout = 50 * time.Millisecond
	t.Cleanup(func() { paneSnapshotTimeout = orig })
	f.m.tmux = stuckBatchTmux{f.tx}
	start := time.Now()
	if _, err := f.m.liveSessionProjections(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("read took %v after a batch timeout, want about the timeout", d)
	}
	if b, pid, sess := f.tx.counts(); b != 1 || pid != 0 || sess != 0 {
		t.Fatalf("calls batch=%d pid=%d session=%d, want 1/0/0", b, pid, sess)
	}
}
