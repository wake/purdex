package poolmod

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/wire"
)

const (
	testPoll  = 10 * time.Millisecond
	testMerge = 150 * time.Millisecond
)

// livePool is a wwd whose pool the test changes while the module watches it.
type livePool struct {
	mu   sync.Mutex
	pool wire.Pool
}

func (p *livePool) get() wire.Pool {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, _ := json.Marshal(p.pool)
	var cp wire.Pool
	_ = json.Unmarshal(b, &cp)
	return cp
}

// change edits the pool in place under the lock.
func (p *livePool) change(fn func(*wire.Pool)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(&p.pool)
}

func (p *livePool) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p.get())
	}
}

func newLivePool(t *testing.T) *livePool {
	t.Helper()
	var p wire.Pool
	require.NoError(t, json.Unmarshal(poolOf(t), &p))
	return &livePool{pool: p}
}

// noticed records the pool.changed the module sent, with when.
type noticed struct {
	mu  sync.Mutex
	got []notice
}

type notice struct {
	host string
	at   time.Time
}

func (n *noticed) emit(ev core.HostEvent) {
	var p wire.PoolChanged
	if ev.Type != EventPoolChanged || json.Unmarshal([]byte(ev.Value), &p) != nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.got = append(n.got, notice{p.Host, time.Now()})
}

func (n *noticed) all() []notice {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notice(nil), n.got...)
}

func (n *noticed) hosts() []string {
	var out []string
	for _, x := range n.all() {
		out = append(out, x.host)
	}
	return out
}

// watching starts a module on cl that reports to the returned recorder; the module stops with the test.
func watching(t *testing.T, cl *client.Client) (*Module, *noticed) {
	t.Helper()
	m := New()
	m.cl = cl
	m.pollEvery, m.mergeWithin = testPoll, testMerge
	n := &noticed{}
	m.emit = n.emit
	m.logf = func(string, ...any) {}
	require.NoError(t, m.Start(context.Background()))
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m, n
}

// The first look at the pool is only the baseline: what is there already is not news.
func TestPoolChanged_TheFirstReadIsOnlyTheBaseline(t *testing.T) {
	lp := newLivePool(t)
	_, n := watching(t, fakeWWD(t, lp.handler()))
	time.Sleep(10 * testPoll)
	assert.Empty(t, n.all())
}

// A worker switch, an exception added or removed, and a zone change each send one notice for that host, and only that host.
func TestPoolChanged_OneNoticeForAWorkerAnExceptionAndAZone(t *testing.T) {
	lp := newLivePool(t)
	_, n := watching(t, fakeWWD(t, lp.handler()))
	time.Sleep(5 * testPoll) // the baseline

	steps := []struct {
		name string
		fn   func(*wire.Pool)
	}{
		{"worker switched off", func(p *wire.Pool) { p.Peers[0].Facts.Worker.Enabled = false }},
		{"exception added", func(p *wire.Pool) {
			until := time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC)
			p.Peers[0].Facts.Exception = []wire.FactExc{{Name: "pkg", Since: until.Add(-time.Hour), Until: &until}}
		}},
		{"exception removed", func(p *wire.Pool) { p.Peers[0].Facts.Exception = []wire.FactExc{} }},
		{"zone changed", func(p *wire.Pool) { p.Peers[0].Facts.Derived.Zone = wire.ZoneWarning }},
	}
	for i, s := range steps {
		lp.change(s.fn)
		require.Eventually(t, func() bool { return len(n.all()) == i+1 }, 3*time.Second, testPoll, s.name)
		time.Sleep(testMerge + 5*testPoll) // nothing more for the same change, and the window for the next one has passed
		assert.Len(t, n.all(), i+1, "%s: one notice, not several", s.name)
	}
	for _, h := range n.hosts() {
		assert.Equal(t, "mlab", h, "air19 did not change")
	}
}

// A host's liveness changing is news for that host; the time it was last looked at moving, with nothing else, is not (it moves at every
// poll of wwd, and a host that stays down would otherwise be announced every few seconds).
func TestPoolChanged_LivenessChangesAreNewsButTheLookTimeIsNot(t *testing.T) {
	lp := newLivePool(t)
	t0 := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	set := func(l wire.Liveness, at, since time.Time) func(*wire.Pool) {
		return func(p *wire.Pool) {
			p.Peers[1].Liveness, p.Peers[1].LivenessAt, p.Peers[1].LivenessSince = l, &at, &since
		}
	}
	lp.change(set(wire.LivenessWwdDown, t0, t0))
	_, n := watching(t, fakeWWD(t, lp.handler()))
	time.Sleep(5 * testPoll) // the baseline

	lp.change(set(wire.LivenessWwdDown, t0.Add(time.Minute), t0)) // looked at again, nothing else
	time.Sleep(testMerge + 5*testPoll)
	assert.Empty(t, n.all(), "only the look time moved")

	lp.change(set(wire.LivenessHostDown, t0.Add(2*time.Minute), t0.Add(2*time.Minute)))
	require.Eventually(t, func() bool { return len(n.all()) == 1 }, 3*time.Second, testPoll)
	assert.Equal(t, []string{"air19"}, n.hosts())
}

