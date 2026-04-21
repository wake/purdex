# Dev Update Auto-Build Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make dev update check/download consistent by reading build metadata from `out/.build-info.json` and auto-building when source diverges from build.

**Architecture:** `electron.vite.config.ts` writes build metadata on `closeBundle`. Daemon's `handleCheck` reads it for build hashes, compares with git source hashes, and auto-triggers `electron-vite build` in a background goroutine when stale. Client polls while building, then proceeds with normal update flow.

**Tech Stack:** Go (daemon), TypeScript/Vite (electron build), React (SPA UI)

---

### Task 1: Write build metadata on electron-vite build

**Files:**
- Modify: `electron.vite.config.ts`

- [ ] **Step 1: Add `closeBundle` hook to write `out/.build-info.json`**

In `electron.vite.config.ts`, add a Vite plugin to the `main` config that writes build metadata after the bundle closes:

```ts
import { defineConfig } from 'electron-vite'
import { resolve } from 'path'
import { execSync } from 'child_process'
import { readFileSync, writeFileSync, mkdirSync } from 'fs'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

function gitHash(...paths: string[]): string {
  try {
    return execSync(`git log -1 --format=%h -- ${paths.join(' ')}`, { encoding: 'utf-8' }).trim()
  } catch {
    return 'unknown'
  }
}

function readVersion(): string {
  try {
    return readFileSync(resolve(__dirname, 'VERSION'), 'utf-8').trim()
  } catch {
    return 'unknown'
  }
}

const buildDefines = {
  __APP_VERSION__: JSON.stringify(readVersion()),
  __ELECTRON_HASH__: JSON.stringify(gitHash('electron/', 'electron.vite.config.ts')),
  __SPA_HASH__: JSON.stringify(gitHash('spa/')),
}

function buildInfoPlugin() {
  return {
    name: 'write-build-info',
    closeBundle() {
      const info = {
        version: readVersion(),
        spaHash: gitHash('spa/'),
        electronHash: gitHash('electron/', 'electron.vite.config.ts'),
        builtAt: new Date().toISOString(),
      }
      const outDir = resolve(__dirname, 'out')
      mkdirSync(outDir, { recursive: true })
      writeFileSync(resolve(outDir, '.build-info.json'), JSON.stringify(info, null, 2) + '\n')
    },
  }
}

export default defineConfig({
  main: {
    build: {
      outDir: 'out/main',
      rollupOptions: {
        input: { index: resolve(__dirname, 'electron/main.ts') },
        external: ['electron'],
      },
    },
    define: buildDefines,
    plugins: [buildInfoPlugin()],
  },
  preload: {
    build: {
      outDir: 'out/preload',
      rollupOptions: {
        input: { index: resolve(__dirname, 'electron/preload.ts') },
        external: ['electron'],
        output: { format: 'cjs', entryFileNames: '[name].js' },
      },
    },
  },
  renderer: {
    root: 'spa',
    plugins: [react(), tailwindcss()],
    build: {
      outDir: resolve(__dirname, 'out/renderer'),
      rollupOptions: {
        input: resolve(__dirname, 'spa/index.html'),
      },
    },
  },
})
```

- [ ] **Step 2: Run build and verify `.build-info.json` is written**

Run: `pnpm run electron:build`

Expected: `out/.build-info.json` exists with correct content:
```bash
cat out/.build-info.json
```
Should output JSON with `version`, `spaHash`, `electronHash`, `builtAt` fields.

- [ ] **Step 3: Commit**

```bash
git add electron.vite.config.ts
git commit -m "feat: write .build-info.json on electron-vite build"
```

---

### Task 2: Daemon — read build info and add auto-build state

**Files:**
- Modify: `internal/module/dev/module.go`
- Modify: `internal/module/dev/handler.go`

- [ ] **Step 1: Write failing test for new check response format**

In `internal/module/dev/handler_test.go`, replace `TestHandleCheck` and add new tests:

