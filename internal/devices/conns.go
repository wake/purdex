package devices

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
)

// ConnRegistry tracks the WebSocket connections that device principals hold, so revoking a device closes them all (QR
// pairing spec §3.2 / §3.4). It does so in ONE place, around the handler chain, instead of asking every WebSocket handler
// to register: it wraps the ResponseWriter of a device's WebSocket handshake, and the connection a handler takes by
// hijacking it (host events, terminal, mirror, conversations, and any WebSocket route added later) is recorded under the
// device id and closed on revoke. A connection opened with the bearer and one opened with a one-time ticket are the same
// thing here: both requests carry the device principal.
type ConnRegistry struct {
	live func(deviceID string) bool // does the store still hold this device live (not revoked)?

	mu    sync.Mutex
	byDev map[string]map[*trackedConn]struct{}
}

// NewConnRegistry returns a registry; live says whether a device is still unrevoked (asked after a connection is
// recorded, so a revoke that raced the open is caught). A nil live treats every device as live.
func NewConnRegistry(live func(deviceID string) bool) *ConnRegistry {
	return &ConnRegistry{live: live, byDev: map[string]map[*trackedConn]struct{}{}}
}

// Track wraps next: for a request that carries a device principal and has the shape of a WebSocket handshake, the
// ResponseWriter is wrapped so the hijacked connection is tracked. Anything else passes through untouched. A nil registry
// passes everything through.
func (r *ConnRegistry) Track(next http.Handler) http.Handler {
	if r == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if p, ok := PrincipalFrom(req.Context()); ok && isHandshake(req) {
			w = &trackingWriter{ResponseWriter: w, reg: r, id: p.ID}
		}
		next.ServeHTTP(w, req)
	})
}

func isHandshake(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// CloseDevices closes every tracked connection of the given devices.
func (r *ConnRegistry) CloseDevices(ids []string) {
	if r == nil {
		return
	}
	var victims []*trackedConn
	r.mu.Lock()
	for _, id := range ids {
		for c := range r.byDev[id] {
			victims = append(victims, c)
		}
	}
	r.mu.Unlock()
	for _, c := range victims {
		_ = c.Close() // Close unregisters
	}
}

// Tracker is what the daemon's handler chain needs of a registry: wrap a handler so a device's WebSocket connections are
// tracked. *ConnRegistry is one.
type Tracker interface {
	Track(next http.Handler) http.Handler
}

// Count is the number of tracked connections of one device; Total of all.
func (r *ConnRegistry) Count(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byDev[id])
}

func (r *ConnRegistry) Total() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.byDev {
		n += len(m)
	}
	return n
}

func (r *ConnRegistry) add(id string, c *trackedConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byDev[id] == nil {
		r.byDev[id] = map[*trackedConn]struct{}{}
	}
	r.byDev[id][c] = struct{}{}
}

func (r *ConnRegistry) remove(id string, c *trackedConn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byDev[id], c)
	if len(r.byDev[id]) == 0 {
		delete(r.byDev, id)
	}
}

// trackingWriter is the ResponseWriter a device's handshake gets: Hijack records the connection.
type trackingWriter struct {
	http.ResponseWriter
	reg *ConnRegistry
	id  string
}

// Unwrap lets http.ResponseController reach the real writer.
func (t *trackingWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

func (t *trackingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := t.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("devices: the response writer cannot be hijacked")
	}
	conn, rw, err := h.Hijack()
	if err != nil {
		return nil, nil, err
	}
	tc := &trackedConn{Conn: conn, reg: t.reg, id: t.id}
	// Record first, ask second: a revoke that came before this point finds the device not live here and closes the
	// connection; one that comes after finds it recorded and closes it. Either order leaves nothing open.
	t.reg.add(t.id, tc)
	if t.reg.live != nil && !t.reg.live(t.id) {
		_ = tc.Close()
		return nil, nil, errors.New("devices: the device was revoked")
	}
	return tc, rw, nil
}

// trackedConn forgets itself from the registry when it is closed, by whoever closes it.
type trackedConn struct {
	net.Conn
	reg  *ConnRegistry
	id   string
	once sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() { c.reg.remove(c.id, c) })
	return c.Conn.Close()
}
