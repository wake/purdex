package devices

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// QP-1 task 4: every WebSocket a device opens is tracked, however it authenticated, so revoking the device closes them all.

// wsServer serves a WebSocket endpoint behind the tracker. Who the request is comes from the X-Test-* headers (standing in
// for the token middleware): X-Test-Device = a device id, X-Test-Admin = the admin.
type wsServer struct {
	*httptest.Server
	reg    *ConnRegistry
	live   map[string]bool
	mu     sync.Mutex
	opened atomic.Int32
	ended  atomic.Int32
}

func newWSServer(t *testing.T) *wsServer {
	t.Helper()
	s := &wsServer{live: map[string]bool{"d_aaaaaaaaaaaa": true, "d_bbbbbbbbbbbb": true}}
	s.reg = NewConnRegistry(func(id string) bool { s.mu.Lock(); defer s.mu.Unlock(); return s.live[id] })
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s.opened.Add(1)
		defer func() { conn.Close(); s.ended.Add(1) }()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	identify := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if id := r.Header.Get("X-Test-Device"); id != "" {
			ctx = WithPrincipal(ctx, Principal{ID: id, PairingID: "pair"})
		}
		if r.Header.Get("X-Test-Admin") != "" {
			ctx = WithAdmin(ctx)
		}
		s.reg.Track(echo).ServeHTTP(w, r.WithContext(ctx))
	})
	s.Server = httptest.NewServer(identify)
	t.Cleanup(s.Server.Close)
	return s
}

func (s *wsServer) dial(t *testing.T, header http.Header) *websocket.Conn {
	t.Helper()
	u := url.URL{Scheme: "ws", Host: strings.TrimPrefix(s.URL, "http://"), Path: "/ws/x"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), header)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func (s *wsServer) setLive(id string, live bool) { s.mu.Lock(); s.live[id] = live; s.mu.Unlock() }

// closed reports whether the client's read fails within a second (the server closed the connection).
func closed(c *websocket.Conn) bool {
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err := c.ReadMessage()
	if err == nil {
		return false
	}
	ne, isNet := err.(interface{ Timeout() bool })
	return !(isNet && ne.Timeout())
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func devHeader(id string) http.Header { return http.Header{"X-Test-Device": {id}} }

// Revoking closes every connection of that device and only that device's; the admin's stays. Mutation gate: CloseDevices
// does nothing → red; closes everything → red.
func TestConnRegistry_CloseDevicesClosesThatDevicesConnectionsOnly(t *testing.T) {
	s := newWSServer(t)
	a1 := s.dial(t, devHeader("d_aaaaaaaaaaaa"))
	a2 := s.dial(t, devHeader("d_aaaaaaaaaaaa"))
	b := s.dial(t, devHeader("d_bbbbbbbbbbbb"))
	admin := s.dial(t, http.Header{"X-Test-Admin": {"1"}})
	waitFor(t, "four connections", func() bool { return s.opened.Load() == 4 })
	if n := s.reg.Count("d_aaaaaaaaaaaa"); n != 2 {
		t.Fatalf("tracked for a = %d", n)
	}
	if s.reg.Count("d_bbbbbbbbbbbb") != 1 {
		t.Fatal("b is not tracked")
	}

	s.reg.CloseDevices([]string{"d_aaaaaaaaaaaa"})
	if !closed(a1) || !closed(a2) {
		t.Fatal("a connection of the revoked device stayed open")
	}
	if closed(b) {
		t.Fatal("another device's connection was closed")
	}
	if closed(admin) {
		t.Fatal("the admin's connection was closed")
	}
	waitFor(t, "the registry to forget a's connections", func() bool { return s.reg.Count("d_aaaaaaaaaaaa") == 0 })
	waitFor(t, "the handlers to end", func() bool { return s.ended.Load() == 2 })
}

// The admin's connections are not tracked at all (nothing to revoke).
func TestConnRegistry_AdminConnectionsAreNotTracked(t *testing.T) {
	s := newWSServer(t)
	s.dial(t, http.Header{"X-Test-Admin": {"1"}})
	waitFor(t, "the connection", func() bool { return s.opened.Load() == 1 })
	if s.reg.Total() != 0 {
		t.Fatalf("tracked %d connections for the admin", s.reg.Total())
	}
}

// A connection that ends by itself leaves the registry (no leak).
func TestConnRegistry_AConnectionThatEndsIsForgotten(t *testing.T) {
	s := newWSServer(t)
	c := s.dial(t, devHeader("d_aaaaaaaaaaaa"))
	waitFor(t, "tracked", func() bool { return s.reg.Count("d_aaaaaaaaaaaa") == 1 })
	c.Close()
	waitFor(t, "forgotten", func() bool { return s.reg.Total() == 0 })
}

// A device revoked between authenticating and hijacking never gets a connection: the hijack re-checks, and the revoke that
// came before the registration has nothing to close. Mutation gate: skip the live check after registering → red.
func TestConnRegistry_ADeviceRevokedBeforeTheHijackIsRefused(t *testing.T) {
	s := newWSServer(t)
	s.setLive("d_aaaaaaaaaaaa", false)
	u := url.URL{Scheme: "ws", Host: strings.TrimPrefix(s.URL, "http://"), Path: "/ws/x"}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), devHeader("d_aaaaaaaaaaaa"))
	if err == nil {
		// If the dial itself succeeded, the server must close it at once.
		defer c.Close()
		if !closed(c) {
			t.Fatal("a revoked device kept an open connection")
		}
	}
	if s.reg.Total() != 0 {
		t.Fatalf("tracked %d", s.reg.Total())
	}
}