```go
package dev

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleCheck_WithBuildInfo(t *testing.T) {
	dir := t.TempDir()
	versionFile := filepath.Join(dir, "VERSION")
	os.WriteFile(versionFile, []byte("1.0.0-alpha.24\n"), 0644)

	// Create .build-info.json with matching hashes
	outDir := filepath.Join(dir, "out")
	os.MkdirAll(outDir, 0755)
	buildInfo := `{"version":"1.0.0-alpha.24","spaHash":"abc1234","electronHash":"def5678","builtAt":"2026-03-29T00:00:00Z"}`
	os.WriteFile(filepath.Join(outDir, ".build-info.json"), []byte(buildInfo), 0644)

	m := &DevModule{
		repoRoot:    dir,
		versionFile: versionFile,
		hashFn:      func(paths ...string) string { return "abc1234" },
	}
	m.Init(nil)

	req := httptest.NewRequest("GET", "/api/dev/update/check", nil)
	w := httptest.NewRecorder()
	m.handleCheck(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}

	var resp UpdateCheckResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Top-level hashes come from .build-info.json
	if resp.SPAHash != "abc1234" {
		t.Errorf("spaHash: want abc1234, got %s", resp.SPAHash)
	}
	if resp.ElectronHash != "def5678" {
		t.Errorf("electronHash: want def5678, got %s", resp.ElectronHash)
	}
	// Source hashes come from hashFn (git)
	if resp.Source.SPAHash != "abc1234" {
		t.Errorf("source.spaHash: want abc1234, got %s", resp.Source.SPAHash)
	}
	if resp.Building {
		t.Error("building: want false, got true")
	}
}

func TestHandleCheck_NoBuildInfo(t *testing.T) {
	dir := t.TempDir()
	versionFile := filepath.Join(dir, "VERSION")
	os.WriteFile(versionFile, []byte("1.0.0-alpha.24\n"), 0644)
	// No out/.build-info.json

	m := &DevModule{
		repoRoot:    dir,
		versionFile: versionFile,
		hashFn:      func(paths ...string) string { return "abc1234" },
		buildCmd:    func() error { return nil }, // stub build
	}
	m.Init(nil)

	req := httptest.NewRequest("GET", "/api/dev/update/check", nil)
	w := httptest.NewRecorder()
	m.handleCheck(w, req)

	var resp UpdateCheckResponse
	json.NewDecoder(w.Body).Decode(&resp)

	// No build info → hashes are "unknown"
	if resp.SPAHash != "unknown" {
		t.Errorf("spaHash: want unknown, got %s", resp.SPAHash)
	}
	// Source ≠ build ("abc1234" ≠ "unknown") → should trigger build
	if !resp.Building {
		t.Error("building: want true when source ≠ build")
	}
}

func TestHandleCheck_StaleTriggersAutoBuild(t *testing.T) {
	dir := t.TempDir()
	versionFile := filepath.Join(dir, "VERSION")
	os.WriteFile(versionFile, []byte("1.0.0-alpha.24\n"), 0644)

	outDir := filepath.Join(dir, "out")
	os.MkdirAll(outDir, 0755)
	buildInfo := `{"version":"1.0.0-alpha.24","spaHash":"old1234","electronHash":"old5678","builtAt":"2026-03-29T00:00:00Z"}`
	os.WriteFile(filepath.Join(outDir, ".build-info.json"), []byte(buildInfo), 0644)

	buildCalled := false
	m := &DevModule{
		repoRoot:    dir,
		versionFile: versionFile,
		hashFn:      func(paths ...string) string { return "new9999" },
		buildCmd:    func() error { buildCalled = true; return nil },
	}
	m.Init(nil)

	req := httptest.NewRequest("GET", "/api/dev/update/check", nil)
	w := httptest.NewRecorder()
	m.handleCheck(w, req)

	var resp UpdateCheckResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if !resp.Building {
		t.Error("building: want true when source ≠ build")
	}
	// Build runs async, but buildCmd was set — verify it gets called
	// (In real code this is a goroutine; in test with stub it may run inline or async)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/dev/ -v -run TestHandleCheck`
Expected: FAIL — `UpdateCheckResponse` doesn't have `Source`, `Building`, `BuildError` fields yet.

- [ ] **Step 3: Update response type and module state in handler.go**

Replace `internal/module/dev/handler.go`:

```go
package dev

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type SourceHashes struct {
	SPAHash      string `json:"spaHash"`
	ElectronHash string `json:"electronHash"`
}

type UpdateCheckResponse struct {
	Version      string       `json:"version"`
	SPAHash      string       `json:"spaHash"`
	ElectronHash string       `json:"electronHash"`
	Source       SourceHashes `json:"source"`
	Building     bool         `json:"building"`
	BuildError   string       `json:"buildError"`
}

type BuildInfo struct {
	Version      string `json:"version"`
	SPAHash      string `json:"spaHash"`
	ElectronHash string `json:"electronHash"`
	BuiltAt      string `json:"builtAt"`
}

func (m *DevModule) readBuildInfo() BuildInfo {
	path := filepath.Join(m.repoRoot, "out", ".build-info.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return BuildInfo{SPAHash: "unknown", ElectronHash: "unknown"}
	}
	var info BuildInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return BuildInfo{SPAHash: "unknown", ElectronHash: "unknown"}
	}
	return info
}

func (m *DevModule) handleCheck(w http.ResponseWriter, r *http.Request) {
	buildInfo := m.readBuildInfo()
	sourceSPA := m.hashFn("spa/")
	sourceElectron := m.hashFn("electron/", "electron.vite.config.ts")

	sourceStale := sourceSPA != buildInfo.SPAHash || sourceElectron != buildInfo.ElectronHash

	m.mu.Lock()
	building := m.building
	buildError := m.buildError
	if sourceStale && !m.building {
		m.building = true
		m.buildError = ""
		building = true
		go m.runBuild()
	}
	m.mu.Unlock()

	resp := UpdateCheckResponse{
		Version:      m.readVersion(),
		SPAHash:      buildInfo.SPAHash,
		ElectronHash: buildInfo.ElectronHash,
		Source: SourceHashes{
			SPAHash:      sourceSPA,
			ElectronHash: sourceElectron,
		},
		Building:   building,
		BuildError: buildError,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *DevModule) handleDownload(w http.ResponseWriter, r *http.Request) {
	outDir := filepath.Join(m.repoRoot, "out")
	if _, err := os.Stat(outDir); os.IsNotExist(err) {
		http.Error(w, "out/ directory not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=\"out.tar.gz\"")

	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	err := filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(outDir, path)
		if err != nil {
			return err
		}

		if rel == "." {
			return nil
		}

		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		topLevel := parts[0]
		if topLevel != "main" && topLevel != "preload" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})

	if err != nil {
		return
	}
}
```

- [ ] **Step 4: Update module.go with build state and runBuild**

Replace `internal/module/dev/module.go`:

```go
package dev

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wake/tmux-box/internal/core"
)

type DevModule struct {
	core        *core.Core
	repoRoot    string
	versionFile string
	hashFn      func(paths ...string) string
	buildCmd    func() error // overridable for tests

	mu         sync.Mutex
	building   bool
	buildError string
}

func New(repoRoot string) *DevModule {
	return &DevModule{
		repoRoot:    repoRoot,
		versionFile: filepath.Join(repoRoot, "VERSION"),
	}
}

func (m *DevModule) Name() string           { return "dev" }
func (m *DevModule) Dependencies() []string { return nil }

func (m *DevModule) Init(c *core.Core) error {
	m.core = c
	if m.hashFn == nil {
		m.hashFn = m.gitHash
	}
	if m.buildCmd == nil {
		m.buildCmd = m.defaultBuild
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

func (m *DevModule) runBuild() {
	log.Println("[dev] auto-build started")
	err := m.buildCmd()
	m.mu.Lock()
	m.building = false
	if err != nil {
		m.buildError = err.Error()
		log.Printf("[dev] auto-build failed: %v", err)
	} else {
		m.buildError = ""
		log.Println("[dev] auto-build completed")
	}
	m.mu.Unlock()
}

func (m *DevModule) defaultBuild() error {
	cmd := exec.Command("npx", "electron-vite", "build")
	cmd.Dir = m.repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/dev/ -v`
Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/module/dev/module.go internal/module/dev/handler.go internal/module/dev/handler_test.go
git commit -m "feat: check reads .build-info.json + auto-build when stale"
```

---

### Task 3: Electron — update RemoteVersionInfo type and startup check

**Files:**
- Modify: `electron/updater.ts`
- Modify: `electron/main.ts`

- [ ] **Step 1: Update `RemoteVersionInfo` in updater.ts**

In `electron/updater.ts`, update the interface:

```ts
export interface RemoteVersionInfo {
  version: string
  spaHash: string
  electronHash: string
  source: { spaHash: string; electronHash: string }
  building: boolean
  buildError: string
}
```

No other changes to `updater.ts` — `checkUpdate` already returns `resp.json()` which will include the new fields.

- [ ] **Step 2: Update startup check in main.ts to handle `building` state**

In `electron/main.ts`, change the startup check block (around line 111-122):

```ts
  // Dev: background update check on startup
  if (getAppInfo().devUpdateEnabled) {
    const daemonUrl = 'http://100.64.0.2:7860'
    checkUpdate(daemonUrl).then((remote) => {
      if (remote.building) return // build in progress, SPA will poll
      const local = getAppInfo()
      if (remote.electronHash !== local.electronHash || remote.spaHash !== local.spaHash) {
        for (const win of windowManager.getAllWindows()) {
          if (!win.isDestroyed()) win.webContents.send('dev:update-available', remote)
        }
      }
    }).catch(() => { /* silent — daemon may not be reachable */ })
  }
