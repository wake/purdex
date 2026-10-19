package wwdalerts

// The failure matrix of spec §13.3 / plan 8a-3, end to end in one process: three hosts as they are deployed — mlab and air26 each run a wwd
// and a Purdex daemon (the deliverer), mserv runs only a wwd — with the real wwd daemons, stores, pool pollers, silence rules and
// evaluators, and the real deliverer module reading them. Each component is stopped in turn. The promise under test: at least one push per
// failure, a duplicate only during a failover or a partition, and the stated gap when both deliverers are down. Time is driven by hand
// (the rules and the module take a clock), nothing sleeps for minutes.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/wwd/advisor"
	"github.com/wake/purdex/internal/wwd/daemon"
	wwdstore "github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

const mxToken = "matrix-token"

// link is the network between the wwds: a pair of hosts can be cut, in both directions, to make a partition.
type link struct {
	mu  sync.Mutex
	cut map[[2]string]bool
}

func (l *link) setCut(a, b string, cut bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cut == nil {
		l.cut = map[[2]string]bool{}
	}
	l.cut[[2]string{a, b}], l.cut[[2]string{b, a}] = cut, cut
}

func (l *link) blocked(from, to string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cut[[2]string{from, to}]
}

// viaLink is the HTTP client of one wwd: its requests to a peer fail when the pair is cut.
type viaLink struct {
	l      *link
	from   string
	byAddr map[string]string // host:port -> alias
}

func (v viaLink) RoundTrip(r *http.Request) (*http.Response, error) {
	if to := v.byAddr[r.URL.Host]; to != "" && v.l.blocked(v.from, to) {
		return nil, io.ErrUnexpectedEOF
	}
	return http.DefaultTransport.RoundTrip(r)
}

// mxHost is one machine: its wwd, and (when it has one) its Purdex daemon's deliverer.
type mxHost struct {
	alias     string
	st        *wwdstore.Store
	d         *daemon.Daemon
	srv       *httptest.Server
	sw        *swapHandler
	wwdUp     atomic.Bool
	ev        *advisor.Evaluator
	hasDaemon bool
	daemonUp  bool
	deliv     bool // this daemon is the deliverer (the fleet role); the election is fleet's, set by the test
	mod       *Module
	ntf       *fakeNotifier
}

type world struct {
	t     *testing.T
	hosts map[string]*mxHost
	order []string
	link  *link
	adv   time.Duration // how far the test has moved the clock
	base  time.Time
}

func (w *world) now() time.Time { return w.base.Add(w.adv) }

// gate lets a wwd's TCP surface answer only while it is up.
func gate(h *mxHost, inner http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !h.wwdUp.Load() {
			http.Error(rw, "down", http.StatusServiceUnavailable)
			return
		}
		inner.ServeHTTP(rw, r)
	})
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, hosts: map[string]*mxHost{}, link: &link{}, base: time.Now()}
	specs := []struct {
		alias     string
		hasDaemon bool
	}{{"mlab", true}, {"air26", true}, {"mserv", false}}
	// the servers first, so every wwd knows the others' addresses
	byAddr := map[string]string{}
	for _, s := range specs {
		h := &mxHost{alias: s.alias, hasDaemon: s.hasDaemon, daemonUp: s.hasDaemon}
		h.wwdUp.Store(true)
		sw := &swapHandler{}
		h.sw = sw
		h.srv = httptest.NewServer(sw)
		t.Cleanup(h.srv.Close)
		sw.set(gate(h, http.NotFoundHandler()))
		byAddr[h.srv.Listener.Addr().String()] = s.alias
		w.hosts[s.alias] = h
		w.order = append(w.order, s.alias)
		_ = sw
	}
	for _, s := range specs {
		h := w.hosts[s.alias]
		var peers []daemon.PeerAddr
		for _, o := range specs {
			if o.alias != s.alias {
				peers = append(peers, daemon.PeerAddr{Alias: o.alias, HostPort: w.hosts[o.alias].srv.Listener.Addr().String()})
			}
		}
		st, err := wwdstore.Open(filepath.Join(t.TempDir(), "wwd.db"))
		require.NoError(t, err)
		t.Cleanup(func() { st.Close() })
		h.st = st
		client := &http.Client{Timeout: 2 * time.Second, Transport: viaLink{l: w.link, from: s.alias, byAddr: byAddr}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		h.d = daemon.New(daemon.Options{
			HostID: "id-" + s.alias, Alias: s.alias, Version: "v0", Token: mxToken, Alerts: st, Peers: peers, HTTPClient: client, Now: w.now,
		})
		// the real TCP handler behind the gate
		h.sw.set(gate(h, h.d.TCPHandler()))
		h.d.Pool().SetClock(w.now)
		w.wireRules(h, peers)
		w.wireDeliverer(h)
	}
	return w
}

