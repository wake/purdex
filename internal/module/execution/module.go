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
}

// New returns a new ExecutionModule ready for registration.
func New() *ExecutionModule { return &ExecutionModule{} }

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
	c.Registry.Register(RegistryKey, m.store)
	return nil
}

// RegisterRoutes reserves the /api/execution/* namespace. P.1 exposes no HTTP
// surface yet (the store is consumed in-process by the dispatch worker in later
// tasks); routes are added when the read path lands (P.12).
func (m *ExecutionModule) RegisterRoutes(_ *http.ServeMux) {}

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