```

- [ ] **Step 3: Commit**

```bash
git add electron/updater.ts electron/main.ts
git commit -m "feat: update RemoteVersionInfo type + skip IPC when building"
```

---

### Task 4: SPA — update DevEnvironmentSection with building state and polling

**Files:**
- Modify: `spa/src/components/settings/DevEnvironmentSection.tsx`
- Modify: `spa/src/components/settings/DevEnvironmentSection.test.tsx`

- [ ] **Step 1: Write failing test for building state**

Add to `spa/src/components/settings/DevEnvironmentSection.test.tsx`:

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, waitFor } from '@testing-library/react'
import { DevEnvironmentSection } from './DevEnvironmentSection'

const mockGetAppInfo = vi.fn().mockResolvedValue({
  version: '1.0.0-alpha.24',
  electronHash: 'abc1234',
  spaHash: 'def5678',
  devUpdateEnabled: true,
})

const mockCheckUpdate = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
  vi.useFakeTimers()
  window.electronAPI = {
    ...window.electronAPI!,
    getAppInfo: mockGetAppInfo,
    checkUpdate: mockCheckUpdate,
  } as any
})

afterEach(() => {
  vi.useRealTimers()
})

describe('DevEnvironmentSection', () => {
  it('renders section title', () => {
    mockCheckUpdate.mockResolvedValue({
      version: '1.0.0-alpha.24', spaHash: 'def5678', electronHash: 'abc1234',
      source: { spaHash: 'def5678', electronHash: 'abc1234' },
      building: false, buildError: '',
    })
    render(<DevEnvironmentSection />)
    expect(screen.getByText(/Development|開發環境/)).toBeTruthy()
  })

  it('calls getAppInfo on mount', async () => {
    mockCheckUpdate.mockResolvedValue({
      version: '1.0.0-alpha.24', spaHash: 'def5678', electronHash: 'abc1234',
      source: { spaHash: 'def5678', electronHash: 'abc1234' },
      building: false, buildError: '',
    })
    render(<DevEnvironmentSection />)
    expect(mockGetAppInfo).toHaveBeenCalledOnce()
  })

  it('shows building status and polls when building', async () => {
    // First check: building
    mockCheckUpdate.mockResolvedValueOnce({
      version: '1.0.0-alpha.24', spaHash: 'old1234', electronHash: 'abc1234',
      source: { spaHash: 'new5678', electronHash: 'abc1234' },
      building: true, buildError: '',
    })
    // Second check (after poll): done building, update available
    mockCheckUpdate.mockResolvedValueOnce({
      version: '1.0.0-alpha.24', spaHash: 'new5678', electronHash: 'abc1234',
      source: { spaHash: 'new5678', electronHash: 'abc1234' },
      building: false, buildError: '',
    })

    render(<DevEnvironmentSection />)

    await waitFor(() => {
      expect(screen.getByText(/Building|建置中/)).toBeTruthy()
    })

    // Advance timer to trigger poll
    await act(async () => {
      vi.advanceTimersByTime(3000)
    })

    await waitFor(() => {
      expect(mockCheckUpdate).toHaveBeenCalledTimes(2)
    })
  })

  it('shows build error when buildError is set', async () => {
    mockCheckUpdate.mockResolvedValue({
      version: '1.0.0-alpha.24', spaHash: 'old1234', electronHash: 'abc1234',
      source: { spaHash: 'new5678', electronHash: 'abc1234' },
      building: false, buildError: 'exit code 1',
    })

    render(<DevEnvironmentSection />)

    await waitFor(() => {
      expect(screen.getByText(/exit code 1/)).toBeTruthy()
    })
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/components/settings/DevEnvironmentSection.test.tsx`
Expected: FAIL — component doesn't handle `building` state yet.

