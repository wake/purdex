package daemon

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

type fakeAlerts struct {
	list []wire.Alert
	err  error
}

func (f fakeAlerts) ActiveAlerts() ([]wire.Alert, error) { return f.list, f.err }

var alertT0 = time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC)

func oneAlert() []wire.Alert {
	return []wire.Alert{{Key: "disk_low:/", Kind: wire.AlertDiskLow, Level: wire.AlertPush, Since: alertT0, Last: alertT0.Add(time.Minute)}}
}

func alertsDaemon(alias string, st AlertStore, peers ...PeerAddr) *Daemon {
	return New(Options{HostID: "id-" + alias, Alias: alias, Version: "v0", Token: testToken, Alerts: st, Peers: peers})
}

func TestAlertsRouteAnswersTheHostsActiveAlerts(t *testing.T) {
	d := alertsDaemon("mlab", fakeAlerts{list: oneAlert()})
	w := do(d.SocketHandler(), "GET", "/v1/alerts", "")
	l, err := wire.Decode[wire.AlertList](w.Body.Bytes())
	if w.Code != 200 || err != nil || l.Host != "mlab" || len(l.Alerts) != 1 || l.Alerts[0].Key != "disk_low:/" {
		t.Fatalf("%d %s (%v)", w.Code, w.Body, err)
	}
}

func TestAlertsRouteEmptyIsAnEmptyList(t *testing.T) {
	for _, st := range []AlertStore{fakeAlerts{}, fakeAlerts{list: []wire.Alert{}}} {
		w := do(alertsDaemon("mlab", st).SocketHandler(), "GET", "/v1/alerts", "")
		if w.Code != 200 || !contains(w.Body.String(), `"alerts":[]`) {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
}

func TestAlertsRouteStoreFailureIs500(t *testing.T) {
	w := do(alertsDaemon("mlab", fakeAlerts{err: errors.New("disk I/O")}).SocketHandler(), "GET", "/v1/alerts", "")
	if w.Code != 500 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestAlertsRouteNeedsTheTokenOnTCP(t *testing.T) {
	h := alertsDaemon("mlab", fakeAlerts{list: oneAlert()}).TCPHandler()
	if w := do(h, "GET", "/v1/alerts", ""); w.Code != 401 {
		t.Errorf("without a token: %d", w.Code)
	}
	if w := do(h, "GET", "/v1/alerts", "Bearer "+testToken); w.Code != 200 {
		t.Errorf("with the token: %d", w.Code)
	}
	if w := do(h, "GET", "/v1/alerts?host=air26", "Bearer "+testToken); w.Code != 400 {
		t.Errorf("a host over TCP (no multi-hop): %d", w.Code)
	}
}

func TestAlertsRouteForwardsToAPeer(t *testing.T) {
	peerAlert := []wire.Alert{{Key: "thrash", Kind: wire.AlertThrash, Level: wire.AlertDigest, Since: alertT0, Last: alertT0}}
	srv := httptest.NewServer(alertsDaemon("air26", fakeAlerts{list: peerAlert}).TCPHandler())
	t.Cleanup(srv.Close)
	local := alertsDaemon("mlab", fakeAlerts{list: oneAlert()}, PeerAddr{"air26", addr(srv)})
	w := do(local.SocketHandler(), "GET", "/v1/alerts?host=air26", "")
	l, err := wire.Decode[wire.AlertList](w.Body.Bytes())
	if w.Code != 200 || err != nil || l.Host != "air26" || len(l.Alerts) != 1 || l.Alerts[0].Key != "thrash" {
		t.Fatalf("the peer's own list expected: %d %s (%v)", w.Code, w.Body, err)
	}
	if w := do(local.SocketHandler(), "GET", "/v1/alerts?host=nowhere", ""); w.Code != 404 {
		t.Fatalf("unknown host: %d", w.Code)
	}
	// the local alias asked for explicitly is answered here
	w = do(local.SocketHandler(), "GET", "/v1/alerts?host=mlab", "")
	if l, err := wire.Decode[wire.AlertList](w.Body.Bytes()); w.Code != 200 || err != nil || l.Host != "mlab" {
		t.Fatalf("%d %s (%v)", w.Code, w.Body, err)
	}
}

// A host whose wwd predates the contract answers 404 for the route, and so does one built without a store: readers take 404 as
// "no alerts yet" (spec §13.1), so it must stay a plain 404.
func TestAlertsRouteIsNotRegisteredWithoutAStore(t *testing.T) {
	d := alertsDaemon("mlab", nil)
	if w := do(d.SocketHandler(), "GET", "/v1/alerts", ""); w.Code != 404 {
		t.Fatalf("socket: %d", w.Code)
	}
	if w := do(d.TCPHandler(), "GET", "/v1/alerts", "Bearer "+testToken); w.Code != 404 {
		t.Fatalf("tcp: %d", w.Code)
	}
}
