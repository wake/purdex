package daemon

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// stateDaemon is a daemon with a HostState: alias, the shared token, a sampler
// that reads the state, the pressure level read from *level.
func stateDaemon(t *testing.T, alias string, level *int, peers ...PeerAddr) (*Daemon, *rig) {
	t.Helper()
	b := NewBroker()
	r := &rig{now: s0, alive: map[int]bool{}}
	st, err := NewHostState(HostStateOptions{
		Host: alias, OwnerRuleSupported: true, Initial: wire.WorkerSettings{Enabled: true, Slots: wire.Slots{Heavy: 1, Light: 2}},
		Now: func() time.Time { return r.now }, PidAlive: func(p int) bool { return r.alive[p] }, Events: b,
		PidIdent: func(p int) (string, bool) { return "id", r.alive[p] },
	})
	if err != nil {
		t.Fatal(err)
	}
	s := NewSampler(SamplerOptions{HostID: "id-" + alias, Alias: alias, Sources: fakeSources(level, nil), State: st})
	d := New(Options{HostID: "id-" + alias, Alias: alias, Version: "v0", Token: testToken, Sampler: s, State: st, Peers: peers, Broker: b})
	r.tick = func() { s.Tick(r.now); r.now = r.now.Add(5 * time.Second) }
	return d, r
}

type rig struct {
	now   time.Time
	alive map[int]bool
	tick  func()
}

