package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"strings"
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

func (c *batchCountingTmux) ActivePanePIDCtx(ctx context.Context, target string) (string, error) {
	c.note(&c.pid)
	return c.FakeExecutor.ActivePanePIDCtx(ctx, target)
}

func (c *batchCountingTmux) PaneSessionNameCtx(ctx context.Context, target string) (string, error) {
	c.note(&c.session)
	return c.FakeExecutor.PaneSessionNameCtx(ctx, target)
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
	if _, err := f.m.liveSessionProjections(); !errors.Is(err, errPaneSnapshotTimeout) {
		t.Fatalf("read after a batch timeout: err = %v, want errPaneSnapshotTimeout", err)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("read took %v after a batch timeout, want about the timeout", d)
	}
	if b, pid, sess := f.tx.counts(); b != 1 || pid != 0 || sess != 0 {
		t.Fatalf("calls batch=%d pid=%d session=%d, want 1/0/0", b, pid, sess)
	}
}

// ---- #2039: the per-pane fallback has a deadline ----

// stuckPaneTmux is a tmux whose batch call FAILS (not a timeout: the fallback case) and whose per-pane lookups hang
// until their context ends, like a tmux that stopped answering mid-read. The unbounded variants hang for good: a read that
// still reaches them never comes back.
type stuckPaneTmux struct{ *batchCountingTmux }

func (s stuckPaneTmux) ListPanePlacements(ctx context.Context) (map[string]tmux.PanePlacement, error) {
	s.note(&s.batch)
	return nil, errors.New("tmux: list-panes: exit status 1")
}

func (s stuckPaneTmux) ActivePanePIDCtx(ctx context.Context, _ string) (string, error) {
	s.note(&s.pid)
	<-ctx.Done()
	return "", ctx.Err()
}

func (s stuckPaneTmux) PaneSessionNameCtx(ctx context.Context, _ string) (string, error) {
	s.note(&s.session)
	<-ctx.Done()
	return "", ctx.Err()
}

func (s stuckPaneTmux) ActivePanePID(string) (string, error)   { select {} }
func (s stuckPaneTmux) PaneSessionName(string) (string, error) { select {} }

func shortLookupBudget(t *testing.T, d time.Duration) {
	t.Helper()
	orig := paneSnapshotTimeout
	paneSnapshotTimeout = d
	t.Cleanup(func() { paneSnapshotTimeout = orig })
}

// within fails the test when fn has not returned in d: a read stuck on a hung tmux never comes back, so it is run aside.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %v", what, d)
	}
}

// A batch that fails without timing out falls back to per-pane lookups; a tmux that hangs there must cost the read ONE
// budget, not one per pane, and not forever. A lookup that ran out of time is "unreadable", not "pane gone": the read FAILS
// as a timed-out batch does, so no empty answer is broadcast as a clear (#717). Mutations: the lookups back on the
// unbounded variants → the read hangs (red); a budget per lookup instead of per read → 30 × 40 ms overruns (red).
func TestProjectionRead_PerPaneFallbackIsBoundedByOneBudget(t *testing.T) {
	f := newProjFixture(t, 30, false)
	shortLookupBudget(t, 40*time.Millisecond)
	f.m.tmux = stuckPaneTmux{f.tx}
	start := time.Now()
	within(t, 3*time.Second, "a projection read over a hung tmux", func() {
		if _, err := f.m.liveSessionProjections(); !errors.Is(err, errPaneSnapshotTimeout) {
			t.Errorf("a fallback lookup that timed out: err = %v, want errPaneSnapshotTimeout (an unreadable read, never an empty one: #717)", err)
		}
	})
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("read took %v, want about one budget (40ms) for the whole fallback, not one per pane", d)
	}
}

// Single lookups outside a snapshot (an event's pane, the hook verifier) are bounded too, each by the budget.
func TestPaneLookups_WithoutASnapshotAreBounded(t *testing.T) {
	f := newProjFixture(t, 2, false)
	shortLookupBudget(t, 40*time.Millisecond)
	f.m.tmux = stuckPaneTmux{f.tx}
	within(t, 3*time.Second, "paneSessionName", func() {
		if got := f.m.paneSessionName("%0"); got != "" {
			t.Errorf("paneSessionName = %q, want \"\" (not found)", got)
		}
	})
	within(t, 3*time.Second, "resolvePaneSession", func() {
		if name, code := f.m.resolvePaneSession("%0"); name != "" || code != "" {
			t.Errorf("resolvePaneSession = %q, %q", name, code)
		}
	})
	within(t, 3*time.Second, "resolvePanePID", func() {
		if _, err := resolvePanePID(f.m.tmux, "%0"); err == nil {
			t.Error("resolvePanePID answered for a hung tmux")
		}
	})
}

// The replay cache's per-pane name lookups share one budget for the whole round.
func TestReplayCache_PaneNameLookupsShareOneBudget(t *testing.T) {
	f := newProjFixture(t, 30, false)
	shortLookupBudget(t, 40*time.Millisecond)
	f.m.tmux = stuckPaneTmux{f.tx}
	rc := &replayProjectionCache{}
	start := time.Now()
	within(t, 3*time.Second, "a replay round over a hung tmux", func() {
		for _, s := range []string{"s0", "s1", "s2", "s3"} {
			if _, err := f.m.projectionForSessionWith(s, rc); !errors.Is(err, errPaneSnapshotTimeout) {
				t.Errorf("projectionForSessionWith(%s): err = %v, want errPaneSnapshotTimeout", s, err)
			}
		}
	})
	if d := time.Since(start); d > 400*time.Millisecond {
		t.Fatalf("round took %v, want about one budget", d)
	}
}

