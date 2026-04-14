# Editor Module Plan B: Daemon FS + 整合

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 Go daemon 端的 FS module，實作 SPA 端的 DaemonBackend，將 FileTreeView 遷移到 FS 抽象層，實現從遠端主機開檔、編輯、存回的完整流程。

**Architecture:** 在 daemon 新增 `internal/module/fs/` 提供 7 個 REST endpoint（全部 POST），SPA 端新增 DaemonBackend 透過 HTTP API 操作。FileTreeView 從直接呼叫 `GET /api/files` 改為使用 FS 抽象層。File tree 點擊檔案透過 file-opener-registry 開啟 editor tab。

**Tech Stack:** Go (net/http), React 19, Zustand 5, Vitest

**Spec:** `docs/superpowers/specs/2026-04-14-editor-module-design.md` Section 3.4

**前置條件:** Plan A 已完成（FS 介面、InAppBackend、file-opener-registry、editor pane、buffer store 就位）

---

## File Structure

### 新增檔案

| 路徑 | 職責 |
|------|------|
| `internal/module/fs/module.go` | Go daemon FS module 定義 + 路由註冊 |
| `internal/module/fs/handler.go` | 7 個 FS API handler |
| `internal/module/fs/handler_test.go` | Handler 單元測試 |
| `spa/src/lib/fs-backend-daemon.ts` | DaemonBackend 實作（HTTP API） |
| `spa/src/lib/fs-backend-daemon.test.ts` | DaemonBackend 測試 |

### 修改檔案

| 路徑 | 修改內容 |
|------|---------|
| `cmd/pdx/main.go` | 替換 `files.New()` 為 `fs.New()` |
| `spa/src/components/FileTreeView.tsx` | 改用 FS 抽象層 DaemonBackend |
| `spa/src/lib/register-modules.tsx` | 註冊 DaemonBackend + file tree 點擊開啟 editor |

---

## Task 1: Go FS Module 骨架

**Files:**
- Create: `internal/module/fs/module.go`

- [ ] **Step 1: 建立 FS module**

```go
// internal/module/fs/module.go
package fs

import (
	"context"
	"log"
	"net/http"

	"github.com/wake/purdex/internal/core"
)

type FsModule struct{}

func New() *FsModule {
	return &FsModule{}
}

func (m *FsModule) Name() string            { return "fs" }
func (m *FsModule) Dependencies() []string  { return nil }
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
```

- [ ] **Step 2: 確認編譯**

Run: `go build ./internal/module/fs/`
Expected: 編譯成功（handler 方法尚未定義會失敗，下一步補）

- [ ] **Step 3: Commit**

```bash
git add internal/module/fs/module.go
git commit -m "feat(daemon): add fs module skeleton"
```

---

## Task 2: Go FS Handler — list + stat

**Files:**
- Create: `internal/module/fs/handler.go`

- [ ] **Step 1: 實作共用的請求解析和 list + stat handler**

```go
// internal/module/fs/handler.go
package fs

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// --- Request/Response types ---

type pathRequest struct {
	Path string `json:"path"`
}

type fileEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

type listResponse struct {
	Path    string      `json:"path"`
	Entries []fileEntry `json:"entries"`
}

type statResponse struct {
	Size        int64 `json:"size"`
	Mtime       int64 `json:"mtime"`
	IsDirectory bool  `json:"isDirectory"`
	IsFile      bool  `json:"isFile"`
}

// --- Helpers ---

func decodePath(r *http.Request) (string, error) {
	var req pathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", err
	}
	return req.Path, nil
}

func validatePath(path string) bool {
	cleaned := filepath.Clean(path)
	return filepath.IsAbs(cleaned) && cleaned == filepath.Clean(path)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func jsonOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

// --- Handlers ---

func (m *FsModule) handleList(w http.ResponseWriter, r *http.Request) {
	path, err := decodePath(r)
	if err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validatePath(path) {
		jsonError(w, "invalid path: must be absolute", http.StatusBadRequest)
		return
	}

	dirEntries, err := os.ReadDir(path)
	if err != nil {
		jsonError(w, "cannot read directory", http.StatusNotFound)
		return
	}

	entries := make([]fileEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		if strings.HasPrefix(de.Name(), ".") {
			continue // skip hidden
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		entries = append(entries, fileEntry{
			Name:  de.Name(),
			IsDir: de.IsDir(),
			Size:  info.Size(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})

	jsonOK(w, listResponse{Path: path, Entries: entries})
}

func (m *FsModule) handleStat(w http.ResponseWriter, r *http.Request) {
	path, err := decodePath(r)
	if err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validatePath(path) {
		jsonError(w, "invalid path: must be absolute", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}

	jsonOK(w, statResponse{
		Size:        info.Size(),
		Mtime:       info.ModTime().UnixMilli(),
		IsDirectory: info.IsDir(),
		IsFile:      !info.IsDir(),
	})
}
```

