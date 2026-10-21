package watchmod

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/middleware"
	"github.com/wake/purdex/internal/module/agent"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/watchsettings"
)

// Two daemons wired to each other in process: the host caller of one dispatches into the other's mux with the caller as the host
// principal, the way the peer routes see a paired host. The tmux lookup and the owner lookup of each are faked.

const (
	idA = "mlab:aaaaaa" // alias mlab
	idB = "a26:bbbbbb"  // alias a26 ("a26:" sorts before "mlab:")
)

type stands struct {
	mu    sync.Mutex
	left  []time.Duration // the time each send had left on its context
	sent  []watch.Holder
	win   []watch.HolderAnswer
	fail  int // fail this many sends first
	calls int
	enter chan struct{} // when set, each send announces itself here...
	block chan struct{} // ...and waits for this to be closed
}

func (s *stands) StandDown(ctx context.Context, own watch.Holder, winner watch.HolderAnswer) error {
	s.mu.Lock()
	enter, block := s.enter, s.block
	s.mu.Unlock()
	if enter != nil {
		enter <- struct{}{}
	}
	if block != nil {
		<-block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if dl, ok := ctx.Deadline(); ok {
		s.left = append(s.left, time.Until(dl))
	}
	s.calls++
	if s.fail > 0 {
		s.fail--
		return context.DeadlineExceeded
	}
	s.sent = append(s.sent, own)
	s.win = append(s.win, winner)
	return nil
}

func (s *stands) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

type daemon struct {
	m      *Module
	mux    *http.ServeMux
	panes  *fakePanes
	owners *fakeOwners
	stand  *stands
	clk    sync.Mutex
	now    time.Time // read and written through getNow/setNow (the module reads the clock from other goroutines)
	alias  string
	id     string
	// noAutoWake turns the auto_wake setting off for a test (read by the module's goroutines, so atomic)
	noAutoWake atomic.Bool
}

func (d *daemon) getNow() time.Time  { d.clk.Lock(); defer d.clk.Unlock(); return d.now }
func (d *daemon) setNow(t time.Time) { d.clk.Lock(); d.now = t; d.clk.Unlock() }

type pair struct{ a, b *daemon }

// loopback is the host caller of one daemon: it POSTs into the other daemon's mux.
type loopback struct {
	from  *daemon
	peers map[string]*daemon // by host id
	down  map[string]bool    // hosts that cannot be reached
	old   map[string]bool    // hosts that are an older daemon (404)
	mu    sync.Mutex
	paths []string
}

func (l *loopback) setDown(host string, v bool) { l.mu.Lock(); l.down[host] = v; l.mu.Unlock() }
func (l *loopback) setOld(host string, v bool)  { l.mu.Lock(); l.old[host] = v; l.mu.Unlock() }
func (l *loopback) pathsNow() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.paths...)
}

func (l *loopback) Call(ctx context.Context, hostID, path string, body any) peersmod.CallResult {
	l.mu.Lock()
	l.paths = append(l.paths, hostID+" "+path)
	down, old := l.down[hostID], l.old[hostID]
	l.mu.Unlock()
	if down {
		return peersmod.CallResult{Class: peersmod.ClassTransient, Detail: "unreachable"}
	}
	if old {
		return peersmod.CallResult{Class: peersmod.ClassUnsupported, Status: 404}
	}
	to := l.peers[hostID]
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw)).WithContext(ctx)
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{Kind: middleware.PrincipalHost, Alias: l.from.alias, HostID: l.from.id}))
	rr := httptest.NewRecorder()
	to.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		return peersmod.CallResult{Class: peersmod.ClassRefused, Status: rr.Code, Code: "refused", Detail: rr.Body.String()}
	}
	// HostCaller treats a 2xx whose JSON does not name the host it asked as a wrong host
	var named struct {
		HostID string `json:"host_id"`
	}
	if json.Unmarshal(rr.Body.Bytes(), &named) != nil || named.HostID != hostID {
		return peersmod.CallResult{Class: peersmod.ClassWrongHost, Status: rr.Code, Detail: "the answer does not name the host asked"}
	}
	return peersmod.CallResult{Class: peersmod.ClassDone, Status: rr.Code, Body: rr.Body.Bytes()}
}

