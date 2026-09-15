package devicestate

import (
	"context"
	"log"
	"net/http"
	"path/filepath"

	"github.com/wake/purdex/internal/core"
)

// Module serves per-device workspace/tab state over /api/device-state/*.
type Module struct {
	core  *core.Core
	store *Store
}

// New returns a new Module ready for registration.
func New() *Module { return &Module{} }

func (m *Module) Name() string           { return "devicestate" }
func (m *Module) Dependencies() []string { return nil }

// Init opens (or creates) the device state SQLite database inside DataDir.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	var err error
	m.store, err = OpenStore(filepath.Join(c.Cfg.DataDir, "device_state.db"))
	return err
}

// RegisterRoutes wires up all /api/device-state endpoints.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/device-state", m.handleList)
	mux.HandleFunc("PUT /api/device-state/{clientId}", m.handlePut)
	mux.HandleFunc("GET /api/device-state/{clientId}", m.handleGet)
	mux.HandleFunc("DELETE /api/device-state/{clientId}", m.handleDelete)
}

// Start logs a banner; no background work required.
func (m *Module) Start(_ context.Context) error {
	log.Println("[devicestate] endpoints enabled")
	return nil
}

// Stop closes the underlying SQLite database.
func (m *Module) Stop(_ context.Context) error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}
