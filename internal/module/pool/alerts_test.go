package poolmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// alertsWWD answers GET /v1/alerts with the given status and body and records what it was asked.
func alertsWWD(t *testing.T, s *seen, status int, body string) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/alerts", func(w http.ResponseWriter, r *http.Request) {
		s.add(r)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return mux
}

func alertJSON(key, kind, detail string) string {
	b, _ := json.Marshal(map[string]any{"key": key, "kind": kind, "level": "push", "since": "2026-10-20T01:00:00Z",
		"last": "2026-10-20T01:05:00Z", "cleared_at": nil, "detail": detail})
	return string(b)
}

// A host's alerts, asked of the local wwd with that host; each carries what wwd said, and a silence carries which one it is. A device may read it.
func TestAlerts_ReadsAHostsAlertsThroughTheLocalWWD(t *testing.T) {
	var s seen
	body := `{"host":"air19","alerts":[` + alertJSON("disk:air19", "disk_low", "air19 剩 3 GB") + `,` +
		alertJSON("silence:a", "silence", wire.SilenceDetailHostDown("a")) + `,` +
		alertJSON("silence:b", "silence", wire.SilenceDetailWwdDown("b")) + `,` +
		alertJSON("silence:c", "silence", "something else") + `]}`
	mux := newModule(t, fakeWWD(t, alertsWWD(t, &s, 200, body)))
	for name, ctx := range map[string]func(context.Context) context.Context{"admin": asAdmin, "device": asDevice} {
		rr := send(mux, "GET", "/api/pool/hosts/air19/alerts", "", ctx)
		require.Equal(t, 200, rr.Code, "%s: %s", name, rr.Body.String())
		var got struct {
			Host   string
			Known  bool
			Alerts []map[string]any
		}
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got), name)
		assert.Equal(t, "air19", got.Host)
		assert.True(t, got.Known)
		require.Len(t, got.Alerts, 4)
		_, hasSilence := got.Alerts[0]["silence"]
		assert.False(t, hasSilence, "an alert that is not a silence has no silence key")
		assert.Equal(t, "host_down", got.Alerts[1]["silence"])
		assert.Equal(t, "wwd_down", got.Alerts[2]["silence"])
		assert.Equal(t, "unknown", got.Alerts[3]["silence"])
		assert.Equal(t, "air19 剩 3 GB", got.Alerts[0]["detail"], "wwd's own fields are kept")
		assert.Contains(t, got.Alerts[0], "cleared_at", "null stays null, not dropped")
	}
	assert.Contains(t, s.all()[0], "GET /v1/alerts host=air19")
}