// newPair builds the two daemons. orderA and orderB are the targets setting of each (so a test can list them in opposite orders).
func newPair(t *testing.T, orderA, orderB []string) *pair {
	t.Helper()
	mk := func(id, alias, peerAlias, peerID string, order []string) *daemon {
		cfg := &config.Config{DataDir: t.TempDir(), HostID: id}
		cfg.Peers.Alias = alias
		cfg.Watch = &config.WatchConfig{WakeSource: alias} // each is its own wake settings source: a host that is not reads its source at Start, and these tests count the calls a daemon makes
		cfg.Peers.Hosts = []config.PeerHost{{Alias: peerAlias, HostID: peerID, URL: "http://x", Token: "t", InboundToken: "i"}}
		c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
		m := New()
		m.logf = func(string, ...any) {}
		require.NoError(t, m.Init(c))
		d := &daemon{m: m, panes: &fakePanes{sid: "$3", pane: "%7", found: true}, owners: &fakeOwners{}, stand: &stands{},
			now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC), alias: alias, id: id}
		s := watchsettings.Default()
		s.Targets = order
		m.settings = func() (watchsettings.Settings, error) {
			out := s
			out.AutoWake = out.AutoWake && !d.noAutoWake.Load()
			return out, nil
		}
		// events never go to a live watcher, and a test drives each delivery pass itself
		m.dlv.notifier, m.manualDelivery = &fakeNotifier{}, true
		m.panes, m.ownerSrc, m.standDowner = d.panes, d.owners, d.stand
		m.now = d.getNow
		d.mux = http.NewServeMux()
		m.RegisterRoutes(d.mux)
		// the holder route is rate limited per host; the harness makes a lot of calls
		m.limit = peersmod.NewHostLimiter(1_000_000, time.Minute, time.Now)
		return d
	}
	p := &pair{a: mk(idA, "mlab", "a26", idB, orderA), b: mk(idB, "a26", "mlab", idA, orderB)}
	byID := map[string]*daemon{idA: p.a, idB: p.b}
	p.a.m.caller = &loopback{from: p.a, peers: byID, down: map[string]bool{}, old: map[string]bool{}}
	p.b.m.caller = &loopback{from: p.b, peers: byID, down: map[string]bool{}, old: map[string]bool{}}
	for _, d := range []*daemon{p.a, p.b} {
		d := d
		// the hook and the announce handler run only while the module runs
		require.NoError(t, d.m.Start(context.Background()))
		t.Cleanup(func() { _ = d.m.Stop(context.Background()) })
	}
	return p
}

func (d *daemon) loop() *loopback { return d.m.caller.(*loopback) }

// register makes the daemon's agent module report a watcher frame, awake since the daemon's clock, and reconciles (the hook
// announces and settles).
func (d *daemon) register(t *testing.T, frame string) {
	t.Helper()
	d.owners.set(watcherOwner(frame), true, nil)
	_, _, err := d.m.Reconcile(context.Background())
	require.NoError(t, err)
}

var (
	bothOrder     = []string{"mlab", "a26"}
	oppositeOrder = []string{"a26", "mlab"}
)

// Spec §12.3: two watchers awake at once — every daemon picks the same one (the one awake the longest) whatever the order of its
// targets, and exactly one stand-down notice goes out, to the loser's own watcher.
func TestTwoAwake_SimultaneousRegistrationsPickOneAndOneNoticeToTheLoser(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	p.b.setNow(p.a.getNow().Add(time.Hour))

	var wg sync.WaitGroup
	for _, d := range []*daemon{p.a, p.b} {
		wg.Add(1)
		go func(d *daemon) { defer wg.Done(); d.register(t, "frame-"+d.alias) }(d)
	}
	wg.Wait()
	// the notices are sent from the hook and from the announce handler's goroutine: wait for the dust to settle
	require.Eventually(t, func() bool { return p.b.stand.count() == 1 }, 3*time.Second, 5*time.Millisecond)
	time.Sleep(150 * time.Millisecond)

	require.Equal(t, 0, p.a.stand.count(), "the watcher that has been awake longer stays")
	require.Equal(t, 1, p.b.stand.count(), "exactly one notice, to the loser's own watcher")
	require.Equal(t, "frame-a26", p.b.stand.sent[0].FrameID)
	require.Equal(t, idA, p.b.stand.win[0].HostID)

	// both locators pick A's watcher, whatever the order of their targets
	wa, err := p.a.m.loc.Current(context.Background(), bothOrder, true)
	require.NoError(t, err)
	wb, err := p.b.m.loc.Current(context.Background(), oppositeOrder, true)
	require.NoError(t, err)
	require.Equal(t, idA, wa.HostID)
	require.Equal(t, idA, wb.HostID)
}