func send(h http.Handler, method, path, auth, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestWorkerRoutesCarryTheDiskFloor(t *testing.T) {
	level := 1
	d, _ := stateDaemon(t, "mlab", &level)
	h := d.TCPHandler()
	auth := "Bearer " + testToken
	floor := func(body []byte) float64 {
		t.Helper()
		ws, err := wire.Decode[wire.WorkerView](body)
		if err != nil || ws.DiskMinGB == nil {
			t.Fatalf("answer %s: %v", body, err)
		}
		return *ws.DiskMinGB
	}
	if w := send(h, "GET", "/v1/worker", auth, ""); w.Code != 200 || floor(w.Body.Bytes()) != wire.DefaultDiskMinGB {
		t.Fatalf("GET: %d %s", w.Code, w.Body)
	}
	w := send(h, "PUT", "/v1/worker", auth, `{"enabled":true,"slots":{"heavy":1,"light":2},"owner_rule":"","disk_min_gb":35}`)
	if w.Code != 200 || floor(w.Body.Bytes()) != 35 {
		t.Fatalf("PUT 35: %d %s", w.Code, w.Body)
	}
	// a body from a client that does not know the field keeps the floor
	w = send(h, "PUT", "/v1/worker", auth, `{"enabled":true,"slots":{"heavy":2,"light":2},"owner_rule":""}`)
	if w.Code != 200 || floor(w.Body.Bytes()) != 35 {
		t.Fatalf("PUT without the floor: %d %s", w.Code, w.Body)
	}
	w = send(h, "PUT", "/v1/worker", auth, `{"enabled":true,"slots":{"heavy":2,"light":2},"owner_rule":"","disk_min_gb":-3}`)
	if w.Code != 400 {
		t.Fatalf("a negative floor: %d %s", w.Code, w.Body)
	}
}

func TestWorkerRoutes(t *testing.T) {
	level := 1
	d, _ := stateDaemon(t, "mlab", &level)
	h := d.TCPHandler()
	auth := "Bearer " + testToken

	w := send(h, "GET", "/v1/worker", auth, "")
	ws, err := wire.Decode[wire.WorkerView](w.Body.Bytes())
	if w.Code != 200 || err != nil || !ws.Enabled || ws.Slots != (wire.Slots{Heavy: 1, Light: 2}) {
		t.Fatalf("GET: %d %s", w.Code, w.Body)
	}
	w = send(h, "PUT", "/v1/worker", auth, `{"enabled":false,"slots":{"heavy":0,"light":1},"owner_rule":"hid<3m"}`)
	if w.Code != 200 {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
	ws, _ = wire.Decode[wire.WorkerView](send(h, "GET", "/v1/worker", auth, "").Body.Bytes())
	if ws.Enabled || ws.Slots != (wire.Slots{Heavy: 0, Light: 1}) || ws.OwnerRule != "hid<3m" {
		t.Fatalf("after PUT: %+v", ws)
	}
	// info follows the switch
	info, _ := wire.Decode[wire.Info](send(h, "GET", "/v1/info", auth, "").Body.Bytes())
	if info.Worker.Enabled {
		t.Error("/v1/info still says the worker is enabled")
	}
	for name, body := range map[string]string{
		"negative slots": `{"enabled":true,"slots":{"heavy":-1,"light":0},"owner_rule":""}`,
		"unknown field":  `{"enabled":true,"slots":{"heavy":1,"light":0},"owner_rule":"","zzz":1}`,
		"not json":       `nope`,
		"empty":          ``,
	} {
		if w := send(h, "PUT", "/v1/worker", auth, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	if send(h, "PUT", "/v1/worker", "", `{}`).Code != http.StatusUnauthorized || send(h, "GET", "/v1/worker", "", "").Code != http.StatusUnauthorized {
		t.Error("the token is required")
	}
	if send(h, "PUT", "/v1/worker", auth, strings.Repeat("x", 100000)).Code != http.StatusBadRequest {
		t.Error("an oversized body must be refused")
	}
}

func TestExceptionRoutesChangeHeadroom(t *testing.T) {
	level := 1
	d, r := stateDaemon(t, "mlab", &level)
	h := d.SocketHandler()
	r.tick()
	if f, _ := d.opts.Sampler.Latest(); f.Derived.Headroom.Heavy != 1 || len(f.Exception) != 0 {
		t.Fatalf("before: %+v", f.Derived)
	}
	until := r.now.Add(time.Hour).Format(time.RFC3339)
	w := send(h, "POST", "/v1/exceptions", "", `{"name":"app-dev-package","until":"`+until+`","pid":null,"holds":["heavy","light"]}`)
	if w.Code != 200 {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	r.tick()
	f, _ := d.opts.Sampler.Latest()
	if f.Derived.Headroom != (wire.Slots{}) || len(f.Exception) != 1 || f.Exception[0].Name != "app-dev-package" {
		t.Fatalf("an exception that holds both must close both lanes: %+v exceptions=%+v", f.Derived, f.Exception)
	}
	if w := send(h, "DELETE", "/v1/exceptions/app-dev-package", "", ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d", w.Code)
	}
	if w := send(h, "DELETE", "/v1/exceptions/app-dev-package", "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("DELETE again: %d", w.Code)
	}
	r.tick()
	if f, _ := d.opts.Sampler.Latest(); f.Derived.Headroom != (wire.Slots{Heavy: 1, Light: 2}) || len(f.Exception) != 0 {
		t.Fatalf("after: %+v", f.Derived)
	}
	for name, body := range map[string]string{
		"past":     `{"name":"x","until":"2020-01-01T00:00:00Z","pid":null,"holds":[]}`,
		"no bound": `{"name":"x","until":null,"pid":null,"holds":[]}`,
		"bad hold": `{"name":"x","until":"` + until + `","pid":null,"holds":["gpu"]}`,
		"junk":     `{`,
	} {
		if w := send(h, "POST", "/v1/exceptions", "", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

func TestAnExceptionThatExpiresLiftsItself(t *testing.T) {
	level := 1
	d, r := stateDaemon(t, "mlab", &level)
	until := r.now.Add(8 * time.Second).Format(time.RFC3339Nano)
	if w := send(d.SocketHandler(), "POST", "/v1/exceptions", "", `{"name":"short","until":"`+until+`","pid":null,"holds":["heavy"]}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	r.tick()
	if f, _ := d.opts.Sampler.Latest(); f.Derived.Headroom.Heavy != 0 {
		t.Fatalf("held: %+v", f.Derived)
	}
	r.tick() // +5 s
	r.tick() // +10 s: past until
	if f, _ := d.opts.Sampler.Latest(); f.Derived.Headroom.Heavy != 1 || len(f.Exception) != 0 {
		t.Fatalf("after expiry: %+v exceptions=%+v", f.Derived, f.Exception)
	}
}

func fwdPath(path, host string) string {
	return path + "?" + url.Values{"host": {host}}.Encode()
}

func TestWorkerAndExceptionMutationsForwardToAPeerFromTheSocketOnly(t *testing.T) {
	level := 1
	remote, rr := stateDaemon(t, "air26", &level)
	srv := httptest.NewServer(remote.TCPHandler())
	defer srv.Close()
	local, lr := stateDaemon(t, "mlab", &level, PeerAddr{"air26", addr(srv)})
	sock := local.SocketHandler()

	// PUT /v1/worker?host=air26 changes air26, not this host
	w := send(sock, "PUT", fwdPath("/v1/worker", "air26"), "", `{"enabled":false,"slots":{"heavy":0,"light":0},"owner_rule":""}`)
	if w.Code != 200 {
		t.Fatalf("forwarded PUT: %d %s", w.Code, w.Body)
	}
	if remote.opts.State.Worker().Enabled || !local.opts.State.Worker().Enabled {
		t.Fatalf("remote=%+v local=%+v", remote.opts.State.Worker(), local.opts.State.Worker())
	}
	// GET is forwarded too
	ws, err := wire.Decode[wire.WorkerView](send(sock, "GET", fwdPath("/v1/worker", "air26"), "", "").Body.Bytes())
	if err != nil || ws.Enabled {
		t.Fatalf("forwarded GET: %+v %v", ws, err)
	}
	// POST and DELETE of an exception on the peer
	until := rr.now.Add(time.Hour).Format(time.RFC3339)
	if w := send(sock, "POST", fwdPath("/v1/exceptions", "air26"), "", `{"name":"remote-x","until":"`+until+`","pid":null,"holds":["heavy"]}`); w.Code != 200 {
		t.Fatalf("forwarded POST: %d %s", w.Code, w.Body)
	}
	rr.tick()
	if f, _ := remote.opts.Sampler.Latest(); len(f.Exception) != 1 || f.Exception[0].Name != "remote-x" {
		t.Fatalf("peer facts: %+v", f.Exception)
	}
	lr.tick()
	if f, _ := local.opts.Sampler.Latest(); len(f.Exception) != 0 {
		t.Fatalf("the exception landed on this host: %+v", f.Exception)
	}
	if w := send(sock, "DELETE", fwdPath("/v1/exceptions/remote-x", "air26"), "", ""); w.Code != http.StatusNoContent {
		t.Fatalf("forwarded DELETE: %d", w.Code)
	}
	// a peer that is gone: 502 {host, reason}; one that is not in the pool: 404
	srv.Close()
	if w := send(sock, "PUT", fwdPath("/v1/worker", "air26"), "", `{"enabled":true,"slots":{"heavy":1,"light":1},"owner_rule":""}`); w.Code != http.StatusBadGateway {
		t.Errorf("unreachable: %d", w.Code)
	}
	if w := send(sock, "POST", fwdPath("/v1/exceptions", "nowhere"), "", `{}`); w.Code != http.StatusNotFound {
		t.Errorf("unknown host: %d", w.Code)
	}
	// host=<this host> is a local request
	if w := send(sock, "PUT", fwdPath("/v1/worker", "mlab"), "", `{"enabled":false,"slots":{"heavy":1,"light":1},"owner_rule":""}`); w.Code != 200 || local.opts.State.Worker().Enabled {
		t.Errorf("host=self: %d", w.Code)
	}
}

// No multi-hop and no relaying: every mutating route refuses host over TCP.
func TestTCPRefusesHostOnMutations(t *testing.T) {
	level := 1
	d, _ := stateDaemon(t, "mlab", &level)
	h := d.TCPHandler()
	auth := "Bearer " + testToken
	for _, c := range []struct{ method, path string }{
		{"PUT", "/v1/worker"}, {"GET", "/v1/worker"}, {"GET", "/v1/exceptions"}, {"POST", "/v1/exceptions"}, {"DELETE", "/v1/exceptions/x"},
	} {
		for _, host := range []string{"air26", "mlab"} {
			if w := send(h, c.method, fwdPath(c.path, host), auth, `{}`); w.Code != http.StatusBadRequest {
				t.Errorf("%s %s host=%s: %d, want 400", c.method, c.path, host, w.Code)
			}
		}
	}
}