// A host that is never reached and still is never reached is not news; reaching it is.
func TestPoolChanged_AHostThatBecomesReachableAndOneThatAppearsOrGoes(t *testing.T) {
	lp := newLivePool(t)
	_, n := watching(t, fakeWWD(t, lp.handler()))
	time.Sleep(5 * testPoll)

	lp.change(func(p *wire.Pool) { p.Peers[1].Reachable = true })
	require.Eventually(t, func() bool { return len(n.all()) == 1 }, 3*time.Second, testPoll)
	assert.Equal(t, []string{"air19"}, n.hosts())

	lp.change(func(p *wire.Pool) {
		extra := p.Peers[1]
		extra.Info.Alias, extra.Info.HostID = "mserv", "h-mserv"
		p.Peers = append(p.Peers, extra)
	})
	require.Eventually(t, func() bool { return len(n.all()) == 2 }, 3*time.Second, testPoll)
	assert.Equal(t, []string{"air19", "mserv"}, n.hosts())

	lp.change(func(p *wire.Pool) { p.Peers = p.Peers[:2] }) // mserv is gone
	require.Eventually(t, func() bool { return len(n.all()) == 3 }, 3*time.Second, testPoll)
	assert.Equal(t, []string{"air19", "mserv", "mserv"}, n.hosts())
}

// One host sends at most one notice per window however often it changes, and the last state is not lost: a change that arrives
// inside the window is announced when the window ends.
func TestPoolChanged_MergedPerHostWithinTheWindow(t *testing.T) {
	lp := newLivePool(t)
	_, n := watching(t, fakeWWD(t, lp.handler()))
	time.Sleep(5 * testPoll)

	var lastChange time.Time
	for i := 0; i < 20; i++ { // a change every few polls for longer than two windows
		lp.change(func(p *wire.Pool) { p.Peers[0].Facts.Worker.Slots.Light = 3 + i })
		lastChange = time.Now()
		time.Sleep(3 * testPoll)
	}
	require.Eventually(t, func() bool {
		got := n.all()
		return len(got) > 0 && got[len(got)-1].at.After(lastChange)
	}, 3*time.Second, testPoll, "the last change is announced when the window ends")

	got := n.all()
	require.GreaterOrEqual(t, len(got), 2)
	for i := 1; i < len(got); i++ {
		assert.GreaterOrEqual(t, got[i].at.Sub(got[i-1].at), testMerge-20*time.Millisecond, "notice %d came too soon after notice %d", i, i-1)
	}
	assert.Less(t, len(got), 20, "the changes were merged")
}

// wwd not running is quiet; the first pool it answers is a baseline, not a change.
func TestPoolChanged_WWDAbsentThenBack(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "wwd")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "w.sock")
	m, n := watching(t, client.New(sock))
	_ = m
	time.Sleep(10 * testPoll)
	assert.Empty(t, n.all(), "wwd is not there")

	lp := newLivePool(t)
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	srv := &http.Server{Handler: lp.handler()}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	time.Sleep(10 * testPoll)
	assert.Empty(t, n.all(), "what is there when wwd comes back is the baseline")

	lp.change(func(p *wire.Pool) { p.Peers[0].Facts.Worker.Enabled = false })
	require.Eventually(t, func() bool { return len(n.all()) == 1 }, 3*time.Second, testPoll)
}

// A notice waiting for its window is dropped by Stop: nothing is sent after it.
func TestPoolChanged_NothingIsSentAfterStop(t *testing.T) {
	lp := newLivePool(t)
	m := New()
	m.cl = fakeWWD(t, lp.handler())
	m.pollEvery, m.mergeWithin = testPoll, time.Second
	n := &noticed{}
	m.emit = n.emit
	m.logf = func(string, ...any) {}
	require.NoError(t, m.Start(context.Background()))
	time.Sleep(5 * testPoll)

	lp.change(func(p *wire.Pool) { p.Peers[0].Facts.Worker.Slots.Light = 9 })
	require.Eventually(t, func() bool { return len(n.all()) == 1 }, 3*time.Second, testPoll)
	lp.change(func(p *wire.Pool) { p.Peers[0].Facts.Worker.Slots.Light = 8 }) // inside the window: it waits
	time.Sleep(5 * testPoll)
	require.NoError(t, m.Stop(context.Background()))
	time.Sleep(1200 * time.Millisecond)
	assert.Len(t, n.all(), 1)
}

// pool.events.v1 is declared by the real module in a real core while its watch runs, and not before Start or after Stop.
func TestInfo_AnnouncesPoolEventsV1WhileTheWatchRuns(t *testing.T) {
	t.Setenv("PDX_WWD_SOCK", filepath.Join(t.TempDir(), "none.sock"))
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	c.AddModule(m)
	require.NoError(t, m.Init(c))
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	caps := func() []any {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		list, _ := body["capabilities"].([]any)
		return list
	}
	require.Contains(t, caps(), "pool.v1")
	require.NotContains(t, caps(), "pool.events.v1", "no watch yet: the App must keep polling")
	require.NoError(t, m.Start(context.Background()))
	require.Contains(t, caps(), "pool.events.v1")
	require.NoError(t, m.Stop(context.Background()))
	require.NotContains(t, caps(), "pool.events.v1")
}
