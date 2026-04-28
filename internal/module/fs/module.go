package fs

import (
	"context"
	"log"
	"net/http"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/session"
)

// FsModule exposes filesystem endpoints (list/read/write/stat/mkdir/delete/rename/search).
// `sessions` is required by handleSearch to resolve `kind: "session-cwd"` capability roots.
type FsModule struct {
	sessions session.SessionProvider
}

func New() *FsModule { return &FsModule{} }

func (m *FsModule) Name() string           { return "fs" }
func (m *FsModule) Dependencies() []string { return []string{"session"} }

func (m *FsModule) Init(c *core.Core) error {
	m.sessions = c.Registry.MustGet(session.RegistryKey).(session.SessionProvider)
	return nil
}

func (m *FsModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/fs/list", m.handleList)
	mux.HandleFunc("POST /api/fs/read", m.handleRead)
	mux.HandleFunc("POST /api/fs/write", m.handleWrite)
	mux.HandleFunc("POST /api/fs/stat", m.handleStat)
	mux.HandleFunc("POST /api/fs/mkdir", m.handleMkdir)
	mux.HandleFunc("POST /api/fs/delete", m.handleDelete)
	mux.HandleFunc("POST /api/fs/rename", m.handleRename)
	mux.HandleFunc("POST /api/fs/search", m.handleSearch)
}

func (m *FsModule) Start(_ context.Context) error {
	log.Println("[fs] endpoints enabled")
	return nil
}

func (m *FsModule) Stop(_ context.Context) error { return nil }
