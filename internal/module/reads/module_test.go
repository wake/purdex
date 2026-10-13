package reads

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	agentpkg "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/reads"
)

// BG-4a (badge spec §7.2): the read route, lastRead, the reads=v1 frames, the cursor the daemon knows for a session, and the
// pruning against the session list the daemon already holds.

type fakeNotify struct {
	mu sync.Mutex
	fn func(agent.NotifyEvent)
}

func (f *fakeNotify) SubscribeNotify(fn func(agent.NotifyEvent)) func() {
	f.mu.Lock()
	f.fn = fn
	f.mu.Unlock()
	return func() { f.mu.Lock(); f.fn = nil; f.mu.Unlock() }
}

func (f *fakeNotify) emit(code, epoch string, seq uint64) {
	f.mu.Lock()
	fn := f.fn
	f.mu.Unlock()
	if fn != nil {
		fn(agent.NotifyEvent{SessionCode: code, Event: agentpkg.NormalizedEvent{Epoch: epoch, Seq: seq}})
	}
}

// codes is the fake in-memory session list: nil = unknown (never filled / invalidated / stale).
type codes struct {
	mu  sync.Mutex
	set map[string]struct{}
}

func (c *codes) get(time.Duration) (map[string]struct{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.set == nil {
		return map[string]struct{}{}, false // an unknown list: a caller that trusts the (empty) map instead of ok prunes everything
	}
	out := map[string]struct{}{}
	for k := range c.set {
		out[k] = struct{}{}
	}
	return out, true
}

func (c *codes) put(codes ...string) {
	c.mu.Lock()
	c.set = map[string]struct{}{}
	for _, k := range codes {
		c.set[k] = struct{}{}
	}
	c.mu.Unlock()
}

type env struct {
	m      *Module
	core   *core.Core
	notify *fakeNotify
	list   *codes
	mux    *http.ServeMux
	srv    *httptest.Server
}

// A valid session code is 6 base36 characters; these are fixed strings the codec accepts.
const (
	codeA = "aaaaaa"
	codeB = "bbbbbb"
	codeC = "cccccc"
)

func newEnv(t *testing.T) *env {
	t.Helper()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	e := &env{m: m, core: c, notify: &fakeNotify{}, list: &codes{}}
	m.notify, m.sessions = e.notify, e.list.get
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	e.mux = http.NewServeMux()
	m.RegisterRoutes(e.mux)
	e.mux.HandleFunc("/ws/host-events", c.Events.HandleHostEvents)
	e.srv = httptest.NewServer(e.mux)
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Stop(context.Background()); e.srv.Close() })
	return e
}

func (e *env) post(code, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+code+"/read", strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

// dial opens /ws/host-events with query and returns a reader of its text frames.
func (e *env) dial(t *testing.T, query string) *wsConn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(e.srv.URL, "http") + "/ws/host-events" + query
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := &wsConn{conn: conn, frames: make(chan core.HostEvent, 64)}
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				close(w.frames)
				return
			}
			var ev core.HostEvent
			if json.Unmarshal(raw, &ev) == nil {
				w.frames <- ev
			}
		}
	}()
	t.Cleanup(func() { conn.Close() })
	return w
}

type wsConn struct {
	conn   *websocket.Conn
	frames chan core.HostEvent
}

// next returns the next frame of type typ within a second.
func (w *wsConn) next(t *testing.T, typ string) core.HostEvent {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case ev, ok := <-w.frames:
			if !ok {
				t.Fatalf("connection closed waiting for %s", typ)
			}
			if ev.Type == typ {
				return ev
			}
		case <-deadline:
			t.Fatalf("no %s frame", typ)
		}
	}
}

func (w *wsConn) none(t *testing.T, typ string) {
	t.Helper()
	deadline := time.After(150 * time.Millisecond)
	for {
		select {
		case ev, ok := <-w.frames:
			if !ok {
				return
			}
			if ev.Type == typ {
				t.Fatalf("unexpected %s frame: %+v", typ, ev)
			}
		case <-deadline:
			return
		}
	}
}

