package files

import (
	"context"
	"net/http"

	"github.com/wake/tmux-box/internal/core"
)

type FilesModule struct{}

func New() *FilesModule {
	return &FilesModule{}
}

func (m *FilesModule) Name() string              { return "files" }
func (m *FilesModule) Dependencies() []string    { return nil }
func (m *FilesModule) Init(_ *core.Core) error   { return nil }
func (m *FilesModule) Start(_ context.Context) error { return nil }
func (m *FilesModule) Stop(_ context.Context) error  { return nil }

func (m *FilesModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/files", m.handleList)
}
