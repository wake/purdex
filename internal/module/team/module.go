package teammod

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/hostconfig"
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
	// ResolveOriginBySession is ResolveOrigin keyed by CC session id: the
	// relay routes are called by the mod with its session id, not its
	// inbox (P5a). Same ok/err contract.
	ResolveOriginBySession(sessionID string) (team.Origin, bool, error)
	LiveSession(sessionID string) bool
}

// Module owns team.db and serves /api/team/*.
type Module struct {
	core    *core.Core
	store   *Store
	origins OriginResolver
	// responders answers "can anyone remote answer a hook_ask right now?"
	// (spec §6.6 step 1): the WS half (core.Events.HasSubscribers) in P8a;
	// the iOS line adds the push registry behind the same interface.
	responders RemoteResponders
	now        func() int64 // unix ms; injectable for tests
	logf       func(format string, args ...any)

	// P5a: the relay switches (host config), the title mover (meta.db; nil
	// without a meta store), the op/request id minter, the handoff
	// directory and what each session's mod said in hello (under mu).
	// modSeen is THE mod-presence record: P6 reads it for
	// relay_unsupported, P8a-1a's modPresent() reads it for the
	// terminal-only degradation; nothing else writes it.
	switches hostconfig.RelaySwitchReader
	titles   TitleMover
	// usage is the agent module's per-session statusline reading; begin
	// copies model_id / effort from it into the self_relay payload (the mod
	// sends neither). Nil when the agent module is absent: both stay "".
	usage    agent.ContextUsageReader
	newID    func() string
	relayDir string
	modSeen  map[string]helloInfo

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

	// afterRead, when set, runs in pollRow right after the row is read
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
	// afterOpenCheck, when set, runs in handleRelayBegin between its
	// OpenRelayOpBySession check and its CreateRelayOp; tests open an op
	// for the same session in that window and prove the table's conflict
	// (ErrRelayOpOpen) is answered as 409 relay_open too. nil in production.
	afterOpenCheck func(sessionID string)
	// afterOpenByToolUse, when set, runs in handleAskBegin between its
	// OpenByToolUse and its insert; tests start a second begin for the same
	// tool use in that window and prove it waits for createMu. nil in
	// production.
	afterOpenByToolUse func()
	// beforeTerminalClose is a test seam run by a terminal relay report just
	// before it closes the op's approval row (the approve that races it).
	beforeTerminalClose func(opID string)
	// clearedWait / clearedPoll bound how long a cleared report waits for
	// the registry to show the new session id (checkClearedTarget).
	clearedWait, clearedPoll time.Duration
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
		newID:      uuid.NewString,
		modSeen:    map[string]helloInfo{},
		// A cleared report waits this long for the registry to show the new
		// session id (measured ~0.6 s after /clear), polling every 100 ms.
		clearedWait: 3 * time.Second,
		clearedPoll: 100 * time.Millisecond,
	}
}

// WithTitles sets the title mover (the meta store's PeerLabels in
// production). Nil is allowed: titles then stay on the old session id.
func (m *Module) WithTitles(t TitleMover) *Module {
	m.titles = t
	return m
}

