package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/team"
)

// ---- a fake team.ApprovalFeed that counts what the connection holds ----

type fakeFeed struct {
	mu       sync.Mutex
	holds    int
	minHolds int
	subs     map[int]func(string, team.Approval)
	next     int
	open     []team.Approval
	err      error
	onArm    func(fn func(string, team.Approval)) // runs right after arming, before SubscribeSession returns
}

func (f *fakeFeed) HoldResponder() func() {
	f.mu.Lock()
	f.holds++
	f.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			f.holds--
			if f.holds < f.minHolds {
				f.minHolds = f.holds
			}
			f.mu.Unlock()
		})
	}
}

func (f *fakeFeed) SubscribeSession(_ string, fn func(string, team.Approval)) ([]team.Approval, func(), error) {
	f.mu.Lock()
	if f.err != nil {
		f.mu.Unlock()
		return nil, nil, f.err
	}
	if f.subs == nil {
		f.subs = map[int]func(string, team.Approval){}
	}
	f.next++
	id := f.next
	f.subs[id] = fn
	open := append([]team.Approval{}, f.open...)
	hook := f.onArm
	f.mu.Unlock()
	if hook != nil {
		hook(fn)
	}
	var once sync.Once
	return open, func() {
		once.Do(func() {
			f.mu.Lock()
			delete(f.subs, id)
			f.mu.Unlock()
		})
	}, nil
}

func (f *fakeFeed) emit(op string, a team.Approval) {
	f.mu.Lock()
	fns := make([]func(string, team.Approval), 0, len(f.subs))
	for _, fn := range f.subs {
		fns = append(fns, fn)
	}
	f.mu.Unlock()
	for _, fn := range fns {
		fn(op, a)
	}
}

func (f *fakeFeed) state() (holds, subs int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.holds, len(f.subs)
}

// ---- a WebSocket client ----

type wsFrame struct {
	Type  string          `json:"type"`
	Seq   uint64          `json:"seq"`
	Value json.RawMessage `json:"value"`
}

type wsClient struct {
	t    *testing.T
	conn *websocket.Conn
	last uint64
}

func (e *env) server() *httptest.Server {
	e.t.Helper()
	srv := httptest.NewServer(e.mux)
	e.t.Cleanup(srv.Close)
	return srv
}

func (e *env) dial(srv *httptest.Server, query string) (*wsClient, *http.Response, error) {
	e.t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/conversations/claude/" + sid + query
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, resp, err
	}
	c := &wsClient{t: e.t, conn: conn}
	e.t.Cleanup(func() { conn.Close() })
	return c, resp, nil
}

func (e *env) connect(srv *httptest.Server, query string) *wsClient {
	e.t.Helper()
	c, resp, err := e.dial(srv, query)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		e.t.Fatalf("dial: %v (status %d)", err, code)
	}
	return c
}

// read returns the next frame, checking that seq is contiguous per connection.
func (c *wsClient) read() wsFrame {
	c.t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, b, err := c.conn.ReadMessage()
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	var f wsFrame
	if err := json.Unmarshal(b, &f); err != nil {
		c.t.Fatalf("frame %s: %v", b, err)
	}
	if f.Seq != c.last+1 {
		c.t.Fatalf("seq %d after %d (%s): not contiguous", f.Seq, c.last, f.Type)
	}
	c.last = f.Seq
	return f
}

func (c *wsClient) expect(typ string) wsFrame {
	c.t.Helper()
	f := c.read()
	if f.Type != typ {
		c.t.Fatalf("frame %q (seq %d), want %q: %.200s", f.Type, f.Seq, typ, f.Value)
	}
	return f
}

// closed reports whether the server ends the connection (draining frames) within the time. A read that times out
// poisons a gorilla connection, so there is one deadline for the whole wait: a timeout means it is still open.
func (c *wsClient) closed(within time.Duration) bool {
	_ = c.conn.SetReadDeadline(time.Now().Add(within))
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			var ne interface{ Timeout() bool }
			return !(errors.As(err, &ne) && ne.Timeout())
		}
	}
}

