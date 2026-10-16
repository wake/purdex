package fleetmod

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/fleet"
)

// ---- fakes ----

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)} }
func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type fakeSettings struct {
	mu    sync.Mutex
	set   Settings
	rev   int64
	reads int
}

func (f *fakeSettings) Rev() (int64, error) { f.mu.Lock(); defer f.mu.Unlock(); return f.rev, nil }
func (f *fakeSettings) Read() (Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.set, nil
}
func (f *fakeSettings) change(fn func(*Settings)) {
	f.mu.Lock()
	fn(&f.set)
	f.rev++
	f.mu.Unlock()
}

// link is a Peer that calls straight into the other engine; down makes it unreachable.
type link struct {
	mu        sync.Mutex
	to        *Engine
	down      bool
	announced []int64
	from      string
}

func (l *link) Role(_ context.Context, req RoleRequest) (RoleAnswer, error) {
	l.mu.Lock()
	down, to := l.down, l.to
	l.mu.Unlock()
	if down || to == nil {
		return RoleAnswer{}, errors.New("unreachable")
	}
	return to.HandleRole(req), nil
}
func (l *link) Announce(_ context.Context, term int64) error {
	l.mu.Lock()
	down, to := l.down, l.to
	l.mu.Unlock()
	if down || to == nil {
		return errors.New("unreachable")
	}
	l.mu.Lock()
	l.announced = append(l.announced, term)
	l.mu.Unlock()
	to.HandleAnnounce(l.from, term)
	return nil
}
func (l *link) setDown(d bool) { l.mu.Lock(); l.down = d; l.mu.Unlock() }
func (l *link) count() int     { l.mu.Lock(); defer l.mu.Unlock(); return len(l.announced) }

type pair struct {
	clk    *clock
	a, b   *Engine // a = air26 (first in order), b = mlab
	ab, ba *link   // a's peer (to b), b's peer (to a)
	as, bs *fakeSettings
}

func newPair(t *testing.T) *pair {
	t.Helper()
	clk := newClock()
	order := []string{"air26", "mlab"}
	p := &pair{clk: clk}
	p.as = &fakeSettings{set: Settings{Order: order, Enabled: true, Self: "air26"}}
	p.bs = &fakeSettings{set: Settings{Order: order, Enabled: true, Self: "mlab"}}
	p.ab, p.ba = &link{from: "air26"}, &link{from: "mlab"}
	p.a = NewEngine(p.ab, p.as, clk.Now, nil, "", "host-a")
	p.b = NewEngine(p.ba, p.bs, clk.Now, nil, "", "host-b")
	p.ab.to, p.ba.to = p.b, p.a
	return p
}

func ctx() context.Context { return context.Background() }

func age(d time.Duration) func() (time.Duration, bool) {
	return func() (time.Duration, bool) { return d, true }
}

// ---- start ----

func TestStart_OtherUnreachable_PrimaryThenAnnounceOnceOtherAnswers(t *testing.T) {
	p := newPair(t)
	p.ab.setDown(true)
	p.a.Startup(ctx())
	require.Equal(t, fleet.RolePrimary, p.a.State().Role)
	require.Equal(t, int64(1), p.a.State().Term)

	p.a.Beat(ctx()) // still down: nothing announced
	require.Equal(t, 0, p.ab.count())
	p.ab.setDown(false)
	p.b.Startup(ctx())
	p.a.Beat(ctx())
	require.Equal(t, 1, p.ab.count(), "one announcement once the other answered")
	p.a.Beat(ctx())
	require.Equal(t, 1, p.ab.count(), "and only one")
}

func TestStart_OtherIsPrimary_StandbyAdoptsTerm(t *testing.T) {
	p := newPair(t)
	p.b.Startup(ctx()) // b: a unreachable? no, a is not started -> a answers standby term 0, b is later in order => standby
	require.Equal(t, fleet.RoleStandby, p.b.State().Role)
	p.ba.setDown(true)
	p.b.Beat(ctx()) // unreachable standby takes over
	require.Equal(t, fleet.RolePrimary, p.b.State().Role)
	hi := p.b.State().Term
	p.ba.setDown(false)
	p.a.Startup(ctx())
	st := p.a.State()
	require.Equal(t, fleet.RoleStandby, st.Role, "air26 is first in order but a primary exists")
	require.Equal(t, hi, st.Term)
	require.Equal(t, "mlab", st.Primary)
}