- [ ] **Step 2: 確認編譯**

Run: `go build ./internal/module/fs/`
Expected: 失敗（read/write/mkdir/delete/rename 未定義）— 先加 stub

- [ ] **Step 3: 補上剩餘 handler stub**

在 `handler.go` 底部新增：

```go
func (m *FsModule) handleRead(w http.ResponseWriter, r *http.Request) {
	path, err := decodePath(r)
	if err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validatePath(path) {
		jsonError(w, "invalid path: must be absolute", http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		jsonError(w, "cannot read file", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

func (m *FsModule) handleWrite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"` // base64 encoded
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validatePath(req.Path) {
		jsonError(w, "invalid path: must be absolute", http.StatusBadRequest)
		return
	}

	// Ensure parent directory exists
	dir := filepath.Dir(req.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		jsonError(w, "cannot create directory", http.StatusInternalServerError)
		return
	}

	decoded, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		jsonError(w, "invalid content encoding", http.StatusBadRequest)
		return
	}

	if err := os.WriteFile(req.Path, decoded, 0o644); err != nil {
		jsonError(w, "cannot write file", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (m *FsModule) handleMkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validatePath(req.Path) {
		jsonError(w, "invalid path: must be absolute", http.StatusBadRequest)
		return
	}

	var err error
	if req.Recursive {
		err = os.MkdirAll(req.Path, 0o755)
	} else {
		err = os.Mkdir(req.Path, 0o755)
	}
	if err != nil {
		jsonError(w, "cannot create directory", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (m *FsModule) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validatePath(req.Path) {
		jsonError(w, "invalid path: must be absolute", http.StatusBadRequest)
		return
	}

	var err error
	if req.Recursive {
		err = os.RemoveAll(req.Path)
	} else {
		err = os.Remove(req.Path)
	}
	if err != nil {
		jsonError(w, "cannot delete", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (m *FsModule) handleRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !validatePath(req.From) || !validatePath(req.To) {
		jsonError(w, "invalid path: must be absolute", http.StatusBadRequest)
		return
	}

	if err := os.Rename(req.From, req.To); err != nil {
		jsonError(w, "cannot rename", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
```

注意：`handleWrite` 的 import 需要放到檔案頂部。重新整理 import：

```go
import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)
```

並移除 `handleWrite` 裡多餘的 `content, err := io.ReadAll(r.Body)` 和 inline import。

- [ ] **Step 4: 確認編譯**

Run: `go build ./internal/module/fs/`
Expected: 成功

- [ ] **Step 5: Commit**

```bash
git add internal/module/fs/handler.go
git commit -m "feat(daemon): implement fs module handlers (list/read/write/stat/mkdir/delete/rename)"
```

---

## Task 3: Go FS Handler 測試

**Files:**
- Create: `internal/module/fs/handler_test.go`

- [ ] **Step 1: 寫 handler 測試**

```go
// internal/module/fs/handler_test.go
package fs

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func setupTestModule() (*FsModule, string) {
	dir, _ := os.MkdirTemp("", "fs-test-*")
	return New(), dir
}

func postJSON(handler http.HandlerFunc, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)
	return w
}

func TestHandleList(t *testing.T) {
	m, dir := setupTestModule()
	defer os.RemoveAll(dir)

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)

	w := postJSON(m.handleList, pathRequest{Path: dir})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp listResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}
	// Directories first
	if !resp.Entries[0].IsDir || resp.Entries[0].Name != "sub" {
		t.Errorf("first entry should be dir 'sub', got %+v", resp.Entries[0])
	}
	if resp.Entries[1].IsDir || resp.Entries[1].Name != "a.txt" {
		t.Errorf("second entry should be file 'a.txt', got %+v", resp.Entries[1])
	}
}

func TestHandleListRejectsRelativePath(t *testing.T) {
	m := New()
	w := postJSON(m.handleList, pathRequest{Path: "relative/path"})
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleStat(t *testing.T) {
	m, dir := setupTestModule()
	defer os.RemoveAll(dir)

	fpath := filepath.Join(dir, "test.txt")
	os.WriteFile(fpath, []byte("hello"), 0o644)

	w := postJSON(m.handleStat, pathRequest{Path: fpath})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp statResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Size != 5 {
		t.Errorf("expected size 5, got %d", resp.Size)
	}
	if !resp.IsFile || resp.IsDirectory {
		t.Error("expected file, not directory")
	}
}

func TestHandleReadWrite(t *testing.T) {
	m, dir := setupTestModule()
	defer os.RemoveAll(dir)

	fpath := filepath.Join(dir, "rw.txt")
	content := "hello world"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	// Write
	ww := postJSON(m.handleWrite, struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}{Path: fpath, Content: encoded})
	if ww.Code != 204 {
		t.Fatalf("write: expected 204, got %d: %s", ww.Code, ww.Body.String())
	}

	// Read
	wr := postJSON(m.handleRead, pathRequest{Path: fpath})
	if wr.Code != 200 {
		t.Fatalf("read: expected 200, got %d", wr.Code)
	}
	if wr.Body.String() != content {
		t.Errorf("read: expected %q, got %q", content, wr.Body.String())
	}
}

func TestHandleDelete(t *testing.T) {
	m, dir := setupTestModule()
	defer os.RemoveAll(dir)

	fpath := filepath.Join(dir, "del.txt")
	os.WriteFile(fpath, []byte("x"), 0o644)

	w := postJSON(m.handleDelete, struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}{Path: fpath, Recursive: false})
	if w.Code != 204 {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	if _, err := os.Stat(fpath); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestHandleRename(t *testing.T) {
	m, dir := setupTestModule()
	defer os.RemoveAll(dir)

	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	os.WriteFile(oldPath, []byte("content"), 0o644)

	w := postJSON(m.handleRename, struct {
		From string `json:"from"`
		To   string `json:"to"`
	}{From: oldPath, To: newPath})
	if w.Code != 204 {
		t.Fatalf("expected 204, got %d", w.Code)
	}

	data, err := os.ReadFile(newPath)
	if err != nil || string(data) != "content" {
		t.Error("renamed file should exist with content")
	}
}
```

- [ ] **Step 2: 確認測試通過**

Run: `go test ./internal/module/fs/ -v`
Expected: 所有測試 PASS

- [ ] **Step 3: Commit**

```bash
git add internal/module/fs/handler_test.go
git commit -m "test(daemon): add fs module handler tests"
```

---

## Task 4: SPA DaemonBackend

**Files:**
- Create: `spa/src/lib/fs-backend-daemon.ts`
- Test: `spa/src/lib/fs-backend-daemon.test.ts`

- [ ] **Step 1: 寫失敗測試**

```typescript
// spa/src/lib/fs-backend-daemon.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { DaemonBackend } from './fs-backend-daemon'

const mockFetch = vi.fn()
global.fetch = mockFetch

describe('DaemonBackend', () => {
  let backend: DaemonBackend

  beforeEach(() => {
    vi.clearAllMocks()
    backend = new DaemonBackend('http://localhost:7860', () => ({ Authorization: 'Bearer test-token' }))
  })

  it('list sends POST to /api/fs/list', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ path: '/home', entries: [{ name: 'a.txt', isDir: false, size: 10 }] }),
    })

    const entries = await backend.list('/home')
    expect(mockFetch).toHaveBeenCalledWith(
      'http://localhost:7860/api/fs/list',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ path: '/home' }),
      }),
    )
    expect(entries).toEqual([{ name: 'a.txt', isDir: false, size: 10 }])
  })

  it('read returns Uint8Array from response', async () => {
    const content = new TextEncoder().encode('hello')
    mockFetch.mockResolvedValueOnce({
      ok: true,
      arrayBuffer: async () => content.buffer,
    })

    const result = await backend.read('/test.txt')
    expect(new TextDecoder().decode(result)).toBe('hello')
  })

  it('stat returns FileStat from response', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ size: 100, mtime: 1234567890, isDirectory: false, isFile: true }),
    })

    const stat = await backend.stat('/test.txt')
    expect(stat.size).toBe(100)
    expect(stat.isFile).toBe(true)
  })

  it('write sends base64 encoded content', async () => {
    mockFetch.mockResolvedValueOnce({ ok: true })

    const content = new TextEncoder().encode('hello')
    await backend.write('/test.txt', content)

    const call = mockFetch.mock.calls[0]
    const body = JSON.parse(call[1].body)
    expect(body.path).toBe('/test.txt')
    expect(body.content).toBeTruthy() // base64 string
  })

  it('throws on non-ok response', async () => {
    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 404,
      text: async () => 'not found',
    })

    await expect(backend.read('/no-such-file')).rejects.toThrow()
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && npx vitest run src/lib/fs-backend-daemon.test.ts`
Expected: FAIL

- [ ] **Step 3: 實作 DaemonBackend**

```typescript
// spa/src/lib/fs-backend-daemon.ts
import type { FsBackend } from './fs-backend'
import type { FileStat, FileEntry } from '../types/fs'