func settled(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("not settled: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func wsEnv(t *testing.T) (*env, *fakeFeed) {
	t.Helper()
	e := newEnv(t)
	feed := &fakeFeed{}
	e.mod.feed = feed
	e.mod.tweak = wsTuning{poll: 10 * time.Millisecond, reresolve: 20 * time.Millisecond}
	return e, feed
}

func approval(id string) team.Approval {
	return team.Approval{ID: id, Kind: team.KindHookPermission, Origin: team.Origin{SessionID: sid}, State: team.StateOpen}
}

// ---- tests ----

func TestWS_FirstFramesAreSnapshotsWithContiguousSeq(t *testing.T) {
	e, feed := wsEnv(t)
	e.transcript(idleTurns(3))
	feed.open = []team.Approval{approval("a1")}
	srv := e.server()
	c := e.connect(srv, "")
	f := c.expect("conversation.snapshot")
	if f.Seq != 1 {
		t.Fatalf("first seq %d", f.Seq)
	}
	var snap snapshot
	if err := json.Unmarshal(f.Value, &snap); err != nil || len(snap.Conversation.Turns) != 3 || snap.Cursor == "" {
		t.Fatalf("snapshot %v: %.200s", err, f.Value)
	}
	a := c.expect("approvals.snapshot")
	var as struct {
		Approvals []team.Approval `json:"approvals"`
	}
	if err := json.Unmarshal(a.Value, &as); err != nil || len(as.Approvals) != 1 || as.Approvals[0].ID != "a1" {
		t.Fatalf("approvals.snapshot %v: %s", err, a.Value)
	}
}

func TestWS_AppendedRowIsOneChangesFrame(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(2))
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	appendRows(t, p, userRow("u9", 60, "live row"))
	f := c.expect("conversation.changes")
	var inc incResp
	if err := json.Unmarshal(f.Value, &inc); err != nil || len(inc.Changes) == 0 || inc.Cursor == "" {
		t.Fatalf("changes %v: %.300s", err, f.Value)
	}
	if last := inc.Changes[len(inc.Changes)-1]; last.Turn.Index != 2 {
		t.Fatalf("last change = %+v", last)
	}
	// nothing more arrives while nothing happens
	_ = c.conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if _, b, err := c.conn.ReadMessage(); err == nil {
		t.Fatalf("an unexpected frame: %s", b)
	}
}

func TestWS_AfterCatchesUpWithExactlyTheMissedChanges(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(2))
	snap := decode(t, e.get("/api/conversations/claude/"+sid))
	appendRows(t, p, userRow("u8", 50, "missed 1"), assistantRow("a8", 51, obj{"type": "text", "text": "missed reply"}))
	srv := e.server()
	c := e.connect(srv, "?after="+snap.Cursor)
	f := c.expect("conversation.changes") // the catch-up, not a snapshot
	var inc incResp
	if err := json.Unmarshal(f.Value, &inc); err != nil || len(inc.Changes) == 0 {
		t.Fatalf("catch-up %v: %.300s", err, f.Value)
	}
	for _, ch := range inc.Changes {
		if ch.Turn.Index < 1 {
			t.Fatalf("turn %d was not missed", ch.Turn.Index)
		}
	}
	c.expect("approvals.snapshot")
	// a stale cursor gets a snapshot first
	c2 := e.connect(srv, "?after=ffffffffffffffff:3")
	c2.expect("conversation.snapshot")
}

func TestWS_RewrittenFileIsResetThenSnapshotWithTheConnectionsTurns(t *testing.T) {
	e, _ := wsEnv(t)
	e.transcript(idleTurns(10))
	srv := e.server()
	c := e.connect(srv, "?turns=3")
	f := c.expect("conversation.snapshot")
	var snap snapshot
	_ = json.Unmarshal(f.Value, &snap)
	if len(snap.Conversation.Turns) != 3 {
		t.Fatalf("turns=3 gave %d", len(snap.Conversation.Turns))
	}
	c.expect("approvals.snapshot")
	e.transcript(idleTurns(8)) // the file shrank: a new epoch
	c.expect("conversation.reset")
	f = c.expect("conversation.snapshot")
	_ = json.Unmarshal(f.Value, &snap)
	if len(snap.Conversation.Turns) != 3 || snap.Window.TotalTurns != 8 {
		t.Fatalf("after the reset: %d turns of %d, want the connection's 3 of 8", len(snap.Conversation.Turns), snap.Window.TotalTurns)
	}
}

