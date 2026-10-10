package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/wake/purdex/internal/convfeed"
	"github.com/wake/purdex/internal/convmodel"
	"github.com/wake/purdex/internal/team"
)

// Defaults of the live stream (spec §8.2); the Module fields of the same names let tests shorten them.
const (
	defaultPollEvery      = 500 * time.Millisecond
	defaultReresolveEvery = 10 * time.Second
	sendQueue             = 64
	pingEvery             = 25 * time.Second
	pongWait              = 60 * time.Second
	writeWait             = 10 * time.Second
	// frameOverhead is what {"type":…,"seq":…,"value":…} adds around a body (the longest type name and a 20-digit seq
	// fit in it): a changes frame is cut over to reset + snapshot by its whole size, not the body's.
	frameOverhead = 128
)

type frame struct {
	Type  string `json:"type"`
	Seq   uint64 `json:"seq"`
	Value any    `json:"value"`
}

type approvalOpJSON struct {
	Op       string        `json:"op"`
	Approval team.Approval `json:"approval"`
}

type heldOp struct {
	op string
	a  team.Approval
}

// outFrame is one queued frame: its type and an immutable value (or already-encoded JSON). The writer numbers and
// encodes it, so nothing expensive runs on the goroutine that queues (which may hold the team module's event lock).
type outFrame struct {
	typ   string
	value any
}

// wsConn is one live connection. Everything that reaches the client goes through enqueue onto the bounded send queue;
// the single writer numbers frames as it writes them, so seq is contiguous in queue order across frame types.
type wsConn struct {
	m      *Module
	conn   *websocket.Conn
	entry  *convfeed.Entry
	sid    string
	hostID string
	turns  int

	ctx    context.Context
	cancel context.CancelFunc
	out    chan outFrame

	mu       sync.Mutex // guards closed, ready, held, cleanups, done
	closed   bool       // no more frames are accepted
	ready    bool       // the approvals snapshot is queued: later ops go straight to the queue
	held     []heldOp
	cleanups []func()
	done     bool // shutdown has run its cleanups

	closeOnce sync.Once

	// the follower's position (only the follower goroutine touches these)
	epoch   string
	sentRev uint64
	// what the last capabilities frame (or the snapshot) said about sending: a change is pushed (U3-2)
	caps capsKey
}

// capsKey is the part of the capability table that moves while a conversation is open: whether a mod can take a prompt.
type capsKey struct{ send, interrupt, answer string }

func capsKeyOf(c *convmodel.Capabilities) capsKey {
	return capsKey{c.Send, c.Interrupt, c.AnswerQuestion}
}

// pushCaps queues a conversation.capabilities frame when the table changed since the snapshot or the last frame (a mod
// that announced prompt.v1 came or went), so the App enables or disables its input at once instead of on the next fetch.
func (c *wsConn) pushCaps() bool {
	caps := c.m.capabilitiesFor(c.sid)
	if k := capsKeyOf(caps); k != c.caps {
		c.caps = k
		return c.enqueue("conversation.capabilities", map[string]any{"capabilities": caps})
	}
	return true
}

// enqueue numbers and queues one frame. It never blocks: a full queue is a real overflow and ends the connection.
func (c *wsConn) enqueue(typ string, value any) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enqueueLocked(typ, value)
}

func (c *wsConn) enqueueLocked(typ string, value any) bool {
	if c.closed {
		return false
	}
	select {
	case c.out <- outFrame{typ, value}:
		return true
	default:
		c.overflowLocked()
		return false
	}
}

// overflowLocked ends the connection from a context that may hold the team module's event lock: shutdown takes that
// lock (cancelling the subscription), so it runs on its own goroutine.
func (c *wsConn) overflowLocked() {
	c.closed = true
	go c.shutdown()
}

// onApproval is the SubscribeSession callback. It runs under the team module's event lock: it only enqueues.
func (c *wsConn) onApproval(op string, a team.Approval) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if !c.ready { // the approvals snapshot is not queued yet: keep the op behind it, in order
		if len(c.held) >= sendQueue {
			c.overflowLocked()
			return
		}
		c.held = append(c.held, heldOp{op, a})
		return
	}
	c.enqueueLocked("approval", approvalOpJSON{Op: op, Approval: a})
}

// sendApprovals queues the approvals snapshot, then the ops that arrived meanwhile, and opens the direct path.
func (c *wsConn) sendApprovals(open []team.Approval) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if open == nil {
		open = []team.Approval{}
	}
	if !c.enqueueLocked("approvals.snapshot", map[string]any{"approvals": open}) {
		return
	}
	for _, h := range c.held {
		if !c.enqueueLocked("approval", approvalOpJSON{Op: h.op, Approval: h.a}) {
			return
		}
	}
	c.held, c.ready = nil, true
}

