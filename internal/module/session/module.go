package session

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

const listCacheTTL = time.Second

// listReadTimeout is the one deadline covering a whole session-list read —
// tmux list-sessions, every session's pane metadata, the tmux-instance probe
// and the meta-DB reads (#1293 §3.3). A read that exceeds it is killed and
// the list call fails; it never returns a partial list.
const listReadTimeout = 5 * time.Second

// ListReadTimeout is listReadTimeout for callers outside this package that
// deliberately open their own detached read budget instead of following a
// request (the agent hook handler, which must finish an event even if its
// sender hangs up). The read itself stays capped by the session module.
const ListReadTimeout = listReadTimeout

// SessionModule manages tmux sessions, meta cache, and HTTP API.
type SessionModule struct {
	meta            *store.MetaStore
	tmux            tmux.Executor
	core            *core.Core
	shellHomeReader func(pid string) (string, error)
	// tmuxInstanceFn reads the tmux server identity; swapped in tests.
	tmuxInstanceFn func(context.Context) string
	// shellProbe runs the command-word probe (spec §4.4). A field, not a
	// package global, so a test can prove a rejected token never reached a
	// shell without mutating shared state.
	shellProbe shellProbeFunc
	// passwdShell is the third rung of the probe's shell ladder. It takes a
	// context because on darwin it shells out to `dscl`, and that read is on
	// the resolve-command deadline like everything else on the path.
	passwdShell func(ctx context.Context) string
	cancelWatch context.CancelFunc
	wstate      watcherState
	waitForGate chan bool
	// createMu serializes handleCreate's HasSession→NewSession→SetMeta
	// critical section so two concurrent POSTs with the same name can't
	// both slip past the duplicate check. See #61. A ctxMutex so a caller
	// that gives up while waiting for it returns at once (#1293).
	createMu ctxMutex

	// listCache debounces rapid ListSessions calls (1s TTL). See #128.
	// listCacheSlot serializes refills (one tmux read at a time) and, being
	// an abandonable slot, lets a waiting request give up when its context
	// ends (#1293). listCacheMu guards only the cached data and is never
	// held across a tmux read, so invalidateListCache never waits on one;
	// listCacheGen lets it win over a refill already in flight.
	listCacheSlot slot
	listCacheMu   sync.Mutex
	listCacheData []SessionInfo
	listCacheAt   time.Time
	listCacheGen  uint64

	// nameCache backs LookupCodeByName: a name→code map plus a TTL stamp.
	// Separate from listCache because the lookup fast path runs only one
	// tmux subprocess (list-sessions) — no meta merge, no per-session
	// metadata fan-out — and is hit on the hook hot path (#?).
	nameCacheMu   sync.Mutex
	nameCacheData map[string]string
	nameCacheAt   time.Time

	// Versioned session lists (spec 2026-09-23 §3.3, versioned.go). epoch
	// identifies this process's counter; snapSlot (a one-holder slot a
	// waiter can abandon, #1293) serializes the tmux read together with seq
	// assignment, and guards snapSeq and epoch (it rotates at maxSeq).
	snapSlot slot
	snapSeq  uint64
	epoch    string

	// listTimeout overrides listReadTimeout (tests only; 0 = the constant).
	listTimeout time.Duration

	// Subscribe-snapshot recovery (#1293): a failed snapshot is retried in
	// the background after each of snapshotRetryDelays (nil = the default
	// schedule), each try with a fresh read budget; snapshotRetriesLive
	// counts retry goroutines still running. runCtx ends at Stop.
	snapshotRetryDelays []time.Duration
	snapshotRetriesLive atomic.Int32
	runCtx              context.Context
}

// defaultSnapshotRetryDelays is the wait before each subscribe-snapshot
// retry: three more tries over ~7s (plus their read budgets), then the
// connection is closed so the client reconnects.
var defaultSnapshotRetryDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// readTimeout is the session-list budget: listReadTimeout unless a test
// shortened it.
func (m *SessionModule) readTimeout() time.Duration {
	if m.listTimeout > 0 {
		return m.listTimeout
	}
	return listReadTimeout
}

// listReadContext is the context for a session-list read by a caller with no
// context of its own (the WS subscribe snapshot, the wait-for and ticker
// pushes): one fresh listReadTimeout budget, covering both the wait for the
// versioned-read slot and the read.
func (m *SessionModule) listReadContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), m.readTimeout())
}

// NewSessionModule creates a SessionModule with the given MetaStore.
func NewSessionModule(meta *store.MetaStore) *SessionModule {
	return &SessionModule{
		meta:            meta,
		shellHomeReader: readShellHomeFromProc,
		tmuxInstanceFn:  config.GetTmuxInstanceContext,
		shellProbe:      runShellProbe,
		passwdShell:     passwdShellForCurrentUser,
		epoch:           newEpoch(),
		snapSlot:        newSlot(),
		listCacheSlot:   newSlot(),
	}
}

func (m *SessionModule) Name() string           { return "session" }
func (m *SessionModule) Dependencies() []string { return nil }

func (m *SessionModule) Init(c *core.Core) error {
	m.core = c
	m.tmux = c.Tmux
	c.Registry.Register(RegistryKey, SessionProvider(m))
	// One handoff lock instance for the daemon (HandoffLocksKey): the nex
	// module depends on "session", so it exists before its Init reads it.
	c.Registry.Register(HandoffLocksKey, NewHandoffLocks())
	return nil
}

