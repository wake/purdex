package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/devmode"
	"github.com/wake/purdex/internal/execstat"
)

type stepRunner func(ctx context.Context, session *BuildSession, phase, dir, name string, args ...string) error

type DevModule struct {
	core        *core.Core
	repoRoot    string
	versionFile string
	hashFn      func(paths ...string) string
	mu          sync.Mutex
	building    bool
	buildError  string
	buildCmd    func(session *BuildSession) error
	runStep     stepRunner
	// tools computes the PATH for pnpm / node / go subprocesses.
	tools              toolEnv
	buildSession       *BuildSession
	lastFailedSPA      string
	lastFailedElectron string
	stopCtx            context.Context
	stopCancel         context.CancelFunc
	// restartFn asks core for a restart that execs execPath (core's
	// RequestRestart: ordered shutdown, then re-exec). Nil means skip the
	// restart (test-safe default); Init sets it to the core's.
	restartFn func(reason, execPath string, afterAccept func()) error
	// selfPathFn returns the resolved path of the binary this daemon is running
	// from; a rebuild replaces that file (spec 2026-10-22-kc3-offline §1). Nil
	// means <repoRoot>/bin/pdx (test-safe default: a test never renames over its
	// own test executable); Init sets it to runningBinary.
	selfPathFn func() (string, error)
	// verifyFn runs the freshly built binary at path before it replaces the installed one and returns an error when it is not a working pdx
	// of wantHash. Nil means no check (test-safe default: the binaries tests build have no version command); Init sets verifyBinary.
	verifyFn func(ctx context.Context, path, wantHash string) error
	// gitHeadFn returns the short hash of HEAD, or "" when unavailable.
	// Injected by tests; defaulted to m.gitHead in Init.
	gitHeadFn func() string
}

func New(repoRoot string) *DevModule {
	return &DevModule{
		repoRoot:    repoRoot,
		versionFile: filepath.Join(repoRoot, "VERSION"),
		hashFn:      nil,
	}
}

// WithExtraPath sets the [dev] extra_path directories searched first for
// pnpm / node / go.
func (m *DevModule) WithExtraPath(dirs []string) *DevModule {
	m.tools.extra = dirs
	return m
}

func (m *DevModule) Name() string           { return "dev" }
func (m *DevModule) Dependencies() []string { return nil }

func (m *DevModule) Init(c *core.Core) error {
	m.core = c
	m.stopCtx, m.stopCancel = context.WithCancel(context.Background())
	if m.hashFn == nil {
		m.hashFn = m.gitHash
	}
	if m.gitHeadFn == nil {
		m.gitHeadFn = m.gitHead
	}
	if m.runStep == nil {
		m.runStep = m.streamTool
	}
	if m.buildCmd == nil {
		m.buildCmd = m.defaultBuild
	}
	if m.restartFn == nil {
		m.restartFn = c.RequestRestart
	}
	if m.selfPathFn == nil {
		m.selfPathFn = runningBinary
	}
	if m.verifyFn == nil {
		m.verifyFn = verifyBinary
	}
	return nil
}

// runningBinary is the file this process was started from, symlinks resolved: the same value `pdx setup` and the hook and statusline
// installers write into settings. It never falls back to the unresolved path, so a caller can tell "unknown" from "known".
func runningBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// verifyBinary runs `path version --json` and requires exit 0 and JSON output; when wantHash is not empty the JSON's hash must equal it.
func verifyBinary(ctx context.Context, path, wantHash string) error {
	out, err := execstat.CommandContext(ctx, "dev.verify-binary", path, "version", "--json").Output()
	if err != nil {
		return fmt.Errorf("%s version --json: %w", filepath.Base(path), err)
	}
	var info struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return fmt.Errorf("%s version --json printed no JSON: %w", filepath.Base(path), err)
	}
	if wantHash != "" && info.Hash != wantHash {
		return fmt.Errorf("the new binary reports hash %q, the build injected %q", info.Hash, wantHash)
	}
	return nil
}

// rebuildTarget is the file a daemon rebuild replaces. Rebuilding into the repo while the daemon runs from elsewhere (mlab runs
// ~/.config/pdx/bin/pdx so a missing Workspace volume cannot take it down) would send the next restart, and the next `pdx setup`, back to the repo.
func (m *DevModule) rebuildTarget() (string, error) {
	if m.selfPathFn == nil {
		return filepath.Join(m.repoRoot, "bin", "pdx"), nil
	}
	p, err := m.selfPathFn()
	if err != nil {
		return "", err
	}
	if p == "" {
		return "", fmt.Errorf("the executable path is empty")
	}
	return p, nil
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
	// Two-layer gate. Layer 1 (config.Dev.Update in main.go) decides whether
	// to instantiate this module at all — when false, DevModule isn't even
	// constructed. Layer 2 (devmode.Enabled(), on unless PDX_DEV_MODE=0)
	// gates route registration so operators can run the module without
	// exposing the dev endpoints (useful for staging or partial rollouts).
	// Both must be set to serve /api/dev/* requests.
	if !devmode.Enabled() {
		return
	}
	mux.HandleFunc("GET /api/dev/update/check", m.handleCheck)
	mux.HandleFunc("GET /api/dev/update/check/stream", m.handleCheckStream)
	mux.HandleFunc("GET /api/dev/update/download", m.handleDownload)
	mux.HandleFunc("GET /api/dev/daemon/check", m.handleDaemonCheck)
	mux.HandleFunc("POST /api/dev/daemon/rebuild", m.handleDaemonRebuild)
	mux.HandleFunc("GET /api/dev/daemon/download", m.handleDaemonDownload)
}

func (m *DevModule) Start(_ context.Context) error {
	if !devmode.Enabled() {
		log.Println("[dev] update endpoints disabled (PDX_DEV_MODE=0)")
		return nil
	}
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
	cmd := execstat.Command("dev.git", "git", args...)
	cmd.Dir = m.repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// gitHead returns the short hash of HEAD, or "" if git is unavailable.
// Bounded so a wedged git cannot hold the download mutex.
func (m *DevModule) gitHead() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := execstat.CommandContext(ctx, "dev.git-log", "git", "-C", m.repoRoot, "log", "-1", "--format=%h").Output()
	if err != nil {
		return ""
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
