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
type OriginResolver interface {
	ResolveOrigin(inbox string) (team.Origin, bool)
	LiveSession(sessionID string) bool
}

// Module owns team.db and serves /api/team/*.
type Module struct {
	core    *core.Core
	store   *Store
	origins OriginResolver
	now     func() int64 // unix ms; injectable for tests
	logf    func(format string, args ...any)

	// stopCtx is cancelled first in Stop: long-polls return, the sweeper
	// exits and POST create answers 503 not_ready. The DB stays open until
	// Close (PD6): in-flight handlers still read it during srv.Shutdown.
	stopCtx    context.Context
	stopCancel context.CancelFunc
	sweepWG    sync.WaitGroup
	tickN      int // sweeper ticks so far; only the sweeper goroutine (or a test) touches it

	createMu sync.Mutex // serialises the request_open check with the insert

	mu      sync.Mutex
	waiters map[string][]chan struct{} // long-polls per approval id; closed when it closes
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
	return nil
}

// RegisterRoutes mounts the create and list routes. The per-id routes
// (GET long-poll, DELETE, POST decide) and GET /api/team/inflight follow
// in the next PR; the module is not mounted in cmd/pdx until then.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/team/approvals", m.handleCreate)
	mux.HandleFunc("GET /api/team/approvals", m.handleList)
}

// Start is filled in by Task 2.6 (boot lease grace, snapshot, sweeper).
func (m *Module) Start(context.Context) error { return nil }

// Stop cancels stopCtx (long-polls return, create answers not_ready) and
// joins the sweeper. Idempotent. The DB is closed in Close.
func (m *Module) Stop(context.Context) error {
	m.stopCancel()
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

func (m *Module) broadcast(op string, a *team.Approval) {
	v, err := json.Marshal(team.EventValue{Op: op, Approval: a})
	if err != nil {
		m.logf("[team] encode %s event: %v", op, err)
		return
	}
	m.core.Events.BroadcastEvent(core.HostEvent{Type: team.EventType, Value: string(v)})
}