// One line, not one per pane, when lookups run out of time.
func TestPerPaneFallback_DeadlineIsLoggedOnce(t *testing.T) {
	f := newProjFixture(t, 30, false)
	shortLookupBudget(t, 20*time.Millisecond)
	f.m.tmux = stuckPaneTmux{f.tx}
	lookupDeadlineLastLog.Store(0)
	lookupDeadlineSuppressed.Store(0)
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	within(t, 3*time.Second, "the read", func() { _, _ = f.m.liveSessionProjections() })
	if n := strings.Count(buf.String(), "ran out of time"); n != 1 {
		t.Fatalf("%d deadline lines for one read, want 1:\n%s", n, buf.String())
	}
}

// A read whose batch call fails only near the end of its budget has little left for the per-pane lookups: batch and
// fallback share ONE deadline, they do not add up (codex attack). Mutation: a fresh budget for the fallback → the read takes
// about twice the budget → red.
type slowFailBatchTmux struct {
	stuckPaneTmux
	after time.Duration
}

func (s slowFailBatchTmux) ListPanePlacements(ctx context.Context) (map[string]tmux.PanePlacement, error) {
	s.note(&s.batch)
	time.Sleep(s.after) // fails on its own, a little before the context would end it
	return nil, errors.New("tmux: list-panes: exit status 1")
}

func TestProjectionRead_BatchAndFallbackShareOneBudget(t *testing.T) {
	f := newProjFixture(t, 10, false)
	shortLookupBudget(t, 100*time.Millisecond)
	f.m.tmux = slowFailBatchTmux{stuckPaneTmux{f.tx}, 80 * time.Millisecond}
	start := time.Now()
	within(t, 3*time.Second, "the read", func() {
		if _, err := f.m.liveSessionProjections(); !errors.Is(err, errPaneSnapshotTimeout) {
			t.Errorf("err = %v, want errPaneSnapshotTimeout", err)
		}
	})
	if d := time.Since(start); d > 170*time.Millisecond {
		t.Fatalf("read took %v, want about one budget (100ms): the batch's 80ms plus the fallback must not add a second budget", d)
	}
}

// A mod round whose name lookups ran out of time keeps every update it took: the dirty set comes back (codex attack:
// before, the sids were dropped and the update lost until another event marked them).
func TestModWorker_LookupTimeoutKeepsTheDirtyUpdates(t *testing.T) {
	r := newWorkerRig(t)
	seedIdentityFrame(t, r.m, "%5", "cc", 501, "s501", 10, modSID1, "/w")
	feedMod(r.m, modStrm, modStart, modTurnStart)
	shortLookupBudget(t, 40*time.Millisecond)
	r.m.tmux = stuckPaneTmux{&batchCountingTmux{FakeExecutor: tmux.NewFakeExecutor()}}
	within(t, 3*time.Second, "the mod round", func() { r.round() })
	if got := r.drain(t); len(got) != 0 {
		t.Fatalf("a round whose lookups timed out emitted: %+v", got)
	}
	r.m.modMu.Lock()
	_, back := r.m.modDirty[modSID1]
	r.m.modMu.Unlock()
	if !back {
		t.Fatal("the sid was dropped by a round whose name lookups timed out; it must come back dirty for the next round")
	}
}

// Each stage of the fallback fails the read on its own: a tmux that answers the pid lookups but hangs on the names (and the
// other way round) is still an unreadable read, never an empty or partial one (#717). Mutations: ignoring expiry after the pid
// stage, or after the name stage, each → the matching case green-lights an incomplete read → red.
type namesOnlyStuckTmux struct{ stuckPaneTmux }

func (s namesOnlyStuckTmux) ActivePanePIDCtx(ctx context.Context, target string) (string, error) {
	return s.FakeExecutor.ActivePanePIDCtx(ctx, target)
}

type pidsOnlyStuckTmux struct{ stuckPaneTmux }

func (s pidsOnlyStuckTmux) PaneSessionNameCtx(ctx context.Context, target string) (string, error) {
	return s.FakeExecutor.PaneSessionNameCtx(ctx, target)
}

func TestProjectionRead_EitherFallbackStageTimingOutFailsTheRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(stuckPaneTmux) tmux.Executor
	}{
		{"names hang, pids answer", func(s stuckPaneTmux) tmux.Executor { return namesOnlyStuckTmux{s} }},
		{"pids hang, names answer", func(s stuckPaneTmux) tmux.Executor { return pidsOnlyStuckTmux{s} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProjFixture(t, 6, false)
			shortLookupBudget(t, 40*time.Millisecond)
			f.m.tmux = tc.wrap(stuckPaneTmux{f.tx})
			within(t, 3*time.Second, "the read", func() {
				if _, err := f.m.liveSessionProjections(); !errors.Is(err, errPaneSnapshotTimeout) {
					t.Errorf("err = %v, want errPaneSnapshotTimeout", err)
				}
				if _, err := f.m.projectionForSession("s0"); !errors.Is(err, errPaneSnapshotTimeout) {
					t.Errorf("projectionForSession: err = %v, want errPaneSnapshotTimeout", err)
				}
				// the replay round reads the frames through its own cache: the pid stage's expiry must reach it too
				if _, err := f.m.projectionForSessionWith("s0", &replayProjectionCache{}); !errors.Is(err, errPaneSnapshotTimeout) {
					t.Errorf("projectionForSessionWith: err = %v, want errPaneSnapshotTimeout", err)
				}
			})
		})
	}
}