// A host with no alerts is `[]`, never null; one whose wwd predates the route is "not known", never a 500.
func TestAlerts_EmptyIsAnEmptyListAndAnOldHostIsNotKnown(t *testing.T) {
	var s seen
	mux := newModule(t, fakeWWD(t, alertsWWD(t, &s, 200, `{"host":"air19","alerts":[]}`)))
	rr := send(mux, "GET", "/api/pool/hosts/air19/alerts", "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"host":"air19","known":true,"alerts":[]}`, rr.Body.String())

	mux = newModule(t, fakeWWD(t, alertsWWD(t, &s, 404, `404 page not found`)))
	rr = send(mux, "GET", "/api/pool/hosts/mserv/alerts", "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"host":"mserv","known":false,"alerts":[]}`, rr.Body.String())
}

func TestAlerts_TheErrorsAreTheOnesTheOtherReadsHave(t *testing.T) {
	var s seen
	mux := newModule(t, fakeWWD(t, alertsWWD(t, &s, 404, `{"error":"unknown host","host":"nowhere"}`)))
	rr := send(mux, "GET", "/api/pool/hosts/nowhere/alerts", "", asAdmin)
	assert.Equal(t, 404, rr.Code)
	assert.Equal(t, "wwd_rejected", errCode(rr), "wwd's own 4xx is passed on")

	mux = newModule(t, fakeWWD(t, alertsWWD(t, &s, 502, `{"host":"air26","reason":"x"}`)))
	assert.Equal(t, 502, send(mux, "GET", "/api/pool/hosts/air26/alerts", "", asAdmin).Code)

	down := newModule(t, client.New(filepath.Join(t.TempDir(), "none.sock"))) // no wwd at all
	rr = send(down, "GET", "/api/pool/hosts/air26/alerts", "", asAdmin)
	assert.Equal(t, 503, rr.Code)
	assert.Equal(t, "wwd_unavailable", errCode(rr))

	n := len(s.all())
	rr = send(mux, "GET", "/api/pool/hosts/a%00b/alerts", "", asAdmin)
	assert.Equal(t, 400, rr.Code, "a bad alias never reaches wwd")
	assert.Equal(t, n, len(s.all()))
}

// A device token reads alerts and writes nothing: there is no write route under the alerts path.
func TestAlerts_NoWriteRoute(t *testing.T) {
	var s seen
	mux := newModule(t, fakeWWD(t, alertsWWD(t, &s, 200, `{"host":"air19","alerts":[]}`)))
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		rr := send(mux, method, "/api/pool/hosts/air19/alerts", "{}", asAdmin)
		assert.Contains(t, []int{404, 405}, rr.Code, method)
	}
}

// The answer means what the golden the App copies says (internal/wwd/wire/testdata): compared as a JSON value.
func TestAlerts_AnswerMeansTheGoldenTheAppCopies(t *testing.T) {
	golden, err := os.ReadFile("../../wwd/wire/testdata/pool_host_alerts.json")
	require.NoError(t, err)
	var g struct {
		Host   string            `json:"host"`
		Alerts []json.RawMessage `json:"alerts"`
	}
	require.NoError(t, json.Unmarshal(golden, &g))
	// what wwd answers is the same alerts without the silence key
	var raw []string
	for _, a := range g.Alerts {
		var m map[string]any
		require.NoError(t, json.Unmarshal(a, &m))
		delete(m, "silence")
		b, _ := json.Marshal(m)
		raw = append(raw, string(b))
	}
	var s seen
	mux := newModule(t, fakeWWD(t, alertsWWD(t, &s, 200, `{"host":"`+g.Host+`","alerts":[`+strings.Join(raw, ",")+`]}`)))
	rr := send(mux, "GET", "/api/pool/hosts/"+g.Host+"/alerts", "", asAdmin)
	require.Equal(t, 200, rr.Code, rr.Body.String())
	assert.JSONEq(t, string(golden), rr.Body.String())
}

// ---- the alerts watch ----

// liveAlerts is a wwd whose pool, per-host alerts and event stream the test controls.
type liveAlerts struct {
	livePool
	amu    sync.Mutex
	alerts map[string][]wire.Alert // by the `host` query ("" is the local host)
	down   bool                    // alerts answer 503
	old    map[string]bool         // hosts whose wwd predates the route: 404
	asked  []string
	events chan string // a line to write to the event stream
}

func newLiveAlerts(t *testing.T) *liveAlerts {
	t.Helper()
	la := &liveAlerts{livePool: *newLivePool(t), alerts: map[string][]wire.Alert{}, events: make(chan string, 16)}
	return la
}

func (la *liveAlerts) set(host string, list []wire.Alert) {
	la.amu.Lock()
	defer la.amu.Unlock()
	la.alerts[host] = list
}

func (la *liveAlerts) setOld(host string, v bool) {
	la.amu.Lock()
	defer la.amu.Unlock()
	if la.old == nil {
		la.old = map[string]bool{}
	}
	la.old[host] = v
}

func (la *liveAlerts) setDown(v bool) {
	la.amu.Lock()
	defer la.amu.Unlock()
	la.down = v
}

func (la *liveAlerts) askedHosts() []string {
	la.amu.Lock()
	defer la.amu.Unlock()
	return append([]string(nil), la.asked...)
}

func (la *liveAlerts) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/pool", la.livePool.handler())
	mux.HandleFunc("GET /v1/alerts", func(w http.ResponseWriter, r *http.Request) {
		la.amu.Lock()
		host := r.URL.Query().Get("host")
		la.asked = append(la.asked, host)
		down, list, old := la.down, la.alerts[host], la.old[host]
		la.amu.Unlock()
		if old {
			http.NotFound(w, r)
			return
		}
		if down {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		shown := host
		if shown == "" {
			shown = "mlab"
		}
		if list == nil {
			list = []wire.Alert{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wire.AlertList{Host: shown, Alerts: list})
	})
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for {
			select {
			case line := <-la.events:
				_, _ = w.Write([]byte("data: " + line + "\n\n"))
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
	return mux
}

func (la *liveAlerts) alertEvent(host string) {
	la.events <- `{"type":"alert","data":{"host":"` + host + `"}}`
}

func anAlert(key string, detail string) wire.Alert {
	t := time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC)
	return wire.Alert{Key: key, Kind: wire.AlertDiskLow, Level: wire.AlertPush, Since: t, Last: t, Detail: detail}
}

// alertsNoticed records the alerts.changed the module sent.
type alertsNoticed struct {
	mu  sync.Mutex
	got []notice
}

func (n *alertsNoticed) emit(ev core.HostEvent) {
	var p wire.PoolChanged
	if ev.Type != EventAlertsChanged || json.Unmarshal([]byte(ev.Value), &p) != nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.got = append(n.got, notice{p.Host, time.Now()})
}

func (n *alertsNoticed) hosts() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for _, x := range n.got {
		out = append(out, x.host)
	}
	return out
}

const testAlertsEvery = 100 * time.Millisecond

// watchingAlerts starts a module on la; set changes it before Start (the loops read their timings without a lock).
func watchingAlerts(t *testing.T, la *liveAlerts, set ...func(*Module)) (*Module, *alertsNoticed) {
	t.Helper()
	m := New()
	m.cl = fakeWWD(t, la.handler())
	m.pollEvery, m.mergeWithin, m.alertsEvery = testPoll, 20*time.Millisecond, testAlertsEvery
	n := &alertsNoticed{}
	m.emit = n.emit
	m.logf = func(string, ...any) {}
	for _, f := range set {
		f(m)
	}
	require.NoError(t, m.Start(context.Background()))
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m, n
}

// The first look at a host's alerts is only its baseline, and the capability is announced only while the watch runs.
func TestAlertsWatch_TheFirstReadIsOnlyTheBaselineAndTheCapabilityFollowsTheWatch(t *testing.T) {
	la := newLiveAlerts(t)
	la.set("", []wire.Alert{anAlert("disk:mlab", "x")})
	m, n := watchingAlerts(t, la)
	assert.Equal(t, true, m.Status()["alerts"])
	time.Sleep(3 * testAlertsEvery)
	assert.Empty(t, n.hosts())
	require.NoError(t, m.Stop(context.Background()))
	assert.Equal(t, false, m.Status()["alerts"], "not announced after Stop")
	assert.Equal(t, false, New().Status()["alerts"], "nor before Start")
}

// The local host is asked without a host, a peer by its alias; a change of a peer's list is announced at the next regular read, and a list that
// did not change is not announced however many times it is read.
func TestAlertsWatch_APeersChangeIsAnnouncedAtTheNextRead(t *testing.T) {
	la := newLiveAlerts(t)
	_, n := watchingAlerts(t, la)
	time.Sleep(3 * testAlertsEvery) // baselines
	require.Contains(t, la.askedHosts(), "")
	require.Contains(t, la.askedHosts(), "air19")
	assert.Empty(t, n.hosts(), "nothing changed")

	la.set("air19", []wire.Alert{anAlert("disk:air19", "air19 剩 3 GB")})
	require.Eventually(t, func() bool { return len(n.hosts()) == 1 }, 3*time.Second, testPoll)
	assert.Equal(t, []string{"air19"}, n.hosts())
	time.Sleep(3 * testAlertsEvery)
	assert.Len(t, n.hosts(), 1, "read again, unchanged: no second notice")
}

// `last` moves at every evaluation of a rule that still holds; that alone is not news.
func TestAlertsWatch_TheLastTimeMovingIsNotNews(t *testing.T) {
	la := newLiveAlerts(t)
	a := anAlert("disk:air19", "x")
	la.set("air19", []wire.Alert{a})
	_, n := watchingAlerts(t, la)
	time.Sleep(3 * testAlertsEvery)
	a.Last = a.Last.Add(time.Minute)
	la.set("air19", []wire.Alert{a})
	time.Sleep(4 * testAlertsEvery)
	assert.Empty(t, n.hosts())
	cleared := a.Last.Add(time.Minute)
	a.ClearedAt = &cleared
	la.set("air19", []wire.Alert{a})
	require.Eventually(t, func() bool { return len(n.hosts()) == 1 }, 3*time.Second, testPoll, "clearing is news")
}

// The local wwd's alert event reads the local host's list at once, not at the next turn; the regular read is made slow so that only the event
// can be what noticed.
func TestAlertsWatch_TheLocalAlertEventReadsAtOnce(t *testing.T) {
	la := newLiveAlerts(t)
	_, n := watchingAlerts(t, la, func(m *Module) { m.alertsEvery = time.Hour })
	require.Eventually(t, func() bool { return len(la.askedHosts()) >= 2 }, 3*time.Second, testPoll, "the baseline is taken soon, not a turn later")
	la.set("", []wire.Alert{anAlert("disk:mlab", "x")})
	la.alertEvent("mlab")
	require.Eventually(t, func() bool { return len(n.hosts()) >= 1 }, 3*time.Second, testPoll)
	assert.Equal(t, "mlab", n.hosts()[0])
	assert.NotContains(t, n.hosts(), "air19", "the event reads the local host only")
}

// Changes inside the window are merged into one notice per host.
func TestAlertsWatch_MergedWithinTheWindow(t *testing.T) {
	la := newLiveAlerts(t)
	_, n := watchingAlerts(t, la, func(m *Module) { m.mergeWithin = 400 * time.Millisecond })
	time.Sleep(3 * testAlertsEvery)
	for i := 0; i < 3; i++ {
		la.set("air19", []wire.Alert{anAlert("disk:air19", strings.Repeat("x", i+1))})
		time.Sleep(testAlertsEvery + 20*time.Millisecond)
	}
	require.Eventually(t, func() bool { return len(n.hosts()) >= 1 }, 3*time.Second, testPoll)
	time.Sleep(600 * time.Millisecond)
	assert.LessOrEqual(t, len(n.hosts()), 2, "three changes inside a window are not three notices: %v", n.hosts())
}

// While the local wwd cannot be read every host looked at is announced once; when it is back, every one of them is announced once more.
func TestAlertsWatch_AwayAndBackEachAnnounceEveryHostOnce(t *testing.T) {
	la := newLiveAlerts(t)
	_, n := watchingAlerts(t, la)
	time.Sleep(3 * testAlertsEvery)
	require.Empty(t, n.hosts())

	la.setDown(true)
	require.Eventually(t, func() bool { return len(n.hosts()) == 2 }, 3*time.Second, testPoll)
	time.Sleep(4 * testAlertsEvery)
	assert.Len(t, n.hosts(), 2, "away: once per host, not at every read")

	la.setDown(false)
	require.Eventually(t, func() bool { return len(n.hosts()) == 4 }, 3*time.Second, testPoll)
	time.Sleep(4 * testAlertsEvery)
	assert.Len(t, n.hosts(), 4, "back: once per host")
	assert.ElementsMatch(t, []string{"mlab", "air19", "mlab", "air19"}, n.hosts())
}

// Stop ends all the loops the module started and nothing is announced after it returns.
func TestAlertsWatch_NothingAfterStop(t *testing.T) {
	la := newLiveAlerts(t)
	m, n := watchingAlerts(t, la)
	time.Sleep(2 * testAlertsEvery)
	require.NoError(t, m.Stop(context.Background()))
	before := len(la.askedHosts())
	la.set("air19", []wire.Alert{anAlert("disk:air19", "x")})
	time.Sleep(4 * testAlertsEvery)
	assert.Equal(t, before, len(la.askedHosts()), "no read after Stop")
	assert.Empty(t, n.hosts())
}

// The real module in a real core: pool.alerts.v1 is declared by /api/info while the module's watch runs, and neither before Start nor after Stop
// (the lesson of fleet.v1: a capability the module's Status does not feed is announced by nothing).
func TestAlertsWatch_TheRealModuleAnnouncesPoolAlertsV1WhileItWatches(t *testing.T) {
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
	require.NotContains(t, caps(), "pool.alerts.v1", "not before Start")
	require.NoError(t, m.Start(context.Background()))
	require.Contains(t, caps(), "pool.alerts.v1")
	require.NoError(t, m.Stop(context.Background()))
	require.NotContains(t, caps(), "pool.alerts.v1", "not after Stop")
}

// An App that has just read the route and is told alerts.changed reads the new list, not the answer kept for the last second: a notice
// drops what the route kept for that host. (Polling stopped with pool.alerts.v1, so a stale read would stay.)
func TestAlertsWatch_ANoticeDropsTheAnswerKeptForThatHost(t *testing.T) {
	la := newLiveAlerts(t)
	m, n := watchingAlerts(t, la)
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	time.Sleep(3 * testAlertsEvery) // baselines
	first := send(mux, "GET", "/api/pool/hosts/air19/alerts", "", asAdmin)
	require.Equal(t, 200, first.Code)
	assert.JSONEq(t, `{"host":"air19","known":true,"alerts":[]}`, first.Body.String())

	la.set("air19", []wire.Alert{anAlert("disk:air19", "x")})
	require.Eventually(t, func() bool { return len(n.hosts()) == 1 }, 3*time.Second, testPoll)
	again := send(mux, "GET", "/api/pool/hosts/air19/alerts", "", asAdmin)
	assert.Contains(t, again.Body.String(), "disk:air19", "the read after the notice is the new list: %s", again.Body.String())
}

// A peer whose wwd gains the route turns known:false into known:true with the same (empty) list; that is a change the App must hear.
func TestAlertsWatch_KnownChangingIsNews(t *testing.T) {
	la := newLiveAlerts(t)
	la.setOld("air19", true)
	_, n := watchingAlerts(t, la)
	time.Sleep(3 * testAlertsEvery)
	require.Empty(t, n.hosts())
	la.setOld("air19", false)
	require.Eventually(t, func() bool { return len(n.hosts()) == 1 }, 3*time.Second, testPoll)
	assert.Equal(t, []string{"air19"}, n.hosts())
}

// If the local alert event is already waiting when wwd comes back, the read that follows is only the local host's; the peers are still read
// and announced once more after it (their lists may have changed while wwd was away).
func TestAlertsWatch_RecoveryThroughAKickStillAnnouncesThePeers(t *testing.T) {
	la := newLiveAlerts(t)
	_, n := watchingAlerts(t, la, func(m *Module) { m.alertsEvery = time.Hour })
	require.Eventually(t, func() bool { return len(la.askedHosts()) >= 2 }, 3*time.Second, testPoll)

	la.setDown(true)
	la.alertEvent("mlab")
	require.Eventually(t, func() bool { return len(n.hosts()) == 2 }, 3*time.Second, testPoll, "away: every host once")

	la.set("air19", []wire.Alert{anAlert("disk:air19", "x")})
	la.setDown(false)
	la.alertEvent("mlab") // the kick that comes first when wwd is back
	require.Eventually(t, func() bool { return countOf(n.hosts(), "air19") >= 2 }, 3*time.Second, testPoll, "back: the peer too, not only the local host: %v", n.hosts())
}

func countOf(list []string, s string) int {
	c := 0
	for _, x := range list {
		if x == s {
			c++
		}
	}
	return c
}

// A pool that can still be read but names no host lets every host looked at before leave: each is announced once and forgotten, and the
// watch carries on (a later pool with hosts is a new baseline, not news).
func TestAlertsWatch_AnEmptyPoolLetsEveryHostLeave(t *testing.T) {
	la := newLiveAlerts(t)
	_, n := watchingAlerts(t, la)
	time.Sleep(3 * testAlertsEvery) // baselines for mlab and air19
	require.Empty(t, n.hosts())

	la.change(func(p *wire.Pool) { p.Peers = nil })
	require.Eventually(t, func() bool { return len(n.hosts()) == 2 }, 3*time.Second, testPoll)
	assert.ElementsMatch(t, []string{"mlab", "air19"}, n.hosts())
	time.Sleep(3 * testAlertsEvery)
	assert.Len(t, n.hosts(), 2, "once each, not at every turn")
}

// An alias is one host: the local host's name listed again among the peers, or a peer twice, is asked about once, and the local host is
// the one asked without a host whatever else the pool lists.
func TestAlertsWatch_AnAliasIsAskedAboutOnce(t *testing.T) {
	la := newLiveAlerts(t)
	la.change(func(p *wire.Pool) {
		again := p.Peers[1]
		dupLocal := p.Peers[0]
		p.Peers = append(p.Peers, again, dupLocal) // air19 twice, mlab (the local one) among the peers
	})
	_, _ = watchingAlerts(t, la, func(m *Module) { m.alertsEvery = time.Hour }) // one pass only
	require.Eventually(t, func() bool { return len(la.askedHosts()) >= 2 }, 3*time.Second, testPoll)
	time.Sleep(3 * testAlertsEvery)
	asked := la.askedHosts()
	assert.ElementsMatch(t, []string{"", "air19"}, asked, "the local host once without a host, air19 once, mlab never by name")
}

// A restart does not read the last run's hosts: until its own watch has read the pool, the alerts watch asks no one.
func TestAlertsWatch_StartDoesNotAskTheLastRunsHosts(t *testing.T) {
	la := newLiveAlerts(t)
	hung := make(chan struct{})
	t.Cleanup(func() { close(hung) })
	h := la.handler()
	m := New()
	m.cl = fakeWWD(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/pool" { // the new run's pool read does not come back
			select {
			case <-hung:
			case <-r.Context().Done():
			}
			return
		}
		h.ServeHTTP(w, r)
	}))
	m.pollEvery, m.mergeWithin, m.alertsEvery = testPoll, 20*time.Millisecond, testAlertsEvery
	m.logf = func(string, ...any) {}
	m.hosts = &poolHosts{local: "stale", others: []string{"gone"}} // what the last run left
	m.alertKick <- struct{}{}                                      // and an alert event it had not answered
	require.NoError(t, m.Start(context.Background()))
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	time.Sleep(4 * testAlertsEvery)
	assert.Empty(t, la.askedHosts(), "no alert read from the last run's topology")
}