// A revoke racing the open: whichever order, no connection of the revoked device survives. (Run many times.)
func TestConnRegistry_RevokeRacingTheOpenLeavesNothingOpen(t *testing.T) {
	for i := 0; i < 30; i++ {
		s := newWSServer(t)
		var wg sync.WaitGroup
		wg.Add(2)
		var c *websocket.Conn
		go func() {
			defer wg.Done()
			u := url.URL{Scheme: "ws", Host: strings.TrimPrefix(s.URL, "http://"), Path: "/ws/x"}
			c, _, _ = websocket.DefaultDialer.Dial(u.String(), devHeader("d_aaaaaaaaaaaa"))
		}()
		go func() {
			defer wg.Done()
			s.setLive("d_aaaaaaaaaaaa", false) // the store's revoke happens first, then the registry is told
			s.reg.CloseDevices([]string{"d_aaaaaaaaaaaa"})
		}()
		wg.Wait()
		if c != nil {
			if !closed(c) {
				t.Fatalf("iteration %d: a connection survived the revoke", i)
			}
			c.Close()
		}
		waitFor(t, "registry empty", func() bool { return s.reg.Total() == 0 })
		s.Close()
	}
}

// Requests that are not WebSocket handshakes pass through untouched (the writer is not wrapped).
func TestConnRegistry_PlainRequestsAreNotWrapped(t *testing.T) {
	reg := NewConnRegistry(func(string) bool { return true })
	var gotWriter http.ResponseWriter
	h := reg.Track(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gotWriter = w }))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/x", nil).WithContext(WithPrincipal(httptest.NewRequest("GET", "/", nil).Context(), Principal{ID: "d_aaaaaaaaaaaa"}))
	h.ServeHTTP(rec, req)
	if gotWriter != http.ResponseWriter(rec) {
		t.Fatal("a plain request's writer was wrapped")
	}
}

// A nil registry passes everything through (the module is not mounted).
func TestConnRegistry_NilIsAPassThrough(t *testing.T) {
	var reg *ConnRegistry
	called := false
	reg.Track(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !called {
		t.Fatal("not passed through")
	}
	reg.CloseDevices([]string{"x"}) // must not panic
}
