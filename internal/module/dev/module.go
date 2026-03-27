package dev

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wake/tmux-box/internal/core"
)

type DevModule struct {
	core        *core.Core
	repoRoot    string
	versionFile string
	hashFn      func(paths ...string) string
}

func New(repoRoot string) *DevModule {
	return &DevModule{
		repoRoot:    repoRoot,
		versionFile: filepath.Join(repoRoot, "VERSION"),
		hashFn:      nil,
	}
}

func (m *DevModule) Name() string           { return "dev" }
func (m *DevModule) Dependencies() []string { return nil }

func (m *DevModule) Init(c *core.Core) error {
	m.core = c
	if m.hashFn == nil {
		m.hashFn = m.gitHash
	}
	return nil
}

func (m *DevModule) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/dev/update/check", m.handleCheck)
	mux.HandleFunc("GET /api/dev/update/download", m.handleDownload)
}

func (m *DevModule) Start(_ context.Context) error {
	log.Println("[dev] update endpoints enabled")
	return nil
}

func (m *DevModule) Stop(_ context.Context) error { return nil }

func (m *DevModule) gitHash(paths ...string) string {
	args := append([]string{"log", "-1", "--format=%h", "--"}, paths...)
	cmd := exec.Command("git", args...)
	cmd.Dir = m.repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func (m *DevModule) readVersion() string {
	data, err := os.ReadFile(m.versionFile)
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}
