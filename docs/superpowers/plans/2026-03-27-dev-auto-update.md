# Dev Auto-Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a dev-only auto-update system so the Electron `.app` on Air can check for and download updates from Mini's daemon.

**Architecture:** Daemon adds a `dev` module with `/api/dev/update/check` and `/api/dev/update/download` endpoints, gated by config flag. Electron main process injects build hashes at compile time via Vite `define`, checks daemon on startup, and downloads/replaces `out/` files on user request. SPA adds a "Dev Environment" settings section showing version info and update controls.

**Tech Stack:** Go (daemon module), electron-vite (build hash injection), Electron IPC (update flow), React + Zustand (settings UI), Vitest (tests)

---

### Task 1: Config — add `[dev]` section

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add DevConfig struct and field to Config**

```go
// In config.go, add before the Config struct:

type DevConfig struct {
	Update bool `toml:"update" json:"update"`
}

// Add field to Config struct:
// Dev DevConfig `toml:"dev" json:"dev"`
```

Add `Dev DevConfig` field to the `Config` struct after `Features`:

```go
type Config struct {
	Bind         string         `toml:"bind"           json:"bind"`
	Port         int            `toml:"port"           json:"port"`
	Token        string         `toml:"token"          json:"token"`
	Allow        []string       `toml:"allow"          json:"allow"`
	DataDir      string         `toml:"data_dir"       json:"data_dir"`
	AllowedPaths []string       `toml:"allowed_paths"  json:"allowed_paths"`
	Terminal     TerminalConfig `toml:"terminal"       json:"terminal"`
	Stream       StreamConfig   `toml:"stream"         json:"stream"`
	JSONL        JSONLConfig    `toml:"jsonl"          json:"jsonl"`
	Detect       DetectConfig   `toml:"detect"         json:"detect"`
	Features     FeaturesConfig `toml:"features"       json:"features"`
	Dev          DevConfig      `toml:"dev"            json:"dev"`
}
```

No default needed — `Dev.Update` defaults to `false` (zero value).

- [ ] **Step 2: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add [dev] section with update flag"
```

---

### Task 2: Daemon dev module — update check endpoint

**Files:**
- Create: `internal/module/dev/module.go`
- Create: `internal/module/dev/handler.go`
- Test: `internal/module/dev/handler_test.go`

- [ ] **Step 1: Write the test for /api/dev/update/check**

Create `internal/module/dev/handler_test.go`:

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

func TestHandleCheck(t *testing.T) {
	// Create temp VERSION file
	dir := t.TempDir()
	versionFile := filepath.Join(dir, "VERSION")
	os.WriteFile(versionFile, []byte("1.0.0-alpha.21\n"), 0644)

	m := &DevModule{
		repoRoot:    dir,
		versionFile: versionFile,
		hashFn:      func(paths ...string) string { return "abc1234" },
	}

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
	if resp.Version != "1.0.0-alpha.21" {
		t.Errorf("version: want 1.0.0-alpha.21, got %s", resp.Version)
	}
	if resp.ElectronHash != "abc1234" {
		t.Errorf("electronHash: want abc1234, got %s", resp.ElectronHash)
	}
	if resp.SPAHash != "abc1234" {
		t.Errorf("spaHash: want abc1234, got %s", resp.SPAHash)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/dev/ -run TestHandleCheck -v`
Expected: FAIL (package doesn't exist yet)

- [ ] **Step 3: Create module.go**

Create `internal/module/dev/module.go`:

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

	"github.com/wake/tmux-box/internal/core"
)

// DevModule provides dev-only endpoints for auto-update.
// Only registered when config [dev] update = true.
type DevModule struct {
	core        *core.Core
	repoRoot    string
	versionFile string
	hashFn      func(paths ...string) string // injectable for testing
}