func readValue(t *testing.T, ev core.HostEvent) reads.Read {
	t.Helper()
	var r reads.Read
	if err := json.Unmarshal([]byte(ev.Value), &r); err != nil {
		t.Fatalf("%s: %v", ev.Value, err)
	}
	return r
}

func TestRead_Accepted204AndForwardedToEveryOptedInConnection(t *testing.T) {
	e := newEnv(t)
	a, b := e.dial(t, "?reads=v1"), e.dial(t, "?reads=v1&nex=v1")
	plain := e.dial(t, "")
	a.next(t, "session.reads") // the (empty) snapshot of the connect
	b.next(t, "session.reads")
	rec := e.post(codeA, `{"through":{"epoch":"e1","seq":7}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	for name, c := range map[string]*wsConn{"a": a, "b": b} {
		ev := c.next(t, "session.read")
		if r := readValue(t, ev); r.Code != codeA || r.Through != (reads.Cursor{Epoch: "e1", Seq: 7}) {
			t.Fatalf("%s: frame %+v", name, ev)
		}
	}
	plain.none(t, "session.read")
	plain.none(t, "session.reads")
}

// lastRead only moves forward: a smaller or equal T is accepted (204) and announced to no one.
// Mutation gate: announce every POST → red.
func TestRead_OnlyAnAdvanceIsAnnounced(t *testing.T) {
	e := newEnv(t)
	c := e.dial(t, "?reads=v1")
	c.next(t, "session.reads")
	var fed []reads.Read
	var mu sync.Mutex
	e.m.feed.subscribe(func(r reads.Read) { mu.Lock(); fed = append(fed, r); mu.Unlock() })
	e.post(codeA, `{"through":{"epoch":"e1","seq":9}}`)
	c.next(t, "session.read")
	for _, body := range []string{`{"through":{"epoch":"e1","seq":9}}`, `{"through":{"epoch":"e1","seq":4}}`} {
		if rec := e.post(codeA, body); rec.Code != http.StatusNoContent {
			t.Fatalf("status %d", rec.Code)
		}
	}
	c.none(t, "session.read")
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(fed) != 1 {
		t.Fatalf("the feed got %d reads, want only the advance", len(fed))
	}
}

// Without `through` the daemon uses the newest cursor it has emitted for the session; with none known, a cursorless read.
func TestRead_ThroughOmittedUsesTheNewestEmittedCursor(t *testing.T) {
	e := newEnv(t)
	c := e.dial(t, "?reads=v1")
	c.next(t, "session.reads")
	e.notify.emit(codeA, "e1", 3)
	e.notify.emit(codeA, "e1", 8)
	e.notify.emit(codeA, "e1", 5) // out of order: the newest stays 8
	e.notify.emit(codeB, "e1", 100)
	time.Sleep(50 * time.Millisecond)
	e.post(codeA, `{}`)
	if r := readValue(t, c.next(t, "session.read")); r.Code != codeA || r.Through != (reads.Cursor{Epoch: "e1", Seq: 8}) {
		t.Fatalf("read = %+v", r)
	}
	e.post(codeC, ``)
	if r := readValue(t, c.next(t, "session.read")); r.Code != codeC || r.Through != (reads.Cursor{}) {
		t.Fatalf("a session with no known cursor: read = %+v", r)
	}
}

func TestRead_RequestValidation(t *testing.T) {
	e := newEnv(t)
	if rec := e.post(codeA, `{"through":`); rec.Code != http.StatusBadRequest {
		t.Fatalf("broken JSON: %d", rec.Code)
	}
	if rec := e.post(codeA, `{"through":{"epoch":"`+strings.Repeat("x", 200)+`","seq":1}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("an oversized epoch: %d", rec.Code)
	}
	if rec := e.post("not-a-code", `{}`); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"no_such_session"`) {
		t.Fatalf("a malformed code: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.post(codeA, strings.Repeat(" ", 5000)); rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized body: %d", rec.Code)
	}
}

// A code the in-memory session list does not have is 404 no_such_session; when the list is unknown (never filled, invalidated,
// stale) nothing is evidence, so the read is accepted. Mutation gate: always accept / always check → red.
func TestRead_UnknownSessionIs404OnlyWhenTheListIsKnown(t *testing.T) {
	e := newEnv(t)
	e.list.put(codeA)
	if rec := e.post(codeB, `{}`); rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != `{"error":"no_such_session"}` {
		t.Fatalf("absent from a known list: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.post(codeA, `{}`); rec.Code != http.StatusNoContent {
		t.Fatalf("present in the list: %d", rec.Code)
	}
	e.list.set = nil // unknown
	if rec := e.post(codeB, `{}`); rec.Code != http.StatusNoContent {
		t.Fatalf("with an unknown list: %d", rec.Code)
	}
	if _, ok := e.m.store.Get(codeB); !ok {
		t.Fatal("the accepted read was not remembered")
	}
}

// A connect gets every remembered read in one session.reads frame, an empty one too; a connection that did not opt in gets none.
func TestRead_ConnectGetsTheSnapshot(t *testing.T) {
	e := newEnv(t)
	empty := e.dial(t, "?reads=v1")
	var snap struct {
		Reads []reads.Read `json:"reads"`
	}
	if err := json.Unmarshal([]byte(empty.next(t, "session.reads").Value), &snap); err != nil || snap.Reads == nil || len(snap.Reads) != 0 {
		t.Fatalf("empty snapshot = %+v err=%v (want reads:[])", snap, err)
	}
	e.post(codeA, `{"through":{"epoch":"e1","seq":2}}`)
	e.post(codeB, `{"through":{"epoch":"e1","seq":5}}`)
	late := e.dial(t, "?reads=v1")
	if err := json.Unmarshal([]byte(late.next(t, "session.reads").Value), &snap); err != nil || len(snap.Reads) != 2 {
		t.Fatalf("snapshot = %+v err=%v", snap, err)
	}
	if snap.Reads[0].Code != codeA || snap.Reads[1].Through.Seq != 5 {
		t.Fatalf("snapshot = %+v", snap.Reads)
	}
	e.dial(t, "").none(t, "session.reads")
}

// A full buffer closes only an opted-in connection, and only over a read frame; the plain one is untouched.
func TestRead_AFullBufferClosesOnlyTheOptedInConnection(t *testing.T) {
	e := newEnv(t)
	slow := e.core.Events.AddTestSubscriberWith(core.FeatureReadsV1)
	plain := e.core.Events.AddTestSubscriber()
	defer e.core.Events.RemoveTestSubscriber(plain)
	for i := 0; i < cap(slow.SendCh()); i++ {
		slow.TrySend([]byte(`{"type":"fill"}`))
	}
	for i := 0; i < cap(plain.SendCh()); i++ {
		plain.TrySend([]byte(`{"type":"fill"}`))
	}
	e.post(codeA, `{"through":{"epoch":"e1","seq":1}}`)
	select {
	case <-slow.Done():
	case <-time.After(time.Second):
		t.Fatal("the opted-in connection with a full buffer was not closed")
	}
	select {
	case <-plain.Done():
		t.Fatal("a connection that did not opt in was closed over a read frame")
	default:
	}
}

// The feed (what BG-4b's push side subscribes to) gets the accepted reads in order and stops at unsubscribe.
func TestFeed_AcceptedReadsInOrderUntilUnsubscribe(t *testing.T) {
	e := newEnv(t)
	got := make(chan reads.Read, 8)
	svc, ok := e.core.Registry.Get(reads.FeedKey)
	feed, isFeed := svc.(reads.Feed)
	if !ok || !isFeed {
		t.Fatalf("no reads.Feed under %q", reads.FeedKey)
	}
	unsub := feed.SubscribeReads(func(r reads.Read) { got <- r })
	e.post(codeA, `{"through":{"epoch":"e1","seq":1}}`)
	e.post(codeB, `{"through":{"epoch":"e1","seq":2}}`)
	e.post(codeA, `{"through":{"epoch":"e1","seq":3}}`)
	for i, want := range []string{codeA, codeB, codeA} {
		select {
		case r := <-got:
			if r.Code != want {
				t.Fatalf("read %d = %s, want %s", i, r.Code, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("read %d never arrived", i)
		}
	}
	unsub()
	e.post(codeC, `{"through":{"epoch":"e1","seq":1}}`)
	select {
	case r := <-got:
		t.Fatalf("a read after unsubscribe: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

// Pruning: the codes the known in-memory list no longer has go (from lastRead and from the newest-cursor memory); an unknown
// list prunes nothing. Mutation gate: prune on an unknown list / never prune → red.
func TestPrune_AgainstTheInMemoryList(t *testing.T) {
	e := newEnv(t)
	e.post(codeA, `{"through":{"epoch":"e1","seq":1}}`)
	e.post(codeB, `{"through":{"epoch":"e1","seq":1}}`)
	e.notify.emit(codeB, "e1", 4)
	time.Sleep(50 * time.Millisecond)

	e.list.set = nil
	e.m.pruneOnce()
	if e.m.store.Len() != 2 {
		t.Fatal("pruned on an unknown list")
	}
	e.list.put(codeA)
	e.m.pruneOnce()
	if _, ok := e.m.store.Get(codeB); ok {
		t.Fatal("the read of an ended session survived the prune")
	}
	if _, ok := e.m.store.Get(codeA); !ok {
		t.Fatal("the read of a live session was pruned")
	}
	if _, ok := e.m.newestCursor(codeB); ok {
		t.Fatal("the newest cursor of an ended session survived the prune")
	}
}

// The prune runs by itself every pruneEvery, and Stop ends it (no goroutine left).
func TestPrune_RunsOnATickerAndStopEndsIt(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	list := &codes{}
	m.notify, m.sessions, m.pruneEvery = &fakeNotify{}, list.get, 20*time.Millisecond
	if err := m.Init(c); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.store.Advance(codeA, reads.Cursor{Epoch: "e1", Seq: 1})
	list.put(codeB)
	deadline := time.Now().Add(2 * time.Second)
	for m.store.Len() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the ticker never pruned")
		}
		time.Sleep(5 * time.Millisecond)
	}
	done := make(chan struct{})
	go func() { m.Stop(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return: the prune goroutine is still running")
	}
}

// The newest-cursor memory keeps the largest seq of an epoch and follows a new epoch (a restart); it is bounded.
func TestNewestCursor_FollowsEpochsAndIsBounded(t *testing.T) {
	e := newEnv(t)
	e.notify.emit(codeA, "e1", 10)
	e.notify.emit(codeA, "e2", 1) // a new epoch replaces the old one whatever the seq
	time.Sleep(50 * time.Millisecond)
	if got, _ := e.m.newestCursor(codeA); got != (reads.Cursor{Epoch: "e2", Seq: 1}) {
		t.Fatalf("newest = %+v", got)
	}
	for i := 0; i < maxNewest+50; i++ {
		e.m.noteEvent(string(rune('a'+i%26))+strings.Repeat("x", i%7)+strings.Repeat("y", i/26), reads.Cursor{Epoch: "e2", Seq: 1})
	}
	if n := e.m.newestLen(); n > maxNewest {
		t.Fatalf("newest memory holds %d codes, bound is %d", n, maxNewest)
	}
}

func TestRegisterRoutes_ThePattern(t *testing.T) {
	e := newEnv(t)
	_, pattern := e.mux.Handler(httptest.NewRequest(http.MethodPost, "/api/sessions/aaaaaa/read", nil))
	if pattern != "POST /api/sessions/{code}/read" {
		t.Fatalf("pattern = %q", pattern)
	}
}
