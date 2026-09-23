package session

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
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
	// both slip past the duplicate check. See #61.
	createMu sync.Mutex

	// listCache debounces rapid ListSessions calls (1s TTL). See #128.
	listCacheMu   sync.Mutex
	listCacheData []SessionInfo
	listCacheAt   time.Time

	// nameCache backs LookupCodeByName: a name→code map plus a TTL stamp.
	// Separate from listCache because the lookup fast path runs only one
	// tmux subprocess (list-sessions) — no meta merge, no per-session
	// metadata fan-out — and is hit on the hook hot path (#?).
	nameCacheMu   sync.Mutex
	nameCacheData map[string]string
	nameCacheAt   time.Time

	// Versioned session lists (spec 2026-09-23 §3.3, versioned.go). epoch
	// identifies this process's counter; snapMu serializes seq assignment
	// together with the tmux read, and guards epoch (it rotates at maxSeq).
	snapMu  sync.Mutex
	snapSeq uint64
	epoch   string
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
	m.wstate.setTmuxAlive(m.tmux.TmuxAlive())
	m.core.TmuxAliveFunc = m.TmuxAlive
	m.watchSessions(watchCtx)

	// Register OnSubscribe callback to send initial sessions snapshot.
	m.core.Events.OnSubscribe(m.sendSessionsSnapshot)

	return nil
}

// sendSessionsSnapshot pushes a versioned session list to one new subscriber.
func (m *SessionModule) sendSessionsSnapshot(sub *core.EventSubscriber) {
	v, err := m.versionedList()
	if err != nil {
		log.Printf("session: OnSubscribe list error: %v", err)
		return
	}
	data, err := json.Marshal(v.hostEvent())
	if err != nil {
		return
	}
	sub.Send(data)
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