func (m *Module) Name() string           { return "team" }
func (m *Module) Dependencies() []string { return []string{"agent", "peers", "hostconfig"} }

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
	sw, ok := c.Registry.Get(hostconfig.RelaySwitchesKey)
	if !ok {
		return fmt.Errorf("team: service %q not registered", hostconfig.RelaySwitchesKey)
	}
	switches, ok := sw.(hostconfig.RelaySwitchReader)
	if !ok {
		return fmt.Errorf("team: service %q does not implement RelaySwitchReader (%T)", hostconfig.RelaySwitchesKey, sw)
	}
	m.switches = switches
	// The statusline reading lives in the agent module (P1); as peers does,
	// type-assert the reader on the owner-resolver service rather than add
	// a registry key. Optional: a daemon without it fills no model/effort.
	if svc, ok := c.Registry.Get(agent.OwnerResolverKey); ok {
		if r, ok := svc.(agent.ContextUsageReader); ok {
			m.usage = r
		}
	}
	store, err := OpenStore(filepath.Join(c.Cfg.DataDir, "team.db"))
	if err != nil {
		return fmt.Errorf("team: %w", err)
	}
	m.store = store
	if m.responders == nil {
		m.responders = wsResponders{events: c.Events}
	}
	m.dataDir = c.Cfg.DataDir
	m.relayDir = filepath.Join(c.Cfg.DataDir, team.RelayDir)
	// The peers inventory reads the relay lineage through this (spec §8.4).
	c.Registry.Register(team.LineageReaderKey, store)
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
	// P5a relay routes (spec §8.3, §8.7); all under TokenAuth like /api/team/*.
	mux.HandleFunc("POST /api/relay/hello", m.handleRelayHello)
	mux.HandleFunc("POST /api/relay/begin", m.handleRelayBegin)
	mux.HandleFunc("GET /api/relay/wait/{id}", m.handleRelayWait)
	mux.HandleFunc("POST /api/relay/self", m.handleRelaySelf)
	mux.HandleFunc("POST /api/relay/ops/{id}/report", m.handleRelayReport)
	mux.HandleFunc("GET /api/relay/ops/{id}", m.handleRelayOp)
	// P8a 分流 routes (spec §6.6); TokenAuth like /api/team/*.
	mux.HandleFunc("POST /api/ask/begin", m.handleAskBegin)
	mux.HandleFunc("GET /api/ask/wait/{id}", m.handleAskWait)
	mux.HandleFunc("POST /api/ask/report/{id}", m.handleAskReport)
}

// Start applies the boot lease grace (spec §9.2: every open request's
// lease becomes max(lease_until, boot + 30 s), so its pdx can reconnect),
// registers the snapshot for new subscribers and starts the sweeper. It
// does not prune hook lock flags: during that same grace a CC session may
// not have re-registered, so a registry snapshot taken here would call it
// dead and the prune would delete the flag of an open lead request. The
// sweeper prunes on its 10th tick, and never a flag whose request is open.
func (m *Module) Start(context.Context) error {
	// <data_dir>/relay/ exists from boot (spec §8.3); begin re-creates it
	// too. A failure is logged, not fatal: begin reports its own.
	if err := os.MkdirAll(m.relayDir, 0o700); err != nil {
		m.logf("[team] relay dir %s: %v", m.relayDir, err)
	}
	n, err := m.store.ExtendOpenLeases(m.now() + team.BootGraceS*1000)
	if err != nil {
		return fmt.Errorf("team: %w", err)
	}
	if n > 0 {
		m.logf("[team] boot: extended the lease of %d open approval request(s) by %ds", n, team.BootGraceS)
	}
	m.reconcileRelays()
	m.core.Events.OnSubscribe(m.sendSnapshot)
	m.sweepWG.Add(2)
	go m.runSweeper()
	go m.runRetention()
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

// closeAsWithOp is closeAs for a close that a relay REPORT drives (a
// terminal report on an op still awaiting approval): the op takes the
// report's own state and reason instead of the mapping a close implies.
func (m *Module) closeAsWithOp(id string, c Close, rep RelayReport) (team.Approval, bool, error) {
	return m.closeWithOp(id, func() (team.Approval, bool, error) { return m.store.CloseIfOpen(id, c) }, &rep)
}

// closeWith is closeAs over a given store CAS — CloseIfOpen for decide,
// DELETE and a vanished origin; CloseIfExpired for the sweeper's timeout
// and lease paths. The winner alone broadcasts and wakes.
func (m *Module) closeWith(id string, cas func() (team.Approval, bool, error)) (team.Approval, bool, error) {
	return m.closeWithOp(id, cas, nil)
}

// closeWithOp is closeWith with the op report the winner applies to a
// self_relay row's op (nil: the mapping the row's state implies).
func (m *Module) closeWithOp(id string, cas func() (team.Approval, bool, error), rep *RelayReport) (team.Approval, bool, error) {
	after, won, err := cas()
	if err != nil {
		return team.Approval{}, false, err
	}
	if won {
		m.announceClosed(after, rep)
	}
	return after, won, nil
}

// announceClosed is what follows every close that won, once it is
// committed: the closed broadcast, the long-poll wake-up and afterClose.
func (m *Module) announceClosed(after team.Approval, rep *RelayReport) {
	m.broadcast("closed", &after)
	m.wake(after.ID)
	m.afterClose(after, rep)
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