func TestStart_OtherStandby_FirstInOrderBecomesPrimary(t *testing.T) {
	p := newPair(t)
	p.b.Startup(ctx()) // a answers standby (never started), b later in order -> standby
	require.Equal(t, fleet.RoleStandby, p.b.State().Role)
	p.a.Startup(ctx()) // b standby: a is first -> primary term max+1
	require.Equal(t, fleet.RolePrimary, p.a.State().Role)
	require.Equal(t, int64(1), p.a.State().Term)
}

func TestStart_PersistedTermIsRespected(t *testing.T) {
	p := newPair(t)
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, saveState(path, persisted{Role: "primary", Term: 9}))
	p.ab.setDown(true)
	e := NewEngine(p.ab, p.as, p.clk.Now, nil, path, "host-a")
	e.Startup(ctx())
	require.Equal(t, int64(10), e.State().Term)
	require.Equal(t, int64(10), loadState(path).Term, "written on every change")
}

// ---- heartbeat, liveness, takeover ----

// primaryWithStandby makes a primary (air26) and a standby (mlab) the way a normal start does.
func primaryWithStandby(t *testing.T) *pair {
	p := newPair(t)
	p.b.Startup(ctx())
	p.a.Startup(ctx())
	require.Equal(t, fleet.RolePrimary, p.a.State().Role)
	p.b.Beat(ctx())
	require.Equal(t, fleet.RoleStandby, p.b.State().Role)
	return p
}

func TestHeartbeat_AliveSilentAndStale(t *testing.T) {
	p := primaryWithStandby(t)
	p.a.SetNewestAge(age(2 * time.Second))
	p.b.Beat(ctx())
	require.Equal(t, fleet.RoleStandby, p.b.State().Role, "answers and fresh: alive")

	p.a.SetNewestAge(age(10 * time.Second))
	p.b.Beat(ctx())
	require.Equal(t, fleet.RoleStandby, p.b.State().Role, "10 s is still alive")

	n := p.ba.count()
	p.a.SetNewestAge(age(11 * time.Second)) // stuck in paging: answers but not fresh
	p.b.Beat(ctx())
	require.Equal(t, fleet.RolePrimary, p.b.State().Role)
	require.Equal(t, int64(2), p.b.State().Term, "highest seen + 1")
	require.Equal(t, n+1, p.ba.count(), "one announcement")
	// a heard the announcement: it is standby now
	require.Equal(t, fleet.RoleStandby, p.a.State().Role)
}

func TestHeartbeat_SilentPrimaryIsTakenOver(t *testing.T) {
	p := primaryWithStandby(t)
	p.a.SetNewestAge(age(time.Second))
	p.ba.setDown(true)
	p.b.Beat(ctx())
	require.Equal(t, fleet.RolePrimary, p.b.State().Role)
	require.Equal(t, int64(2), p.b.State().Term)
}

func TestHeartbeat_NullAgeIsAliveNoMatterHowLong(t *testing.T) {
	p := primaryWithStandby(t) // a has no newestAge: null
	p.clk.Add(10 * time.Minute)
	p.b.Beat(ctx())
	require.Equal(t, fleet.RoleStandby, p.b.State().Role, "nothing measurable: a takeover would not help")
	require.Equal(t, fleet.RolePrimary, p.a.State().Role)
}

func TestHeartbeat_NoSourcesAnywhere_TenBeatsNeverFlip(t *testing.T) {
	p := primaryWithStandby(t) // neither engine has a newestAge
	term := p.a.State().Term
	for i := 0; i < 10; i++ {
		p.clk.Add(HeartbeatEvery * 3)
		p.b.Beat(ctx())
		p.a.Beat(ctx())
		require.Equal(t, fleet.RolePrimary, p.a.State().Role, "beat %d", i)
		require.Equal(t, fleet.RoleStandby, p.b.State().Role, "beat %d", i)
	}
	require.Equal(t, term, p.a.State().Term, "no term bump")
}

func TestHeartbeat_FreshRemoteReadingsKeepAPrimaryWithoutOwnSource(t *testing.T) {
	p := primaryWithStandby(t)
	// the primary has no own-host source, but its newest reading of any host (an SSH one) is fresh
	p.a.SetNewestAge(age(1500 * time.Millisecond))
	for i := 0; i < 5; i++ {
		p.clk.Add(HeartbeatEvery)
		p.b.Beat(ctx())
	}
	require.Equal(t, fleet.RoleStandby, p.b.State().Role, "not taken over after 150 s")
	require.Equal(t, fleet.RolePrimary, p.a.State().Role)
}