func New(repoRoot string) *DevModule {
	return &DevModule{
		repoRoot:    repoRoot,
		versionFile: filepath.Join(repoRoot, "VERSION"),
		hashFn:      nil, // set in Init
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

// gitHash returns the short commit hash for the given paths relative to repoRoot.
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

// readVersion reads the VERSION file content.
func (m *DevModule) readVersion() string {
	data, err := os.ReadFile(m.versionFile)
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}
```

- [ ] **Step 4: Create handler.go with handleCheck**

Create `internal/module/dev/handler.go`:

```go
package dev

import (
	"encoding/json"
	"net/http"
)

type UpdateCheckResponse struct {
	Version      string `json:"version"`
	SPAHash      string `json:"spaHash"`
	ElectronHash string `json:"electronHash"`
}

func (m *DevModule) handleCheck(w http.ResponseWriter, r *http.Request) {
	resp := UpdateCheckResponse{
		Version:      m.readVersion(),
		SPAHash:      m.hashFn("spa/"),
		ElectronHash: m.hashFn("electron/", "electron.vite.config.ts"),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *DevModule) handleDownload(w http.ResponseWriter, r *http.Request) {
	// Implemented in Task 3
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/dev/ -run TestHandleCheck -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/module/dev/
git commit -m "feat(dev): update check endpoint — /api/dev/update/check"
```

---

### Task 3: Daemon dev module — download endpoint

**Files:**
- Modify: `internal/module/dev/handler.go`
- Modify: `internal/module/dev/handler_test.go`

- [ ] **Step 1: Write the test for /api/dev/update/download**

Add to `handler_test.go`:

```go
func TestHandleDownload(t *testing.T) {
	dir := t.TempDir()

	// Create fake out/ structure
	outMain := filepath.Join(dir, "out", "main")
	outPreload := filepath.Join(dir, "out", "preload")
	os.MkdirAll(outMain, 0755)
	os.MkdirAll(outPreload, 0755)
	os.WriteFile(filepath.Join(outMain, "index.mjs"), []byte("// main"), 0644)
	os.WriteFile(filepath.Join(outPreload, "index.js"), []byte("// preload"), 0644)

	m := &DevModule{repoRoot: dir}

	req := httptest.NewRequest("GET", "/api/dev/update/download", nil)
	w := httptest.NewRecorder()
	m.handleDownload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("content-type: want application/gzip, got %s", ct)
	}
	if w.Body.Len() == 0 {
		t.Error("body is empty")
	}
}

func TestHandleDownloadMissingOut(t *testing.T) {
	dir := t.TempDir() // no out/ directory

	m := &DevModule{repoRoot: dir}

	req := httptest.NewRequest("GET", "/api/dev/update/download", nil)
	w := httptest.NewRecorder()
	m.handleDownload(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/dev/ -run TestHandleDownload -v`
Expected: FAIL (handleDownload returns 501)

- [ ] **Step 3: Implement handleDownload**

Replace the placeholder `handleDownload` in `handler.go`:

```go
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

func (m *DevModule) handleDownload(w http.ResponseWriter, r *http.Request) {
	outDir := filepath.Join(m.repoRoot, "out")
	if _, err := os.Stat(outDir); os.IsNotExist(err) {
		http.Error(w, "out/ directory not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=out.tar.gz")

	gw := gzip.NewWriter(w)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	filepath.Walk(outDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Only include main/ and preload/ subdirectories
		rel, _ := filepath.Rel(outDir, path)
		if !strings.HasPrefix(rel, "main") && !strings.HasPrefix(rel, "preload") && rel != "." {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel
		if err := tw.WriteHeader(header); err != nil {
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
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/module/dev/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/dev/
git commit -m "feat(dev): download endpoint — /api/dev/update/download serves out.tar.gz"
```

---

### Task 4: Register dev module in main.go

**Files:**
- Modify: `cmd/tbox/main.go`

- [ ] **Step 1: Conditionally register dev module**

Add import and registration after existing modules:

```go
import "github.com/wake/tmux-box/internal/module/dev"
```

After `c.AddModule(stream.New())`, add:

```go
if c.Cfg.Dev.Update {
	// repoRoot = directory containing VERSION file (working directory)
	wd, _ := os.Getwd()
	c.AddModule(dev.New(wd))
}
```

- [ ] **Step 2: Verify daemon compiles**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go build ./cmd/tbox/`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add cmd/tbox/main.go
git commit -m "feat(dev): register dev module when [dev] update = true"
```

---

### Task 5: Build hash injection in electron-vite config

**Files:**
- Modify: `electron.vite.config.ts`

- [ ] **Step 1: Add define constants for build hashes**

```typescript
import { defineConfig } from 'electron-vite'
import { resolve } from 'path'
import { execSync } from 'child_process'
import { readFileSync } from 'fs'

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
    build: {
      outDir: resolve(__dirname, 'out/renderer'),
      rollupOptions: {
        input: resolve(__dirname, 'spa/index.html'),
      },
    },
  },
})
```

- [ ] **Step 2: Verify build works**

Run: `npx electron-vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 3: Commit**

```bash
git add electron.vite.config.ts
git commit -m "feat(electron): inject build hash + version via Vite define"
```

---

### Task 6: Electron preload — add update IPC methods

**Files:**
- Modify: `electron/preload.ts`
- Modify: `spa/src/types/electron.d.ts`
- Modify: `spa/src/lib/platform.ts`

- [ ] **Step 1: Add IPC methods to preload.ts**

Add after the existing `getProcessMetrics` / `onMetricsUpdate` block:

```typescript
  // Dev Update
  getAppInfo: () => ipcRenderer.invoke('dev:app-info'),
  checkUpdate: (daemonUrl: string) => ipcRenderer.invoke('dev:check-update', daemonUrl),
  applyUpdate: (daemonUrl: string) => ipcRenderer.invoke('dev:apply-update', daemonUrl),
```

- [ ] **Step 2: Add types to electron.d.ts**

Add the interface for app info and update result:

```typescript
interface ElectronAppInfo {
  version: string
  electronHash: string
  spaHash: string
  devUpdateEnabled: boolean
}

interface ElectronUpdateResult {
  success: boolean
  message: string
}
```

Add to the `electronAPI` property in the Window interface:

```typescript
  getAppInfo: () => Promise<ElectronAppInfo>
  checkUpdate: (daemonUrl: string) => Promise<{ version: string; spaHash: string; electronHash: string }>
  applyUpdate: (daemonUrl: string) => Promise<ElectronUpdateResult>
```

- [ ] **Step 3: Add devUpdateEnabled to PlatformCapabilities**

In `spa/src/lib/platform.ts`, add `devUpdateEnabled` to the interface and return value:

```typescript
export interface PlatformCapabilities {
  isElectron: boolean
  canTearOffTab: boolean
  canMergeWindow: boolean
  canBrowserPane: boolean
  canSystemTray: boolean
  devUpdateEnabled: boolean
}

export function getPlatformCapabilities(): PlatformCapabilities {
  const isElectron = !!window.electronAPI
  const devUpdateEnabled = !!window.electronAPI?.getAppInfo
  return {
    isElectron,
    canTearOffTab: isElectron,
    canMergeWindow: isElectron,
    canBrowserPane: isElectron,
    canSystemTray: isElectron,
    devUpdateEnabled,
  }
}
```

- [ ] **Step 4: Commit**

```bash
git add electron/preload.ts spa/src/types/electron.d.ts spa/src/lib/platform.ts
git commit -m "feat(electron): add dev update IPC — getAppInfo, checkUpdate, applyUpdate"
```

---

### Task 7: Electron main — IPC handlers + startup check

**Files:**
- Modify: `electron/main.ts`
- Create: `electron/updater.ts`

- [ ] **Step 1: Create updater.ts**

Create `electron/updater.ts`:

```typescript
import { app } from 'electron'
import { createWriteStream, mkdirSync, existsSync, rmSync, renameSync } from 'fs'
import { join } from 'path'
import { pipeline } from 'stream/promises'
import { createGunzip } from 'zlib'
import { extract } from 'tar'

declare const __APP_VERSION__: string
declare const __ELECTRON_HASH__: string
declare const __SPA_HASH__: string

export interface AppInfo {
  version: string
  electronHash: string
  spaHash: string
  devUpdateEnabled: boolean
}

export interface RemoteVersionInfo {
  version: string
  spaHash: string
  electronHash: string
}

export function getAppInfo(): AppInfo {
  return {
    version: typeof __APP_VERSION__ !== 'undefined' ? __APP_VERSION__ : 'unknown',
    electronHash: typeof __ELECTRON_HASH__ !== 'undefined' ? __ELECTRON_HASH__ : 'unknown',
    spaHash: typeof __SPA_HASH__ !== 'undefined' ? __SPA_HASH__ : 'unknown',
    devUpdateEnabled: !!process.env.TBOX_DEV_UPDATE,
  }
}

export async function checkUpdate(daemonUrl: string): Promise<RemoteVersionInfo> {
  const resp = await fetch(`${daemonUrl}/api/dev/update/check`)
  if (!resp.ok) throw new Error(`check failed: ${resp.status}`)
  return resp.json()
}

export async function applyUpdate(daemonUrl: string): Promise<{ success: boolean; message: string }> {
  const resp = await fetch(`${daemonUrl}/api/dev/update/download`)
  if (!resp.ok) throw new Error(`download failed: ${resp.status}`)

  const tmpDir = join(app.getPath('temp'), 'tbox-update')
  if (existsSync(tmpDir)) rmSync(tmpDir, { recursive: true })
  mkdirSync(tmpDir, { recursive: true })

  // Save tar.gz to temp file
  const tarPath = join(tmpDir, 'out.tar.gz')
  const fileStream = createWriteStream(tarPath)
  await pipeline(resp.body as any, fileStream)

  // Extract to temp dir
  const extractDir = join(tmpDir, 'extracted')
  mkdirSync(extractDir, { recursive: true })
  await extract({ file: tarPath, cwd: extractDir })

  // Replace out/main and out/preload in app directory
  const appOutDir = join(__dirname, '..')
  const mainDst = join(appOutDir, 'main')
  const preloadDst = join(appOutDir, 'preload')
  const mainSrc = join(extractDir, 'main')
  const preloadSrc = join(extractDir, 'preload')

  if (existsSync(mainSrc)) {
    if (existsSync(mainDst)) rmSync(mainDst, { recursive: true })
    renameSync(mainSrc, mainDst)
  }
  if (existsSync(preloadSrc)) {
    if (existsSync(preloadDst)) rmSync(preloadDst, { recursive: true })
    renameSync(preloadSrc, preloadDst)
  }

  // Cleanup
  rmSync(tmpDir, { recursive: true })

  // Relaunch
  app.relaunch()
  app.exit(0)

  return { success: true, message: 'Update applied, restarting...' }
}
```

- [ ] **Step 2: Add IPC handlers in main.ts**

Add imports at top of `electron/main.ts`:

```typescript
import { getAppInfo, checkUpdate, applyUpdate } from './updater'
```

Add IPC handlers in the `app.whenReady()` block, after existing handlers:

```typescript
// Dev Update IPC
ipcMain.handle('dev:app-info', () => getAppInfo())
ipcMain.handle('dev:check-update', (_event, daemonUrl: string) => checkUpdate(daemonUrl))
ipcMain.handle('dev:apply-update', (_event, daemonUrl: string) => applyUpdate(daemonUrl))
```

- [ ] **Step 3: Add tar to electron package.json dependencies**

```bash
cd /Users/wake/Workspace/wake/tmux-box && pnpm add tar --filter tmux-box-electron
```

Note: If `tar` package has issues in Electron context, we can use Node.js built-in `zlib` + manual tar extraction. Test this during implementation.

- [ ] **Step 4: Verify electron-vite build succeeds**

Run: `npx electron-vite build 2>&1 | tail -5`
Expected: Build succeeds

- [ ] **Step 5: Commit**

```bash
git add electron/updater.ts electron/main.ts electron/package.json pnpm-lock.yaml
git commit -m "feat(electron): updater — download, extract, replace, relaunch"
```

---

### Task 8: i18n keys for Dev Environment section

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: Add en.json keys**

Add to the settings section in `en.json`:

```json
"settings.section.dev_environment": "Development",
"settings.dev.title": "Development Environment",
"settings.dev.desc": "Dev-only update mechanism for cross-machine development",
"settings.dev.app_version": "App Version",
"settings.dev.spa_hash": "SPA Build",
"settings.dev.electron_hash": "Electron Build",
"settings.dev.status.checking": "Checking...",
"settings.dev.status.up_to_date": "Up to date",
"settings.dev.status.update_available": "Update available",
"settings.dev.status.error": "Check failed",
"settings.dev.btn.check": "Check for Updates",
"settings.dev.btn.update_app": "Update App",
"settings.dev.btn.reload_spa": "Reload SPA",
"settings.dev.btn.updating": "Updating..."
```

- [ ] **Step 2: Add zh-TW.json keys**

Add corresponding keys in `zh-TW.json`:

```json
"settings.section.dev_environment": "開發環境",
"settings.dev.title": "開發環境",
"settings.dev.desc": "開發專用的跨機更新機制",
"settings.dev.app_version": "App 版本",
"settings.dev.spa_hash": "SPA Build",
"settings.dev.electron_hash": "Electron Build",
"settings.dev.status.checking": "檢查中...",
"settings.dev.status.up_to_date": "已是最新",
"settings.dev.status.update_available": "有新版本",
"settings.dev.status.error": "檢查失敗",
"settings.dev.btn.check": "檢查更新",
"settings.dev.btn.update_app": "更新 App",
"settings.dev.btn.reload_spa": "重新載入 SPA",
"settings.dev.btn.updating": "更新中..."
```

- [ ] **Step 3: Run locale completeness test**

Run: `cd spa && npx vitest run locale-completeness -v`
Expected: PASS (en/zh-TW keys symmetric)

- [ ] **Step 4: Commit**

```bash
git add spa/src/locales/
git commit -m "feat(i18n): add dev environment settings keys — en + zh-TW"
```

---

### Task 9: DevEnvironmentSection component

**Files:**
- Create: `spa/src/components/settings/DevEnvironmentSection.tsx`
- Test: `spa/src/components/settings/DevEnvironmentSection.test.tsx`

- [ ] **Step 1: Write the test**

Create `spa/src/components/settings/DevEnvironmentSection.test.tsx`:

```tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { DevEnvironmentSection } from './DevEnvironmentSection'

// Mock electronAPI
const mockGetAppInfo = vi.fn().mockResolvedValue({
  version: '1.0.0-alpha.21',
  electronHash: 'abc1234',
  spaHash: 'def5678',
  devUpdateEnabled: true,
})

beforeEach(() => {
  vi.clearAllMocks()
  window.electronAPI = {
    ...window.electronAPI!,
    getAppInfo: mockGetAppInfo,
  } as any
})

describe('DevEnvironmentSection', () => {
  it('renders section title', () => {
    render(<DevEnvironmentSection />)
    // i18n falls back to key if translation not loaded, but title should render
    expect(screen.getByText(/Development|開發環境/)).toBeTruthy()
  })

  it('calls getAppInfo on mount', async () => {
    render(<DevEnvironmentSection />)
    expect(mockGetAppInfo).toHaveBeenCalledOnce()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run DevEnvironmentSection -v`
Expected: FAIL (component doesn't exist)

- [ ] **Step 3: Implement DevEnvironmentSection**

Create `spa/src/components/settings/DevEnvironmentSection.tsx`:

```tsx
import { useState, useEffect, useCallback } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'

type UpdateStatus = 'idle' | 'checking' | 'up_to_date' | 'update_available' | 'error'

interface AppInfo {
  version: string
  electronHash: string
  spaHash: string
}

interface RemoteInfo {
  version: string
  spaHash: string
  electronHash: string
}

export function DevEnvironmentSection() {
  const t = useI18nStore((s) => s.t)
  const getDaemonBase = useHostStore((s) => s.getDaemonBase)
  const daemonBase = getDaemonBase('local')

  const [appInfo, setAppInfo] = useState<AppInfo | null>(null)
  const [remoteInfo, setRemoteInfo] = useState<RemoteInfo | null>(null)
  const [status, setStatus] = useState<UpdateStatus>('idle')
  const [updating, setUpdating] = useState(false)

  // Load local app info on mount
  useEffect(() => {
    window.electronAPI?.getAppInfo().then(setAppInfo)
  }, [])

  const checkUpdate = useCallback(async () => {
    setStatus('checking')
    try {
      const remote = await window.electronAPI!.checkUpdate(daemonBase)
      setRemoteInfo(remote)
      if (remote.electronHash !== appInfo?.electronHash || remote.spaHash !== appInfo?.spaHash) {
        setStatus('update_available')
      } else {
        setStatus('up_to_date')
      }
    } catch {
      setStatus('error')
    }
  }, [daemonBase, appInfo])

  // Auto-check on mount
  useEffect(() => {
    if (appInfo) checkUpdate()
  }, [appInfo, checkUpdate])

  const handleUpdate = async () => {
    setUpdating(true)
    try {
      await window.electronAPI!.applyUpdate(daemonBase)
    } catch {
      setUpdating(false)
    }
  }

  const hasElectronUpdate = remoteInfo && appInfo && remoteInfo.electronHash !== appInfo.electronHash
  const hasSPAUpdate = remoteInfo && appInfo && remoteInfo.spaHash !== appInfo.spaHash

  const statusText = {
    idle: '',
    checking: t('settings.dev.status.checking'),
    up_to_date: t('settings.dev.status.up_to_date'),
    update_available: t('settings.dev.status.update_available'),
    error: t('settings.dev.status.error'),
  }

  return (
    <div className="space-y-6">
      <div>
        <h3 className="text-sm font-medium text-text-primary">{t('settings.dev.title')}</h3>
        <p className="text-xs text-text-muted mt-1">{t('settings.dev.desc')}</p>
      </div>

      {/* Version info */}
      <div className="space-y-3">
        <div className="flex items-center justify-between">
          <span className="text-sm text-text-primary">{t('settings.dev.app_version')}</span>
          <span className="text-xs text-text-muted font-mono">{appInfo?.version ?? '...'}</span>
        </div>
        <div className="flex items-center justify-between">
          <span className="text-sm text-text-primary">{t('settings.dev.spa_hash')}</span>
          <div className="flex items-center gap-2">
            <span className="text-xs text-text-muted font-mono">{appInfo?.spaHash ?? '...'}</span>
            {hasSPAUpdate && <span className="text-xs text-status-warning font-mono">→ {remoteInfo.spaHash}</span>}
          </div>
        </div>
        <div className="flex items-center justify-between">
          <span className="text-sm text-text-primary">{t('settings.dev.electron_hash')}</span>
          <div className="flex items-center gap-2">
            <span className="text-xs text-text-muted font-mono">{appInfo?.electronHash ?? '...'}</span>
            {hasElectronUpdate && <span className="text-xs text-status-warning font-mono">→ {remoteInfo.electronHash}</span>}
          </div>
        </div>
      </div>

      {/* Status */}
      {status !== 'idle' && (
        <div className={`text-xs ${status === 'error' ? 'text-red-400' : status === 'update_available' ? 'text-yellow-400' : 'text-text-muted'}`}>
          {statusText[status]}
        </div>
      )}

      {/* Actions */}
      <div className="flex gap-2">
        <button
          onClick={checkUpdate}
          disabled={status === 'checking'}
          className="px-3 py-1.5 text-xs rounded-md bg-surface-input border border-border-default text-text-primary hover:bg-surface-hover disabled:opacity-50 cursor-pointer disabled:cursor-default"
        >
          {t('settings.dev.btn.check')}
        </button>
        {hasElectronUpdate && (
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
            onClick={() => window.location.reload()}
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

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run DevEnvironmentSection -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/settings/DevEnvironmentSection.tsx spa/src/components/settings/DevEnvironmentSection.test.tsx
git commit -m "feat(settings): DevEnvironmentSection — version info + update controls"
```

---

### Task 10: Register DevEnvironmentSection in settings

**Files:**
- Modify: `spa/src/lib/register-panes.tsx`

- [ ] **Step 1: Add import and registration**

Add import at top:

```typescript
import { DevEnvironmentSection } from '../components/settings/DevEnvironmentSection'
```

Add registration inside `registerBuiltinPanes()`, after the Electron section registration:

```typescript
if (caps.devUpdateEnabled) {
  registerSettingsSection({
    id: 'dev-environment',
    label: 'settings.section.dev_environment',
    order: 20,
    component: DevEnvironmentSection,
  })
}
```

- [ ] **Step 2: Run all tests**

Run: `cd spa && npx vitest run`
Expected: All pass

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/register-panes.tsx
git commit -m "feat(settings): register DevEnvironmentSection when devUpdateEnabled"
```

---

### Task 11: Enable dev update in daemon config

**Files:**
- Modify: `~/.config/tbox/config.toml` (local config, not committed)

- [ ] **Step 1: Add [dev] section to config**

Add to `~/.config/tbox/config.toml`:

```toml
[dev]
update = true
```

- [ ] **Step 2: Restart daemon and verify endpoint**

```bash
# Restart daemon
pkill tbox; sleep 1; bin/tbox &

# Test check endpoint
curl -s http://100.64.0.2:7860/api/dev/update/check | jq
```

Expected: JSON with version, spaHash, electronHash

- [ ] **Step 3: Test download endpoint**

```bash
curl -s http://100.64.0.2:7860/api/dev/update/download -o /tmp/out.tar.gz
tar tzf /tmp/out.tar.gz
```

Expected: Lists `main/index.mjs` and `preload/index.js`

---

### Task 12: End-to-end verification

- [ ] **Step 1: Build Electron and launch**

```bash
npx electron-vite build
TBOX_DEV_UPDATE=1 npx electron out/main/index.mjs
```

- [ ] **Step 2: Open Settings → Development**

Navigate to Settings, verify "Development" section appears with:
- App version number
- SPA hash + remote hash
- Electron hash + remote hash
- Check for Updates button
- Update App button (if update available)

- [ ] **Step 3: Verify startup check works**

Check console/status for auto-check result on app launch.

- [ ] **Step 4: Final commit with .gitignore update**

```bash
echo 'out/' >> .gitignore
git add .gitignore
git commit -m "chore: add out/ to .gitignore"
```