export class DaemonBackend implements FsBackend {
  readonly id = 'daemon'
  readonly label = 'Remote Host'

  constructor(
    private baseUrl: string,
    private getHeaders: () => Record<string, string>,
  ) {}

  available(): boolean {
    return !!this.baseUrl
  }

  private async post(endpoint: string, body: unknown): Promise<Response> {
    const res = await fetch(`${this.baseUrl}${endpoint}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...this.getHeaders() },
      body: JSON.stringify(body),
    })
    if (!res.ok) {
      const text = await res.text().catch(() => `HTTP ${res.status}`)
      throw new Error(text)
    }
    return res
  }

  async read(path: string): Promise<Uint8Array> {
    const res = await this.post('/api/fs/read', { path })
    const buf = await res.arrayBuffer()
    return new Uint8Array(buf)
  }

  async write(path: string, content: Uint8Array): Promise<void> {
    // chunked btoa to avoid stack overflow on large files
    let binary = ''
    const chunkSize = 8192
    for (let i = 0; i < content.length; i += chunkSize) {
      binary += String.fromCharCode(...content.subarray(i, i + chunkSize))
    }
    const base64 = btoa(binary)
    await this.post('/api/fs/write', { path, content: base64 })
  }

  async stat(path: string): Promise<FileStat> {
    const res = await this.post('/api/fs/stat', { path })
    return res.json()
  }

  async list(path: string): Promise<FileEntry[]> {
    const res = await this.post('/api/fs/list', { path })
    const data = await res.json()
    return data.entries
  }

  async mkdir(path: string, recursive?: boolean): Promise<void> {
    await this.post('/api/fs/mkdir', { path, recursive: recursive ?? false })
  }

  async delete(path: string, recursive?: boolean): Promise<void> {
    await this.post('/api/fs/delete', { path, recursive: recursive ?? false })
  }

  async rename(from: string, to: string): Promise<void> {
    await this.post('/api/fs/rename', { from, to })
  }
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && npx vitest run src/lib/fs-backend-daemon.test.ts`
Expected: 5 tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/fs-backend-daemon.ts spa/src/lib/fs-backend-daemon.test.ts
git commit -m "feat(editor): add DaemonBackend for remote filesystem access"
```

---

## Task 5: 註冊 DaemonBackend + FileTreeView 遷移

**Files:**
- Modify: `spa/src/lib/register-modules.tsx`
- Modify: `spa/src/components/FileTreeView.tsx`

- [ ] **Step 1: 在 register-modules.tsx 註冊 DaemonBackend**

新增 DaemonBackend 的動態註冊。由於 DaemonBackend 需要 hostId 對應的 baseUrl 和 authHeaders，在 host 連線時動態建立：

```typescript
import { DaemonBackend } from '../lib/fs-backend-daemon'
import { registerFsBackend } from '../lib/fs-backend'
import { useHostStore } from '../stores/useHostStore'

// 在 registerBuiltinModules() 中新增：
// DaemonBackend 動態註冊（依賴 active host）
// 這裡先註冊一個 lazy proxy，實際 backend 在使用時根據 hostId 建立
registerFsBackend('daemon', {
  id: 'daemon',
  label: 'Remote Host',
  available: () => !!useHostStore.getState().activeHostId,
  read: (path) => getDaemonBackend().read(path),
  write: (path, content) => getDaemonBackend().write(path, content),
  stat: (path) => getDaemonBackend().stat(path),
  list: (path) => getDaemonBackend().list(path),
  mkdir: (path, recursive) => getDaemonBackend().mkdir(path, recursive),
  delete: (path, recursive) => getDaemonBackend().delete(path, recursive),
  rename: (from, to) => getDaemonBackend().rename(from, to),
})

function getDaemonBackend(): DaemonBackend {
  const state = useHostStore.getState()
  const hostId = state.activeHostId ?? state.hostOrder[0] ?? ''
  return new DaemonBackend(
    state.getDaemonBase(hostId),
    () => state.getAuthHeaders(hostId),
  )
}
```

- [ ] **Step 2: 改寫 FileTreeView 使用 FS 抽象層**

在 `FileTreeView.tsx` 中，替換直接 fetch 呼叫為使用 `getFsBackend`：

```typescript
// Before (line 34-39)
const fetchDir = useCallback(async (path: string) => {
  const url = `${baseUrl}/api/files?path=${encodeURIComponent(path)}`
  const authHeaders = useHostStore.getState().getAuthHeaders(activeHostId)
  const res = await fetch(url, { headers: authHeaders })
  if (!res.ok) throw new Error(`Failed to fetch: ${res.statusText}`)
  return res.json()
}, [baseUrl, activeHostId])

// After
import { getFsBackend } from '../lib/fs-backend'
import type { FileSource } from '../types/fs'

const source: FileSource = { type: 'daemon', hostId: activeHostId }

const fetchDir = useCallback(async (path: string) => {
  const backend = getFsBackend(source)
  if (!backend) throw new Error('No FS backend available')
  const entries = await backend.list(path)
  return { path, entries }
}, [source])
```

同時，在檔案點擊時透過 file-opener-registry 開啟 editor：

```typescript
import { getDefaultOpener } from '../lib/file-opener-registry'
import type { FileInfo } from '../types/fs'

// 在 file entry 的 onClick 中（非目錄時）：
onClick={() => {
  if (entry.isDir) {
    toggleDir(fullPath)
  } else {
    const fileInfo: FileInfo = {
      name: entry.name,
      path: fullPath,
      extension: entry.name.split('.').pop() ?? '',
      size: entry.size,
      isDirectory: false,
    }
    const opener = getDefaultOpener(fileInfo)
    if (opener) {
      const content = opener.createContent(source, fileInfo)
      // 建立 tab（需要 import tab store）
      const tab = createTab(content)
      useTabStore.getState().addTab(tab)
      useTabStore.getState().setActiveTab(tab.id)
      // 加入 workspace
      const ws = useWorkspaceStore.getState().findWorkspaceByTab(/* ... */)
      // ... workspace 插入邏輯
    }
  }
}}
```

- [ ] **Step 3: 確認 TypeScript 編譯通過**

Run: `cd spa && npx tsc --noEmit --pretty`
Expected: 無錯誤

- [ ] **Step 4: 確認全部測試通過**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/register-modules.tsx spa/src/components/FileTreeView.tsx
git commit -m "feat(editor): integrate DaemonBackend and file tree → editor opening"
```

---

## Task 6: 替換 daemon files module

> **注意：必須在 Task 5（FileTreeView 遷移）之後執行，否則 SPA 端的 file tree 會因舊 `GET /api/files` endpoint 被移除而壞掉。**

**Files:**
- Modify: `cmd/pdx/main.go`

- [ ] **Step 1: 替換 import 和 AddModule**

在 `cmd/pdx/main.go` 中：

```go
// Before
import "github.com/wake/purdex/internal/module/files"
// ...
c.AddModule(files.New())

// After
import fsmod "github.com/wake/purdex/internal/module/fs"
// ...
c.AddModule(fsmod.New())
```

- [ ] **Step 2: 確認編譯**

Run: `go build ./cmd/pdx/`
Expected: 成功

- [ ] **Step 3: Commit**

```bash
git add cmd/pdx/main.go
git commit -m "refactor(daemon): replace files module with fs module"
```

---

## Task 7: 端到端驗證

**Files:** 無新增

- [ ] **Step 1: 啟動 daemon**

Run: `bin/pdx serve`（或你的標準啟動方式）
確認 log 中出現 `[fs] endpoints enabled`

- [ ] **Step 2: 用 curl 測試 FS API**

```bash
# List
curl -X POST http://100.64.0.2:7860/api/fs/list \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"path":"/Users/wake"}'

# Stat
curl -X POST http://100.64.0.2:7860/api/fs/stat \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"path":"/Users/wake/.zshrc"}'
```

Expected: JSON 回應正常

- [ ] **Step 3: 在瀏覽器中驗證**

1. 開啟 SPA → 設定 workspace 的 projectPath
2. File tree 應正常顯示（使用新的 `POST /api/fs/list`）
3. 點擊檔案 → 應開啟 editor tab
4. 編輯 → ⌘S 存檔 → 重新開啟確認內容一致

- [ ] **Step 4: Lint**

Run: `cd spa && pnpm run lint`
Expected: 無錯誤

- [ ] **Step 5: Commit（如有修正）**

```bash
git add -A
git commit -m "fix(editor): address issues from daemon FS integration testing"
```