// ---- two primaries ----

func TestPartition_TwoPrimariesConvergeInOneHeartbeat(t *testing.T) {
	p := newPair(t)
	p.ab.setDown(true)
	p.ba.setDown(true)
	p.a.Startup(ctx())
	p.b.Startup(ctx())
	require.Equal(t, fleet.RolePrimary, p.a.State().Role)
	require.Equal(t, fleet.RolePrimary, p.b.State().Role)

	p.ab.setDown(false)
	p.ba.setDown(false)
	p.a.Beat(ctx()) // one heartbeat round: both beat
	p.b.Beat(ctx())
	roles := []string{p.a.State().Role, p.b.State().Role}
	require.ElementsMatch(t, []string{fleet.RolePrimary, fleet.RoleStandby}, roles)
	require.Equal(t, fleet.RolePrimary, p.a.State().Role, "equal terms: the later in order (mlab) steps down")
}

func TestPartition_HigherTermWinsRegardlessOfOrder(t *testing.T) {
	p := newPair(t)
	p.ab.setDown(true)
	p.ba.setDown(true)
	p.a.Startup(ctx())
	p.b.Startup(ctx())
	// b has seen more takeovers
	p.b.mu.Lock()
	p.b.term = 5
	p.b.mu.Unlock()
	p.ab.setDown(false)
	p.ba.setDown(false)
	p.a.Beat(ctx())
	require.Equal(t, fleet.RoleStandby, p.a.State().Role, "the higher term wins even from the later host")
	require.Equal(t, int64(5), p.a.State().Term)
	require.Equal(t, "mlab", p.a.State().Primary)
}

func TestEqualTerms_BothPrimary_LaterInOrderStepsDown(t *testing.T) {
	p := newPair(t)
	for _, e := range []*Engine{p.a, p.b} {
		e.refresh()
		e.mu.Lock()
		e.role, e.term = fleet.RolePrimary, 3
		e.mu.Unlock()
	}
	p.b.Beat(ctx())
	require.Equal(t, fleet.RoleStandby, p.b.State().Role)
	require.Equal(t, fleet.RolePrimary, p.a.State().Role)
}

func TestRecoveredOldPrimary_BecomesStandbyNoSwitchBack(t *testing.T) {
	p := primaryWithStandby(t)
	p.a.SetNewestAge(age(30 * time.Second))
	p.b.Beat(ctx()) // takeover; the announcement already demotes a
	require.Equal(t, fleet.RolePrimary, p.b.State().Role)
	// the announcement was lost: a still believes it is primary
	p.a.mu.Lock()
	p.a.role, p.a.term = fleet.RolePrimary, 1
	p.a.mu.Unlock()
	p.a.Beat(ctx())
	require.Equal(t, fleet.RoleStandby, p.a.State().Role, "a primary with a higher term exists")
	require.Equal(t, fleet.RolePrimary, p.b.State().Role, "no switch back by order")
	require.Equal(t, int64(2), p.a.State().Term)
}

// ---- off ----

func TestOff_OwnAliasMissingOrEmptyOrDisabled(t *testing.T) {
	for name, fn := range map[string]func(*Settings){
		"not in order": func(s *Settings) { s.Self = "other" },
		"empty":        func(s *Settings) { s.Self = "" },
		"disabled":     func(s *Settings) { s.Enabled = false },
	} {
		t.Run(name, func(t *testing.T) {
			p := newPair(t)
			p.as.change(fn)
			p.ab.setDown(true)
			p.a.Startup(ctx())
			st := p.a.State()
			require.Equal(t, fleet.RoleOff, st.Role)
			require.NotEmpty(t, st.Reason)
			require.Equal(t, fleet.RoleOff, p.a.HandleRole(RoleRequest{FromAlias: "mlab", Role: fleet.RolePrimary, Term: 4}).Role)
			require.Equal(t, fleet.RoleOff, p.a.State().Role, "never becomes primary")
		})
	}
}