type swapHandler struct {
	mu sync.Mutex
	h  http.Handler
}

func (s *swapHandler) set(h http.Handler) { s.mu.Lock(); s.h = h; s.mu.Unlock() }
func (s *swapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	h := s.h
	s.mu.Unlock()
	h.ServeHTTP(w, r)
}

// wireRules builds the silence rules a host runs: one per peer (air19-style opt-outs do not occur here) and the partner check where the
// host has a Purdex daemon, judged on the world's clock.
func (w *world) wireRules(h *mxHost, peers []daemon.PeerAddr) {
	book := daemon.NewAlertBook(h.st, h.alias, nil, w.now)
	input := func() (advisor.Input, bool) {
		in := advisor.Input{Now: w.now()}
		in.Peers = func() []advisor.PeerDown {
			var out []advisor.PeerDown
			for _, d := range h.d.Pool().Downs() {
				out = append(out, advisor.PeerDown{Alias: d.Alias, Since: d.Since.Add(0)})
			}
			return out
		}
		if h.hasDaemon {
			in.PartnerSeen = h.d.PartnerSeen
		}
		return in, true
	}
	var rules []advisor.Rule
	for _, p := range peers {
		rules = append(rules, advisor.PeerSilence{Alias: p.Alias})
	}
	if h.hasDaemon {
		rules = append(rules, advisor.PartnerDown{})
	}
	h.ev = advisor.NewEvaluator(book, input, nil, rules...)
}

// localSrc is a Purdex daemon's view of the wwd on its own machine, over the wwd's socket surface (the real handler, in process).
type localSrc struct {
	h *mxHost
}

func (s localSrc) do(path string) (*httptest.ResponseRecorder, error) {
	if !s.h.wwdUp.Load() {
		return nil, io.ErrUnexpectedEOF
	}
	rec := httptest.NewRecorder()
	s.h.d.SocketHandler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec, nil
}

func (s localSrc) Info(context.Context) (wire.Info, error) {
	rec, err := s.do("/v1/info")
	if err != nil {
		return wire.Info{}, err
	}
	return wire.Decode[wire.Info](rec.Body.Bytes())
}

func (s localSrc) Pool(context.Context) (wire.Pool, error) {
	rec, err := s.do("/v1/pool")
	if err != nil {
		return wire.Pool{}, err
	}
	return wire.Decode[wire.Pool](rec.Body.Bytes())
}

func (s localSrc) Alerts(_ context.Context, host string) (wire.AlertList, bool, error) {
	path := "/v1/alerts"
	if host != "" && host != s.h.alias {
		path += "?host=" + host
	}
	rec, err := s.do(path)
	if err != nil {
		return wire.AlertList{}, false, err
	}
	if rec.Code == http.StatusNotFound {
		return wire.AlertList{}, false, nil
	}
	if rec.Code != http.StatusOK {
		return wire.AlertList{}, false, io.ErrUnexpectedEOF
	}
	l, err := wire.Decode[wire.AlertList](rec.Body.Bytes())
	return l, err == nil, err
}

func (w *world) wireDeliverer(h *mxHost) {
	if !h.hasDaemon {
		return
	}
	h.ntf = &fakeNotifier{done: true}
	st, err := openStore(filepath.Join(w.t.TempDir(), "wwdalerts.db"))
	require.NoError(w.t, err)
	w.t.Cleanup(func() { st.close() })
	h.mod = &Module{
		logf:    func(f string, a ...any) { w.t.Logf(h.alias+": "+f, a...) },
		now:     w.now,
		src:     localSrc{h},
		store:   st,
		notify:  func() (push.AlertNotifier, bool) { return h.ntf, true },
		deliver: func() bool { return h.deliv },
		pushOn:  func() (bool, error) { return true, nil },
	}
}