// The announce arrives after the other host's holder answer was already used: the loser's locator forgets the watcher it had
// remembered and the next pick is the winner.
func TestTwoAwake_AnAnnounceAfterTheHolderAnswerReachesTheSamePick(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	// B's watcher registers first, with A unreachable: nothing is announced, B's locator settles on itself
	p.b.setNow(p.a.getNow().Add(time.Hour))
	p.b.loop().setDown(idA, true)
	p.b.register(t, "frame-b")
	wb, err := p.b.m.loc.Current(context.Background(), oppositeOrder, true)
	require.NoError(t, err)
	require.Equal(t, idB, wb.HostID, "B knows only itself")
	require.Equal(t, 0, p.b.stand.count())

	// A's older watcher registers; its announce reaches B, which asks A for its holder as it is now
	p.b.loop().setDown(idA, false)
	p.a.register(t, "frame-a")
	require.Eventually(t, func() bool { return p.b.stand.count() == 1 }, 3*time.Second, 5*time.Millisecond)
	cur, _ := p.b.m.loc.Remembered()
	require.Nil(t, cur, "B's locator forgot the watcher it had, the announced one beats it")
	w, err := p.b.m.loc.Current(context.Background(), oppositeOrder, true)
	require.NoError(t, err)
	require.Equal(t, idA, w.HostID)
	require.Equal(t, 0, p.a.stand.count())
}

// Equal since (the same second): the smaller host id wins.
func TestTwoAwake_EqualSinceTheSmallerHostIDWins(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	p.b.setNow(p.a.getNow()) // the same instant
	p.a.register(t, "frame-a")
	p.b.register(t, "frame-b")
	require.Eventually(t, func() bool { return p.a.stand.count() == 1 }, 3*time.Second, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 0, p.b.stand.count(), "a26:bbbbbb sorts before mlab:aaaaaa: B stays")
	require.Equal(t, 1, p.a.stand.count())
}

// The notice goes out once for a frame, however many times the rule leads there; one that failed to be sent is tried again by the
// next trigger (which may be this test's, or a legitimate one from the harness's own announce handlers).
func TestTwoAwake_OneNoticePerFrameAndAFailedOneIsRetried(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	p.b.setNow(p.a.getNow().Add(time.Hour))
	p.b.stand.fail = 1 // the first send fails
	p.a.register(t, "frame-a")
	p.b.register(t, "frame-b")
	require.Eventually(t, func() bool {
		p.b.stand.mu.Lock()
		defer p.b.stand.mu.Unlock()
		return p.b.stand.calls >= 1
	}, 3*time.Second, 5*time.Millisecond, "the registration's settle sent, and the first send failed")

	// the next trigger tries again, and the notice has been delivered once by the time it is done
	p.b.m.settle(context.Background())
	require.Equal(t, 1, p.b.stand.count())
	callsAfter := func() int { p.b.stand.mu.Lock(); defer p.b.stand.mu.Unlock(); return p.b.stand.calls }
	before := callsAfter()
	// and then nothing more for that frame, however often the rule leads here
	p.b.m.settle(context.Background())
	p.b.m.standDown(context.Background(), p.b.m.holder.Get(), watch.HolderAnswer{HostID: idA})
	require.Equal(t, 1, p.b.stand.count())
	require.Equal(t, before, callsAfter(), "a delivered notice is not sent again")
}

// A host that is an older daemon (the announce route answers 404) or cannot be reached is not a failure: the registration stands.
func TestAnnounce_AnOlderOrUnreachableHostIsIgnored(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	p.a.loop().setOld(idB, true)
	p.a.register(t, "frame-a")
	require.Equal(t, watch.HolderAwake, p.a.m.holder.Get().State)
	p.a.loop().setOld(idB, false)
	p.a.loop().setDown(idB, true)
	p.a.register(t, "frame-a2")
	require.Equal(t, "frame-a2", p.a.m.holder.Get().FrameID)
	require.Equal(t, 0, p.a.stand.count())
}