func TestOff_PrimaryDisabledStopsAtOnceAndStandbyTakesOverNextBeat(t *testing.T) {
	p := primaryWithStandby(t)
	p.a.SetNewestAge(age(time.Second))
	p.as.change(func(s *Settings) { s.Enabled = false })
	p.a.checkOff() // the settings poll, not the 30 s heartbeat
	require.Equal(t, fleet.RoleOff, p.a.State().Role)
	p.b.Beat(ctx())
	require.Equal(t, fleet.RolePrimary, p.b.State().Role, "the off answer counts as dead")
	// switched on again: the start procedure finds a primary
	p.as.change(func(s *Settings) { s.Enabled = true })
	p.a.Beat(ctx())
	require.Equal(t, fleet.RoleStandby, p.a.State().Role)
}

func TestSettings_ReadOnlyWhenRevisionMoves(t *testing.T) {
	p := primaryWithStandby(t)
	before := p.as.reads
	for i := 0; i < 10; i++ {
		p.a.Beat(ctx())
		p.a.checkOff()
		p.a.HandleRole(RoleRequest{FromAlias: "mlab", Role: fleet.RoleStandby, Term: 1})
	}
	require.Equal(t, before, p.as.reads, "no re-read while the revision stands")
	p.as.change(func(s *Settings) { s.Order = []string{"air26", "mlab"} })
	p.a.Beat(ctx())
	require.Equal(t, before+1, p.as.reads)
}

// ---- order mismatch warning ----

func TestOrderMismatch_WarnsAtMostOncePerHour(t *testing.T) {
	p := primaryWithStandby(t)
	var n atomic.Int32
	p.b.logf = func(f string, a ...any) {
		if len(f) > 7 && f[:7] == "[fleet]" {
			n.Add(1)
		}
	}
	p.as.change(func(s *Settings) { s.Order = []string{"mlab", "air26"} })
	p.a.SetNewestAge(age(time.Second))
	p.b.Beat(ctx())
	p.b.Beat(ctx())
	require.Equal(t, int32(1), n.Load())
	p.clk.Add(time.Hour + time.Second)
	p.b.Beat(ctx())
	require.Equal(t, int32(2), n.Load())
}

// ---- notification ----

func TestOnChange_InOrderOnceEachNeverConcurrent(t *testing.T) {
	p := newPair(t)
	var mu sync.Mutex
	var got []int64
	var inside, overlap atomic.Int32
	p.a.OnChange(func(s fleet.RoleState) {
		if inside.Add(1) > 1 {
			overlap.Add(1)
		}
		time.Sleep(time.Millisecond)
		mu.Lock()
		got = append(got, s.Term)
		mu.Unlock()
		inside.Add(-1)
	})
	p.ab.setDown(true)
	p.a.Startup(ctx()) // primary term 1
	var wg sync.WaitGroup
	for i := int64(2); i <= 20; i++ {
		wg.Add(1)
		go func(i int64) { defer wg.Done(); p.a.HandleAnnounce("mlab", i) }(i)
	}
	wg.Wait()
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= 2 }, 2*time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(0), overlap.Load(), "never concurrently with itself")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, int64(1), got[0])
	for i := 1; i < len(got); i++ {
		require.NotEqual(t, got[i-1], got[i], "a change is reported once")
	}
}

func TestOnChange_CallbackMayReadAndChangeStateWithoutDeadlock(t *testing.T) {
	p := newPair(t)
	var calls atomic.Int32
	p.a.OnChange(func(s fleet.RoleState) {
		_ = p.a.State() // reads under the lock
		if calls.Add(1) == 1 {
			p.a.HandleAnnounce("mlab", s.Term+10) // changes state from inside the callback
		}
	})
	done := make(chan struct{})
	go func() {
		p.ab.setDown(true)
		p.a.Startup(ctx())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock in Startup")
	}
	require.Eventually(t, func() bool { return calls.Load() >= 2 }, 2*time.Second, time.Millisecond)
	require.Equal(t, fleet.RoleStandby, p.a.State().Role)
	require.Equal(t, int64(11), p.a.State().Term)
}

// ---- state.json ----

func TestState_WriteFailureOnlyLogs(t *testing.T) {
	p := newPair(t)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	var logged atomic.Int32
	e := NewEngine(p.ab, p.as, p.clk.Now, func(string, ...any) { logged.Add(1) }, filepath.Join(blocker, "x", "state.json"), "h")
	p.ab.setDown(true)
	e.Startup(ctx())
	require.Equal(t, fleet.RolePrimary, e.State().Role, "the role stays in memory")
	require.Greater(t, logged.Load(), int32(0))
}

func TestEngineImplementsRoleView(t *testing.T) {
	var _ fleet.RoleView = (*Engine)(nil)
	_ = fmt.Sprint
}