// addCleanup registers something to undo when the connection ends; if it already ended, it runs now.
func (c *wsConn) addCleanup(f func()) {
	c.mu.Lock()
	if c.done {
		c.mu.Unlock()
		f()
		return
	}
	c.cleanups = append(c.cleanups, f)
	c.mu.Unlock()
}

// shutdown is the one close path: normal close, read or write error, context cancel, overflow, module Stop. It
// releases the approvals subscription and the responder hold exactly once (both are idempotent besides).
func (c *wsConn) shutdown() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		c.cancel()
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		c.conn.Close()
		c.mu.Lock()
		fs := c.cleanups
		c.cleanups, c.done = nil, true
		c.mu.Unlock()
		for i := len(fs) - 1; i >= 0; i-- {
			fs[i]()
		}
	})
}

// handleWS answers GET /ws/conversations/{provider}/{session_id}?after=<cursor>&turns=N. Everything that can fail with
// a plain status (validation, the cache pin, a transcript that is not there) happens before the upgrade.
func (m *Module) handleWS(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("provider") != "claude" {
		writeError(w, http.StatusNotFound, "provider_unsupported")
		return
	}
	sid := r.PathValue("session_id")
	if !sessionIDRe.MatchString(sid) {
		writeError(w, http.StatusBadRequest, "bad_session_id")
		return
	}
	q := r.URL.Query()
	if q.Has("before") || q.Has("around") {
		writeError(w, http.StatusBadRequest, "bad_query")
		return
	}
	var afterEpoch string
	var afterRev uint64
	hasAfter := q.Has("after")
	if hasAfter {
		var err error
		if afterEpoch, afterRev, err = convfeed.ParseCursor(q.Get("after")); err != nil {
			writeError(w, http.StatusBadRequest, "bad_cursor")
			return
		}
	}
	turns, ok := intParam(w, r, "turns", "bad_turns", defaultTurns, 1, maxTurns)
	if !ok {
		return
	}

	entry, release, err := m.cache.Acquire(r.Context(), sid)
	if err != nil {
		if errors.Is(err, convfeed.ErrBusy) {
			writeError(w, http.StatusServiceUnavailable, "busy")
		}
		return
	}
	defer release() // every path below, the upgrade failing included

	if err := entry.Exclusive(r.Context(), func() error { return m.refresh(r.Context(), entry, sid) }); err != nil {
		m.writeRefreshError(w, r, err)
		return
	}

	// A connection is admitted only while the module runs: Stop and this check share m.mu, so no connection can start
	// (or be counted) after Stop began waiting for them.
	runCtx, wg, ok := m.admitWS()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "stopping")
		return
	}
	defer wg.Done()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade has answered
	}
	ctx, cancel := context.WithCancel(runCtx)
	c := &wsConn{m: m, conn: conn, entry: entry, sid: sid, hostID: m.hostID(), turns: turns,
		ctx: ctx, cancel: cancel, out: make(chan outFrame, m.queueCap())}
	defer c.shutdown()

	go c.writeLoop()
	go c.readLoop()

	// a conversation stream is a remote responder for terminal-only approvals while it is open
	c.addCleanup(m.feed.HoldResponder())
	open, cancelSub, err := m.feed.SubscribeSession(sid, c.onApproval)
	if err != nil { // no empty snapshot: the client reconnects
		log.Printf("[conversation] approvals subscription: %v", err)
		return
	}
	c.addCleanup(cancelSub)

	c.caps = capsKeyOf(m.capabilitiesFor(sid)) // the snapshot (and any later fetch) carries the table: only a change is a frame
	if !c.sendFirst(hasAfter, afterEpoch, afterRev) {
		return
	}
	c.sendApprovals(open)
	c.follow()
}

// sendFirst queues the first conversation frame: the catch-up from a valid cursor, else a snapshot.
func (c *wsConn) sendFirst(hasAfter bool, afterEpoch string, afterRev uint64) bool {
	if hasAfter {
		inc := c.entry.Increment(afterEpoch, afterRev)
		if !inc.Stale {
			if body, ok := c.m.encodeIncrement(inc, c.sid, c.hostID, frameOverhead); ok {
				c.setPosition(inc.Cursor)
				return c.enqueue("conversation.changes", json.RawMessage(body))
			}
		}
	}
	return c.sendSnapshot()
}