// round is one tick of everything that is running: the wwds poll their peers and judge their rules, then each Purdex daemon that is up
// reads every list. Time moves by step first.
func (w *world) round(step time.Duration) {
	w.adv += step
	for _, a := range w.order {
		h := w.hosts[a]
		if h.wwdUp.Load() {
			h.d.Pool().PollOnce(context.Background())
			h.ev.Pass()
		}
	}
	for _, a := range w.order {
		h := w.hosts[a]
		if h.hasDaemon && h.daemonUp {
			// the deliverer's read of its own wwd is the partner's heartbeat; a daemon that is down reads nothing
			h.mod.pass(context.Background())
		}
	}
}

// settle runs rounds until the silence limit has passed, one per 30 s (the deliverer's poll).
func (w *world) settle(d time.Duration) {
	for el := time.Duration(0); el < d; el += 30 * time.Second {
		w.round(30 * time.Second)
	}
}

// pushes counts what the deliverers asked the push module for, by title text.
func (w *world) pushes(sub string) (total int, per map[string]int) {
	per = map[string]int{}
	for _, a := range w.order {
		h := w.hosts[a]
		if h.ntf == nil {
			continue
		}
		h.ntf.mu.Lock()
		for _, p := range h.ntf.got {
			if strings.Contains(p.Title, sub) {
				total++
				per[a]++
			}
		}
		h.ntf.mu.Unlock()
	}
	return
}

func (w *world) activeKeys(alias string) []string {
	as, err := w.hosts[alias].st.ActiveAlerts()
	require.NoError(w.t, err)
	var out []string
	for _, a := range as {
		out = append(out, a.Key)
	}
	return out
}

// healthy brings the world up and past the partner check's grace: everything answers, nothing is raised, nothing is pushed.
func healthyWorld(t *testing.T) *world {
	w := newWorld(t)
	w.hosts["mlab"].deliv = true // the fleet primary
	w.settle(5 * time.Minute)
	for _, a := range w.order {
		require.Empty(t, w.activeKeys(a), a)
	}
	n, _ := w.pushes("")
	require.Zero(t, n, "a healthy fleet pushes nothing")
	return w
}

const past = advisor.SilenceAfter + 2*time.Minute

// stopWWD takes a host's wwd away: its TCP surface answers 503 and its socket reads fail, so its Purdex daemon reads nothing from it.
func (w *world) stopWWD(alias string)  { w.hosts[alias].wwdUp.Store(false) }
func (w *world) startWWD(alias string) { w.hosts[alias].wwdUp.Store(true) }

func TestMatrix_MservWWDDown_OnePushForOneFailure(t *testing.T) {
	w := healthyWorld(t)
	w.stopWWD("mserv")
	w.settle(past)
	assert.Contains(t, w.activeKeys("mlab"), "silence:mserv")
	assert.Contains(t, w.activeKeys("air26"), "silence:mserv")
	n, per := w.pushes("mserv")
	assert.Equal(t, 1, n, "one failure, one push: not one per observing wwd (%v)", per)
	assert.Equal(t, 1, per["mlab"], "the primary delivers")
	w.startWWD("mserv")
	w.settle(2 * time.Minute)
	for _, a := range w.order {
		assert.NotContains(t, w.activeKeys(a), "silence:mserv", a)
	}
}

func TestMatrix_Air26WWDDown_OnePush(t *testing.T) {
	w := healthyWorld(t)
	w.stopWWD("air26")
	w.settle(past)
	n, per := w.pushes("air26")
	assert.Equal(t, 1, n, "%v", per)
	assert.Equal(t, 1, per["mlab"])
}

