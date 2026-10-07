package teammod

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// OriginResolver attributes a request's caller to a live CC session and
// answers whether a session is still live. The peers module's
// *OriginResolver is the production value (registry key
// peersmod.OriginResolverKey); tests inject a fake.
//
// ResolveOrigin's error is a registry read failure only: the caller answers
// it with 503 not_ready (retry), never origin_unknown. ok=false with a nil
// error means the registry was read and the inbox is not a live session.
type OriginResolver interface {
	ResolveOrigin(inbox string) (team.Origin, bool, error)
	LiveSession(sessionID string) bool
}

// Module owns team.db and serves /api/team/*.
type Module struct {
	core    *core.Core
	store   *Store
	origins OriginResolver
	now     func() int64 // unix ms; injectable for tests
	logf    func(format string, args ...any)

	// dataDir is the daemon's data dir; the hook lock flags live under
	// <dataDir>/hooklocks (spec §6.6): the hook decide route removes a flag
	// it answered {} for, and the sweeper prunes flags of sessions the
	// registry no longer lists.
	dataDir string

	// stopCtx is cancelled first in Stop: long-polls return, the sweeper
	// exits and POST create answers 503 not_ready. The DB stays open until
	// Close (PD6): in-flight handlers still read it during srv.Shutdown.
	stopCtx    context.Context
	stopCancel context.CancelFunc
	sweepWG    sync.WaitGroup
	tickN      int // sweeper ticks so far; only the sweeper goroutine (or a test) touches it

	// createMu serialises create's check-then-insert (idempotent retry,
	// request_open, insert) and Stop's cancel of stopCtx: a create either
	// finishes before Stop, or takes the lock after it and sees stopping.
	createMu sync.Mutex

	mu      sync.Mutex
	waiters map[string][]chan struct{} // long-polls per approval id; closed when it closes

	// eventMu orders the approval.request stream (spec §6.2): it is held
	// across every opened/closed broadcast and across sendSnapshot's
	// ListOpen + send, so no event is queued between a snapshot's read and
	// its delivery. A client that replaces its set from the snapshot thus
	// never loses a just-opened request or revives a just-closed one. No
	// store write happens under it: each broadcast follows its own write.
	eventMu sync.Mutex

	// afterRead, when set, runs in handleGet right after the row is read
	// and before the wait. Tests use it to close the row in that window
	// and prove the waiter was registered before the read; nil in production.
	afterRead func(id string)
	// afterSnapshotRead, when set, runs in sendSnapshot between its ListOpen
	// and its send; tests open or close a request in that window and prove
	// the event is delivered after the snapshot. nil in production.
	afterSnapshotRead func()
	// afterListOpen, when set, runs in tick between its ListOpen and its
	// closes; tests renew a lease in that window and prove the sweeper
	// does not close on the stale copy. nil in production.
	afterListOpen func()
	// afterOpenByOrigin, when set, runs in handleHookDecide between its
	// OpenByOrigin and its flag removal; tests run a create for the same
	// origin in that window and prove it waits for createMu. nil in
	// production.
	afterOpenByOrigin func()
}

// New returns a Module with production defaults.
func New() *Module {
	stopCtx, stopCancel := context.WithCancel(context.Background())
	return &Module{
		now:        func() int64 { return time.Now().UnixMilli() },
		logf:       log.Printf,
		stopCtx:    stopCtx,
		stopCancel: stopCancel,
		waiters:    map[string][]chan struct{}{},
	}
}

func (m *Module) Name() string           { return "team" }
func (m *Module) Dependencies() []string { return []string{"peers"} }

// Init resolves the origin resolver peers registered and opens team.db in
// the data dir. Both are hard errors: without either the module cannot
// attribute or persist a single request.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	svc, ok := c.Registry.Get(peersmod.OriginResolverKey)
	if !ok {
		return fmt.Errorf("team: service %q not registered", peersmod.OriginResolverKey)
	}
	origins, ok := svc.(OriginResolver)
	if !ok {
		return fmt.Errorf("team: service %q does not implement OriginResolver (%T)", peersmod.OriginResolverKey, svc)
	}
	m.origins = origins
	store, err := OpenStore(filepath.Join(c.Cfg.DataDir, "team.db"))
	if err != nil {
		return fmt.Errorf("team: %w", err)
	}
	m.store = store
	m.dataDir = c.Cfg.DataDir
	return nil
}

// RegisterRoutes mounts the six /api/team/* routes and the hook decision
// route (Go method patterns).
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/team/approvals", m.handleCreate)
	mux.HandleFunc("GET /api/team/approvals", m.handleList)
	mux.HandleFunc("GET /api/team/approvals/{id}", m.handleGet)
	mux.HandleFunc("DELETE /api/team/approvals/{id}", m.handleDelete)
	mux.HandleFunc("POST /api/team/approvals/{id}/decide", m.handleDecide)
	mux.HandleFunc("GET /api/team/inflight", m.handleInflight)
	mux.HandleFunc("POST /api/hooks/decide", m.handleHookDecide)
}

// Start applies the boot lease grace (spec §9.2: every open request's
// lease becomes max(lease_until, boot + 30 s), so its pdx can reconnect),
// registers the snapshot for new subscribers and starts the sweeper. It
// does not prune hook lock flags: during that same grace a CC session may
// not have re-registered, so a registry snapshot taken here would call it
// dead and the prune would delete the flag of an open lead request. The
// sweeper prunes on its 10th tick, and never a flag whose request is open.
func (m *Module) Start(context.Context) error {
	n, err := m.store.ExtendOpenLeases(m.now() + team.BootGraceS*1000)
	if err != nil {
		return fmt.Errorf("team: %w", err)
	}
	if n > 0 {
		m.logf("[team] boot: extended the lease of %d open approval request(s) by %ds", n, team.BootGraceS)
	}
	m.core.Events.OnSubscribe(m.sendSnapshot)
	m.sweepWG.Add(1)
	go m.runSweeper()
	m.logf("[team] endpoints enabled")
	return nil
}

