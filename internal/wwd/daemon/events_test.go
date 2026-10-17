package daemon

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

const queuedJob = `{
  "id": "j_01JC", "origin": "mlab",
  "repo": {"name": "purdex", "root": "abc123", "remote": "git@github.com:wake/purdex.git"},
  "snapshot": {"commit": "c0ffee", "tree": "7ee", "base": "ba5e", "rel_cwd": "spa"},
  "task": "spa-related", "args": ["src/a.ts"], "class": "merge",
  "labels": ["os:darwin"], "locks": [], "cacheable": true, "timeout_s": 1800,
  "interruptible": false, "queue_timeout_s": 0, "adhoc": false,
  "supersede_key": "deadbeef:spa-related",
  "submitter": {"session": "s1", "ref": "_abc123", "team": "wwd", "host": "mlab"},
  "created_at": "2026-10-18T05:00:00+08:00", "state": "queued", "claim": null, "result": null
}`

func jobEvent(adhoc bool) wire.JobEvent {
	j, err := wire.Decode[wire.Job]([]byte(queuedJob))
	if err != nil {
		panic(err)
	}
	j.AdHoc = adhoc
	return wire.JobEvent{Host: "mlab", Job: j}
}

func recv(t *testing.T, ch <-chan wire.Event) wire.Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
	}
	return wire.Event{}
}

func none(t *testing.T, ch <-chan wire.Event) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("unexpected event %s", e.Type)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBrokerFansOutAndDropsAdHocForPeers(t *testing.T) {
	b := NewBroker()
	local, cancelL := b.Subscribe(false)
	peer, cancelP := b.Subscribe(true)
	defer cancelL()
	defer cancelP()

	b.PublishJob(jobEvent(true))
	if e := recv(t, local); e.Type != wire.EventJob {
		t.Fatalf("local got %s", e.Type)
	}
	none(t, peer) // an ad-hoc job's event never reaches a peer, not even without its args (spec §4.4, §11)

	b.PublishJob(jobEvent(false))
	if e := recv(t, local); e.Type != wire.EventJob {
		t.Fatalf("local got %s", e.Type)
	}
	e := recv(t, peer)
	if e.Type != wire.EventJob {
		t.Fatalf("peer got %s", e.Type)
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}

	b.PublishFacts(wire.Facts{V: wire.Proto, Derived: wire.Derived{Zone: wire.ZoneNormal}})
	if recv(t, local).Type != wire.EventFacts || recv(t, peer).Type != wire.EventFacts {
		t.Fatal("facts not delivered to both")
	}
}

func TestBrokerNeverBlocksOnASlowSubscriber(t *testing.T) {
	b := NewBroker()
	slow, cancel := b.Subscribe(false) // nobody reads from it
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10*subscriberBuffer; i++ {
			b.PublishFacts(wire.Facts{V: wire.Proto, Derived: wire.Derived{Zone: wire.ZoneNormal}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that does not read")
	}
	// the slow subscriber was dropped (its channel is closed), the broker lives on
	n := 0
	for range slow {
		n++
	}
	if n > subscriberBuffer {
		t.Fatalf("slow subscriber got %d events, buffer is %d", n, subscriberBuffer)
	}
	other, cancel2 := b.Subscribe(false)
	defer cancel2()
	b.PublishFacts(wire.Facts{V: wire.Proto, Derived: wire.Derived{Zone: wire.ZoneNormal}})
	recv(t, other)
}

func TestCancelStopsDelivery(t *testing.T) {
	b := NewBroker()
	ch, cancel := b.Subscribe(false)
	cancel()
	cancel() // twice is fine
	b.PublishFacts(wire.Facts{V: wire.Proto, Derived: wire.Derived{Zone: wire.ZoneNormal}})
	if _, open := <-ch; open {
		t.Fatal("a cancelled subscription still delivers")
	}
}

// The SSE stream: TCP needs the token, the first frame is the current facts, each
// frame is `event: <type>` and `data: <Event JSON>`.
func TestEventsStream(t *testing.T) {
	level := 1
	d := New(Options{HostID: "h1", Alias: "mlab", Version: "v0", Token: testToken, Sampler: newSampler(&level, nil)})
	d.opts.Sampler.Tick(s0)
	srv := httptest.NewServer(d.TCPHandler())
	defer srv.Close()

	if resp, err := http.Get(srv.URL + "/v1/events"); err != nil || resp.StatusCode != 401 {
		t.Fatalf("no token: %v %v", resp, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	readFrame := func() (event, data string) {
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && data != "":
				return
			}
		}
		t.Fatal("stream ended")
		return
	}
	ev, data := readFrame()
	if ev != "facts" {
		t.Fatalf("first frame %q", ev)
	}
	e, err := wire.Decode[wire.Event]([]byte(data))
	if err != nil || e.Type != wire.EventFacts {
		t.Fatalf("%v %+v", err, e)
	}

	d.PublishJob(jobEvent(true))  // a peer (TCP) must not see this
	d.PublishJob(jobEvent(false)) // but this one
	ev, data = readFrame()
	if ev != "job" {
		t.Fatalf("frame %q", ev)
	}
	e, _ = wire.Decode[wire.Event]([]byte(data))
	je, err := wire.Decode[wire.JobEvent](e.Data)
	if err != nil || je.Job.AdHoc {
		t.Fatalf("an ad-hoc job reached the TCP stream: %v %+v", err, je.Job)
	}
}

func TestSocketStreamCarriesAdHocJobs(t *testing.T) {
	level := 1
	d := New(Options{HostID: "h1", Alias: "mlab", Version: "v0", Token: testToken, Sampler: newSampler(&level, nil)})
	srv := httptest.NewServer(d.SocketHandler())
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	time.Sleep(100 * time.Millisecond) // let the handler subscribe
	d.PublishJob(jobEvent(true))
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	seen := false
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "event: job") {
			seen = true
			break
		}
	}
	if !seen {
		t.Fatal("the local socket must see ad-hoc job events")
	}
}
