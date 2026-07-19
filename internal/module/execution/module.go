package execution

import (
	"context"
	"log"
	"net/http"
	"path/filepath"

	"github.com/wake/purdex/internal/core"
)

// RegistryKey is the service-locator key under which the execution store is
// published for in-process consumers (the dispatch report sender's durability
// cut, admission, reconcile).
const RegistryKey = "execution.store"

// ExecutionModule owns the Purdex-side execution runtime SOT: the execution row
// store, execution_id generation, the state machine, and dispatch_id upsert
// idempotency (spec §4.3/§5.1). M0 Task P.1 wires the store only; admission,
// launch, reconcile, and reporting land in later tasks.
type ExecutionModule struct {
	core  *core.Core
	store *ExecutionStore
	// hostID scopes artifact pointers in the read projection. Captured from core
	// config at Init; overridable in tests.
	hostID string
	// diff computes the best-effort diff summary surfaced by the read path
	// (P.12). Defaults to BuildDiffArtifact; overridable in tests.
	diff diffFunc
}

// New returns a new ExecutionModule ready for registration.
func New() *ExecutionModule { return &ExecutionModule{diff: BuildDiffArtifact} }

func (m *ExecutionModule) Name() string           { return "execution" }
func (m *ExecutionModule) Dependencies() []string { return nil }

// Init opens (or creates) the execution SQLite database inside DataDir.
func (m *ExecutionModule) Init(c *core.Core) error {
	m.core = c
	dbPath := filepath.Join(c.Cfg.DataDir, "execution.db")
	var err error
	m.store, err = OpenExecution(dbPath)
	if err != nil {
		return err
	}
	c.CfgMu.RLock()
	m.hostID = c.Cfg.HostID
	c.CfgMu.RUnlock()
	c.Registry.Register(RegistryKey, m.store)
	return nil
}

// RegisterRoutes exposes the M0 read path (spec §9, Task P.12): a read-only
// projection of an execution row consumed by the SPA detail page and deeplink
// resolver. It is strictly a read — observe-only, no stdin. Auth is the global
// TokenAuth middleware's job (this mux is wrapped by it in main).
func (m *ExecutionModule) RegisterRoutes(mux *http.ServeMux) {
	if m.store == nil {
		return
	}
	mux.HandleFunc("GET /api/execution/{id}", m.handleGetExecution)
}

// Start logs a banner. Startup reconcile sweep lands in P.9.
func (m *ExecutionModule) Start(_ context.Context) error {
	log.Println("[execution] runtime store ready")
	return nil
}

// Stop closes the underlying SQLite database.
func (m *ExecutionModule) Stop(_ context.Context) error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}

// Store exposes the execution store for in-process consumers (dispatch worker,
// admission, reconcile) once those tasks land.
func (m *ExecutionModule) Store() *ExecutionStore { return m.store }
