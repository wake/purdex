# Agent File Upload Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enable drag-and-drop file upload from SPA terminal view to CC agent via daemon upload + tmux send-keys injection.

**Architecture:** SPA TerminalView intercepts drag-drop when agent is active, uploads each file sequentially to daemon `POST /api/agent/upload`, daemon saves to `~/tmp/tbox-upload/{session}/` and injects the path into the tmux pane via `send-keys -l`. StatusBar shows per-session upload progress via a lightweight Zustand store.

**Tech Stack:** Go (daemon handler, multipart), React (TerminalView drag-drop, StatusBar), Zustand (upload state), Vitest (tests)

---

## File Structure

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `internal/module/agent/upload.go` | handleUpload handler + deduplicateFilename helper |
| Create | `internal/module/agent/upload_test.go` | Unit tests for upload handler |
| Modify | `internal/module/agent/module.go` | Register upload route + store tmux executor ref |
| Create | `spa/src/stores/useUploadStore.ts` | Per-session upload state (uploading/done/error) |
| Create | `spa/src/stores/useUploadStore.test.ts` | Store unit tests |
| Modify | `spa/src/lib/api.ts` | Add `agentUpload()` function |
| Modify | `spa/src/lib/api.test.ts` | Test for agentUpload |
| Modify | `spa/src/components/TerminalView.tsx` | Drag-drop handling + drop overlay |
| Modify | `spa/src/components/TerminalView.test.tsx` | Drag-drop tests |
| Modify | `spa/src/components/StatusBar.tsx` | Upload progress display + agent label badge |
| Modify | `spa/src/components/StatusBar.test.tsx` | Upload progress + badge tests |

---

### Task 1: Daemon — deduplicateFilename helper

**Files:**
- Create: `internal/module/agent/upload.go`
- Create: `internal/module/agent/upload_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/module/agent/upload_test.go
package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeduplicateFilename(t *testing.T) {
	dir := t.TempDir()

	// No conflict — returns original name.
	got := deduplicateFilename(dir, "photo.png")
	assert.Equal(t, "photo.png", got)

	// Create file to trigger conflict.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "photo.png"), []byte("x"), 0644))
	got = deduplicateFilename(dir, "photo.png")
	assert.Equal(t, "photo-1.png", got)

	// Second conflict.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "photo-1.png"), []byte("x"), 0644))
	got = deduplicateFilename(dir, "photo.png")
	assert.Equal(t, "photo-2.png", got)

	// No extension.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0644))
	got = deduplicateFilename(dir, "README")
	assert.Equal(t, "README-1", got)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/agent/ -run TestDeduplicateFilename -v`
Expected: FAIL — `deduplicateFilename` undefined

- [ ] **Step 3: Write minimal implementation**

```go
// internal/module/agent/upload.go
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// deduplicateFilename returns a filename that does not conflict with
// existing files in dir. If "photo.png" exists it tries "photo-1.png",
// "photo-2.png", etc.
func deduplicateFilename(dir, name string) string {
	if _, err := os.Stat(filepath.Join(dir, name)); os.IsNotExist(err) {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if _, err := os.Stat(filepath.Join(dir, candidate)); os.IsNotExist(err) {
			return candidate
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/agent/ -run TestDeduplicateFilename -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/module/agent/upload.go internal/module/agent/upload_test.go
git commit -m "feat(agent): add deduplicateFilename helper for upload"
```

---

### Task 2: Daemon — handleUpload endpoint

**Files:**
- Modify: `internal/module/agent/upload.go`
- Modify: `internal/module/agent/module.go`
- Modify: `internal/module/agent/upload_test.go`

- [ ] **Step 1: Write the failing test**

Append to `upload_test.go`:

```go
import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"

	"github.com/wake/tmux-box/internal/core"
	"github.com/wake/tmux-box/internal/tmux"
)

func newTestModule(t *testing.T) (*Module, *tmux.FakeExecutor) {
	t.Helper()
	fake := tmux.NewFakeExecutor()
	fake.AddSession("my-sess", "/tmp")

	// Minimal core with tmux executor.
	c := &core.Core{Tmux: fake}

	m := &Module{core: c}
	// Override upload dir to temp.
	m.uploadDir = t.TempDir()
	return m, fake
}

func TestHandleUpload_Success(t *testing.T) {
	m, fake := newTestModule(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("session", "my-sess")
	fw, _ := w.CreateFormFile("file", "test.png")
	fw.Write([]byte("fake image data"))
	w.Close()

	req := httptest.NewRequest("POST", "/api/agent/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	m.handleUpload(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "test.png", resp["filename"])
	assert.Equal(t, true, resp["injected"])

	// File should exist on disk.
	_, err := os.Stat(filepath.Join(m.uploadDir, "my-sess", "test.png"))
	assert.NoError(t, err)

	// send-keys should have been called with space prefix + literal flag.
	calls := fake.GetRawKeysCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "my-sess", calls[0].Target)
	assert.Contains(t, calls[0].Keys, "-l")
}

func TestHandleUpload_MissingSession(t *testing.T) {
	m, _ := newTestModule(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "test.png")
	fw.Write([]byte("data"))
	w.Close()

	req := httptest.NewRequest("POST", "/api/agent/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	m.handleUpload(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleUpload_SessionNotFound(t *testing.T) {
	m, _ := newTestModule(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("session", "nonexistent")
	fw, _ := w.CreateFormFile("file", "test.png")
	fw.Write([]byte("data"))
	w.Close()

	req := httptest.NewRequest("POST", "/api/agent/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()

	m.handleUpload(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/agent/ -run TestHandleUpload -v`
Expected: FAIL — `handleUpload` undefined, `uploadDir` field missing

- [ ] **Step 3: Add uploadDir field to Module**

In `internal/module/agent/module.go`, add `uploadDir` field and initialize in `Init()`:

```go
// Module is the agent hook event module.
type Module struct {
	core      *core.Core
	events    *store.AgentEventStore
	sessions  session.SessionProvider
	uploadDir string // base directory for uploaded files
}
```

In `Init()`, after existing code:

```go
// Default upload directory.
if m.uploadDir == "" {
	home, _ := os.UserHomeDir()
	m.uploadDir = filepath.Join(home, "tmp", "tbox-upload")
}
```

Add `"os"` and `"path/filepath"` imports to module.go.

Register the route in `RegisterRoutes()`:

```go
mux.HandleFunc("POST /api/agent/upload", m.handleUpload)
```

- [ ] **Step 4: Write handleUpload handler**

Append to `internal/module/agent/upload.go`:

