package dev

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
)

type DevModule struct {
	core               *core.Core
	repoRoot           string
	versionFile        string
	hashFn             func(paths ...string) string
	mu                 sync.Mutex
	building           bool
	buildError         string
	buildCmd           func() error
	lastFailedSPA      string
	lastFailedElectron string
	stopCtx            context.Context
	stopCancel         context.CancelFunc
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
	m.stopCtx, m.stopCancel = context.WithCancel(context.Background())
	if m.hashFn == nil {
		m.hashFn = m.gitHash
	}
	if m.buildCmd == nil {
		m.buildCmd = m.defaultBuild
	}
	return nil
}

func (m *DevModule) runBuild() {
	// Remove stale build info so a partial build doesn't leave source hashes
	// that prevent re-triggering on next check
	os.Remove(filepath.Join(m.repoRoot, "out", ".build-info.json"))

	err := m.buildCmd()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.building = false
	if err != nil {
		m.buildError = err.Error()
		log.Printf("[dev] build failed: %v", err)
	} else {
		m.buildError = ""
		m.lastFailedSPA = ""
		m.lastFailedElectron = ""
		log.Println("[dev] build completed successfully")
	}
}

func (m *DevModule) defaultBuild() error {
	parent := m.stopCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pnpm", "exec", "electron-vite", "build")
	cmd.Dir = m.repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("build timed out after 5 minutes")
		}
		log.Printf("[dev] build output: %s", string(out))
		return err
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

func (m *DevModule) Stop(_ context.Context) error {
	if m.stopCancel != nil {
		m.stopCancel()
	}
	return nil
}

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