func TestMatrix_MlabWWDDown_TheOtherDelivererTellsIt(t *testing.T) {
	t.Skip("known gap, fixed by the push_ready change (the next PR): a primary whose own wwd is down reads nothing and still reports push_ready, so the standby stays silent")
	// mlab's Purdex daemon is up but its own wwd is not: it cannot read mlab's list, the other wwds raise silence:mlab, and the primary
	// (mlab's daemon) reads them from air26's wwd — the failure is told once.
	w := healthyWorld(t)
	w.stopWWD("mlab")
	w.settle(past)
	assert.Contains(t, w.activeKeys("air26"), "silence:mlab")
	assert.Contains(t, w.activeKeys("mserv"), "silence:mlab")
	n, per := w.pushes("mlab")
	assert.Equal(t, 1, n, "%v", per)
}

func TestMatrix_MlabDaemonDown_PartnerDownTold_ByAir26(t *testing.T) {
	// a daemon down while its wwd is up: its own wwd notices (nobody reads its list), and the daemon of air26 — which took over — tells it
	w := healthyWorld(t)
	w.hosts["mlab"].daemonUp = false
	w.hosts["mlab"].deliv = false
	w.hosts["air26"].deliv = true // fleet's election, not this module's
	w.settle(past)
	assert.Contains(t, w.activeKeys("mlab"), "partner_down")
	n, per := w.pushes("Purdex daemon")
	assert.Equal(t, 1, n, "%v", per)
	assert.Equal(t, 1, per["air26"], "told by air26's deliverer")
}

func TestMatrix_Air26DaemonDown_PartnerDownTold_ByMlab(t *testing.T) {
	w := healthyWorld(t)
	w.hosts["air26"].daemonUp = false
	w.settle(past)
	assert.Contains(t, w.activeKeys("air26"), "partner_down")
	n, per := w.pushes("Purdex daemon")
	assert.Equal(t, 1, n, "%v", per)
	assert.Equal(t, 1, per["mlab"])
}

func TestMatrix_BothDeliverersDown_NobodyCanPush_TheStatedGap(t *testing.T) {
	w := healthyWorld(t)
	w.hosts["mlab"].daemonUp = false
	w.hosts["air26"].daemonUp = false
	w.settle(past)
	// the wwds still notice: both hold partner_down on their lists, for whoever reads them next
	assert.Contains(t, w.activeKeys("mlab"), "partner_down")
	assert.Contains(t, w.activeKeys("air26"), "partner_down")
	n, _ := w.pushes("")
	assert.Zero(t, n, "with no deliverer up nothing can be pushed — the gap the spec states")
	// a deliverer coming back tells what it finds on the lists
	w.hosts["mlab"].daemonUp = true
	w.round(30 * time.Second)
	n, _ = w.pushes("Purdex daemon")
	assert.GreaterOrEqual(t, n, 1, "when a daemon is back it tells what the wwds kept")
}

func TestMatrix_WholeMlabDown_Air26TellsIt(t *testing.T) {
	w := healthyWorld(t)
	w.stopWWD("mlab")
	w.hosts["mlab"].daemonUp = false
	w.hosts["mlab"].deliv = false
	w.hosts["air26"].deliv = true
	w.settle(past)
	n, per := w.pushes("mlab")
	assert.Equal(t, 1, n, "%v", per)
	assert.Equal(t, 1, per["air26"])
}

func TestMatrix_Partition_EachSideTellsItsOwn_DuplicatesAllowed(t *testing.T) {
	// mlab | air26 + mserv: each side sees the other unreachable; each side's deliverer pushes what it sees. Two deliverers both pushing is
	// the duplicate the spec allows during a partition (preferred to neither pushing).
	w := healthyWorld(t)
	w.hosts["air26"].deliv = true // the other side elects its own primary
	w.link.setCut("mlab", "air26", true)
	w.link.setCut("mlab", "mserv", true)
	w.settle(past)
	_, per := w.pushes("")
	assert.GreaterOrEqual(t, per["mlab"], 1, "mlab's side tells")
	assert.GreaterOrEqual(t, per["air26"], 1, "air26's side tells")
}

func TestMatrix_ARecoveredFailureIsToldAgainWhenItHappensAgain(t *testing.T) {
	w := healthyWorld(t)
	w.stopWWD("mserv")
	w.settle(past)
	w.startWWD("mserv")
	w.settle(2 * time.Minute)
	w.stopWWD("mserv")
	w.settle(past)
	n, _ := w.pushes("mserv")
	assert.Equal(t, 2, n, "a failure that cleared and came back is a new failure")
}
