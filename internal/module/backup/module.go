package backup

import (
	"context"
	"log"
	"net/http"
	"path/filepath"

	"github.com/wake/purdex/internal/core"
)

// BackupModule provides the daemon-side content-addressed snapshot store for
// the In-App /buffer tree over HTTP.
type BackupModule struct {
	core  *core.Core
	store *BackupStore
}

// New returns a new BackupModule ready for registration.
func New() *BackupModule { return &BackupModule{} }

func (m *BackupModule) Name() string           { return "backup" }
func (m *BackupModule) Dependencies() []string { return nil }

// Init opens (or creates) the backup SQLite database inside DataDir.
func (m *BackupModule) Init(c *core.Core) error {
	m.core = c
	dbPath := filepath.Join(c.Cfg.DataDir, "backup.db")
	var err error
	m.store, err = OpenBackup(dbPath)
	return err
}

// RegisterRoutes wires up all /api/backup/* endpoints.
func (m *BackupModule) RegisterRoutes(mux *http.ServeMux) {
	// Routes are registered in later tasks (blob/missing/snapshot/history/get).
}

// Start logs a banner and runs a best-effort startup GC.
func (m *BackupModule) Start(_ context.Context) error {
	log.Println("[backup] endpoints enabled")
	if m.store != nil {
		if err := m.store.GC(m.store.now()); err != nil {
			log.Printf("[backup] startup GC: %v", err)
		}
	}
	return nil
}

// Stop closes the underlying SQLite database.
func (m *BackupModule) Stop(_ context.Context) error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}