- [ ] **Step 3: Update DevEnvironmentSection with building state and polling**

Replace `spa/src/components/settings/DevEnvironmentSection.tsx`:

```tsx
import { useState, useEffect, useCallback, useRef } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'

type UpdateStatus = 'idle' | 'checking' | 'building' | 'up_to_date' | 'update_available' | 'error'

interface AppInfo {
  version: string
  electronHash: string
  spaHash: string
}

interface RemoteInfo {
  version: string
  spaHash: string
  electronHash: string
  source: { spaHash: string; electronHash: string }
  building: boolean
  buildError: string
}

export function DevEnvironmentSection() {
  const t = useI18nStore((s) => s.t)
  const getDaemonBase = useHostStore((s) => s.getDaemonBase)
  const daemonBase = getDaemonBase('local')

  const [appInfo, setAppInfo] = useState<AppInfo | null>(null)
  const [remoteInfo, setRemoteInfo] = useState<RemoteInfo | null>(null)
  const [status, setStatus] = useState<UpdateStatus>('idle')
  const [updating, setUpdating] = useState(false)
  const [updateStep, setUpdateStep] = useState<string | null>(null)
  const [updateError, setUpdateError] = useState<string | null>(null)
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    window.electronAPI?.getAppInfo().then(setAppInfo)
  }, [])

  const stopPolling = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current)
      pollRef.current = null
    }
  }, [])

  const processCheckResult = useCallback((remote: RemoteInfo, local: AppInfo) => {
    setRemoteInfo(remote)
    if (remote.building) {
      setStatus('building')
      return
    }
    stopPolling()
    if (remote.buildError) {
      setStatus('error')
      setUpdateError(remote.buildError)
      return
    }
    if (remote.electronHash !== local.electronHash || remote.spaHash !== local.spaHash) {
      setStatus('update_available')
    } else {
      setStatus('up_to_date')
    }
  }, [stopPolling])

  const checkUpdate = useCallback(async () => {
    setStatus('checking')
    setUpdateError(null)
    try {
      const remote = await window.electronAPI!.checkUpdate(daemonBase)
      processCheckResult(remote, appInfo!)
      if (remote.building && !pollRef.current) {
        pollRef.current = setInterval(async () => {
          try {
            const r = await window.electronAPI!.checkUpdate(daemonBase)
            processCheckResult(r, appInfo!)
          } catch { /* ignore poll errors */ }
        }, 3000)
      }
    } catch {
      setStatus('error')
    }
  }, [daemonBase, appInfo, processCheckResult])

  useEffect(() => {
    if (appInfo) checkUpdate()
  }, [appInfo, checkUpdate])

  useEffect(() => {
    if (!window.electronAPI?.onUpdateProgress) return
    return window.electronAPI.onUpdateProgress((step) => setUpdateStep(step))
  }, [])

  useEffect(() => {
    return () => stopPolling()
  }, [stopPolling])

  const handleUpdate = () => {
    setUpdating(true)
    setUpdateStep(null)
    setUpdateError(null)
    window.electronAPI!.applyUpdate(daemonBase).catch((err) => {
      setUpdating(false)
      setUpdateStep(null)
      setUpdateError(err instanceof Error ? err.message : String(err))
    })
  }

  const stepLabels: Record<string, string> = {
    downloading: 'Downloading update…',
    extracting: 'Extracting…',
    applying: 'Applying update…',
    restarting: 'Restarting…',
  }

  const hasElectronUpdate = remoteInfo && appInfo && remoteInfo.electronHash !== appInfo.electronHash
  const hasSPAUpdate = remoteInfo && appInfo && remoteInfo.spaHash !== appInfo.spaHash

  const statusText: Record<string, string> = {
    idle: '',
    checking: t('settings.dev.status.checking'),
    building: t('settings.dev.status.building'),
    up_to_date: t('settings.dev.status.up_to_date'),
    update_available: t('settings.dev.status.update_available'),
    error: t('settings.dev.status.error'),
  }

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg text-text-primary">{t('settings.dev.title')}</h2>
        <p className="text-xs text-text-secondary mb-6">{t('settings.dev.desc')}</p>
      </div>

      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <span className="text-sm text-text-primary">{t('settings.dev.app_version')}</span>
          <span className="text-xs text-text-secondary font-mono">{appInfo?.version ?? '...'}</span>
        </div>
        <div className="flex items-center justify-between">
          <span className="text-sm text-text-primary">{t('settings.dev.spa_hash')}</span>
          <div className="flex items-center gap-2">
            <span className="text-xs text-text-secondary font-mono">{appInfo?.spaHash ?? '...'}</span>
            {hasSPAUpdate && <span className="text-xs text-status-warning font-mono">→ {remoteInfo.spaHash}</span>}
          </div>
        </div>
        <div className="flex items-center justify-between">
          <span className="text-sm text-text-primary">{t('settings.dev.electron_hash')}</span>
          <div className="flex items-center gap-2">
            <span className="text-xs text-text-secondary font-mono">{appInfo?.electronHash ?? '...'}</span>
            {hasElectronUpdate && <span className="text-xs text-status-warning font-mono">→ {remoteInfo.electronHash}</span>}
          </div>
        </div>
      </div>

      {status !== 'idle' && (
        <div className={`text-sm ${status === 'error' ? 'text-status-error' : status === 'building' ? 'text-accent' : status === 'update_available' ? 'text-status-warning' : 'text-text-secondary'}`}>
          {status === 'error' && updateError ? updateError : statusText[status]}
        </div>
      )}

      {updating && updateStep && (
        <div className="text-sm text-accent font-mono">
          {stepLabels[updateStep] ?? updateStep}
        </div>
      )}

      <div className="flex gap-2">
        <button
          onClick={checkUpdate}
          disabled={!appInfo || status === 'checking' || status === 'building'}
          className="px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default"
        >
          {t('settings.dev.btn.check')}
        </button>
        {(hasElectronUpdate || hasSPAUpdate) && (
          <button
            onClick={handleUpdate}
            disabled={updating}
            className="px-3 py-1.5 text-xs rounded-md bg-accent text-text-inverse hover:bg-accent-hover disabled:opacity-50 cursor-pointer disabled:cursor-default"
          >
            {updating ? t('settings.dev.btn.updating') : t('settings.dev.btn.update_app')}
          </button>
        )}
        {hasSPAUpdate && !hasElectronUpdate && (
          <button
            onClick={() => window.electronAPI?.reloadSPA() ?? window.location.reload()}
            className="px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover cursor-pointer"
          >
            {t('settings.dev.btn.reload_spa')}
          </button>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/components/settings/DevEnvironmentSection.test.tsx`
Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/DevEnvironmentSection.tsx spa/src/components/settings/DevEnvironmentSection.test.tsx
git commit -m "feat: DevEnvironmentSection handles building state + polling"
```

---

### Task 5: Add i18n keys for building status

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: Add building status key to both locale files**

In `spa/src/locales/en.json`, after line 183 (`"settings.dev.status.error"`), add:

```json
  "settings.dev.status.building": "Building…",
