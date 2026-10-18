package daemon

import (
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/wire"
)

// obsDaemon is a daemon whose observer holds what a test gives it; "now" is obt0 + 8 days.
func obsDaemon(t *testing.T, alias string, token string, items ...wire.Observation) *Daemon {
	t.Helper()
	tr, st := &fakeTracker{next: items}, &memStore{}
	o := newObserver(tr, st)
	o.Observe(obt0, facts.ProcSnapshot{}, facts.ProcContext{})
	return New(Options{
		HostID: "id-" + alias, Alias: alias, Version: "v0", Token: token, Observer: o,
		Now: func() time.Time { return obt0.Add(8 * 24 * time.Hour) },
	})
}

func obsItems() []wire.Observation {
	return []wire.Observation{
		ended("old", "make", 2*24*time.Hour, 100),                // 6 days before "now": inside 7 days, outside 24 h
		ended("recent", "go test", 7*24*time.Hour+time.Hour, 80), // inside both
		ended("older", "pnpm vitest run", -10*24*time.Hour, 90),  // 18 days before "now": outside both
	}
}

func TestObservationsRouteDefaultsToTheLastDay(t *testing.T) {
	d := obsDaemon(t, "mlab", testToken, obsItems()...)
	w := do(d.SocketHandler(), "GET", "/v1/observations", "")
	r, err := wire.Decode[wire.ObservationsResponse](w.Body.Bytes())
	if w.Code != 200 || err != nil || r.Host != "mlab" || len(r.Observations) != 1 || r.Observations[0].ID != "recent" {
		t.Fatalf("%d %s (%v)", w.Code, w.Body, err)
	}
	if !r.Since.Equal(obt0.Add(7 * 24 * time.Hour)) {
		t.Fatalf("since: %v", r.Since)
	}
}

func TestObservationsRouteHonoursSince(t *testing.T) {
	d := obsDaemon(t, "mlab", testToken, obsItems()...)
	q := url.Values{"since": {obt0.Add(-3 * 24 * time.Hour).Format(time.RFC3339)}}
	w := do(d.SocketHandler(), "GET", "/v1/observations?"+q.Encode(), "")
	r, err := wire.Decode[wire.ObservationsResponse](w.Body.Bytes())
	if err != nil || len(r.Observations) != 2 || r.Observations[0].ID != "old" || r.Observations[1].ID != "recent" {
		t.Fatalf("%s (%v)", w.Body, err)
	}
	for _, bad := range []string{"yesterday", "2026-10-18", "1"} {
		w := do(d.SocketHandler(), "GET", "/v1/observations?since="+url.QueryEscape(bad), "")
		if w.Code != 400 {
			t.Errorf("since=%q: %d", bad, w.Code)
		}
	}
}

func TestUndeclaredRouteDefaultsToSevenDaysAndGroups(t *testing.T) {
	d := obsDaemon(t, "mlab", testToken, obsItems()...)
	w := do(d.SocketHandler(), "GET", "/v1/observations/undeclared", "")
	r, err := wire.Decode[wire.UndeclaredResponse](w.Body.Bytes())
	if w.Code != 200 || err != nil || r.Host != "mlab" || len(r.Groups) != 2 {
		t.Fatalf("%d %s (%v)", w.Code, w.Body, err)
	}
	if r.Groups[0].Signature != "make" || r.Groups[1].Signature != "go test" || r.Groups[0].LastVia != wire.ObsViaSession || r.Groups[0].LastSession != "_s1" {
		t.Fatalf("%+v", r.Groups)
	}
	if !r.Since.Equal(obt0.Add(24 * time.Hour)) {
		t.Fatalf("since: %v", r.Since)
	}
}

