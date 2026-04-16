package fs

import (
	"context"
	"log"
	"net/http"

	"github.com/wake/purdex/internal/core"
)

type FsModule struct{}

func New() *FsModule { return &FsModule{} }

func (m *FsModule) Name() string           { return "fs" }
func (m *FsModule) Dependencies() []string { return nil }
func (m *FsModule) Init(_ *core.Core) error { return nil }

func (m *FsModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/fs/list", m.handleList)
	mux.HandleFunc("POST /api/fs/read", m.handleRead)
	mux.HandleFunc("POST /api/fs/write", m.handleWrite)
	mux.HandleFunc("POST /api/fs/stat", m.handleStat)
	mux.HandleFunc("POST /api/fs/mkdir", m.handleMkdir)
	mux.HandleFunc("POST /api/fs/delete", m.handleDelete)
	mux.HandleFunc("POST /api/fs/rename", m.handleRename)
}

func (m *FsModule) Start(_ context.Context) error {
	log.Println("[fs] endpoints enabled")
	return nil
}

func (m *FsModule) Stop(_ context.Context) error { return nil }
