package hostconfig

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wake/purdex/internal/core"
)

// Module serves per-host launcher config over /api/hostconfig*.
type Module struct {
	core  *core.Core
	store *Store
	home  func() (string, error) // daemon user's home; injectable for tests
}

// New returns a new Module ready for registration.
func New() *Module { return &Module{home: os.UserHomeDir} }

func (m *Module) Name() string           { return "hostconfig" }
func (m *Module) Dependencies() []string { return nil }

// Init opens (or creates) the host config SQLite database inside DataDir.
func (m *Module) Init(c *core.Core) error {
	m.core = c
	var err error
	m.store, err = OpenStore(filepath.Join(c.Cfg.DataDir, "host_config.db"))
	return err
}

// RegisterRoutes wires up all /api/hostconfig endpoints.
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/hostconfig", m.handleGet)
	mux.HandleFunc("PUT /api/hostconfig/projects", m.putHandler(KeyProjects, func(raw []byte) (any, error) { return normalizeProjects(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/commands", m.putHandler(KeyCommands, func(raw []byte) (any, error) { return normalizeCommands(raw) }))
	mux.HandleFunc("PUT /api/hostconfig/resume-templates", m.putHandler(KeyResumeTemplates, func(raw []byte) (any, error) { return normalizeResumeTemplates(raw) }))
	mux.HandleFunc("POST /api/hostconfig/check-path", m.handleCheckPath)
}

// Start logs a banner; no background work required.
func (m *Module) Start(_ context.Context) error {
	log.Println("[hostconfig] endpoints enabled")
	return nil
}

// Stop closes the underlying SQLite database.
func (m *Module) Stop(_ context.Context) error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}