// Stop cancels stopCtx (long-polls return, create answers not_ready) and
// joins the sweeper. Idempotent. The DB is closed in Close. The cancel is
// taken under createMu so no create inserts after Stop returns: one that
// is past its entry check waits for the lock and then re-checks stopping.
func (m *Module) Stop(context.Context) error {
	m.createMu.Lock()
	m.stopCancel()
	m.createMu.Unlock()
	m.sweepWG.Wait()
	return nil
}

// Close closes team.db, after the HTTP server has stopped (core.Closer).
func (m *Module) Close() error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}

func (m *Module) stopping() bool {
	select {
	case <-m.stopCtx.Done():
		return true
	default:
		return false
	}
}

func (m *Module) hostID() string {
	m.core.CfgMu.RLock()
	defer m.core.CfgMu.RUnlock()
	return m.core.Cfg.HostID
}

// closeAs is the one close path: the store's CAS, then — for the winner
// only — the closed broadcast and the long-poll wake-up. So every close
// produces exactly one closed event, whoever raced for it (spec §6.2).
func (m *Module) closeAs(id string, c Close) (team.Approval, bool, error) {
	return m.closeWith(id, func() (team.Approval, bool, error) { return m.store.CloseIfOpen(id, c) })
}

// closeWith is closeAs over a given store CAS — CloseIfOpen for decide,
// DELETE and a vanished origin; CloseIfExpired for the sweeper's timeout
// and lease paths. The winner alone broadcasts and wakes.
func (m *Module) closeWith(id string, cas func() (team.Approval, bool, error)) (team.Approval, bool, error) {
	after, won, err := cas()
	if err != nil {
		return team.Approval{}, false, err
	}
	if won {
		m.broadcast("closed", &after)
		m.wake(id)
	}
	return after, won, nil
}

// broadcast queues one opened/closed event to every subscriber, under
// eventMu so it cannot land between a snapshot's read and its send.
func (m *Module) broadcast(op string, a *team.Approval) {
	v, err := json.Marshal(team.EventValue{Op: op, Approval: a})
	if err != nil {
		m.logf("[team] encode %s event: %v", op, err)
		return
	}
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	m.core.Events.BroadcastEvent(core.HostEvent{Type: team.EventType, Value: string(v)})
}

// sendSnapshot queues {op:"snapshot", approvals:[…]} to a new subscriber
// (spec §6.2: late or reconnecting clients see the same open set). The
// read and the send happen under eventMu, so every event the subscriber
// receives afterwards is for a change the snapshot does not yet show. A
// subscriber that did not get the snapshot — the open set could not be
// read, or its buffer is already full — is closed so it reconnects and
// asks again (as session/module.go does); keeping it would leave a client
// that never sees the requests open before it connected. One already
// removed is left alone.
func (m *Module) sendSnapshot(sub *core.EventSubscriber) {
	why := m.snapshotUnderLock(sub)
	if why == "" {
		return
	}
	select {
	case <-sub.Done():
	default:
		m.logf("[team] OnSubscribe snapshot %s; closing the connection so the client reconnects", why)
		m.core.Events.Remove(sub)
	}
}

// snapshotUnderLock reads the open set and queues it to sub, holding
// eventMu from the read to the send. It returns "" when the snapshot was
// queued, else why it was not (the open set could not be read; the send
// buffer is full or the subscriber is gone), for the caller to close the
// subscriber. An encode error is logged and reported as "", since
// reconnecting would not change it.
func (m *Module) snapshotUnderLock(sub *core.EventSubscriber) string {
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	open, err := m.store.ListOpen()
	if err != nil {
		return fmt.Sprintf("could not read the open set (%v)", err)
	}
	if m.afterSnapshotRead != nil {
		m.afterSnapshotRead()
	}
	v, err := json.Marshal(team.EventValue{Op: "snapshot", Approvals: open})
	if err != nil {
		m.logf("[team] encode snapshot: %v", err)
		return ""
	}
	data, err := json.Marshal(core.HostEvent{Type: team.EventType, Value: string(v)})
	if err != nil {
		m.logf("[team] encode snapshot event: %v", err)
		return ""
	}
	if !sub.TrySend(data) {
		return "could not be queued (send buffer full)"
	}
	return ""
}

// addWaiter registers a long-poll on id; the channel is closed by wake.
func (m *Module) addWaiter(id string) chan struct{} {
	ch := make(chan struct{})
	m.mu.Lock()
	m.waiters[id] = append(m.waiters[id], ch)
	m.mu.Unlock()
	return ch
}

// removeWaiter drops one long-poll's channel; a no-op once wake took it.
func (m *Module) removeWaiter(id string, ch chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ws := m.waiters[id]
	for i, w := range ws {
		if w == ch {
			ws = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	if len(ws) == 0 {
		delete(m.waiters, id)
	} else {
		m.waiters[id] = ws
	}
}

// wake releases every long-poll on id.
func (m *Module) wake(id string) {
	m.mu.Lock()
	ws := m.waiters[id]
	delete(m.waiters, id)
	m.mu.Unlock()
	for _, ch := range ws {
		close(ch)
	}
}