```go
import (
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// handleUpload handles POST /api/agent/upload.
// It saves the uploaded file and injects the path into the tmux pane.
func (m *Module) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(256 << 20); err != nil {
		http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
		return
	}

	sessionCode := r.FormValue("session")
	if sessionCode == "" {
		http.Error(w, `{"error":"missing session"}`, http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"missing file"}`, http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Resolve session code to tmux session name.
	tmuxName := m.resolveSessionName(sessionCode)
	if tmuxName == "" {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	// Ensure upload directory exists.
	dir := filepath.Join(m.uploadDir, sessionCode)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[agent] mkdir upload dir: %v", err)
		http.Error(w, `{"error":"cannot create upload directory"}`, http.StatusInternalServerError)
		return
	}

	// Save file with dedup.
	filename := deduplicateFilename(dir, header.Filename)
	destPath := filepath.Join(dir, filename)
	dst, err := os.Create(destPath)
	if err != nil {
		log.Printf("[agent] create file: %v", err)
		http.Error(w, `{"error":"cannot save file"}`, http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		log.Printf("[agent] write file: %v", err)
		http.Error(w, `{"error":"write failed"}`, http.StatusInternalServerError)
		return
	}

	// Inject path into tmux pane via send-keys (space prefix, literal mode).
	if err := m.core.Tmux.SendKeysRaw(tmuxName, "-l", " "+destPath); err != nil {
		log.Printf("[agent] send-keys: %v", err)
		http.Error(w, `{"error":"inject failed"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"filename": filename,
		"injected": true,
	})
}

// resolveSessionName maps a tbox session code to the tmux session name.
func (m *Module) resolveSessionName(code string) string {
	if m.sessions == nil {
		return ""
	}
	info, err := m.sessions.GetSession(code)
	if err != nil || info == nil {
		return ""
	}
	return info.Name
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/agent/ -run TestHandleUpload -v`
Expected: PASS (all 3 test cases)

- [ ] **Step 6: Run full agent tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/agent/ -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/module/agent/upload.go internal/module/agent/upload_test.go internal/module/agent/module.go
git commit -m "feat(agent): add POST /api/agent/upload endpoint with send-keys injection"
```

---

### Task 3: SPA — useUploadStore

**Files:**
- Create: `spa/src/stores/useUploadStore.ts`
- Create: `spa/src/stores/useUploadStore.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// spa/src/stores/useUploadStore.test.ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useUploadStore } from './useUploadStore'

beforeEach(() => {
  useUploadStore.setState({ sessions: {} })
})

describe('useUploadStore', () => {
  it('startUpload initialises session state', () => {
    useUploadStore.getState().startUpload('dev', 3, 'a.png')
    const s = useUploadStore.getState().sessions['dev']
    expect(s.status).toBe('uploading')
    expect(s.total).toBe(3)
    expect(s.completed).toBe(0)
    expect(s.failed).toBe(0)
    expect(s.currentFile).toBe('a.png')
  })

  it('fileCompleted increments count and sets done when all complete', () => {
    useUploadStore.getState().startUpload('dev', 2, 'a.png')
    useUploadStore.getState().fileCompleted('dev')
    expect(useUploadStore.getState().sessions['dev'].completed).toBe(1)
    expect(useUploadStore.getState().sessions['dev'].status).toBe('uploading')

    useUploadStore.getState().fileCompleted('dev')
    expect(useUploadStore.getState().sessions['dev'].completed).toBe(2)
    expect(useUploadStore.getState().sessions['dev'].status).toBe('done')
  })

  it('fileFailed increments failed count and sets error status when all done', () => {
    useUploadStore.getState().startUpload('dev', 1, 'a.png')
    useUploadStore.getState().fileFailed('dev', 'a.png')
    const s = useUploadStore.getState().sessions['dev']
    expect(s.failed).toBe(1)
    expect(s.error).toBe('a.png')
    expect(s.status).toBe('error')
  })

  it('partial success: some completed some failed', () => {
    useUploadStore.getState().startUpload('dev', 3, 'a.png')
    useUploadStore.getState().fileCompleted('dev')
    useUploadStore.getState().fileFailed('dev', 'b.png')
    useUploadStore.getState().fileCompleted('dev')
    const s = useUploadStore.getState().sessions['dev']
    expect(s.completed).toBe(2)
    expect(s.failed).toBe(1)
    expect(s.status).toBe('error')
  })

  it('nextFile updates currentFile', () => {
    useUploadStore.getState().startUpload('dev', 2, 'a.png')
    useUploadStore.getState().nextFile('dev', 'b.png')
    expect(useUploadStore.getState().sessions['dev'].currentFile).toBe('b.png')
  })

  it('dismiss clears session state', () => {
    useUploadStore.getState().startUpload('dev', 1, 'a.png')
    useUploadStore.getState().dismiss('dev')
    expect(useUploadStore.getState().sessions['dev']).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/stores/useUploadStore.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Write the store**

```typescript
// spa/src/stores/useUploadStore.ts
import { create } from 'zustand'

interface SessionUploadState {
  total: number
  completed: number
  failed: number
  currentFile: string
  error?: string
  status: 'uploading' | 'done' | 'error'
}

interface UploadState {
  sessions: Record<string, SessionUploadState>
  startUpload: (session: string, total: number, firstFile: string) => void
  fileCompleted: (session: string) => void
  fileFailed: (session: string, filename: string) => void
  nextFile: (session: string, filename: string) => void
  dismiss: (session: string) => void
}

export const useUploadStore = create<UploadState>((set) => ({
  sessions: {},

  startUpload: (session, total, firstFile) =>
    set((s) => ({
      sessions: {
        ...s.sessions,
        [session]: { total, completed: 0, failed: 0, currentFile: firstFile, status: 'uploading' },
      },
    })),

  fileCompleted: (session) =>
    set((s) => {
      const prev = s.sessions[session]
      if (!prev) return s
      const completed = prev.completed + 1
      const allDone = completed + prev.failed >= prev.total
      return {
        sessions: {
          ...s.sessions,
          [session]: {
            ...prev,
            completed,
            status: allDone ? (prev.failed > 0 ? 'error' : 'done') : 'uploading',
          },
        },
      }
    }),

  fileFailed: (session, filename) =>
    set((s) => {
      const prev = s.sessions[session]
      if (!prev) return s
      const failed = prev.failed + 1
      const allDone = prev.completed + failed >= prev.total
      return {
        sessions: {
          ...s.sessions,
          [session]: {
            ...prev,
            failed,
            error: filename,
            status: allDone ? 'error' : 'uploading',
          },
        },
      }
    }),

  nextFile: (session, filename) =>
    set((s) => {
      const prev = s.sessions[session]
      if (!prev) return s
      return {
        sessions: { ...s.sessions, [session]: { ...prev, currentFile: filename } },
      }
    }),

  dismiss: (session) =>
    set((s) => {
      const { [session]: _, ...rest } = s.sessions
      return { sessions: rest }
    }),
}))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/stores/useUploadStore.test.ts`
Expected: PASS (all 6 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useUploadStore.ts spa/src/stores/useUploadStore.test.ts
git commit -m "feat(spa): add useUploadStore for per-session upload state"
```

---

### Task 4: SPA — agentUpload API function

**Files:**
- Modify: `spa/src/lib/api.ts`
- Modify: `spa/src/lib/api.test.ts`

- [ ] **Step 1: Write the failing test**

Append to `spa/src/lib/api.test.ts`:

```typescript
describe('agentUpload', () => {
  it('sends multipart form and returns result', async () => {
    const mockResponse = { filename: 'test.png', injected: true }
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve(mockResponse),
    })

    const { agentUpload } = await import('./api')
    const file = new File(['data'], 'test.png', { type: 'image/png' })
    const result = await agentUpload('http://localhost:7860', file, 'dev001')

    expect(result).toEqual(mockResponse)
    expect(globalThis.fetch).toHaveBeenCalledWith(
      'http://localhost:7860/api/agent/upload',
      expect.objectContaining({ method: 'POST' }),
    )

    // Verify FormData contents.
    const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0]
    const body = call[1].body as FormData
    expect(body.get('session')).toBe('dev001')
    expect(body.get('file')).toBeInstanceOf(File)
  })

  it('throws on non-ok response', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 404,
      statusText: 'Not Found',
    })

    const { agentUpload } = await import('./api')
    const file = new File(['data'], 'test.png')
    await expect(agentUpload('http://localhost:7860', file, 'dev001')).rejects.toThrow('404')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/lib/api.test.ts`
Expected: FAIL — `agentUpload` not exported

- [ ] **Step 3: Add agentUpload to api.ts**

Append to `spa/src/lib/api.ts`:

```typescript
// --- Agent Upload API ---

export async function agentUpload(
  base: string,
  file: File,
  session: string,
): Promise<{ filename: string; injected: boolean }> {
  const form = new FormData()
  form.append('file', file)
  form.append('session', session)
  const res = await fetch(`${base}/api/agent/upload`, {
    method: 'POST',
    body: form,
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/lib/api.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/api.ts spa/src/lib/api.test.ts
git commit -m "feat(spa): add agentUpload API function"
```

---

### Task 5: SPA — TerminalView drag-drop + drop overlay

**Files:**
- Modify: `spa/src/components/TerminalView.tsx`
- Modify: `spa/src/components/TerminalView.test.tsx`

- [ ] **Step 1: Write the failing test**

Append to `spa/src/components/TerminalView.test.tsx` (check existing test patterns first — use the same mocking approach):

```typescript
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { useHostStore } from '../stores/useHostStore'

describe('TerminalView drag-drop', () => {
  beforeEach(() => {
    useAgentStore.setState({ statuses: { dev001: 'idle' }, events: {}, unread: {}, activeSubagents: {}, hooksInstalled: false })
    useUploadStore.setState({ sessions: {} })
    useHostStore.setState({
      hosts: { local: { id: 'local', name: 'mlab', address: '100.64.0.2', port: 7860, status: 'connected' as const } },
      defaultHost: { id: 'local', name: 'mlab', address: '100.64.0.2', port: 7860, status: 'connected' as const },
    })
  })

  it('shows drop overlay on drag-enter when agent is active', () => {
    const { container } = render(<TerminalView wsUrl="ws://localhost/ws/terminal/dev001" sessionCode="dev001" />)
    const dropZone = container.firstChild as HTMLElement

    fireEvent.dragEnter(dropZone, { dataTransfer: { types: ['Files'] } })
    expect(screen.getByTestId('drop-overlay')).toBeTruthy()
  })

  it('does not show drop overlay when agent is not active', () => {
    useAgentStore.setState({ statuses: {} })
    const { container } = render(<TerminalView wsUrl="ws://localhost/ws/terminal/dev001" sessionCode="dev001" />)
    const dropZone = container.firstChild as HTMLElement

    fireEvent.dragEnter(dropZone, { dataTransfer: { types: ['Files'] } })
    expect(screen.queryByTestId('drop-overlay')).toBeNull()
  })

  it('hides drop overlay on drag-leave', () => {
    const { container } = render(<TerminalView wsUrl="ws://localhost/ws/terminal/dev001" sessionCode="dev001" />)
    const dropZone = container.firstChild as HTMLElement

    fireEvent.dragEnter(dropZone, { dataTransfer: { types: ['Files'] } })
    expect(screen.getByTestId('drop-overlay')).toBeTruthy()
    fireEvent.dragLeave(dropZone)
    expect(screen.queryByTestId('drop-overlay')).toBeNull()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/components/TerminalView.test.tsx`
Expected: FAIL — `sessionCode` prop not accepted, `drop-overlay` not found

- [ ] **Step 3: Implement drag-drop in TerminalView**

Update `spa/src/components/TerminalView.tsx`:

```typescript
import { useEffect, useState, useRef, useCallback } from 'react'
import { UploadSimple } from '@phosphor-icons/react'
import { useTerminal } from '../hooks/useTerminal'
import { useTerminalWs } from '../hooks/useTerminalWs'
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { useHostStore } from '../stores/useHostStore'
import { agentUpload } from '../lib/api'
import { useI18nStore } from '../stores/useI18nStore'
import '@xterm/xterm/css/xterm.css'

interface Props {
  wsUrl: string
  visible?: boolean
  connectingMessage?: string
  sessionCode?: string
}

export default function TerminalView({ wsUrl, visible = true, connectingMessage, sessionCode }: Props) {
  const { termRef, fitAddonRef, containerRef } = useTerminal()
  const [ready, setReady] = useState(false)
  const [disconnected, setDisconnected] = useState(false)
  const [isDragging, setIsDragging] = useState(false)
  const dragCounter = useRef(0)
  const prevVisible = useRef(visible)
  const t = useI18nStore((s) => s.t)

  const agentStatus = useAgentStore((s) => sessionCode ? s.statuses[sessionCode] : undefined)
  const agentActive = agentStatus != null
  const daemonBase = useHostStore((s) => s.getDaemonBase('local'))

  const handleReady = useCallback(() => { setReady(true) }, [])
  const handleDisconnect = useCallback(() => { setDisconnected(true) }, [])
  const handleReconnect = useCallback(() => { setDisconnected(false) }, [])

  const connRef = useTerminalWs({
    wsUrl,
    termRef,
    fitAddonRef,
    containerRef,
    onReady: handleReady,
    onDisconnect: handleDisconnect,
    onReconnect: handleReconnect,
  })

  useEffect(() => {
    setReady(false)
    setDisconnected(false)
  }, [wsUrl])

  useEffect(() => {
    if (visible && !prevVisible.current) {
      setReady(true)
      requestAnimationFrame(() => {
        fitAddonRef.current?.fit()
        const term = termRef.current
        const conn = connRef.current
        if (term && conn) conn.resize(term.cols, term.rows)
        termRef.current?.focus()
      })
    }
    prevVisible.current = visible
  }, [visible, termRef, fitAddonRef, connRef])

  // --- Drag-drop handlers ---

  const handleDragEnter = useCallback((e: React.DragEvent) => {
    if (!agentActive) return
    e.preventDefault()
    dragCounter.current++
    setIsDragging(true)
  }, [agentActive])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    if (!agentActive) return
    e.preventDefault()
  }, [agentActive])

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    if (!agentActive) return
    e.preventDefault()
    dragCounter.current--
    if (dragCounter.current <= 0) {
      setIsDragging(false)
      dragCounter.current = 0
    }
  }, [agentActive])

  const handleDrop = useCallback(async (e: React.DragEvent) => {
    e.preventDefault()
    setIsDragging(false)
    dragCounter.current = 0
    if (!agentActive || !sessionCode) return

    const files = Array.from(e.dataTransfer.files)
    if (files.length === 0) return

    const { startUpload, fileCompleted, fileFailed, nextFile, dismiss } = useUploadStore.getState()
    dismiss(sessionCode) // clear any previous error/done state
    startUpload(sessionCode, files.length, files[0].name)

    for (let i = 0; i < files.length; i++) {
      if (i > 0) nextFile(sessionCode, files[i].name)
      try {
        await agentUpload(daemonBase, files[i], sessionCode)
        fileCompleted(sessionCode)
      } catch {
        fileFailed(sessionCode, files[i].name)
      }
    }
  }, [agentActive, sessionCode, daemonBase])

  const showOverlay = !ready || disconnected

  return (
    <div
      className="w-full h-full relative bg-terminal-bg"
      onDragEnter={handleDragEnter}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <div ref={containerRef} className="w-full h-full" />
      <div
        data-testid="terminal-overlay"
        className="absolute inset-0 flex items-center justify-center pointer-events-none"
        style={{
          background: disconnected ? 'color-mix(in srgb, var(--terminal-bg) 50%, transparent)' : 'var(--terminal-bg)',
          opacity: showOverlay ? 1 : 0,
          transition: 'opacity 0.3s ease-out',
        }}
      >
        <span className="text-text-muted text-sm" style={{ animation: 'breathing 2s ease-in-out infinite' }}>
          {disconnected ? 'reconnecting...' : (connectingMessage || 'connecting...')}
        </span>
        <style>{`@keyframes breathing { 0%, 100% { opacity: 0.3; } 50% { opacity: 1; } }`}</style>
      </div>
      {isDragging && (
        <div
          data-testid="drop-overlay"
          className="absolute inset-0 flex flex-col items-center justify-center gap-2 pointer-events-none z-20"
          style={{ background: 'rgba(0, 0, 0, 0.6)' }}
        >
          <UploadSimple size={32} className="text-text-secondary" />
          <span className="text-text-secondary text-sm">{t('upload.drop_files') || 'Drop files to upload'}</span>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 4: Pass sessionCode from SessionPaneContent**

In `spa/src/components/SessionPaneContent.tsx`, add `sessionCode` prop to TerminalView:

```typescript
  return (
    <TerminalView
      key={`${pane.id}-${mode}`}
      wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(sessionCode)}`}
      visible={isActive}
      sessionCode={sessionCode}
    />
  )
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/components/TerminalView.test.tsx`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/TerminalView.tsx spa/src/components/TerminalView.test.tsx spa/src/components/SessionPaneContent.tsx
git commit -m "feat(spa): add drag-drop file upload to TerminalView"
```

---

### Task 6: SPA — StatusBar upload progress + agent label badge

**Files:**
- Modify: `spa/src/components/StatusBar.tsx`
- Modify: `spa/src/components/StatusBar.test.tsx`

- [ ] **Step 1: Write the failing tests**

Append to `spa/src/components/StatusBar.test.tsx`:

```typescript
import { useAgentStore } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'

describe('StatusBar upload progress', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
    useAgentStore.setState({ events: {}, statuses: {}, unread: {}, activeSubagents: {}, hooksInstalled: false })
  })

  it('shows uploading progress', () => {
    useUploadStore.setState({
      sessions: { dev001: { total: 5, completed: 1, failed: 0, currentFile: 'photo.png', status: 'uploading' } },
    })
    const tab = makeTab('t1', { kind: 'session', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByTestId('upload-status')).toBeTruthy()
    expect(screen.getByText(/photo\.png/)).toBeTruthy()
    expect(screen.getByText(/2\/5/)).toBeTruthy()
  })

  it('shows upload done', () => {
    useUploadStore.setState({
      sessions: { dev001: { total: 3, completed: 3, failed: 0, currentFile: '', status: 'done' } },
    })
    const tab = makeTab('t1', { kind: 'session', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText(/3 files uploaded/)).toBeTruthy()
  })

  it('shows upload error', () => {
    useUploadStore.setState({
      sessions: { dev001: { total: 1, completed: 0, failed: 1, currentFile: '', error: 'bad.mp4', status: 'error' } },
    })
    const tab = makeTab('t1', { kind: 'session', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    expect(screen.getByText(/bad\.mp4/)).toBeTruthy()
  })
})

describe('StatusBar agent label badge', () => {
  beforeEach(() => {
    setupStores()
    useUploadStore.setState({ sessions: {} })
  })

  it('renders agent label as badge with model name', () => {
    useAgentStore.setState({
      events: { dev001: { tmux_session: 'dev', event_name: 'SessionStart', raw_event: { modelName: 'Claude Opus 4' }, agent_type: 'cc', broadcast_ts: Date.now() } },
      statuses: { dev001: 'idle' },
      unread: {},
      activeSubagents: {},
      hooksInstalled: true,
    })
    const tab = makeTab('t1', { kind: 'session', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    const badge = screen.getByTestId('agent-label')
    expect(badge.textContent).toBe('Claude Opus 4')
    // Should have badge styling (border).
    expect(badge.className).toContain('border')
  })

  it('renders fallback Agent badge with white styling', () => {
    useAgentStore.setState({
      events: { dev001: { tmux_session: 'dev', event_name: 'UserPromptSubmit', raw_event: {}, agent_type: 'cc', broadcast_ts: Date.now() } },
      statuses: { dev001: 'running' },
      unread: {},
      activeSubagents: {},
      hooksInstalled: true,
    })
    const tab = makeTab('t1', { kind: 'session', sessionCode: 'dev001', mode: 'terminal' })
    render(<StatusBar activeTab={tab} onViewModeChange={vi.fn()} />)
    const badge = screen.getByTestId('agent-label')
    expect(badge.textContent).toBe('Agent')
    expect(badge.className).toContain('border')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/components/StatusBar.test.tsx`
Expected: FAIL — `upload-status` testid not found, badge class mismatch

- [ ] **Step 3: Update StatusBar component**

Replace the full StatusBar in `spa/src/components/StatusBar.tsx`:

```typescript
import { useState, useRef, useCallback, useEffect } from 'react'
import { CaretUp, CircleNotch, CheckCircle, XCircle } from '@phosphor-icons/react'
import type { Tab } from '../types/tab'
import { getPrimaryPane } from '../lib/pane-tree'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore, getAgentLabel } from '../stores/useAgentStore'
import { useUploadStore } from '../stores/useUploadStore'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  activeTab: Tab | null
  onViewModeChange?: (tabId: string, paneId: string, mode: 'terminal' | 'stream') => void
}

const VIEW_MODE_COLORS: Record<string, string> = {
  terminal: 'bg-green-900/40 text-green-400 border-green-700/50',
  stream: 'bg-blue-900/40 text-blue-400 border-blue-700/50',
}

export function StatusBar({ activeTab, onViewModeChange }: Props) {
  const t = useI18nStore((s) => s.t)
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  const sessions = useSessionStore((s) => s.sessions)
  const defaultHost = useHostStore((s) => s.defaultHost)

  const sessionCode = activeTab?.layout
    ? getPrimaryPane(activeTab.layout).content
    : null
  const agentSessionCode = sessionCode && 'sessionCode' in sessionCode ? sessionCode.sessionCode : null
  const agentEvent = useAgentStore((s) => agentSessionCode ? s.events[agentSessionCode] : undefined)

  // Upload state for active session.
  const uploadState = useUploadStore((s) => agentSessionCode ? s.sessions[agentSessionCode] : undefined)
  const dismiss = useUploadStore((s) => s.dismiss)

  // Auto-dismiss "done" after 3s.
  useEffect(() => {
    if (uploadState?.status !== 'done' || !agentSessionCode) return
    const timer = setTimeout(() => dismiss(agentSessionCode), 3000)
    return () => clearTimeout(timer)
  }, [uploadState?.status, agentSessionCode, dismiss])

  // Auto-dismiss "error" after 30s.
  useEffect(() => {
    if (uploadState?.status !== 'error' || !agentSessionCode) return
    const timer = setTimeout(() => dismiss(agentSessionCode), 30000)
    return () => clearTimeout(timer)
  }, [uploadState?.status, agentSessionCode, dismiss])

  const closeMenu = useCallback(() => setMenuOpen(false), [])
  useClickOutside(menuRef, closeMenu)

  if (!activeTab) {
    return (
      <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0">
        {t('status.no_active')}
      </div>
    )
  }

  const primary = getPrimaryPane(activeTab.layout)
  const { content } = primary

  if (content.kind !== 'session') {
    return (
      <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted flex-shrink-0">
        <span>{content.kind}</span>
      </div>
    )
  }

  const session = sessions.find((s) => s.code === content.sessionCode)
  const sessionName = session?.name ?? content.sessionCode
  const hostName = defaultHost.name
  const status = defaultHost.status

  const viewMode = content.mode
  const viewModes: ('terminal' | 'stream')[] = ['terminal', 'stream']

  const label = getAgentLabel(agentEvent)
  const hasModelName = label != null && label !== 'Agent'

  return (
    <div className="h-6 bg-surface-secondary border-t border-border-subtle flex items-center px-3 text-[10px] text-text-muted gap-3 flex-shrink-0 relative z-10">
      <span>{hostName}</span>
      <span>{sessionName}</span>
      <span className={status === 'connected' ? 'text-green-500' : 'text-text-muted'}>
        {status}
      </span>
      {label && (
        <span
          data-testid="agent-label"
          className={`px-[7px] rounded-[3px] border text-[10px] leading-4 ${
            hasModelName
              ? 'bg-[rgba(154,96,56,0.15)] text-[#e8956a] border-[rgba(180,110,65,0.3)]'
              : 'bg-white/8 text-white/70 border-white/15'
          }`}
        >
          {label}
        </span>
      )}
      {uploadState && (
        <UploadStatus
          state={uploadState}
          onDismiss={() => agentSessionCode && dismiss(agentSessionCode)}
        />
      )}
      <span className="ml-auto flex items-center">
        <div className="relative" ref={menuRef}>
          <button
            title={t('nav.toggle_view')}
            onClick={() => setMenuOpen((v) => !v)}
            className={`flex items-center gap-1 px-2 py-0.5 rounded border text-[10px] cursor-pointer transition-colors ${VIEW_MODE_COLORS[viewMode] ?? 'bg-surface-secondary text-text-secondary border-border-default'}`}
          >
            {viewMode}
            <CaretUp size={10} className={`transition-transform ${menuOpen ? '' : 'rotate-180'}`} />
          </button>
          {menuOpen && (
            <div className="absolute bottom-full right-0 mb-1 bg-surface-elevated border border-border-default rounded-md shadow-lg py-1 min-w-[100px]">
              {viewModes.map((vm) => (
                <button
                  key={vm}
                  onClick={() => {
                    onViewModeChange?.(activeTab.id, primary.id, vm)
                    setMenuOpen(false)
                  }}
                  className={`w-full px-3 py-1 text-left text-[10px] cursor-pointer transition-colors hover:bg-surface-hover ${vm === viewMode ? 'text-white' : 'text-text-secondary'}`}
                >
                  {vm} {vm === viewMode && '\u2713'}
                </button>
              ))}
            </div>
          )}
        </div>
      </span>
    </div>
  )
}

function UploadStatus({ state, onDismiss }: {
  state: { total: number; completed: number; failed: number; currentFile: string; error?: string; status: string }
  onDismiss: () => void
}) {
  if (state.status === 'uploading') {
    return (
      <span data-testid="upload-status" className="inline-flex items-center gap-1 text-yellow-400">
        <CircleNotch size={12} className="animate-spin" />
        <span>Uploading {state.currentFile} ({state.completed + 1}/{state.total})...</span>
      </span>
    )
  }

  if (state.status === 'done') {
    return (
      <span data-testid="upload-status" className="inline-flex items-center gap-1 text-green-400">
        <CheckCircle size={12} />
        <span>{state.total} files uploaded</span>
      </span>
    )
  }

  if (state.status === 'error') {
    const msg = state.completed > 0
      ? `${state.completed} uploaded, ${state.failed} failed`
      : `Upload failed: ${state.error}`
    return (
      <span
        data-testid="upload-status"
        className="inline-flex items-center gap-1 text-red-400 cursor-pointer"
        onClick={onDismiss}
      >
        <XCircle size={12} />
        <span>{msg}</span>
      </span>
    )
  }

  return null
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/components/StatusBar.test.tsx`
Expected: PASS

- [ ] **Step 5: Run full SPA test suite**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run`
Expected: All PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/StatusBar.tsx spa/src/components/StatusBar.test.tsx
git commit -m "feat(spa): add upload progress + agent label badge to StatusBar"
```

---

### Task 7: Integration verification

**Files:** None (verification only)

- [ ] **Step 1: Build SPA**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run build`
Expected: Build succeeds with no errors

- [ ] **Step 2: Run lint**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run lint`
Expected: No new lint errors

- [ ] **Step 3: Run Go tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./...`
Expected: All PASS

- [ ] **Step 4: Run SPA tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run`
Expected: All PASS
