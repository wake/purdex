// Package reads is the daemon's read-sync module (badge spec §7, BG-4a): when any device turns a session from unread to read it
// tells the host "read through T" (T a hook cursor, internal/reads); the daemon remembers the last read of each session in
// memory, forwards it at once over WebSocket to every connection that opted into reads=v1, and lets the push module carry it
// on the next push (BG-4b, through the Feed registered here).
package reads

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/reads"
)

const (
	// pruneEvery is how often lastRead is compared with the session list the daemon holds in memory.
	pruneEvery = time.Minute
	// listMaxAge is how old that list may be to count as evidence. An older, invalidated or never-filled one is unknown.
	listMaxAge = 2 * time.Minute
	// maxNewest bounds the memory of the newest hook cursor emitted per session.
	maxNewest = 4096
	// maxBody bounds the request body: one cursor is a few dozen bytes.
	maxBody = 1024
	// maxEpoch bounds an epoch string a client sends (a boot id with a rotation suffix is far shorter).
	maxEpoch = 128
)

// Module is the reads module.
type Module struct {
	core  *core.Core
	store *reads.Store
	feed  *readFeed

	// seams: nil = the real ones, found in the registry at Start.
	notify     agent.NotifyFeed
	sessions   func(maxAge time.Duration) (map[string]struct{}, bool)
	pruneEvery time.Duration

	mu     sync.Mutex
	newest map[string]reads.Cursor // the newest hook cursor emitted per session code

	unsub  func()
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New() *Module {
	return &Module{store: reads.NewStore(reads.DefaultMax), feed: newReadFeed(), newest: map[string]reads.Cursor{}}
}

func (m *Module) Name() string           { return "reads" }
func (m *Module) Dependencies() []string { return []string{"session", "agent"} }

// Init publishes the feed the push module subscribes to.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	c.Registry.Register(reads.FeedKey, reads.Feed(m.feed))
	return nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sessions/{code}/read", m.handleRead)
}

// Start follows the hook frames (for the newest cursor of each session and the current epoch), sends the snapshot to every
// reads=v1 connection that arrives, and prunes lastRead against the session list once a minute.
func (m *Module) Start(ctx context.Context) error {
	if m.notify == nil {
		svc, _ := m.core.Registry.Get(agent.NotifyFeedKey)
		if f, ok := svc.(agent.NotifyFeed); ok {
			m.notify = f
		} else {
			log.Printf("[reads] the agent module's hook feed is not available: a read without a cursor carries none")
		}
	}
	if m.sessions == nil {
		svc, _ := m.core.Registry.Get(session.RegistryKey)
		if l, ok := svc.(interface {
			CachedSessionCodes(time.Duration) (map[string]struct{}, bool)
		}); ok {
			m.sessions = l.CachedSessionCodes
		} else {
			log.Printf("[reads] no session list: a read of an unknown session is accepted and lastRead is not pruned")
		}
	}
	if m.notify != nil {
		m.unsub = m.notify.SubscribeNotify(m.onNotify)
	}
	m.core.Events.OnSubscribe(m.sendSnapshot)
	every := m.pruneEvery
	if every <= 0 {
		every = pruneEvery
	}
	ctx, m.cancel = context.WithCancel(ctx)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.pruneOnce()
			}
		}
	}()
	return nil
}