func (m *SessionModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sessions", m.handleList)
	mux.HandleFunc("GET /api/sessions/{code}", m.handleGet)
	mux.HandleFunc("GET /api/sessions/{code}/home", m.handleSessionHome)
	mux.HandleFunc("GET /api/sessions/{code}/cwd", m.handleSessionCwd)
	mux.HandleFunc("POST /api/sessions", m.handleCreate)
	mux.HandleFunc("PATCH /api/sessions/{code}", m.handleRename)
	mux.HandleFunc("DELETE /api/sessions/{code}", m.handleDelete)
	mux.HandleFunc("POST /api/sessions/{code}/send-keys", m.handleSendKeys)
	mux.HandleFunc("/ws/terminal/{code}", m.handleTerminalWS)
	mux.HandleFunc("POST /api/shell/resolve-command", m.handleShellResolveCommand)
	mux.HandleFunc("GET /api/hooks/tmux/status", m.handleTmuxHookStatus)
	mux.HandleFunc("POST /api/hooks/tmux/setup", m.handleTmuxHookSetup)
}

func (m *SessionModule) Start(ctx context.Context) error {
	if err := m.meta.ResetStaleModes(); err != nil {
		return err
	}

	// Install tmux hooks (log warning on error, don't fail startup).
	if err := m.installTmuxHooks(); err != nil {
		log.Printf("session: failed to install tmux hooks: %v (continuing without push)", err)
	}

	// Start session watcher with a child context.
	watchCtx, cancel := context.WithCancel(ctx)
	m.cancelWatch = cancel
	m.runCtx = watchCtx
	m.wstate.setTmuxAlive(m.tmux.TmuxAlive())
	m.core.TmuxAliveFunc = m.TmuxAlive
	m.watchSessions(watchCtx)

	// Register OnSubscribe callback to send initial sessions snapshot.
	m.core.Events.OnSubscribe(m.sendSessionsSnapshot)

	return nil
}

// sendSessionsSnapshot pushes a versioned session list to one new subscriber.
//
// The SPA keeps a new connection's attach gate shut until it has reconciled
// a first sessions frame, and with an unchanged list no push will ever come,
// so a failed snapshot is not the end of it (#1293): it is retried in the
// background (retrySessionsSnapshot), and if every retry fails too the
// connection is closed so the client reconnects and asks again. A retried
// snapshot may land after a push on the same connection; each carries its own
// seq and the client orders by it (spec 2026-09-23 §3.3/§3.4).
func (m *SessionModule) sendSessionsSnapshot(sub *core.EventSubscriber) {
	ctx, cancel := m.listReadContext()
	defer cancel()
	if err := m.trySessionsSnapshot(ctx, sub); err != nil {
		log.Printf("session: OnSubscribe list error: %v (retrying in background)", err)
		m.snapshotRetriesLive.Add(1)
		go m.retrySessionsSnapshot(sub)
	}
}

// trySessionsSnapshot reads a versioned list under ctx and sends it to sub.
// Only a failed read is an error; Send is a no-op on a closed subscriber.
func (m *SessionModule) trySessionsSnapshot(ctx context.Context, sub *core.EventSubscriber) error {
	v, err := m.versionedList(ctx)
	if err != nil {
		return err
	}
	data, err := json.Marshal(v.hostEvent())
	if err != nil {
		log.Printf("session: OnSubscribe marshal error: %v", err)
		return nil
	}
	sub.Send(data)
	return nil
}

// retrySessionsSnapshot retries a failed subscribe snapshot after each delay,
// each try with a fresh read budget. Everything it does — the waits and the
// reads — ends as soon as the connection does (or the module stops); if every
// try fails it closes the connection so the client reconnects.
func (m *SessionModule) retrySessionsSnapshot(sub *core.EventSubscriber) {
	defer m.snapshotRetriesLive.Add(-1)
	run := m.runCtx
	if run == nil {
		run = context.Background()
	}
	life, endLife := context.WithCancel(run)
	defer endLife()
	go func() {
		select {
		case <-sub.Done():
			endLife()
		case <-life.Done():
		}
	}()

	delays := m.snapshotRetryDelays
	if delays == nil {
		delays = defaultSnapshotRetryDelays
	}
	for i, d := range delays {
		t := time.NewTimer(d)
		select {
		case <-life.Done():
			t.Stop()
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(life, m.readTimeout())
		err := m.trySessionsSnapshot(ctx, sub)
		cancel()
		if err == nil {
			return
		}
		if life.Err() != nil {
			return
		}
		log.Printf("session: OnSubscribe snapshot retry %d/%d failed: %v", i+1, len(delays), err)
	}
	log.Printf("session: OnSubscribe snapshot failed after %d retries; closing the connection so the client reconnects", len(delays))
	m.core.Events.Remove(sub)
}

func (m *SessionModule) Stop(_ context.Context) error {
	// Cancel watcher goroutines.
	if m.cancelWatch != nil {
		m.cancelWatch()
	}
	// Remove tmux hooks (best-effort).
	m.removeTmuxHooks()
	return nil
}