func TestWS_PaneGoesAwayIsAHeaderFrame(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(1))
	e.owners.own = []convfeed.Owner{{TranscriptPath: p, Status: "running", SeenAt: 1}}
	srv := e.server()
	c := e.connect(srv, "")
	f := c.expect("conversation.snapshot")
	var snap snapshot
	_ = json.Unmarshal(f.Value, &snap)
	if !snap.Header.Live || snap.Header.Status != "running" {
		t.Fatalf("header %+v", snap.Header)
	}
	c.expect("approvals.snapshot")
	e.owners.mu.Lock()
	e.owners.own = nil
	e.owners.mu.Unlock()
	// the liveness change settles the unfinished turn too (SetLive), so the frame may be a changes frame that carries
	// the header; a header-only change is a conversation.header frame (the next test)
	f = c.read()
	var hv struct {
		Header struct {
			Live   bool   `json:"live"`
			Status string `json:"status"`
		} `json:"header"`
		Cursor string `json:"cursor"`
	}
	if (f.Type != "conversation.header" && f.Type != "conversation.changes") || json.Unmarshal(f.Value, &hv) != nil ||
		hv.Header.Live || hv.Header.Status != "ended" || hv.Cursor == "" {
		t.Fatalf("frame %q: %s", f.Type, f.Value)
	}
}

// Only the header changed (a new title row): a conversation.header frame, not an empty changes frame.
func TestWS_HeaderOnlyChangeIsAHeaderFrame(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(1))
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	appendRows(t, p, row(obj{"type": "custom-title", "customTitle": "A new title", "sessionId": sid}))
	h := c.expect("conversation.header")
	var hv struct {
		Header struct {
			Title string `json:"title"`
		} `json:"header"`
		Cursor string `json:"cursor"`
	}
	if err := json.Unmarshal(h.Value, &hv); err != nil || hv.Header.Title != "A new title" || hv.Cursor == "" {
		t.Fatalf("header frame %v: %s", err, h.Value)
	}
}

func TestWS_ApprovalOpsFollowTheSnapshotInOrder(t *testing.T) {
	e, feed := wsEnv(t)
	e.transcript(idleTurns(1))
	// an op that arrives between the arming and the return of SubscribeSession must queue behind approvals.snapshot
	feed.onArm = func(fn func(string, team.Approval)) { fn("opened", approval("early")) }
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	as := c.expect("approvals.snapshot")
	if strings.Contains(string(as.Value), "early") {
		t.Fatalf("the early op leaked into the snapshot: %s", as.Value)
	}
	op := c.expect("approval")
	var ov approvalOpJSON
	if err := json.Unmarshal(op.Value, &ov); err != nil || ov.Op != "opened" || ov.Approval.ID != "early" {
		t.Fatalf("op %v: %s", err, op.Value)
	}
	settled(t, "armed", func() bool { _, s := feed.state(); return s == 1 })
	feed.onArm = nil
	feed.emit("closed", approval("early"))
	op = c.expect("approval")
	_ = json.Unmarshal(op.Value, &ov)
	if ov.Op != "closed" || ov.Approval.ID != "early" {
		t.Fatalf("op %s", op.Value)
	}
}

func TestWS_SubscribeErrorClosesTheConnectionWithoutAnEmptySnapshot(t *testing.T) {
	e, feed := wsEnv(t)
	e.transcript(idleTurns(1))
	feed.err = errors.New("team.db unreadable")
	srv := e.server()
	c := e.connect(srv, "")
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, b, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
		if strings.Contains(string(b), "approvals.snapshot") {
			t.Fatalf("an empty approvals snapshot was sent: %s", b)
		}
	}
	settled(t, "hold released", func() bool { h, _ := feed.state(); return h == 0 })
}

