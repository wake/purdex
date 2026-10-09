package devices

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/middleware"
)

// QP-1 task 4: revoking a device closes every WebSocket it holds, opened with its bearer or with a one-time ticket, on any
// route; the admin's connections and other devices' are untouched. Real middleware, real module, real ticket store.

type wsEnv struct {
	srv     *httptest.Server
	mod     *Module
	admin   string
	tickets *core.TicketStore
	opened  atomic.Int32
}

func newWSEnv(t *testing.T) *wsEnv {
	t.Helper()
	adminTok := "adm_" + strings.Repeat("a", 32)
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: adminTok}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	e := &wsEnv{mod: m, admin: adminTok, tickets: core.NewTicketStore()}
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	hold := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		e.opened.Add(1)
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	for _, route := range []string{"/ws/host-events", "/ws/terminal/{code}", "/ws/conversations/{p}/{sid}"} { // the three kinds
		mux.Handle("GET "+route, hold)
	}
	e.srv = httptest.NewServer(middleware.TokenAuthWith(func() string { return adminTok }, e.tickets, m)(m.conns.Track(mux)))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *wsEnv) dial(t *testing.T, path string, h http.Header) *websocket.Conn {
	t.Helper()
	u := url.URL{Scheme: "ws", Host: strings.TrimPrefix(e.srv.URL, "http://")}
	full := u.String() + path
	c, resp, err := websocket.DefaultDialer.Dial(full, h)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("dial %s: %v (status %d)", path, err, code)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func (e *wsEnv) mintDevice(t *testing.T, pairing string) (id, token string) {
	t.Helper()
	row, tok, err := e.mod.store.Mint(MintRequest{PairingID: pairing, Label: "iPhone", CreatedBy: "Purdex.app", UseWithin: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return row.ID, tok
}

func (e *wsEnv) del(t *testing.T, path string) {
	t.Helper()
	req, _ := http.NewRequest("DELETE", e.srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+e.admin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE %s: %v %v", path, err, resp)
	}
	resp.Body.Close()
}

func stillOpen(c *websocket.Conn) bool {
	_ = c.SetReadDeadline(time.Now().Add(600 * time.Millisecond))
	_, _, err := c.ReadMessage()
	if err == nil {
		return true
	}
	ne, ok := err.(interface{ Timeout() bool })
	return ok && ne.Timeout()
}

func (e *wsEnv) ticketFor(t *testing.T, id string) string {
	t.Helper()
	tk, err := e.tickets.GenerateFor(devices.Caller{Device: &devices.Principal{ID: id, PairingID: pairingA}})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// One connection of each kind, opened by bearer, and one of each opened by ticket: revoking the device closes all six; the
// admin's and another device's stay. Mutation gates: no registry wiring in revoked() → red; the ticket path loses the
// principal → the ticket connections survive → red.
func TestRevoke_ClosesEveryWebSocketOfTheDeviceWhateverTheRouteAndTheAuth(t *testing.T) {
	e := newWSEnv(t)
	idA, tokA := e.mintDevice(t, pairingA)
	idB, tokB := e.mintDevice(t, pairingB)
	routes := []string{"/ws/host-events", "/ws/terminal/abc", "/ws/conversations/claude/sid-1"}

	var mine []*websocket.Conn
	for _, r := range routes {
		mine = append(mine, e.dial(t, r, http.Header{"Authorization": {"Bearer " + tokA}}))
		mine = append(mine, e.dial(t, r+"?ticket="+e.ticketFor(t, idA), nil))
	}
	other := e.dial(t, routes[0], http.Header{"Authorization": {"Bearer " + tokB}})
	admin := e.dial(t, routes[1], http.Header{"Authorization": {"Bearer " + e.admin}})
	if n := e.mod.conns.Count(idA); n != 6 {
		t.Fatalf("tracked for the device = %d, want 6", n)
	}
	if e.mod.conns.Count(idB) != 1 || e.mod.conns.Total() != 7 {
		t.Fatalf("tracked: b=%d total=%d (the admin's connection must not be tracked)", e.mod.conns.Count(idB), e.mod.conns.Total())
	}

	e.del(t, "/api/devices/"+idA)

	for i, c := range mine {
		if stillOpen(c) {
			t.Fatalf("connection %d of the revoked device is still open", i)
		}
	}
	if !stillOpen(other) {
		t.Fatal("another device's connection was closed")
	}
	if !stillOpen(admin) {
		t.Fatal("the admin's connection was closed")
	}
	// The revoked bearer cannot open a new one either.
	u := url.URL{Scheme: "ws", Host: strings.TrimPrefix(e.srv.URL, "http://"), Path: "/ws/host-events"}
	if _, resp, err := websocket.DefaultDialer.Dial(u.String(), http.Header{"Authorization": {"Bearer " + tokA}}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a revoked bearer opened a connection: %v %v", err, resp)
	}
}

// Revoking a pairing closes the connections of every device in it.
func TestRevokePairing_ClosesTheConnectionsOfEveryDeviceInIt(t *testing.T) {
	e := newWSEnv(t)
	_, tok1 := e.mintDevice(t, pairingA)
	_, tok2 := e.mintDevice(t, pairingA)
	_, tok3 := e.mintDevice(t, pairingB)
	c1 := e.dial(t, "/ws/host-events", http.Header{"Authorization": {"Bearer " + tok1}})
	c2 := e.dial(t, "/ws/terminal/x", http.Header{"Authorization": {"Bearer " + tok2}})
	c3 := e.dial(t, "/ws/host-events", http.Header{"Authorization": {"Bearer " + tok3}})
	e.del(t, "/api/devices?pairing_id="+pairingA)
	if stillOpen(c1) || stillOpen(c2) {
		t.Fatal("a connection of the revoked pairing stayed open")
	}
	if !stillOpen(c3) {
		t.Fatal("another pairing's connection was closed")
	}
}

// A connection that ends by itself is forgotten, and a revoke afterwards is harmless.
func TestRevoke_AfterTheConnectionEndedIsHarmless(t *testing.T) {
	e := newWSEnv(t)
	id, tok := e.mintDevice(t, pairingA)
	c := e.dial(t, "/ws/host-events", http.Header{"Authorization": {"Bearer " + tok}})
	c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for e.mod.conns.Total() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if e.mod.conns.Total() != 0 {
		t.Fatalf("tracked %d after the connection ended", e.mod.conns.Total())
	}
	e.del(t, "/api/devices/"+id)
}

// The hook SetOnRevoke still runs after the connections are closed.
func TestRevoke_TheExtraHookStillRuns(t *testing.T) {
	e := newWSEnv(t)
	id, _ := e.mintDevice(t, pairingA)
	got := make(chan []string, 1)
	e.mod.SetOnRevoke(func(ids []string) { got <- ids })
	e.del(t, "/api/devices/"+id)
	select {
	case ids := <-got:
		if len(ids) != 1 || ids[0] != id {
			t.Fatalf("ids = %v", ids)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hook not called")
	}
}

// The module publishes its tracker for the daemon's chain.
func TestInit_PublishesTheConnectionTracker(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "adm_x"}})
	m := New()
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	defer m.Stop(context.Background())
	svc, ok := c.Registry.Get(ConnsKey)
	if !ok {
		t.Fatal("tracker not published")
	}
	if _, isTracker := svc.(devices.Tracker); !isTracker {
		t.Fatalf("published %T", svc)
	}
}

// A device revoked AFTER it minted a ticket and BEFORE the ticket is redeemed gets nothing: refused at the middleware, before
// any handler work. Through the real middleware, ticket store and module. Mutation gate: no refresh at redemption → red.
func TestRevoke_ATicketMintedBeforeTheRevokeIsRefusedAfterIt(t *testing.T) {
	e := newWSEnv(t)
	idA, tokA := e.mintDevice(t, pairingA)
	e.dial(t, "/ws/host-events", http.Header{"Authorization": {"Bearer " + tokA}}) // the device is in use (first use recorded)
	ticket := e.ticketFor(t, idA)
	before := e.opened.Load()
	e.del(t, "/api/devices/"+idA)

	u := url.URL{Scheme: "ws", Host: strings.TrimPrefix(e.srv.URL, "http://"), Path: "/ws/host-events"}
	_, resp, err := websocket.DefaultDialer.Dial(u.String()+"?ticket="+ticket, nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a ticket of a revoked device was honoured: %v %v", err, resp)
	}
	if e.opened.Load() != before {
		t.Fatal("the handler ran for a revoked device's ticket")
	}
}

// A live device's ticket is honoured and carries the stored principal.
func TestTicket_ALiveDevicesTicketOpensAConnectionAsThatDevice(t *testing.T) {
	e := newWSEnv(t)
	idA, tokA := e.mintDevice(t, pairingA)
	e.dial(t, "/ws/host-events", http.Header{"Authorization": {"Bearer " + tokA}}) // first use
	c := e.dial(t, "/ws/host-events?ticket="+e.ticketFor(t, idA), nil)
	if !stillOpen(c) {
		t.Fatal("the connection was not kept")
	}
	if e.mod.conns.Count(idA) != 2 {
		t.Fatalf("tracked %d, want the bearer connection and the ticket one", e.mod.conns.Count(idA))
	}
}
