package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

// S3-3b-1: /v1/info says whether the worker takes only explicitly labelled jobs, so that the pool can tell which hosts take a job. A host that
// does not ask for it says nothing (omitempty), which is also what a wwd from before the field says.
func TestInfoAnnouncesExplicitLabelsOnlyOnlyWhenSet(t *testing.T) {
	off := New(Options{HostID: "h1", Alias: "air26", Version: "v0", Token: testToken, WorkerEnabled: true})
	b, _ := json.Marshal(off.info())
	if strings.Contains(string(b), "explicit_labels_only") {
		t.Errorf("a host that takes any job says nothing of it: %s", b)
	}
	on := New(Options{HostID: "h2", Alias: "mserv", Version: "v0", Token: testToken, WorkerEnabled: true, ExplicitLabelsOnly: true})
	if i := on.info(); !i.Worker.ExplicitLabelsOnly || !i.Worker.Enabled {
		t.Errorf("info %+v", i.Worker)
	}
	b, _ = json.Marshal(on.info())
	if !strings.Contains(string(b), `"explicit_labels_only":true`) {
		t.Errorf("not in the json: %s", b)
	}
}

// A reader from before the field (it reads lenient) takes the new info without trouble, and a new reader takes an old info as false.
func TestInfoWithAndWithoutExplicitLabelsOnlyReadsBothWays(t *testing.T) {
	newer := []byte(`{"host_id":"h","alias":"mserv","version":"v","proto":3,"platform":"linux","arch":"amd64","worker":{"enabled":true,"explicit_labels_only":true},"labels":[],"fingerprint":{}}`)
	older := []byte(`{"host_id":"h","alias":"air19","version":"v","proto":3,"platform":"darwin","arch":"amd64","worker":{"enabled":true},"labels":[],"fingerprint":{}}`)
	i, err := wire.DecodeLenient[wire.Info](newer)
	if err != nil || !i.Worker.ExplicitLabelsOnly {
		t.Fatalf("%+v %v", i, err)
	}
	i, err = wire.DecodeLenient[wire.Info](older)
	if err != nil || i.Worker.ExplicitLabelsOnly || !i.Worker.Enabled {
		t.Fatalf("%+v %v", i, err)
	}
	// the peer poller reads a peer's info lenient: a peer that is newer than this build, with a field this build does not know, still reads
	if _, err := wire.DecodeLenient[wire.Info]([]byte(strings.Replace(string(newer), `"labels"`, `"extra":1,"labels"`, 1))); err != nil {
		t.Fatalf("a newer peer: %v", err)
	}
}

// PoolView is this host first and then the peers: what GET /v1/pool answers, and what the queue rules judge on.
func TestPoolViewIsSelfThenPeers(t *testing.T) {
	_, srv := peerDaemon(t, "air26", testToken)
	level := 1
	self := NewSampler(SamplerOptions{HostID: "id-mlab", Alias: "mlab", Sources: fakeSources(&level, nil), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true})
	self.Tick(s0)
	d := New(Options{HostID: "id-mlab", Alias: "mlab", Version: "v0", Token: testToken, Sampler: self, WorkerEnabled: true, ExplicitLabelsOnly: true, Peers: []PeerAddr{{"air26", addr(srv)}}})
	d.Pool().PollOnce(context.Background())

	v := d.PoolView()
	if len(v) != 2 || v[0].Info.Alias != "mlab" || !v[0].Reachable || v[0].Facts == nil || !v[0].Info.Worker.ExplicitLabelsOnly || v[1].Info.Alias != "air26" {
		t.Fatalf("%+v", v)
	}
	// the route answers the same (it is the same function)
	w := do(d.SocketHandler(), "GET", "/v1/pool", "")
	pool, err := wire.Decode[wire.Pool](w.Body.Bytes())
	if err != nil || len(pool.Peers) != 2 || pool.Peers[0].Info.Alias != "mlab" || pool.Peers[0].Info.Worker != v[0].Info.Worker {
		t.Fatalf("%d %s %v", w.Code, w.Body, err)
	}
	// a change in the returned slice does not reach the poller's own copy
	v[1].Info.Alias = "changed"
	if got := d.PoolView()[1].Info.Alias; got != "air26" {
		t.Errorf("the view aliases the poller's state: %q", got)
	}
}

// #3904: /v1/info announces the host's speed so that a slower host can leave a job to a faster one. Always a number above 0 (an unset
// speed_factor is 1, the reference), so a peer that says nothing is a wwd from before the field.
func TestInfoAnnouncesTheSpeedFactor(t *testing.T) {
	unset := New(Options{HostID: "h1", Alias: "air26", Version: "v0", Token: testToken, WorkerEnabled: true})
	if i := unset.info(); i.Worker.SpeedFactor != 1 {
		t.Errorf("an unset speed is the reference, 1: %v", i.Worker.SpeedFactor)
	}
	slow := New(Options{HostID: "h2", Alias: "mserv", Version: "v0", Token: testToken, WorkerEnabled: true, SpeedFactor: 3})
	b, _ := json.Marshal(slow.info())
	if !strings.Contains(string(b), `"speed_factor":3`) {
		t.Errorf("not in the json: %s", b)
	}
	older := []byte(`{"host_id":"h","alias":"air19","version":"v","proto":3,"platform":"darwin","arch":"amd64","worker":{"enabled":true},"labels":[],"fingerprint":{}}`)
	if i, err := wire.DecodeLenient[wire.Info](older); err != nil || i.Worker.SpeedFactor != 0 {
		t.Errorf("an older peer says no speed, which reads 0 = unknown: %+v %v", i.Worker, err)
	}
}
