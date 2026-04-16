package sync

import (
	"context"
	"log"
	"net/http"
	"path/filepath"

	"github.com/wake/purdex/internal/core"
)

// SyncModule provides settings-sync persistence and pairing over HTTP.
type SyncModule struct {
	core  *core.Core
	store *SyncStore
}

// New returns a new SyncModule ready for registration.
func New() *SyncModule { return &SyncModule{} }

func (m *SyncModule) Name() string           { return "sync" }
func (m *SyncModule) Dependencies() []string { return nil }

// Init opens (or creates) the sync SQLite database inside DataDir.
func (m *SyncModule) Init(c *core.Core) error {
	m.core = c
	dbPath := filepath.Join(c.Cfg.DataDir, "sync.db")
	var err error
	m.store, err = OpenSyncStore(dbPath)
	return err
}

// RegisterRoutes wires up all /api/sync/* endpoints.
func (m *SyncModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/sync/push", m.handlePush)
	mux.HandleFunc("GET /api/sync/pull", m.handlePull)
	mux.HandleFunc("GET /api/sync/history", m.handleHistory)
	mux.HandleFunc("POST /api/sync/group/create", m.handleGroupCreate)
	mux.HandleFunc("POST /api/sync/group/join", m.handleGroupJoin)
	mux.HandleFunc("POST /api/sync/pair/create", m.handlePairCreate)
	mux.HandleFunc("POST /api/sync/pair/verify", m.handlePairVerify)
	mux.HandleFunc("GET /api/sync/group/members", m.handleGroupMembers)
	mux.HandleFunc("DELETE /api/sync/group/member", m.handleGroupRemoveMember)
}

// Start logs a banner; no background work required.
func (m *SyncModule) Start(_ context.Context) error {
	log.Println("[sync] endpoints enabled")
	return nil
}

// Stop closes the underlying SQLite database.
func (m *SyncModule) Stop(_ context.Context) error {
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}
