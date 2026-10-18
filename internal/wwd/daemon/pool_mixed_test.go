package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The mixed pool of the contract release (spec §13.1): while the four wwds are upgraded one after another, the pool holds hosts of
// proto 1 and proto 2 at once. A proto 2 reader must take both; a host of an unknown future proto is refused, never half-read.

func wireFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "wire", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// peerOf serves a peer whose /v1/info says proto and whose /v1/facts is the given JSON (as that peer's build would write it).
func peerOf(t *testing.T, alias string, proto int, factsJSON string) PeerAddr {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/info":
			writeJSON(w, 200, wire.Info{HostID: "id-" + alias, Alias: alias, Version: "v", Proto: proto, Platform: "linux", Arch: "amd64", Labels: []string{}})
		case "/v1/facts":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(factsJSON))
		}
	}))
	t.Cleanup(srv.Close)
	return PeerAddr{alias, addr(srv)}
}

// factsFor rewrites a fixture to belong to alias (the pool refuses facts that disagree with the peer's info).
func factsFor(js, alias string) string {
	js = strings.Replace(js, `"host_id": "h-mlab"`, `"host_id": "id-`+alias+`"`, 1)
	return strings.Replace(js, `"alias": "mlab"`, `"alias": "`+alias+`"`, 1)
}

func TestMixedPoolOfProto1AndProto2ReadsBoth(t *testing.T) {
	old := peerOf(t, "air19", 1, factsFor(wireFixture(t, "facts_darwin.json"), "air19"))     // a wwd before the contract: v 1, none of the new fields
	next := peerOf(t, "air26", 2, factsFor(wireFixture(t, "facts_v2_darwin.json"), "air26")) // after it, once its writers are gated open
	p := NewPoolPoller([]PeerAddr{old, next}, testToken, nil)
	p.PollOnce(context.Background())
	peers := p.Peers()
	if !peers[0].Reachable || peers[0].Facts == nil || peers[0].Facts.V != 1 || peers[0].Facts.Mem.DecompressRate != nil || peers[0].Facts.Queue != nil {
		t.Fatalf("a proto 1 peer must read as before, new fields absent: %+v", peers[0])
	}
	f := peers[1].Facts
	if !peers[1].Reachable || f == nil || f.V != 2 || f.Mem.DecompressRate == nil || f.Queue == nil || f.Queue.Queued != 3 {
		t.Fatalf("a proto 2 peer's new fields must be read: %+v", peers[1])
	}
	hold := f.Exception[len(f.Exception)-1]
	if hold.Allow == nil || len(hold.Flags) != 2 {
		t.Fatalf("the hold travels in the facts: %+v", hold)
	}
	// the gate for the writers: shut while a proto 1 host is in the pool, open when all are 2
	if wire.PoolAtLeast(peers, 2) {
		t.Fatal("a pool with a proto 1 host must keep the new fields shut")
	}
	if !wire.PoolAtLeast(peers[1:], 2) {
		t.Fatal("a pool of proto 2 hosts opens the gate")
	}
}

// A host that gave proto 2, went down and comes back rolled back to proto 1: the gate must be shut the moment it is unreachable
// (its last info is old), and stay shut once it answers as proto 1.
func TestARolledBackPeerShutsTheGate(t *testing.T) {
	var proto atomic.Int32
	var down atomic.Bool
	proto.Store(2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/v1/info":
			writeJSON(w, 200, wire.Info{HostID: "id-air26", Alias: "air26", Version: "v", Proto: int(proto.Load()), Platform: "linux", Arch: "amd64", Labels: []string{}})
		case "/v1/facts":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(factsFor(wireFixture(t, "facts_darwin.json"), "air26")))
		}
	}))
	t.Cleanup(srv.Close)
	p := NewPoolPoller([]PeerAddr{{"air26", addr(srv)}}, testToken, nil)
	p.PollOnce(context.Background())
	if !wire.PoolAtLeast(p.Peers(), 2) {
		t.Fatalf("a reachable proto 2 pool opens the gate: %+v", p.Peers())
	}
	down.Store(true)
	p.PollOnce(context.Background())
	if got := p.Peers()[0]; got.Reachable || got.Info.Proto != 2 || wire.PoolAtLeast(p.Peers(), 2) {
		t.Fatalf("unreachable keeps its last info but must shut the gate: %+v", got)
	}
	down.Store(false)
	proto.Store(1)
	p.PollOnce(context.Background())
	if wire.PoolAtLeast(p.Peers(), 2) {
		t.Fatalf("rolled back to proto 1: the gate stays shut: %+v", p.Peers()[0])
	}
}

func TestAPeerOfAFutureProtoIsRefused(t *testing.T) {
	p := NewPoolPoller([]PeerAddr{peerOf(t, "air26", wire.Proto+1, factsFor(wireFixture(t, "facts_v2_darwin.json"), "air26"))}, testToken, nil)
	p.PollOnce(context.Background())
	if g := p.Peers()[0]; g.Reachable || g.Facts != nil {
		t.Fatalf("a host speaking a proto this build does not know must not be half-read: %+v", g)
	}
}

// Old reader, gated writer: before every host is at proto 2 this build writes the v1 shape, so a proto 1 reader (strict decode)
// is never shown a field it would reject.
func TestFactsAWriterSendsBeforeTheGateIsV1Shaped(t *testing.T) {
	d := New(Options{HostID: "id-mlab", Alias: "mlab", Version: "v0", Token: testToken, Alerts: fakeAlerts{}})
	w := do(d.SocketHandler(), "GET", "/v1/info", "")
	info, err := wire.Decode[wire.Info](w.Body.Bytes())
	if err != nil || info.Proto != wire.Proto {
		t.Fatalf("a new build announces proto %d so peers can open their gates: %+v %v", wire.Proto, info, err)
	}
	if wire.WriteProto != 1 {
		t.Fatalf("writers still send v %d", wire.WriteProto)
	}
}
