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
	"strings"
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

	life   sync.Mutex // Start and Stop (Stop may be called twice, or from two goroutines)
	unsub  func()
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// highEpoch / highSeq: the largest seq seen in the newest epoch a hook frame carried; no event was emitted past it. Under mu.
	highEpoch string
	highSeq   uint64
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
	m.life.Lock()
	defer m.life.Unlock()
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

func (m *Module) Stop(ctx context.Context) error {
	m.life.Lock()
	defer m.life.Unlock()
	if m.unsub != nil {
		m.unsub()
		m.unsub = nil
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.wg.Wait()
	return m.feed.stop(ctx)
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
	if _, err := dec.Token(); !errors.Is(err, io.EOF) { // a second value after the body is an ambiguous request
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
	t = m.clamp(code, t)
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
	if m.sessions != nil {
		if codes, ok := m.sessions(listMaxAge); ok {
			_, present := codes[code]
			return present
		}
	}
	// No usable list: unknown is not absent, but a made-up code must not push real reads out of the bounded store, so only a
	// session the daemon has seen (an event of it, or an earlier read) is accepted.
	if _, ok := m.newestCursor(code); ok {
		return true
	}
	_, ok := m.store.Get(code)
	return ok
}

// ownEpoch: the epoch is one this daemon run stamps (core.BootID, or BootID-n after n rotations). The boot id is public
// (/api/info), so a client can name the epoch before the module has seen a single frame.
func (m *Module) ownEpoch(epoch string) bool {
	boot := m.core.BootID
	return boot != "" && (epoch == boot || strings.HasPrefix(epoch, boot+"-"))
}

// clamp keeps a cursor from reaching past what the daemon has emitted: a seq above the newest cursor of that session (or, for
// a session with no event, above the epoch's high-water mark) would cover every later unread of it. A legitimate T is the
// cursor of an event that already happened, so it is never changed. A cursor of another epoch than the current one can never
// cover a current-epoch unread and is left alone.
func (m *Module) clamp(code string, t reads.Cursor) reads.Cursor {
	if t.Epoch == "" {
		return t
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.Epoch != m.highEpoch && !m.ownEpoch(t.Epoch) {
		return t // a previous run's cursor
	}
	var hi uint64 // an epoch of this run in which no frame has been seen yet has emitted nothing
	if t.Epoch == m.highEpoch {
		hi = m.highSeq
	}
	if newest, ok := m.newest[code]; ok && newest.Epoch == t.Epoch {
		hi = newest.Seq
	}
	if t.Seq > hi {
		t.Seq = hi
	}
	return t
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
	if c.Epoch != m.highEpoch {
		m.highEpoch, m.highSeq = c.Epoch, 0
	}
	if c.Seq > m.highSeq {
		m.highSeq = c.Seq
	}
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