// A registration announces to the other hosts of targets and not to itself, once per new frame.
func TestAnnounce_GoesToTheOtherTargetsOncePerNewFrame(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	p.a.register(t, "frame-a")
	p.a.register(t, "frame-a") // the same frame again
	got := p.a.loop().pathsNow()
	require.Equal(t, []string{idB + " " + pathAnnounce, idB + " " + pathHolder}, got[:2], "the announce, then the holder question of the settle")
	n := 0
	for _, s := range got {
		if strings.HasSuffix(s, pathAnnounce) {
			n++
		}
	}
	require.Equal(t, 1, n, "one announce for one frame")
}

func TestAnnounceRoute_Refusals(t *testing.T) {
	r := newRig(t)
	post := func(p *middleware.Principal, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/peers/watch/announce", strings.NewReader(body))
		if p != nil {
			req = req.WithContext(middleware.WithPrincipal(req.Context(), *p))
		}
		rr := httptest.NewRecorder()
		r.mux.ServeHTTP(rr, req)
		return rr
	}
	ok := `{"to_host_id":"` + ourID + `","holder":{"host_id":"` + peerID + `","state":"awake","session_id":"s","since":"2026-10-16T12:00:00Z"}}`
	require.Equal(t, http.StatusForbidden, post(nil, ok).Code)
	require.Equal(t, http.StatusForbidden, post(&middleware.Principal{Kind: middleware.PrincipalAdmin}, ok).Code)
	require.Equal(t, http.StatusForbidden, post(&middleware.Principal{Kind: middleware.PrincipalHost, Alias: "a26", HostID: "other:cccccc"}, ok).Code)
	// addressed to another host
	rr := post(host, strings.Replace(ok, ourID, "c5n:dddddd", 1))
	require.Equal(t, http.StatusConflict, rr.Code)
	// the announcer may only announce itself, awake, with a session
	require.Equal(t, http.StatusBadRequest, post(host, strings.Replace(ok, peerID+`"`, "c5n:dddddd\"", 1)).Code, "someone else's holder")
	require.Equal(t, http.StatusBadRequest, post(host, strings.Replace(ok, "awake", "resting", 1)).Code)
	require.Equal(t, http.StatusBadRequest, post(host, strings.Replace(ok, `"session_id":"s"`, `"session_id":""`, 1)).Code)
	require.Equal(t, http.StatusBadRequest, post(host, `{nope`).Code)
	require.Equal(t, http.StatusRequestEntityTooLarge, post(host, `{"to_host_id":"`+strings.Repeat("x", 9000)+`"}`).Code)
	require.Equal(t, http.StatusOK, post(host, ok).Code)
}

// The peer routes a host principal may call include the announce, and nothing else changed for the others.
func TestAnnounceRoute_IsInTheHostRoutePolicy(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/peers/watch/announce", nil)
	require.True(t, peersmod.HostRoutePolicy(req))
	require.False(t, peersmod.HostRoutePolicy(httptest.NewRequest(http.MethodGet, "/api/peers/watch/announce", nil)))
	require.False(t, peersmod.HostRoutePolicy(httptest.NewRequest(http.MethodPost, "/api/peers/watch/other", nil)))
}

type fakeOrigins struct{ inbox, ref string }

func (f fakeOrigins) InboxOf(string) (string, bool, error) { return f.inbox, f.inbox != "", nil }
func (f fakeOrigins) ResolveOriginBySession(string) (team.Origin, bool, error) {
	return team.Origin{Ref: f.ref}, f.ref != "", nil
}

type fakeSelfNotice struct {
	mu   sync.Mutex
	reqs []ipeers.SendRequest
}

func (f *fakeSelfNotice) SendSelfNotice(_ context.Context, r ipeers.SendRequest) (ipeers.SendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, r)
	return ipeers.SendResponse{Result: "delivered"}, nil
}

