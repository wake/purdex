package daemon

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// R2 (critic): the gate shuts between two ticks. Until the next tick the ring's last record is still the v 2 one; every way out of
// the sampler (Latest, GET /v1/facts, GET /v1/pool's own entry, the first event of a new SSE stream) must hand out the v1 shape.
func gateRig(t *testing.T) (*Daemon, *Sampler, *atomic.Bool) {
	t.Helper()
	level := 1
	var open atomic.Bool
	open.Store(true)
	b := NewBroker()
	s := historySampler(&level, &open, nil)
	s.SetOnFacts(func(f wire.Facts) { b.PublishFacts(f) })
	d := New(Options{HostID: "h1", Alias: "mlab", Version: "v0", Token: testToken, Sampler: s, Broker: b})
	tickAt(s, s0)
	tickAt(s, s0.Add(5*time.Second))
	if f, _ := s.Latest(); f.V != 2 {
		t.Fatalf("setup: v %d", f.V)
	}
	return d, s, &open
}

func TestLatestFollowsTheGateBetweenTicks(t *testing.T) {
	_, s, open := gateRig(t)
	open.Store(false) // no tick since
	f, _ := s.Latest()
	if f.V != wire.WriteProto || f.Mem.DecompressRate != nil || f.Queue != nil {
		t.Fatalf("Latest handed out the v 2 record of before the gate shut: %+v", f)
	}
	open.Store(true)
	if f, _ := s.Latest(); f.V != 2 || f.Queue == nil {
		t.Fatalf("a read must not rewrite the ring: %+v", f)
	}
}

func TestFactsRouteAndNewStreamsFollowTheGate(t *testing.T) {
	d, _, open := gateRig(t)
	open.Store(false)
	w := do(d.SocketHandler(), "GET", "/v1/facts", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "decompress_rate") || strings.Contains(w.Body.String(), `"queue"`) {
		t.Fatalf("/v1/facts: %d %s", w.Code, w.Body)
	}
	if f, err := wire.Decode[wire.Facts](w.Body.Bytes()); err != nil || f.V != wire.WriteProto {
		t.Fatalf("v %d err %v", f.V, err)
	}
	ch, cancel := d.Events().Subscribe(false)
	defer cancel()
	e := <-ch
	if e.Type != wire.EventFacts || strings.Contains(string(e.Data), "decompress_rate") || strings.Contains(string(e.Data), `"queue"`) {
		t.Fatalf("a new stream's first event is the cached v 2 facts: %s", e.Data)
	}
	if f, err := wire.Decode[wire.Facts](e.Data); err != nil || f.V != wire.WriteProto {
		t.Fatalf("v %d err %v", f.V, err)
	}
	// with the gate open again a new stream gets the v 2 record
	open.Store(true)
	ch2, cancel2 := d.Events().Subscribe(false)
	defer cancel2()
	if e := <-ch2; !strings.Contains(string(e.Data), "decompress_rate") {
		t.Fatalf("%s", e.Data)
	}
}