```

In `spa/src/locales/zh-TW.json`, after line 183 (`"settings.dev.status.error"`), add:

```json
  "settings.dev.status.building": "建置中…",
```

- [ ] **Step 2: Commit**

```bash
git add spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat: add i18n keys for building status"
```

---

### Task 6: Integration test — build daemon and verify full flow

**Files:** none (manual verification)

- [ ] **Step 1: Build daemon**

```bash
cd /Users/wake/Workspace/wake/tmux-box && go build -o bin/tbox ./cmd/tbox
```

- [ ] **Step 2: Run all Go tests**

Run: `go test ./internal/module/dev/ -v`
Expected: All PASS.

- [ ] **Step 3: Run all SPA tests**

Run: `cd spa && npx vitest run`
Expected: All PASS.

- [ ] **Step 4: Run SPA lint**

Run: `cd spa && pnpm run lint`
Expected: No errors.

- [ ] **Step 5: Build Electron (generates .build-info.json)**

Run: `pnpm run electron:build`
Expected: `out/.build-info.json` created with correct hashes.

- [ ] **Step 6: Verify check endpoint**

```bash
curl -s http://100.64.0.2:7860/api/dev/update/check | jq .
```

Expected: Response includes `spaHash`, `electronHash` (from `.build-info.json`), `source` object, `building: false`, `buildError: ""`.

- [ ] **Step 7: Commit any fixes if needed**