func (m *Module) Stop(context.Context) error {
	if m.unsub != nil {
		m.unsub()
		m.unsub = nil
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	m.feed.stop()
	return nil
}

type readRequest struct {
	Through *reads.Cursor `json:"through"`
}

// handleRead is POST /api/sessions/{code}/read {"through": T?} → 204. A code the known in-memory session list does not have is
// 404 no_such_session (so a client cannot fill lastRead with made-up codes); with no usable list the read is accepted.
func (m *Module) handleRead(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	var req readRequest
	body := http.MaxBytesReader(w, r.Body, maxBody)
	dec := json.NewDecoder(body)
	if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) { // an empty body is "no through"
		http.Error(w, `{"error":"bad_request"}`, http.StatusBadRequest)
		return
	}
	if req.Through != nil && len(req.Through.Epoch) > maxEpoch {
		http.Error(w, `{"error":"bad_request"}`, http.StatusBadRequest)
		return
	}
	if !m.knownSession(code) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"no_such_session"}` + "\n"))
		return
	}
	var t reads.Cursor
	if req.Through != nil {
		t = *req.Through
	} else if newest, ok := m.newestCursor(code); ok {
		t = newest // the cursor of the newest event the daemon emitted for this session
	}
	if m.store.Advance(code, t) {
		m.announce(reads.Read{Code: code, Through: t})
	}
	w.WriteHeader(http.StatusNoContent)
}

// knownSession is false only for a code that cannot be a session, or that a usable in-memory list does not have.
func (m *Module) knownSession(code string) bool {
	if _, err := session.DecodeSessionID(code); err != nil {
		return false
	}
	if m.sessions == nil {
		return true
	}
	codes, ok := m.sessions(listMaxAge)
	if !ok {
		return true // unknown is not absent
	}
	_, present := codes[code]
	return present
}

// announce tells every reads=v1 connection (strict: a full buffer closes it, its reconnect brings a fresh snapshot) and the
// feed.
func (m *Module) announce(rd reads.Read) {
	val, err := json.Marshal(rd)
	if err != nil {
		return
	}
	m.core.Events.BroadcastStrictTo(core.FeatureReadsV1, core.HostEvent{Type: "session.read", Session: rd.Code, Value: string(val)})
	m.feed.publish(rd)
}

// sendSnapshot is the connect-time frame of a reads=v1 connection: every remembered read, [] when there is none. Applying a
// read is idempotent and monotonic, so it needs no order against the live frames.
func (m *Module) sendSnapshot(sub *core.EventSubscriber) {
	if !sub.Wants(core.FeatureReadsV1) {
		return
	}
	val, err := json.Marshal(struct {
		Reads []reads.Read `json:"reads"`
	}{m.store.Snapshot()})
	if err != nil {
		return
	}
	m.core.Events.SendStrict(sub, core.HostEvent{Type: "session.reads", Value: string(val)})
}

// onNotify records the newest cursor of every session and tells the store which epoch is current.
func (m *Module) onNotify(ev agent.NotifyEvent) {
	if ev.SessionCode == "" || ev.Event.Epoch == "" {
		return
	}
	m.noteEvent(ev.SessionCode, reads.Cursor{Epoch: ev.Event.Epoch, Seq: ev.Event.Seq})
	m.store.SetCurrentEpoch(ev.Event.Epoch)
}

// noteEvent keeps the larger cursor of a session: the same epoch by seq, a different epoch replaces (a restart).
func (m *Module) noteEvent(code string, c reads.Cursor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.newest[code]
	if ok && cur.Epoch == c.Epoch && c.Seq <= cur.Seq {
		return
	}
	if !ok && len(m.newest) >= maxNewest {
		for victim := range m.newest { // at the bound: drop one (the map's order is arbitrary), the next prune trims the rest
			delete(m.newest, victim)
			break
		}
	}
	m.newest[code] = c
}

func (m *Module) newestCursor(code string) (reads.Cursor, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.newest[code]
	return c, ok
}

func (m *Module) newestLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.newest)
}

// pruneOnce drops lastRead and the newest-cursor memory of every session the in-memory list no longer has. It does nothing
// when the list is unknown (never filled, invalidated, stale): unknown is not gone, and the 4096 bound covers the gap.
func (m *Module) pruneOnce() {
	if m.sessions == nil {
		return
	}
	codes, ok := m.sessions(listMaxAge)
	if !ok {
		return
	}
	keep := func(code string) bool { _, present := codes[code]; return present }
	m.store.Prune(keep)
	m.mu.Lock()
	for code := range m.newest {
		if !keep(code) {
			delete(m.newest, code)
		}
	}
	m.mu.Unlock()
}

// readFeed delivers accepted reads to in-process subscribers (the push module), each on a goroutine of its own behind a bounded
// queue: a subscriber that falls behind loses reads (best effort, like everything about read sync) and never slows a request.
type readFeed struct {
	mu   sync.Mutex
	subs map[int]*feedSub
	next int
}

type feedSub struct {
	ch   chan reads.Read
	done chan struct{}
	once sync.Once
}

func (s *feedSub) close() { s.once.Do(func() { close(s.done) }) }

const feedQueue = 256

func newReadFeed() *readFeed { return &readFeed{subs: map[int]*feedSub{}} }

func (f *readFeed) SubscribeReads(fn func(reads.Read)) func() { return f.subscribe(fn) }

func (f *readFeed) subscribe(fn func(reads.Read)) func() {
	s := &feedSub{ch: make(chan reads.Read, feedQueue), done: make(chan struct{})}
	f.mu.Lock()
	id := f.next
	f.next++
	f.subs[id] = s
	f.mu.Unlock()
	go func() {
		for {
			select {
			case rd := <-s.ch:
				fn(rd)
			case <-s.done:
				return
			}
		}
	}()
	return func() {
		f.mu.Lock()
		delete(f.subs, id)
		f.mu.Unlock()
		s.close()
	}
}

func (f *readFeed) publish(rd reads.Read) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subs {
		select {
		case s.ch <- rd:
		default:
			log.Printf("[reads] a read subscriber is %d reads behind: dropping %s", feedQueue, rd.Code)
		}
	}
}

func (f *readFeed) stop() {
	f.mu.Lock()
	subs := f.subs
	f.subs = map[int]*feedSub{}
	f.mu.Unlock()
	for _, s := range subs {
		s.close()
	}
}