func TestWS_OpenConversationIsAResponderAndReleasesOnClose(t *testing.T) {
	e, feed := wsEnv(t)
	e.transcript(idleTurns(1))
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	if h, s := feed.state(); h != 1 || s != 1 {
		t.Fatalf("while open: holds %d subs %d, want 1 and 1", h, s)
	}
	c.conn.Close()
	settled(t, "released", func() bool { h, s := feed.state(); return h == 0 && s == 0 })
}

// The count returns to zero on every way a connection can end.
func TestWS_EveryClosePathReleasesTheResponderAndTheSubscription(t *testing.T) {
	paths := map[string]func(e *env, c *wsClient){
		"client closes politely": func(e *env, c *wsClient) {
			_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
			c.conn.Close()
		},
		"client drops the socket": func(e *env, c *wsClient) { c.conn.UnderlyingConn().Close() },
		"module Stop": func(e *env, c *wsClient) {
			if err := e.mod.Stop(context.Background()); err != nil {
				e.t.Error(err)
			}
		},
	}
	for name, closeIt := range paths {
		t.Run(name, func(t *testing.T) {
			e, feed := wsEnv(t)
			e.transcript(idleTurns(2))
			e.mod.cache = convfeed.NewCache(convfeed.CacheOptions{Max: 1}) // before Start: the sweeper reads it
			if name == "module Stop" {
				if err := e.mod.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			srv := e.server()
			var cs []*wsClient
			for i := 0; i < 3; i++ {
				c := e.connect(srv, "")
				c.expect("conversation.snapshot")
				c.expect("approvals.snapshot")
				cs = append(cs, c)
			}
			if h, s := feed.state(); h != 3 || s != 3 {
				t.Fatalf("open: holds %d subs %d, want 3 and 3", h, s)
			}
			for _, c := range cs {
				closeIt(e, c)
			}
			settled(t, "all released", func() bool { h, s := feed.state(); return h == 0 && s == 0 })
			if feed.minHolds < 0 {
				t.Fatalf("the hold count went to %d", feed.minHolds)
			}
			// every pin is released too: the only cache slot is free for another conversation
			settled(t, "pins released", func() bool {
				_, release, err := e.mod.cache.Acquire(context.Background(), "someone-else")
				if err == nil {
					release()
				}
				return err == nil
			})
		})
	}
}

func TestWS_NoUpgradeReleasesThePinAndAnsweredBeforeUpgrade(t *testing.T) {
	e, feed := wsEnv(t)
	e.transcript(idleTurns(1))
	e.mod.cache = convfeed.NewCache(convfeed.CacheOptions{Max: 1})
	// a plain GET is not a handshake: the upgrade fails after the entry was acquired
	if w := e.get("/ws/conversations/claude/" + sid); w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
	if _, release, err := e.mod.cache.Acquire(context.Background(), "someone-else"); err != nil {
		t.Fatalf("the pin of a failed upgrade leaked: %v", err)
	} else {
		release()
	}
	if h, s := feed.state(); h != 0 || s != 0 {
		t.Fatalf("holds %d subs %d after a failed upgrade", h, s)
	}
}

func TestWS_BusyAndValidationAreAnsweredBeforeTheUpgrade(t *testing.T) {
	e, _ := wsEnv(t)
	e.transcript(idleTurns(1))
	srv := e.server()
	for _, c := range []struct {
		query  string
		status int
	}{
		{"?after=junk", 400}, {"?turns=0", 400}, {"?turns=201", 400}, {"?before=3", 400}, {"?around=x", 400},
	} {
		_, resp, err := e.dial(srv, c.query)
		if err == nil || resp == nil || resp.StatusCode != c.status {
			t.Errorf("%s: err %v resp %v, want %d before any upgrade", c.query, err, resp, c.status)
		}
	}
	// not found
	os.Remove(e.home + "/.claude/projects/-work-x/" + sid + ".jsonl")
	if _, resp, err := e.dial(srv, ""); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Errorf("missing transcript: err %v resp %v", err, resp)
	}
	e.transcript(idleTurns(1))
	// every slot pinned: 503 before the upgrade
	e.mod.cache = convfeed.NewCache(convfeed.CacheOptions{Max: 1})
	_, release, err := e.mod.cache.Acquire(context.Background(), "someone-else")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, resp, err := e.dial(srv, ""); err == nil || resp == nil || resp.StatusCode != 503 {
		t.Errorf("busy: err %v resp %v, want a plain 503", err, resp)
	}
}

