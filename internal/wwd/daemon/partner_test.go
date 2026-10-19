package daemon

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

// The partner heartbeat: only a read of this host's own list on the socket (the local Purdex daemon's deliverer) moves PartnerSeen.
func TestPartnerSeenMovesOnlyOnALocalSocketRead(t *testing.T) {
	clock := alertT0
	d := New(Options{HostID: "id-mlab", Alias: "mlab", Version: "v0", Token: testToken, Alerts: fakeAlerts{}, Now: func() time.Time { return clock }})
	if got := d.PartnerSeen(); !got.Equal(alertT0) {
		t.Fatalf("before any read: %v, want the daemon's start", got)
	}
	// a peer reading over TCP is not the partner
	clock = alertT0.Add(time.Minute)
	req := httptest.NewRequest("GET", "/v1/alerts", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	d.TCPHandler().ServeHTTP(httptest.NewRecorder(), req)
	if got := d.PartnerSeen(); !got.Equal(alertT0) {
		t.Fatalf("a TCP read moved it to %v", got)
	}
	// a socket read of another host's list (forwarded) is not the partner's read of this host either
	do(d.SocketHandler(), "GET", "/v1/alerts?host=nowhere", "")
	if got := d.PartnerSeen(); !got.Equal(alertT0) {
		t.Fatalf("a read of another host moved it to %v", got)
	}
	// the local read, with or without the host's own alias
	do(d.SocketHandler(), "GET", "/v1/alerts", "")
	if got := d.PartnerSeen(); !got.Equal(clock) {
		t.Fatalf("a local read left it at %v", got)
	}
	clock = alertT0.Add(2 * time.Minute)
	do(d.SocketHandler(), "GET", "/v1/alerts?host=mlab", "")
	if got := d.PartnerSeen(); !got.Equal(clock) {
		t.Fatalf("a read by alias left it at %v", got)
	}
}

// A read the store could not answer (500) is a deliverer that reads nothing: it must not keep the partner looking alive.
func TestPartnerSeenIgnoresAReadThatFailed(t *testing.T) {
	clock := alertT0
	d := New(Options{HostID: "id-mlab", Alias: "mlab", Version: "v0", Token: testToken, Alerts: fakeAlerts{err: errors.New("disk I/O")}, Now: func() time.Time { return clock }})
	clock = alertT0.Add(time.Minute)
	if w := do(d.SocketHandler(), "GET", "/v1/alerts", ""); w.Code != 500 {
		t.Fatalf("status %d", w.Code)
	}
	if got := d.PartnerSeen(); !got.Equal(alertT0) {
		t.Fatalf("a failed read moved it to %v", got)
	}
}
