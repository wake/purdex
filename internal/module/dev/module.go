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

type stepRunner func(ctx context.Context, session *BuildSession, phase, dir, name string, args ...string) error

type DevModule struct {
	core               *core.Core
	repoRoot           string
	versionFile        string
	hashFn             func(paths ...string) string
	mu                 sync.Mutex
	building           bool
	buildError         string
	buildCmd           func(session *BuildSession) error
	runStep            stepRunner
	buildSession       *BuildSession
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
	if m.runStep == nil {
		m.runStep = streamCmd
	}
	if m.buildCmd == nil {
		m.buildCmd = m.defaultBuild
	}
	return nil
}

// startBuildLocked transitions the module into building state, allocates a
// fresh session, and kicks off the build goroutine. The caller must already
// hold m.mu. Returns the session so callers can subscribe without a race.
func (m *DevModule) startBuildLocked(spaSource, electronSource string) *BuildSession {
	session := newBuildSession()
	m.buildSession = session
	m.building = true
	m.buildError = ""
	m.lastFailedSPA = spaSource
	m.lastFailedElectron = electronSource
	go m.runBuild(session)
	return session
}

func (m *DevModule) runBuild(session *BuildSession) {
	// Remove stale build info so a partial build doesn't leave source hashes
	// that prevent re-triggering on next check
	os.Remove(filepath.Join(m.repoRoot, "out", ".build-info.json"))

	err := m.buildCmd(session)

	if err != nil {
		session.append(BuildEvent{Type: BuildEventError, Error: err.Error()})
	} else {
		session.append(BuildEvent{Type: BuildEventDone})
	}

	// Clear building state BEFORE closing subscriber channels so any handler
	// that re-reads state after the channel close sees a consistent post-
	// build snapshot (building=false + updated build info).
	m.mu.Lock()
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
	m.mu.Unlock()

	session.finish()
}

func (m *DevModule) defaultBuild(session *BuildSession) error {
	parent := m.stopCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()

	steps := []struct {
		label string
		name  string
		args  []string
	}{
		{
			label: "dependency install",
			name:  "pnpm",
			args:  []string{"install", "--frozen-lockfile"},
		},
		{
			label: "icon generation",
			name:  "node",
			args:  []string{"spa/scripts/generate-icon-data.mjs"},
		},
		{
			label: "renderer/main build",
			name:  "pnpm",
			args:  []string{"exec", "electron-vite", "build"},
		},
	}

	for _, step := range steps {
		if err := m.runStep(ctx, session, step.label, m.repoRoot, step.name, step.args...); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("build timed out after 5 minutes")
			}
			return fmt.Errorf("%s failed: %w", step.label, err)
		}
	}

	return nil
}

func (m *DevModule) RegisterRoutes(mux *http.ServeMux) {
	if os.Getenv("PDX_DEV_UPDATE") != "1" {
		return
	}
	mux.HandleFunc("GET /api/dev/update/check", m.handleCheck)
	mux.HandleFunc("GET /api/dev/update/check/stream", m.handleCheckStream)
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