// The production stand-down notice goes from the watcher's own inbox to the tmux session pdx-watch of this host, as a [負載守門]
// message that names the watcher that stays.
func TestStandDowner_SendsFromTheWatchersOwnInboxToThePdxWatchSession(t *testing.T) {
	r := newRig(t)
	r.m.core.Cfg.Peers.Alias = "mlab"
	sn := &fakeSelfNotice{}
	r.m.core.Registry.Register(peersmod.OriginResolverKey, fakeOrigins{inbox: "/tmp/cc-socks/42.sock", ref: "_abc123"})
	r.m.core.Registry.Register(peersmod.SelfNoticeSenderKey, peersmod.SelfNoticeSender(sn))

	require.NoError(t, peerStandDowner{r.m}.StandDown(context.Background(), watch.Holder{State: watch.HolderAwake, SessionID: "sess-1", FrameID: "f"}, watch.HolderAnswer{HostID: peerID}))
	require.Len(t, sn.reqs, 1)
	require.Equal(t, "mlab/_abc123", sn.reqs[0].To, "addressed by the watcher's own ref, not by the tmux session name the next watcher reuses")
	require.Equal(t, "/tmp/cc-socks/42.sock", sn.reqs[0].OriginInbox)
	require.True(t, strings.HasPrefix(sn.reqs[0].Text, "[負載守門]"))
	require.Contains(t, sn.reqs[0].Text, peerID)

	// no live address for the watcher (it is gone): an error, nothing is sent to whoever took its place
	r.m.core.Registry.Register(peersmod.OriginResolverKey, fakeOrigins{inbox: "/tmp/cc-socks/42.sock"})
	require.Error(t, peerStandDowner{r.m}.StandDown(context.Background(), watch.Holder{SessionID: "sess-1", FrameID: "f"}, watch.HolderAnswer{HostID: peerID}))
	require.Len(t, sn.reqs, 1)
	// no live inbox / no resolver: an error, so the notice is tried again later
	r.m.core.Registry.Register(peersmod.OriginResolverKey, fakeOrigins{ref: "_abc123"})
	require.Error(t, peerStandDowner{r.m}.StandDown(context.Background(), watch.Holder{SessionID: "sess-1", FrameID: "f"}, watch.HolderAnswer{HostID: peerID}))
	r2 := newRig(t)
	require.Error(t, peerStandDowner{r2.m}.StandDown(context.Background(), watch.Holder{SessionID: "s"}, watch.HolderAnswer{}))
}

// The daemon's own default stand-down sender is the peers one, and a reconcile without a sender configured sends nothing but logs.
func TestStart_InstallsThePeersStandDowner(t *testing.T) {
	r := newRecRig(t)
	require.NoError(t, r.m.Start(context.Background()))
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	_, ok := r.m.standDowner.(peerStandDowner)
	require.True(t, ok)
}