// sendSnapshot queues a snapshot frame (with the connection's `turns`) and moves the follower's position to its cursor.
func (c *wsConn) sendSnapshot() bool {
	body, cursor, _, code := c.m.snapshotBody(c.entry, c.sid, c.hostID, c.turns, -1, "", false, false)
	if code != "" {
		log.Printf("[conversation] snapshot frame: %s", code)
		return false
	}
	c.setPosition(cursor)
	return c.enqueue("conversation.snapshot", json.RawMessage(body))
}

func (c *wsConn) setPosition(cursor string) {
	if epoch, rev, err := convfeed.ParseCursor(cursor); err == nil {
		c.epoch, c.sentRev = epoch, rev
	}
}

// follow re-reads the transcript every poll interval and pushes what changed, until the connection ends.
func (c *wsConn) follow() {
	tick := time.NewTicker(c.m.pollEvery())
	defer tick.Stop()
	var (
		src        convfeed.Source
		have       bool
		resolvedAt time.Time
	)
	dropSrc := func() {
		if have {
			src.Closer.Close()
			have = false
		}
	}
	defer dropSrc()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-tick.C:
		}
		if !c.pushCaps() {
			return
		}
		// The cheap light is read before the entry gate (a store query must not hold up other followers of the entry)
		// for the frame the last full lookup confirmed, and applied only to that same source.
		lightAt := time.Now() // before the query: the Entry compares readings by this
		light, lightOK, lightFrame := c.m.cheapLight(src, have, c.sid)
		err := c.entry.Exclusive(c.ctx, func() error {
			if !have || time.Since(resolvedAt) >= c.m.reresolveEvery() {
				dropSrc()
				s, err := c.m.resolver.Resolve(c.ctx, c.sid)
				if err != nil {
					return err
				}
				src, have, resolvedAt = s, true, time.Now()
			} else if lightOK && src.Live && src.FrameID == lightFrame {
				src.Status, src.StatusAt = light, lightAt
			}
			_, err := c.entry.Refresh(c.ctx, src)
			return err
		})
		if c.ctx.Err() != nil {
			return
		}
		if err != nil {
			dropSrc() // a vanished or shrunk file: resolve again on the next tick
			continue
		}
		if !c.push() {
			return
		}
	}
}

// push sends what the entry gained since the last frame. A slow reader gets fewer, larger frames: while the queue is
// half full nothing is sent and the changes keep coalescing in the entry's revisions.
func (c *wsConn) push() bool {
	if len(c.out) >= cap(c.out)/2 {
		return true
	}
	inc := c.entry.Increment(c.epoch, c.sentRev)
	if inc.Stale {
		return c.enqueue("conversation.reset", map[string]any{}) && c.sendSnapshot()
	}
	_, rev, err := convfeed.ParseCursor(inc.Cursor)
	if err != nil || rev == c.sentRev {
		return true
	}
	body, ok := c.m.encodeIncrement(inc, c.sid, c.hostID, frameOverhead)
	if !ok { // too big for one frame
		return c.enqueue("conversation.reset", map[string]any{}) && c.sendSnapshot()
	}
	c.sentRev = rev
	if len(inc.Changes) == 0 {
		return c.enqueue("conversation.header", map[string]any{"header": headerOf(inc.Header), "cursor": inc.Cursor})
	}
	return c.enqueue("conversation.changes", json.RawMessage(body))
}

// writeLoop is the only writer: it drains the queue and keeps the connection alive with pings.
func (c *wsConn) writeLoop() {
	defer c.shutdown()
	var seq uint64
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case f := <-c.out:
			seq++
			b, err := json.Marshal(frame{Type: f.typ, Seq: seq, Value: f.value})
			if err != nil { // cannot happen for our own types; never leave a gap in seq
				log.Printf("[conversation] encode %s frame: %v", f.typ, err)
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
				return
			}
		case <-ping.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readLoop reads (and discards) client messages: it exists to see the client leave and to run the pong deadline.
func (c *wsConn) readLoop() {
	defer c.shutdown()
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// cheapLight reads the current light of the frame the last full lookup confirmed, so a change of the pane's light
// reaches the stream within one poll instead of one full re-resolve. A source that is not live keeps what the
// resolver said ("ended" / "unknown"), and so does one the cheap lookup cannot place; a source resolved in the same
// tick is already current and ignores the reading.
func (m *Module) cheapLight(src convfeed.Source, have bool, sid string) (status string, ok bool, frameID string) {
	if m.light == nil || !have || !src.Live || src.FrameID == "" {
		return "", false, ""
	}
	st, ok := m.light.LightStatus(sid, src.FrameID)
	if !ok || st == "" {
		return "", false, ""
	}
	return st, true, src.FrameID
}