func TestObservationRoutesAnEmptyAnswerIsAnEmptyList(t *testing.T) {
	d := obsDaemon(t, "mlab", testToken)
	for _, p := range []string{"/v1/observations", "/v1/observations/undeclared"} {
		w := do(d.SocketHandler(), "GET", p, "")
		if w.Code != 200 || !contains(w.Body.String(), "[]") {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestObservationRoutesNeedTheTokenOnTCP(t *testing.T) {
	h := obsDaemon(t, "mlab", testToken, obsItems()...).TCPHandler()
	for _, p := range []string{"/v1/observations", "/v1/observations/undeclared"} {
		if w := do(h, "GET", p, ""); w.Code != 401 {
			t.Errorf("%s without a token: %d", p, w.Code)
		}
		if w := do(h, "GET", p, "Bearer "+testToken); w.Code != 200 {
			t.Errorf("%s with the token: %d", p, w.Code)
		}
		if w := do(h, "GET", p+"?host=air26", "Bearer "+testToken); w.Code != 400 {
			t.Errorf("%s with host over TCP: %d", p, w.Code)
		}
	}
}

func TestObservationRoutesForwardToAPeer(t *testing.T) {
	peer := obsDaemon(t, "air26", testToken, ended("p1", "swift build", 7*24*time.Hour+time.Hour, 200))
	srv := httptest.NewServer(peer.TCPHandler())
	t.Cleanup(srv.Close)
	local := New(Options{
		HostID: "id-mlab", Alias: "mlab", Version: "v0", Token: testToken, Peers: []PeerAddr{{"air26", addr(srv)}},
		Observer: obsDaemon(t, "mlab", testToken).opts.Observer, Now: func() time.Time { return obt0.Add(8 * 24 * time.Hour) },
	})
	w := do(local.SocketHandler(), "GET", "/v1/observations/undeclared?host=air26", "")
	r, err := wire.Decode[wire.UndeclaredResponse](w.Body.Bytes())
	if w.Code != 200 || err != nil || r.Host != "air26" || len(r.Groups) != 1 || r.Groups[0].Signature != "swift build" {
		t.Fatalf("the peer's own groups expected: %d %s (%v)", w.Code, w.Body, err)
	}
	if w := do(local.SocketHandler(), "GET", "/v1/observations?host=nowhere", ""); w.Code != 404 {
		t.Fatalf("unknown host: %d", w.Code)
	}
}

// The whole path with the real tracker: a heavy command under a session ends, and the route says so.
func TestObservationRouteServesWhatTheRealTrackerRecorded(t *testing.T) {
	st := &memStore{}
	o := NewObserver("mlab", facts.ObserveConfig{MinCPUSeconds: 60, MinMemMB: 1024, Interval: 5 * time.Second}, st)
	args := map[int][]string{200: {"make", "build"}}
	ctx := func() facts.ProcContext {
		return facts.ProcContext{
			SessionOf: func(pid int) (string, bool) { return "_sess01", pid == 100 },
			ArgsOf:    func(pid int) []string { return args[pid] },
		}
	}
	mb := func(n uint64) uint64 { return n << 20 }
	base := []facts.ProcRec{{PID: 1, Name: "launchd"}, {PID: 100, PPID: 1, Name: "claude", FootprintBytes: mb(5)}}
	withMake := append(append([]facts.ProcRec(nil), base...), facts.ProcRec{PID: 200, PPID: 100, Name: "make", CPUPct: 100, FootprintBytes: mb(2000)})
	o.Observe(obt0, facts.ProcSnapshot{Procs: withMake}, ctx())
	o.Observe(obt0.Add(5*time.Second), facts.ProcSnapshot{Procs: withMake}, ctx())
	d := New(Options{HostID: "id", Alias: "mlab", Version: "v0", Token: testToken, Observer: o, Now: func() time.Time { return obt0.Add(time.Hour) }})

	w := do(d.SocketHandler(), "GET", "/v1/observations", "")
	r, err := wire.Decode[wire.ObservationsResponse](w.Body.Bytes())
	if err != nil || len(r.Observations) != 1 || r.Observations[0].EndedAt != nil || r.Observations[0].Session != "_sess01" || r.Observations[0].Signature != "make build" {
		t.Fatalf("running and over the memory threshold: %s (%v)", w.Body, err)
	}
	for i := 2; i < 4; i++ { // the command is gone for two samples: it ended
		o.Observe(obt0.Add(time.Duration(i)*5*time.Second), facts.ProcSnapshot{Procs: base}, ctx())
	}
	w = do(d.SocketHandler(), "GET", "/v1/observations/undeclared", "")
	u, err := wire.Decode[wire.UndeclaredResponse](w.Body.Bytes())
	if err != nil || len(u.Groups) != 1 || u.Groups[0].Signature != "make build" || u.Groups[0].Count != 1 || u.Groups[0].LastVia != wire.ObsViaSession || u.Groups[0].MaxMemMB != 2000 {
		t.Fatalf("%s (%v)", w.Body, err)
	}
	if len(st.rows) != 1 || st.rows[0].EndedAt == nil {
		t.Fatalf("the ended one is in the store: %+v", st.rows)
	}
}

func TestObservationRoutesAreNotRegisteredWithoutAnObserver(t *testing.T) {
	d := New(Options{HostID: "id", Alias: "mlab", Version: "v0", Token: testToken})
	for _, p := range []string{"/v1/observations", "/v1/observations/undeclared"} {
		if w := do(d.SocketHandler(), "GET", p, ""); w.Code != 404 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}