// The announce's answer names the receiving host (the caller treats any other 2xx as a wrong host), so a successful announce is
// not logged as a failure.
func TestAnnounce_TheAnswerNamesTheReceivingHost(t *testing.T) {
	var logs []string
	var mu sync.Mutex
	p := newPair(t, bothOrder, oppositeOrder)
	p.a.m.logf = func(f string, a ...any) { mu.Lock(); logs = append(logs, f); mu.Unlock() }
	p.a.register(t, "frame-a")
	mu.Lock()
	defer mu.Unlock()
	for _, l := range logs {
		require.NotContains(t, l, "announce to", "a good announce is not reported as a failure")
	}
	r := newRig(t)
	ok := `{"to_host_id":"` + ourID + `","holder":{"host_id":"` + peerID + `","state":"awake","session_id":"s"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/peers/watch/announce", strings.NewReader(ok))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), *host))
	rr := httptest.NewRecorder()
	r.mux.ServeHTTP(rr, req)
	require.Contains(t, rr.Body.String(), `"host_id":"`+ourID+`"`)
}

// A watcher that restarted while the other hosts were being asked is another frame: the notice is not sent for the old snapshot.
func TestStandDown_IsNotSentForAFrameThatIsNoLongerTheWatcher(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	p.b.register(t, "frame-b1")
	old := p.b.m.holder.Get()
	p.b.register(t, "frame-b2") // the watcher restarted
	p.b.m.standDown(context.Background(), old, watch.HolderAnswer{HostID: idA})
	require.Equal(t, 0, p.b.stand.count(), "frame-b1 is gone")
	p.b.m.standDown(context.Background(), p.b.m.holder.Get(), watch.HolderAnswer{HostID: idA})
	require.Equal(t, 1, p.b.stand.count())

	// and not for a watcher that is no longer awake
	p.b.owners.set(agent.PaneOwner{}, false, nil)
	_, _, _ = p.b.m.Reconcile(context.Background())
	p.b.m.standDown(context.Background(), p.b.m.holder.Get(), watch.HolderAnswer{HostID: idA})
	require.Equal(t, 1, p.b.stand.count())
}

// The registration's announce and settle run under the stand-down budget, not a reconcile's: a normal slow peer call does not
// leave the settle without time.
func TestTwoAwake_TheSettleHasTheStandDownBudget(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	p.b.setNow(p.a.getNow().Add(time.Hour))
	p.a.register(t, "frame-a")
	p.b.register(t, "frame-b")
	require.Eventually(t, func() bool { return p.b.stand.count() == 1 }, 3*time.Second, 5*time.Millisecond)
	p.b.stand.mu.Lock()
	defer p.b.stand.mu.Unlock()
	require.NotEmpty(t, p.b.stand.left)
	require.Greater(t, p.b.stand.left[0], reconcileBudget+2*time.Second, "more than a reconcile's budget is left on the context")
}

// An announce is a hint to look, not the truth: this host decides on the announcer's holder as the announcer answers it NOW, so a
// stale or fabricated announce (an early since, a session that is not the watcher) moves nothing.
func TestAnnounce_IsOnlyAHintTheAnnouncersOwnAnswerDecides(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	p.b.setNow(p.a.getNow()) // B awake since 12:00
	p.b.register(t, "frame-b")
	require.Equal(t, 0, p.b.stand.count())

	// A is not awake at all, and announces an old watcher from long ago
	forged := `{"to_host_id":"` + idB + `","holder":{"host_id":"` + idA + `","state":"awake","session_id":"ghost","since":"2001-01-01T00:00:00Z"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/peers/watch/announce", strings.NewReader(forged))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab", HostID: idA}))
	rr := httptest.NewRecorder()
	p.b.mux.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 0, p.b.stand.count(), "A has no watcher awake: nothing to yield to")
	cur, _ := p.b.m.loc.Remembered()
	_ = cur

	// A's real watcher is younger than B's, and the announce claims it is ancient: B still stays
	p.a.setNow(p.b.getNow().Add(time.Hour))
	p.a.register(t, "frame-a")
	req = httptest.NewRequest(http.MethodPost, "/api/peers/watch/announce", strings.NewReader(forged))
	req = req.WithContext(middleware.WithPrincipal(req.Context(), middleware.Principal{Kind: middleware.PrincipalHost, Alias: "mlab", HostID: idA}))
	p.b.mux.ServeHTTP(httptest.NewRecorder(), req)
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 0, p.b.stand.count(), "the announce's since is not believed; A's own answer says it is younger")
	require.Equal(t, 1, p.a.stand.count(), "A, the younger one, yields (its own registration compared the two real answers)")
}

// A queued registration of a frame that is no longer the watcher is not announced.
func TestOnRegistered_SkipsAFrameThatIsNoLongerTheWatcher(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	p.a.register(t, "frame-a1")
	old := p.a.m.holder.Get()
	p.a.register(t, "frame-a2")
	before := len(p.a.loop().pathsNow())
	p.a.m.onRegistered(old)
	require.Equal(t, before, len(p.a.loop().pathsNow()), "nothing is asked or announced for the old frame")
}

// A second trigger that arrives while the first notice is being sent waits for its outcome: when the first fails, the second sends it.
func TestStandDown_ASecondTriggerDuringAFailingSendIsNotLost(t *testing.T) {
	p := newPair(t, bothOrder, oppositeOrder)
	p.b.register(t, "frame-b")
	own := p.b.m.holder.Get()
	p.b.stand.mu.Lock()
	p.b.stand.enter = make(chan struct{}, 4)
	p.b.stand.block = make(chan struct{})
	p.b.stand.fail = 1
	enter, block := p.b.stand.enter, p.b.stand.block
	p.b.stand.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); p.b.m.standDown(context.Background(), own, watch.HolderAnswer{HostID: idA}) }()
		if i == 0 {
			<-enter // the first send is in flight
		}
	}
	time.Sleep(100 * time.Millisecond) // the second trigger is waiting for it
	p.b.stand.mu.Lock()
	p.b.stand.block = nil // later sends do not block
	p.b.stand.mu.Unlock()
	close(block)
	wg.Wait()
	require.Equal(t, 1, p.b.stand.count(), "the first send failed, the waiting second one sent it")
	require.Equal(t, 2, p.b.stand.calls)
}