// A reader that stops reading gets fewer, larger frames — not a disconnect — and nothing is lost.
func TestWS_SlowReaderGetsCoalescedChangesAndEveryChange(t *testing.T) {
	e, _ := wsEnv(t)
	e.mod.tweak.poll = 2 * time.Millisecond
	e.mod.tweak.queue = 8
	pad := strings.Repeat("x", 200_000)
	p := e.transcript(idleTurns(1))
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")

	const rows = 80
	for i := 0; i < rows; i++ { // the client reads nothing meanwhile
		appendRows(t, p, userRow(fmt.Sprintf("slow%03d", i), float64(100+i), pad))
		time.Sleep(3 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)

	// Drain: every frame is a legal one (a catch-up too big for one frame is a reset + snapshot, spec §8.2), the
	// connection is still up, far fewer frames than rows came, and the stream ends at the entry's latest cursor.
	entry, release, err := e.mod.cache.Acquire(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	frames := 0
	var lastCursor string
	deadline := time.Now().Add(20 * time.Second)
	for lastCursor != entry.Cursor() && time.Now().Before(deadline) {
		f := c.read()
		frames++
		switch f.Type {
		case "conversation.changes", "conversation.snapshot", "conversation.header":
			var v struct {
				Cursor string `json:"cursor"`
			}
			if err := json.Unmarshal(f.Value, &v); err != nil || v.Cursor == "" {
				t.Fatalf("%s frame without a cursor: %v", f.Type, err)
			}
			lastCursor = v.Cursor
		case "conversation.reset":
		default:
			t.Fatalf("frame %q in a slow reader's stream", f.Type)
		}
	}
	if lastCursor != entry.Cursor() {
		t.Fatalf("the stream stopped at %q, the entry is at %q: changes were lost", lastCursor, entry.Cursor())
	}
	if frames >= rows {
		t.Fatalf("%d frames for %d rows: nothing was coalesced", frames, rows)
	}
}

// A queue that overflows for real ends the connection (and frees everything).
func TestWS_RealOverflowClosesAndFrees(t *testing.T) {
	e, feed := wsEnv(t)
	e.mod.tweak.queue = 4
	e.transcript(idleTurns(1))
	srv := e.server()
	before := runtime.NumGoroutine()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	big := approval("big")
	big.Payload = json.RawMessage(`{"pad":"` + strings.Repeat("x", 1<<20) + `"}`)
	for i := 0; i < 64; i++ { // a flood of approval ops to a client that reads nothing
		feed.emit("opened", big)
	}
	settled(t, "released after the overflow", func() bool { h, s := feed.state(); return h == 0 && s == 0 })
	if !c.closed(5 * time.Second) {
		t.Fatal("the connection was not closed")
	}
	srv.Close()
	settled(t, "goroutines back", func() bool { return runtime.NumGoroutine() <= before+2 })
}

// A changes frame is cut over to reset + snapshot by its whole size: a body that fits the cap alone but not with the
// frame around it is refused by encodeIncrement.
func TestWS_ChangesBodyIsMeasuredWithTheFrameAroundIt(t *testing.T) {
	e, _ := wsEnv(t)
	p := e.transcript(idleTurns(2))
	entry, release, err := e.mod.cache.Acquire(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := entry.Exclusive(context.Background(), func() error { return e.mod.refresh(context.Background(), entry, sid) }); err != nil {
		t.Fatal(err)
	}
	cur := entry.Cursor()
	epoch, rev, _ := convfeed.ParseCursor(cur)
	appendRows(t, p, userRow("u9", 60, "a row"))
	if err := entry.Exclusive(context.Background(), func() error { return e.mod.refresh(context.Background(), entry, sid) }); err != nil {
		t.Fatal(err)
	}
	inc := entry.Increment(epoch, rev)
	body, ok := e.mod.encodeIncrement(inc, "h", 0)
	if !ok {
		t.Fatal("setup")
	}
	e.mod.maxBody = len(body) + frameOverhead - 1
	if _, ok := e.mod.encodeIncrement(inc, "h", frameOverhead); ok {
		t.Fatal("a body that fits only without its frame was accepted")
	}
	if _, ok := e.mod.encodeIncrement(inc, "h", 0); !ok {
		t.Fatal("the HTTP form (no frame) must still fit")
	}
	// and the frame really is smaller than the allowance
	f, _ := json.Marshal(frame{Type: "conversation.changes", Seq: 1<<64 - 1, Value: json.RawMessage(`{}`)})
	if len(f)-2 > frameOverhead {
		t.Fatalf("a frame adds %d bytes, the allowance is %d", len(f)-2, frameOverhead)
	}
}

// Once the module is stopping, a new connection is refused before the upgrade (and none is left behind).
func TestWS_NoConnectionIsAdmittedAfterStop(t *testing.T) {
	e, feed := wsEnv(t)
	e.transcript(idleTurns(1))
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	if err := e.mod.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, resp, err := e.dial(srv, "")
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("after Stop: err %v resp %v, want a plain 503", err, resp)
	}
	if h, s := feed.state(); h != 0 || s != 0 {
		t.Fatalf("holds %d subs %d after Stop", h, s)
	}
}

// The callback the team module runs under its event lock only queues: a huge approval costs it nothing (the writer
// encodes), so it cannot hold up another approval event.
func TestWS_ApprovalCallbackDoesNoEncodingUnderTheLock(t *testing.T) {
	e, feed := wsEnv(t)
	e.transcript(idleTurns(1))
	srv := e.server()
	c := e.connect(srv, "")
	c.expect("conversation.snapshot")
	c.expect("approvals.snapshot")
	huge := approval("huge")
	huge.Payload = json.RawMessage(`{"pad":"` + strings.Repeat("x", 8<<20) + `"}`)
	t0 := time.Now()
	feed.emit("opened", huge)
	if d := time.Since(t0); d > 20*time.Millisecond {
		t.Fatalf("the callback took %v for an 8 MiB approval: it is encoding under the lock", d)
	}
	op := c.expect("approval") // and it still arrives, whole
	if !strings.Contains(string(op.Value), `"huge"`) || len(op.Value) < 8<<20 {
		t.Fatalf("approval frame of %d bytes", len(op.Value))
	}
}

// A Stop that is still waiting for its lifetime's connections is not disturbed by a connection of the next lifetime.
func TestModule_StopWaitsOnlyForItsOwnLifetimesConnections(t *testing.T) {
	e, _ := wsEnv(t)
	e.transcript(idleTurns(1))
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.mod.mu.Lock()
	old := e.mod.cur
	e.mod.mu.Unlock()
	old.ws.Add(1) // a connection of the first lifetime that is slow to leave
	stopped := make(chan error, 1)
	go func() { stopped <- e.mod.Stop(context.Background()) }()
	settled(t, "the first lifetime is cancelled", func() bool { return old.ctx.Err() != nil })
	settled(t, "its sweeper returned (the module may start again)", func() bool {
		e.mod.mu.Lock()
		defer e.mod.mu.Unlock()
		return !e.mod.running
	})
	if err := e.mod.Start(context.Background()); err != nil { // the next lifetime
		t.Fatal(err)
	}
	srv := e.server()
	c := e.connect(srv, "") // a connection of the next lifetime
	c.expect("conversation.snapshot")
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned (%v) while its own lifetime's connection was still there", err)
	case <-time.After(100 * time.Millisecond):
	}
	old.ws.Done() // the slow connection leaves: the first Stop completes without waiting for the new one
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the first Stop waited for a connection of the next lifetime")
	}
	if c.closed(200 * time.Millisecond) {
		t.Fatal("the first Stop closed the next lifetime's connection")
	}
	if err := e.mod.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
