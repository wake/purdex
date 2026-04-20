# Phase 6: Hooks 統一架構 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unify tmux hooks and agent hooks into a modular API + UI architecture, resolving #150, #109, #108, #103, #142, #127.

**Architecture:** Daemon standardizes on `/api/hooks/{module}/status` + `/setup` per module. SPA introduces `HookModule` interface with pluggable configs, `useModuleHook` custom hook for fetch lifecycle, and `HookModuleCard` component. Dead paths (`hooksInstalled`, orphan `useHookStatus`) are removed.

**Tech Stack:** Go 1.26 / net/http (1.22+ pattern matching) / React 19 / Zustand 5 / Vitest / Tailwind 4

---

## File Structure

### New Files
| File | Responsibility |
|------|----------------|
| `spa/src/lib/hook-modules.ts` | `HookModule` interface, `HookModuleStatus` type, `hookFetch` helper, `TMUX_HOOKS` + `CC_HOOKS` configs |
| `spa/src/hooks/useModuleHook.ts` | Generic fetch lifecycle hook: status, loading, error, setup, lastTrigger |
| `spa/src/components/hosts/HookModuleCard.tsx` | Single hook module card: status badge, event list, install/remove buttons, error display |

### Modified Files
| File | Change |
|------|--------|
| `internal/module/session/hooks.go` | Rewrite handlers to unified response format, merge install/remove into setup |
| `internal/module/session/module.go` | Update route registration to `/api/hooks/tmux/*` |
| `internal/module/agent/handler.go` | Update routes to `/api/hooks/cc/*`, remove `AgentType` from setup request |
| `internal/module/agent/module.go` | Update route registration |
| `spa/src/components/hosts/HooksSection.tsx` | Rewrite to iterate `HOOK_MODULES` with `refreshKey` |
| `spa/src/stores/useAgentStore.ts` | Remove `hooksInstalled`/`setHooksInstalled`, add `models` map, update `getAgentLabel` signature |
| `spa/src/App.tsx` | Remove hook-status init useEffect + `fetchAgentHookStatus` import |
| `spa/src/lib/host-api.ts` | Remove 5 old hook functions |
| `spa/src/components/StatusBar.tsx` | Update `getAgentLabel` call to use composite key |
| `spa/src/locales/en.json` | Add error-related i18n keys |
| `spa/src/locales/zh-TW.json` | Add error-related i18n keys |

### Deleted Files
| File | Reason |
|------|--------|
| `spa/src/hooks/useHookStatus.ts` | Replaced by `useModuleHook` |

### Test Files
| File | Change |
|------|--------|
| `internal/module/session/hooks_test.go` | **New** — test unified tmux hook handlers |
| `spa/src/hooks/useModuleHook.test.ts` | **New** — test fetch lifecycle, error handling, cancellation |
| `spa/src/components/hosts/HookModuleCard.test.tsx` | **New** — render test with mock module |
| `spa/src/components/hosts/HooksSection.test.tsx` | Rewrite for new architecture |
| `spa/src/stores/useAgentStore.test.ts` | Update for `models` + remove `hooksInstalled` |
| `spa/src/lib/host-api.test.ts` | Remove old hook function tests |
| `spa/src/components/StatusBar.test.tsx` | Remove `hooksInstalled` from fixtures |

---

## Task 1: Daemon — Unify tmux hook handlers

**Files:**
- Modify: `internal/tmux/fake_executor.go` (add `HooksOutput` field)
- Modify: `internal/module/session/hooks.go`
- Modify: `internal/module/session/module.go`
- Create: `internal/module/session/hooks_test.go`

- [ ] **Step 1: Write test for `handleTmuxHookStatus` unified response**

First, add a `HooksOutput` field to `FakeExecutor` in `internal/tmux/fake_executor.go` so tests can control `ShowHooksGlobal()` output:

```go
// In FakeExecutor struct, add field:
HooksOutput string // returned by ShowHooksGlobal

// Replace the existing one-liner ShowHooksGlobal stub (line 287):
func (f *FakeExecutor) ShowHooksGlobal() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.HooksOutput, nil
}
```

Then create the test file:

```go
// internal/module/session/hooks_test.go
package session

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wake/tmux-box/internal/tmux"
)

func newHooksTestModule(hooksOutput string) *SessionModule {
	fake := tmux.NewFakeExecutor()
	fake.HooksOutput = hooksOutput
	return &SessionModule{tmux: fake}
}

func TestHandleTmuxHookStatus_AllInstalled(t *testing.T) {
	mod := newHooksTestModule(
		"session-created[0] -> run-shell -b 'tmux wait-for -S tbox_sess_evt'\nsession-closed[0] -> run-shell -b 'tmux wait-for -S tbox_sess_evt'\nsession-renamed[0] -> run-shell -b 'tmux wait-for -S tbox_sess_evt'\n",
	)

	req := httptest.NewRequest("GET", "/api/hooks/tmux/status", nil)
	w := httptest.NewRecorder()
	mod.handleTmuxHookStatus(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Installed bool                       `json:"installed"`
		Events    map[string]json.RawMessage `json:"events"`
		Issues    []string                   `json:"issues"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Installed {
		t.Error("expected installed=true when all hooks present")
	}
	if len(resp.Events) != 3 {
		t.Errorf("expected 3 events, got %d", len(resp.Events))
	}
	if len(resp.Issues) != 0 {
		t.Errorf("expected 0 issues, got %v", resp.Issues)
	}
}

func TestHandleTmuxHookStatus_NoneInstalled(t *testing.T) {
	mod := newHooksTestModule("")

	req := httptest.NewRequest("GET", "/api/hooks/tmux/status", nil)
	w := httptest.NewRecorder()
	mod.handleTmuxHookStatus(w, req)

	var resp struct {
		Installed bool `json:"installed"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Installed {
		t.Error("expected installed=false when no hooks present")
	}
}

func TestHandleTmuxHookSetup_Install(t *testing.T) {
	mod := newHooksTestModule(
		"session-created[0] -> run-shell -b 'tmux wait-for -S tbox_sess_evt'\nsession-closed[0] -> run-shell -b 'tmux wait-for -S tbox_sess_evt'\nsession-renamed[0] -> run-shell -b 'tmux wait-for -S tbox_sess_evt'\n",
	)

	body := strings.NewReader(`{"action":"install"}`)
	req := httptest.NewRequest("POST", "/api/hooks/tmux/setup", body)
	w := httptest.NewRecorder()
	mod.handleTmuxHookSetup(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp struct {
		Installed bool `json:"installed"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.Installed {
		t.Error("expected installed=true after install")
	}
}

func TestHandleTmuxHookSetup_Remove(t *testing.T) {
	mod := newHooksTestModule("")

	body := strings.NewReader(`{"action":"remove"}`)
	req := httptest.NewRequest("POST", "/api/hooks/tmux/setup", body)
	w := httptest.NewRecorder()
	mod.handleTmuxHookSetup(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleTmuxHookSetup_InvalidAction(t *testing.T) {
	mod := newHooksTestModule("")

	body := strings.NewReader(`{"action":"restart"}`)
	req := httptest.NewRequest("POST", "/api/hooks/tmux/setup", body)
	w := httptest.NewRecorder()
	mod.handleTmuxHookSetup(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/session/ -run TestHandleTmuxHook -v`
Expected: FAIL — `handleTmuxHookStatus` and `handleTmuxHookSetup` not defined.

- [ ] **Step 3: Rewrite `hooks.go` with unified handlers**

Replace the entire content of `internal/module/session/hooks.go`:

```go
package session

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// tmux hook events that trigger session list refresh.
var tmuxHookEvents = []string{
	"session-created",
	"session-closed",
	"session-renamed",
}

// waitForChannel is the tmux wait-for channel name used to signal session changes.
const waitForChannel = "tbox_sess_evt"

// installTmuxHooks sets global tmux hooks that signal waitForChannel on session events.
func (m *SessionModule) installTmuxHooks() error {
	cmd := fmt.Sprintf("run-shell -b 'tmux wait-for -S %s'", waitForChannel)
	for _, event := range tmuxHookEvents {
		if err := m.tmux.SetHookGlobal(event, cmd); err != nil {
			return fmt.Errorf("set-hook %s: %w", event, err)
		}
	}
	log.Printf("session: installed tmux hooks for %v", tmuxHookEvents)
	return nil
}

// removeTmuxHooks removes previously installed global hooks (best-effort).
func (m *SessionModule) removeTmuxHooks() {
	for _, event := range tmuxHookEvents {
		if err := m.tmux.RemoveHookGlobal(event); err != nil {
			log.Printf("session: remove hook %s: %v (ignored)", event, err)
		}
	}
	log.Printf("session: removed tmux hooks")
}

// tmuxHookEventStatus is the per-event status in the unified response.
type tmuxHookEventStatus struct {
	Installed bool `json:"installed"`
}

// tmuxHookStatusResponse is the unified hook status response.
type tmuxHookStatusResponse struct {
	Installed bool                               `json:"installed"`
	Events    map[string]tmuxHookEventStatus     `json:"events"`
	Issues    []string                           `json:"issues"`
}

// buildTmuxHookStatus checks which tmux hooks are installed and returns the unified response.
func (m *SessionModule) buildTmuxHookStatus() (*tmuxHookStatusResponse, error) {
	hookOutput, err := m.tmux.ShowHooksGlobal()
	if err != nil {
		return nil, err
	}

	events := make(map[string]tmuxHookEventStatus, len(tmuxHookEvents))
	allInstalled := true
	for _, event := range tmuxHookEvents {
		installed := false
		for _, line := range strings.Split(hookOutput, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, event) && strings.Contains(line, waitForChannel) {
				installed = true
				break
			}
		}
		events[event] = tmuxHookEventStatus{Installed: installed}
		if !installed {
			allInstalled = false
		}
	}

	return &tmuxHookStatusResponse{
		Installed: allInstalled,
		Events:    events,
		Issues:    []string{},
	}, nil
}

// handleTmuxHookStatus handles GET /api/hooks/tmux/status.
func (m *SessionModule) handleTmuxHookStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := m.buildTmuxHookStatus()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// tmuxHookSetupRequest is the JSON body for POST /api/hooks/tmux/setup.
type tmuxHookSetupRequest struct {
	Action string `json:"action"`
}

// handleTmuxHookSetup handles POST /api/hooks/tmux/setup.
// It installs or removes tmux hooks and returns the updated status.
func (m *SessionModule) handleTmuxHookSetup(w http.ResponseWriter, r *http.Request) {
	var req tmuxHookSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	switch req.Action {
	case "install":
		if err := m.installTmuxHooks(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	case "remove":
		m.removeTmuxHooks()
	default:
		http.Error(w, `{"error":"action must be install or remove"}`, http.StatusBadRequest)
		return
	}

	// Return updated status (mode A: single round-trip).
	resp, err := m.buildTmuxHookStatus()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 4: Update routes in `module.go`**

In `internal/module/session/module.go`, replace the 3 old hook routes:

```go
// Old (delete these 3 lines):
// mux.HandleFunc("GET /api/hooks/status", m.handleHooksStatus)
// mux.HandleFunc("POST /api/hooks/install", m.handleHooksInstall)
// mux.HandleFunc("POST /api/hooks/remove", m.handleHooksRemove)

// New (add these 2 lines):
mux.HandleFunc("GET /api/hooks/tmux/status", m.handleTmuxHookStatus)
mux.HandleFunc("POST /api/hooks/tmux/setup", m.handleTmuxHookSetup)
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/module/session/ -v`
Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tmux/fake_executor.go internal/module/session/hooks.go internal/module/session/module.go internal/module/session/hooks_test.go
git commit -m "feat(daemon): unify tmux hook API to /api/hooks/tmux/{status,setup}

Resolves part of #150. Replaces 3 separate endpoints with unified
status + setup pattern. Response format now matches the HookModule
standard (installed/events/issues). ALL semantics for installed flag."
```

---

## Task 2: Daemon — Unify CC hook routes

**Files:**
- Modify: `internal/module/agent/handler.go`
- Modify: `internal/module/agent/module.go`

- [ ] **Step 1: Update routes in `module.go`**

In `internal/module/agent/module.go` `RegisterRoutes`, change:

```go
// Old:
// mux.HandleFunc("GET /api/agent/hook-status", m.handleHookStatus)
// mux.HandleFunc("POST /api/agent/hook-setup", m.handleHookSetup)

// New:
mux.HandleFunc("GET /api/hooks/cc/status", m.handleHookStatus)
mux.HandleFunc("POST /api/hooks/cc/setup", m.handleHookSetup)
```

Keep `POST /api/agent/event` unchanged.

- [ ] **Step 2: Simplify `hookSetupRequest` in `handler.go`**

In `internal/module/agent/handler.go`, remove `AgentType` from the struct (module identity is in the URL now):

```go
// Old:
// type hookSetupRequest struct {
// 	AgentType string `json:"agent_type"`
// 	Action    string `json:"action"`
// }

// New:
type hookSetupRequest struct {
	Action string `json:"action"`
}
```

Also update `handleHookSetup` — the `req.AgentType` was only used for choosing `tbox setup` args, which is always CC. Remove the switch on `req.AgentType` if any; the handler always runs `tbox setup` / `tbox setup --remove`.

- [ ] **Step 3: Run existing daemon tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/... -v`
Expected: All tests PASS (route changes don't affect existing unit tests since they test handler functions directly).

- [ ] **Step 4: Commit**

```bash
git add internal/module/agent/handler.go internal/module/agent/module.go
git commit -m "feat(daemon): unify CC hook API to /api/hooks/cc/{status,setup}

Resolves part of #150. Routes moved from /api/agent/hook-* to
/api/hooks/cc/*. Removed agent_type from setup request body
(module identity is now in the URL path)."
```

---

## Task 3: SPA — Create `hook-modules.ts` + `useModuleHook.ts`

**Files:**
- Create: `spa/src/lib/hook-modules.ts`
- Create: `spa/src/hooks/useModuleHook.ts`
- Create: `spa/src/hooks/useModuleHook.test.ts`

- [ ] **Step 1: Write `useModuleHook` tests**

```typescript
// spa/src/hooks/useModuleHook.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useModuleHook } from './useModuleHook'
import type { HookModule, HookModuleStatus } from '../lib/hook-modules'

const OK_STATUS: HookModuleStatus = {
  installed: true,
  events: { 'event-a': { installed: true }, 'event-b': { installed: true } },
  issues: [],
}

const PARTIAL_STATUS: HookModuleStatus = {
  installed: false,
  events: { 'event-a': { installed: true }, 'event-b': { installed: false } },
  issues: ['event-b hook not installed'],
}

function mockModule(overrides?: Partial<HookModule>): HookModule {
  return {
    id: 'test',
    labelKey: 'test.label',
    descKey: 'test.desc',
    fetchStatus: vi.fn(() => Promise.resolve(OK_STATUS)),
    setup: vi.fn(() => Promise.resolve(OK_STATUS)),
    ...overrides,
  }
}

describe('useModuleHook', () => {
  it('fetches status on mount', async () => {
    const mod = mockModule()
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.status).toEqual(OK_STATUS)
    expect(result.current.error).toBeNull()
    expect(mod.fetchStatus).toHaveBeenCalledWith('host-1')
  })

  it('exposes error on fetch failure (4xx/5xx)', async () => {
    const mod = mockModule({
      fetchStatus: vi.fn(() => Promise.reject(new Error('403 Forbidden'))),
    })
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))

    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.status).toBeNull()
    expect(result.current.error).toBe('403 Forbidden')
  })

  it('cancels stale fetch when hostId changes', async () => {
    let resolveFirst: (v: HookModuleStatus) => void
    const firstPromise = new Promise<HookModuleStatus>((r) => { resolveFirst = r })
    const mod = mockModule({
      fetchStatus: vi.fn()
        .mockReturnValueOnce(firstPromise)
        .mockReturnValueOnce(Promise.resolve(PARTIAL_STATUS)),
    })

    const { result, rerender } = renderHook(
      ({ hostId }) => useModuleHook(mod, hostId, 0),
      { initialProps: { hostId: 'host-1' } },
    )

    // Switch to host-2 before host-1 resolves
    rerender({ hostId: 'host-2' })
    await waitFor(() => expect(result.current.loading).toBe(false))

    // Resolve host-1 late — should be ignored
    resolveFirst!(OK_STATUS)
    await new Promise((r) => setTimeout(r, 50))

    expect(result.current.status).toEqual(PARTIAL_STATUS)
  })

  it('setup() updates status from return value', async () => {
    const mod = mockModule({
      setup: vi.fn(() => Promise.resolve(PARTIAL_STATUS)),
    })
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))
    await waitFor(() => expect(result.current.loading).toBe(false))

    await act(async () => {
      await result.current.setup('remove')
    })

    expect(result.current.status).toEqual(PARTIAL_STATUS)
    expect(mod.setup).toHaveBeenCalledWith('host-1', 'remove')
  })

  it('setup() failure shows error', async () => {
    const mod = mockModule({
      setup: vi.fn(() => Promise.reject(new Error('500 Internal Server Error'))),
    })
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))
    await waitFor(() => expect(result.current.loading).toBe(false))

    await act(async () => {
      await result.current.setup('install')
    })

    expect(result.current.error).toBe('500 Internal Server Error')
  })

  it('refreshKey change triggers re-fetch', async () => {
    const mod = mockModule()
    const { result, rerender } = renderHook(
      ({ refreshKey }) => useModuleHook(mod, 'host-1', refreshKey),
      { initialProps: { refreshKey: 0 } },
    )
    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(mod.fetchStatus).toHaveBeenCalledTimes(1)
    rerender({ refreshKey: 1 })
    await waitFor(() => expect(mod.fetchStatus).toHaveBeenCalledTimes(2))
  })

  it('returns lastTrigger from module.getLastTrigger', async () => {
    const triggers = { SessionStart: 1700000000000 }
    const mod = mockModule({ getLastTrigger: () => triggers })
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))
    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(result.current.lastTrigger).toEqual(triggers)
  })

  it('returns null lastTrigger when module has no getLastTrigger', async () => {
    const mod = mockModule()
    const { result } = renderHook(() => useModuleHook(mod, 'host-1', 0))
    await waitFor(() => expect(result.current.loading).toBe(false))

    expect(result.current.lastTrigger).toBeNull()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/hooks/useModuleHook.test.ts`
Expected: FAIL — modules not found.

- [ ] **Step 3: Create `hook-modules.ts`**

```typescript
// spa/src/lib/hook-modules.ts
import { hostFetch } from './host-api'
import { useAgentStore } from '../stores/useAgentStore'

/* ─── Types ─── */

export interface HookModuleEvent {
  installed: boolean
  command?: string | null
}

export interface HookModuleStatus {
  installed: boolean
  events: Record<string, HookModuleEvent>
  issues?: string[]
}

export interface HookModule {
  id: string
  labelKey: string
  descKey: string
  fetchStatus: (hostId: string) => Promise<HookModuleStatus>
  setup: (hostId: string, action: 'install' | 'remove') => Promise<HookModuleStatus>
  getLastTrigger?: (hostId: string) => Record<string, number> | null
}

/* ─── Shared fetch helper ─── */

async function hookFetch(hostId: string, path: string, init?: RequestInit): Promise<HookModuleStatus> {
  const res = await hostFetch(hostId, path, init)
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

/* ─── Module configs ─── */

const TMUX_HOOKS: HookModule = {
  id: 'tmux',
  labelKey: 'hosts.tmux_hooks',
  descKey: 'hosts.tmux_hooks_desc',
  fetchStatus: (hostId) => hookFetch(hostId, '/api/hooks/tmux/status'),
  setup: (hostId, action) =>
    hookFetch(hostId, '/api/hooks/tmux/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    }),
}

const CC_HOOKS: HookModule = {
  id: 'cc',
  labelKey: 'hosts.agent_hooks',
  descKey: 'hosts.agent_hooks_desc',
  fetchStatus: (hostId) => hookFetch(hostId, '/api/hooks/cc/status'),
  setup: (hostId, action) =>
    hookFetch(hostId, '/api/hooks/cc/setup', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    }),
  getLastTrigger: (hostId) => {
    const events = useAgentStore.getState().events
    const prefix = `${hostId}:`
    const result: Record<string, number> = {}
    for (const [key, event] of Object.entries(events)) {
      if (!key.startsWith(prefix)) continue
      const existing = result[event.event_name]
      if (!existing || event.broadcast_ts > existing) {
        result[event.event_name] = event.broadcast_ts
      }
    }
    return Object.keys(result).length > 0 ? result : null
  },
}

export const HOOK_MODULES: HookModule[] = [TMUX_HOOKS, CC_HOOKS]
```

- [ ] **Step 4: Create `useModuleHook.ts`**

```typescript
// spa/src/hooks/useModuleHook.ts
import { useState, useEffect } from 'react'
import type { HookModule, HookModuleStatus } from '../lib/hook-modules'

export function useModuleHook(module: HookModule, hostId: string, refreshKey: number) {
  const [status, setStatus] = useState<HookModuleStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    module.fetchStatus(hostId)
      .then((data) => { if (!cancelled) setStatus(data) })
      .catch((err) => { if (!cancelled) setError(err instanceof Error ? err.message : String(err)) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [module, hostId, refreshKey])

  const setup = async (action: 'install' | 'remove') => {
    setLoading(true)
    setError(null)
    try {
      const data = await module.setup(hostId, action)
      setStatus(data)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
    setLoading(false)
  }

  const lastTrigger = module.getLastTrigger?.(hostId) ?? null

  return { status, loading, error, setup, lastTrigger }
}
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/hooks/useModuleHook.test.ts`
Expected: All 7 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/hook-modules.ts spa/src/hooks/useModuleHook.ts spa/src/hooks/useModuleHook.test.ts
git commit -m "feat(spa): add HookModule interface + useModuleHook hook

New modular hook system: HookModule defines fetch/setup per module,
useModuleHook manages lifecycle with error exposure, cancellation,
and refreshKey support. Configs for tmux and cc modules included."
```

---

## Task 4: SPA — Create `HookModuleCard` + rewrite `HooksSection`

**Files:**
- Create: `spa/src/components/hosts/HookModuleCard.tsx`
- Modify: `spa/src/components/hosts/HooksSection.tsx`
- Modify: `spa/src/components/hosts/HooksSection.test.tsx`

- [ ] **Step 1: Rewrite `HooksSection.test.tsx`**

```typescript
// spa/src/components/hosts/HooksSection.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react'
import { HooksSection } from './HooksSection'
import { useHostStore } from '../../stores/useHostStore'

// Mock hook-modules to provide controllable test modules
const mockFetchStatus = vi.fn(() => Promise.resolve({
  installed: true,
  events: { 'event-a': { installed: true } },
  issues: [],
}))

const mockSetup = vi.fn(() => Promise.resolve({
  installed: true,
  events: { 'event-a': { installed: true } },
  issues: [],
}))

vi.mock('../../lib/hook-modules', () => ({
  HOOK_MODULES: [
    {
      id: 'test-mod-1',
      labelKey: 'hosts.tmux_hooks',
      descKey: 'hosts.tmux_hooks_desc',
      fetchStatus: (...args: unknown[]) => mockFetchStatus(...args),
      setup: (...args: unknown[]) => mockSetup(...args),
    },
    {
      id: 'test-mod-2',
      labelKey: 'hosts.agent_hooks',
      descKey: 'hosts.agent_hooks_desc',
      fetchStatus: (...args: unknown[]) => mockFetchStatus(...args),
      setup: (...args: unknown[]) => mockSetup(...args),
    },
  ],
}))

const HOST_ID = 'test-host'

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  useHostStore.setState({
    hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST_ID],
    runtime: { [HOST_ID]: { status: 'connected' } },
  })
})

describe('HooksSection', () => {
  it('renders a card for each hook module', async () => {
    render(<HooksSection hostId={HOST_ID} />)
    await waitFor(() => {
      expect(screen.getAllByText('Installed').length).toBeGreaterThanOrEqual(2)
    })
  })

  it('renders error when fetch fails', async () => {
    mockFetchStatus.mockRejectedValueOnce(new Error('503 Service Unavailable'))
    render(<HooksSection hostId={HOST_ID} />)
    await waitFor(() => {
      expect(screen.getByText(/503/)).toBeInTheDocument()
    })
  })

  it('global refresh re-fetches all cards', async () => {
    render(<HooksSection hostId={HOST_ID} />)
    await waitFor(() => expect(mockFetchStatus).toHaveBeenCalledTimes(2))

    const refreshBtn = screen.getByRole('button', { name: /Check Status/i })
    fireEvent.click(refreshBtn)
    await waitFor(() => expect(mockFetchStatus).toHaveBeenCalledTimes(4))
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/components/hosts/HooksSection.test.tsx`
Expected: FAIL — `HookModuleCard` not found.

- [ ] **Step 3: Create `HookModuleCard.tsx`**

```tsx
// spa/src/components/hosts/HookModuleCard.tsx
import { CheckCircle, XCircle, DownloadSimple, Trash, WarningCircle } from '@phosphor-icons/react'
import { useModuleHook } from '../../hooks/useModuleHook'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { HookModule } from '../../lib/hook-modules'

interface Props {
  module: HookModule
  hostId: string
  refreshKey: number
}

function StatusBadge({ installed, t }: { installed: boolean; t: (key: string) => string }) {
  return (
    <span className={`inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded ${
      installed ? 'bg-green-500/20 text-green-400' : 'bg-surface-tertiary text-text-muted'
    }`}>
      {installed ? <CheckCircle size={12} /> : <XCircle size={12} />}
      {installed ? t('hosts.installed') : t('hosts.not_installed')}
    </span>
  )
}

function formatRelativeTime(ts: number, t: (key: string, p?: Record<string, string | number>) => string): string {
  const diff = Date.now() - ts / 1_000_000 // broadcast_ts is nanoseconds
  const seconds = Math.floor(diff / 1000)
  if (seconds < 60) return t('hosts.hook_just_now')
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return t('hosts.hook_minutes_ago', { n: minutes })
  const hours = Math.floor(minutes / 60)
  return t('hosts.hook_hours_ago', { n: hours })
}

export function HookModuleCard({ module, hostId, refreshKey }: Props) {
  const t = useI18nStore((s) => s.t)
  const isOffline = useHostStore((s) => {
    const rt = s.runtime[hostId]
    return rt != null && rt.status !== 'connected'
  })

  const { status, loading, error, setup, lastTrigger } = useModuleHook(module, hostId, refreshKey)

  const eventEntries = status ? Object.entries(status.events) : []

  return (
    <div className="p-4 bg-surface-secondary rounded-lg border border-border-subtle">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold">{t(module.labelKey)}</h3>
        {status && <StatusBadge installed={status.installed} t={t} />}
      </div>
      <p className="text-xs text-text-muted mb-3">{t(module.descKey)}</p>

      {error && (
        <div className="flex items-center gap-1.5 text-xs text-red-400 mb-3">
          <WarningCircle size={14} />
          <span>{error}</span>
        </div>
      )}

      {status && eventEntries.length > 0 && (
        <div className="space-y-1 mb-3">
          {eventEntries.map(([event, detail]) => (
            <div key={event} className="flex items-center gap-3 text-xs py-1">
              <span className="text-text-secondary w-40 shrink-0 font-mono">{event}</span>
              <span className={`inline-flex items-center gap-1 ${detail.installed ? 'text-green-400' : 'text-text-muted'}`}>
                {detail.installed ? <CheckCircle size={12} /> : <XCircle size={12} />}
                {detail.installed ? t('hosts.installed') : t('hosts.not_installed')}
              </span>
              {lastTrigger?.[event] && (
                <span className="text-text-muted ml-auto">
                  {formatRelativeTime(lastTrigger[event], t)}
                </span>
              )}
            </div>
          ))}
        </div>
      )}

      {status?.issues && status.issues.length > 0 && (
        <div className="text-xs text-yellow-400 mb-3 space-y-0.5">
          {status.issues.map((issue, i) => (
            <div key={i} className="flex items-center gap-1">
              <WarningCircle size={12} />
              <span>{issue}</span>
            </div>
          ))}
        </div>
      )}

      <div className="flex gap-2">
        <button
          onClick={() => setup('install')}
          disabled={isOffline || loading || !!status?.installed}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
        >
          <DownloadSimple size={14} />
          {t('hosts.install')}
        </button>
        <button
          onClick={() => setup('remove')}
          disabled={isOffline || loading || !status?.installed}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-red-500/10 text-red-400 border border-red-500/30 cursor-pointer disabled:opacity-50"
        >
          <Trash size={14} />
          {t('hosts.remove')}
        </button>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Rewrite `HooksSection.tsx`**

```tsx
// spa/src/components/hosts/HooksSection.tsx
import { useState } from 'react'
import { ArrowsClockwise } from '@phosphor-icons/react'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { HOOK_MODULES } from '../../lib/hook-modules'
import { HookModuleCard } from './HookModuleCard'

interface Props {
  hostId: string
}

export function HooksSection({ hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const host = useHostStore((s) => s.hosts[hostId])
  const isOffline = useHostStore((s) => {
    const rt = s.runtime[hostId]
    return rt != null && rt.status !== 'connected'
  })
  const [refreshKey, setRefreshKey] = useState(0)

  if (!host) return null

  return (
    <div className="max-w-2xl">
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-lg font-semibold">{t('hosts.hooks')}</h2>
        <button
          onClick={() => setRefreshKey((k) => k + 1)}
          disabled={isOffline}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-surface-secondary hover:bg-surface-tertiary border border-border-default text-text-secondary cursor-pointer disabled:opacity-50"
        >
          <ArrowsClockwise size={14} />
          {t('hosts.check_status')}
        </button>
      </div>

      <div className="space-y-4">
        {HOOK_MODULES.map((mod) => (
          <HookModuleCard key={mod.id} module={mod} hostId={hostId} refreshKey={refreshKey} />
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 5: Run tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/components/hosts/HooksSection.test.tsx`
Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/hosts/HookModuleCard.tsx spa/src/components/hosts/HooksSection.tsx spa/src/components/hosts/HooksSection.test.tsx
git commit -m "feat(spa): rewrite HooksSection with modular HookModuleCard

Each hook module renders as an independent card with status, events,
install/remove buttons, and error display. Supports refreshKey for
global refresh. Closes #150, #103."
```

---

## Task 5: SPA — Clean dead paths

**Files:**
- Delete: `spa/src/hooks/useHookStatus.ts`
- Modify: `spa/src/App.tsx`
- Modify: `spa/src/lib/host-api.ts`
- Modify: `spa/src/lib/host-api.test.ts`
- Modify: `spa/src/stores/useAgentStore.ts`
- Modify: `spa/src/stores/useAgentStore.test.ts`
- Modify: `spa/src/components/StatusBar.test.tsx`
- Modify: `spa/src/lib/host-lifecycle.test.ts`

- [ ] **Step 1: Delete `useHookStatus.ts`**

```bash
rm spa/src/hooks/useHookStatus.ts
```

- [ ] **Step 2: Remove App.tsx hook-status init useEffect**

In `spa/src/App.tsx`:
- Remove import: `import { fetchAgentHookStatus } from './lib/host-api'`
- Remove import: `import { useAgentStore } from './stores/useAgentStore'` (if no other usage remains — check first; `useAgentStore` is likely still used elsewhere in the file, so only remove if it becomes unused)
- Remove the entire `useEffect` block (lines 91-102):

```typescript
// DELETE this entire block:
// useEffect(() => {
//   if (!firstHostId) return
//   fetchAgentHookStatus(firstHostId)
//     .then(...)
//     .catch(...)
// }, [firstHostId])
```

- [ ] **Step 3: Remove 5 old functions from `host-api.ts`**

In `spa/src/lib/host-api.ts`, remove:
- `fetchHooksStatus` (line 61-63)
- `installHooks` (line 65-67)
- `removeHooks` (line 69-71)
- `fetchAgentHookStatus` (line 207-209)
- `setupAgentHook` (line 211-221)

Also remove the `/* ─── Hook Status API ─── */` section header comment.

- [ ] **Step 4: Remove corresponding tests from `host-api.test.ts`**

In `spa/src/lib/host-api.test.ts`, remove:
- The `describe('fetchAgentHookStatus', ...)` block
- The `describe('setupAgentHook', ...)` block
- Remove `fetchAgentHookStatus, setupAgentHook` from the import line

- [ ] **Step 5: Remove `hooksInstalled` from `useAgentStore.ts`**

In `spa/src/stores/useAgentStore.ts`:
- Remove from interface: `hooksInstalled: boolean` (line 25)
- Remove from interface: `setHooksInstalled: (installed: boolean) => void` (line 33 area)
- Remove from initial state: `hooksInstalled: false,` (line 81)
- Remove setter: `setHooksInstalled: (installed) => set({ hooksInstalled: installed }),` (line 209)

- [ ] **Step 6: Update `useAgentStore.test.ts`**

In `spa/src/stores/useAgentStore.test.ts`, remove `hooksInstalled: false` from the `beforeEach` setState call (line 16).

- [ ] **Step 7: Update `StatusBar.test.tsx` and `host-lifecycle.test.ts`**

In `spa/src/components/StatusBar.test.tsx`, remove `hooksInstalled: false` and `hooksInstalled: true` from all `useAgentStore.setState` calls.

In `spa/src/lib/host-lifecycle.test.ts` line 38, remove `hooksInstalled: false` from the `useAgentStore.setState` call.

- [ ] **Step 8: Run all SPA tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run`
Expected: All tests PASS.

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "refactor(spa): remove dead hook paths

Delete orphan useHookStatus hook (#109), remove App.tsx init
useEffect (#108), remove hooksInstalled from useAgentStore,
remove 5 old hook API functions from host-api.ts."
```

---

## Task 6: SPA — #127 `models` map + `getAgentLabel` refactor

**Files:**
- Modify: `spa/src/stores/useAgentStore.ts`
- Modify: `spa/src/stores/useAgentStore.test.ts`
- Modify: `spa/src/components/StatusBar.tsx`
- Modify: `spa/src/components/StatusBar.test.tsx`

- [ ] **Step 1: Write tests for `models` behavior**

Add to `spa/src/stores/useAgentStore.test.ts`:

```typescript
describe('models map (#127)', () => {
  it('SessionStart with modelName populates models', () => {
    const event: AgentHookEvent = {
      tmux_session: 'dev',
      event_name: 'SessionStart',
      raw_event: { modelName: 'claude-sonnet-4-6' },
      agent_type: 'cc',
      broadcast_ts: Date.now(),
    }
    useAgentStore.getState().handleHookEvent(H, 'dev', event)
    expect(useAgentStore.getState().models[`${H}:dev`]).toBe('claude-sonnet-4-6')
  })

  it('subsequent events do not overwrite models', () => {
    // First: SessionStart with model
    useAgentStore.getState().handleHookEvent(H, 'dev', {
      tmux_session: 'dev', event_name: 'SessionStart',
      raw_event: { modelName: 'claude-sonnet-4-6' },
      agent_type: 'cc', broadcast_ts: Date.now(),
    })
    // Then: UserPromptSubmit without model
    useAgentStore.getState().handleHookEvent(H, 'dev', {
      tmux_session: 'dev', event_name: 'UserPromptSubmit',
      raw_event: {}, agent_type: 'cc', broadcast_ts: Date.now(),
    })
    expect(useAgentStore.getState().models[`${H}:dev`]).toBe('claude-sonnet-4-6')
  })

  it('SessionEnd clears models entry', () => {
    useAgentStore.getState().handleHookEvent(H, 'dev', {
      tmux_session: 'dev', event_name: 'SessionStart',
      raw_event: { modelName: 'claude-sonnet-4-6' },
      agent_type: 'cc', broadcast_ts: Date.now(),
    })
    useAgentStore.getState().handleHookEvent(H, 'dev', {
      tmux_session: 'dev', event_name: 'SessionEnd',
      raw_event: {}, agent_type: 'cc', broadcast_ts: Date.now(),
    })
    expect(useAgentStore.getState().models[`${H}:dev`]).toBeUndefined()
  })

  it('removeHost clears models for that host', () => {
    useAgentStore.getState().handleHookEvent(H, 'dev', {
      tmux_session: 'dev', event_name: 'SessionStart',
      raw_event: { modelName: 'claude-sonnet-4-6' },
      agent_type: 'cc', broadcast_ts: Date.now(),
    })
    useAgentStore.getState().handleHookEvent('other', 'dev', {
      tmux_session: 'dev', event_name: 'SessionStart',
      raw_event: { modelName: 'claude-opus-4-6' },
      agent_type: 'cc', broadcast_ts: Date.now(),
    })
    useAgentStore.getState().removeHost(H)
    expect(useAgentStore.getState().models[`${H}:dev`]).toBeUndefined()
    expect(useAgentStore.getState().models['other:dev']).toBe('claude-opus-4-6')
  })

  it('getAgentLabel returns model from models map', () => {
    useAgentStore.getState().handleHookEvent(H, 'dev', {
      tmux_session: 'dev', event_name: 'SessionStart',
      raw_event: { modelName: 'claude-sonnet-4-6' },
      agent_type: 'cc', broadcast_ts: Date.now(),
    })
    expect(getAgentLabel(`${H}:dev`)).toBe('claude-sonnet-4-6')
  })

  it('getAgentLabel returns null when no model', () => {
    expect(getAgentLabel(`${H}:unknown`)).toBeNull()
  })
})
```

Also add `getAgentLabel` to the import at the top of the test file.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run src/stores/useAgentStore.test.ts -t "models map"`
Expected: FAIL — `models` property doesn't exist.

- [ ] **Step 3: Implement `models` in `useAgentStore.ts`**

Add `models: Record<string, string>` to `AgentState` interface and initial state (`models: {}`).

In `handleHookEvent`, after `set((s) => ({ events: { ...s.events, [key]: event } }))` (line 149), add modelName extraction:

```typescript
// Extract modelName if present (persists across event overwrites — #127)
const modelName = event.raw_event?.modelName as string | undefined
if (modelName) {
  set((s) => ({ models: { ...s.models, [key]: modelName } }))
}
```

In the `SessionEnd` clear block (line 121-132), also clear `models[key]`:

```typescript
// Add to the destructured cleanup:
const { [key]: _m, ...restModels } = s.models
return { events: restEvents, statuses: restStatuses, unread: restUnread, activeSubagents: restSubagents, models: restModels }
```

In `removeHost` (line 191-205), add `models` to the filtered keys:

```typescript
return {
  events: filterKeys(s.events),
  statuses: filterKeys(s.statuses),
  unread: filterKeys(s.unread),
  activeSubagents: filterKeys(s.activeSubagents),
  models: filterKeys(s.models),
}
```

Update `getAgentLabel` to new signature:

```typescript
export function getAgentLabel(key: string): string | null {
  const model = useAgentStore.getState().models[key]
  return model || null
}
```

Also add `models: {}` to the `beforeEach` reset in `useAgentStore.test.ts`.

- [ ] **Step 4: Update `StatusBar.tsx`**

In `spa/src/components/StatusBar.tsx`, change the `getAgentLabel` usage (around line 156):

```typescript
// Old:
// {getAgentLabel(agentEvent) && (() => {
//   const label = getAgentLabel(agentEvent)!

// New:
{agentCk && getAgentLabel(agentCk) && (() => {
  const label = getAgentLabel(agentCk)!
```

- [ ] **Step 5: Update `StatusBar.test.tsx`**

Update test fixtures that set `useAgentStore` state:
- Add `models: {}` to all `useAgentStore.setState` calls
- **重要**：對於測試 agent label badge 的 fixture（如 `modelName: 'Claude Opus 4'` 的案例），除了 `events` 中的 `raw_event.modelName`，還必須在 `models` 中設定對應的值：`models: { [ck]: 'Claude Opus 4' }`。因為 `getAgentLabel` 現在從 `models` 讀取，不再從 `events[key].raw_event.modelName` 讀取。
- `StatusBar.tsx` 不直接呼叫 `getAgentLabel`，測試是透過 render 間接驗證 badge 文字。

- [ ] **Step 6: Run all SPA tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run`
Expected: All tests PASS.

- [ ] **Step 7: Commit**

```bash
git add spa/src/stores/useAgentStore.ts spa/src/stores/useAgentStore.test.ts spa/src/components/StatusBar.tsx spa/src/components/StatusBar.test.tsx
git commit -m "fix(spa): persist modelName in dedicated models map (#127)

Model name from SessionStart events is now stored in models[key],
surviving event overwrites from UserPromptSubmit/Stop etc.
getAgentLabel() now reads from models map via composite key."
```

---

## Task 7: SPA — Add i18n keys for hooks error/time

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: Add new i18n keys**

In `spa/src/locales/en.json`, after `"hosts.remove": "Remove",` add:

```json
  "hosts.hook_just_now": "just now",
  "hosts.hook_minutes_ago": "{n}m ago",
  "hosts.hook_hours_ago": "{n}h ago",
```

In `spa/src/locales/zh-TW.json`, after `"hosts.remove": "移除",` add:

```json
  "hosts.hook_just_now": "剛剛",
  "hosts.hook_minutes_ago": "{n} 分鐘前",
  "hosts.hook_hours_ago": "{n} 小時前",
```

- [ ] **Step 2: Run lint to verify JSON is valid**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run lint`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "chore(i18n): add hook trigger time + error display keys"
```

---

## Task 8: Final integration test + lint

- [ ] **Step 1: Run full SPA test suite**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx vitest run`
Expected: All tests PASS.

- [ ] **Step 2: Run lint**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run lint`
Expected: No errors.

- [ ] **Step 3: Run Go tests**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/... -v`
Expected: All tests PASS.

- [ ] **Step 4: Build SPA**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm run build`
Expected: Build succeeds with no TypeScript errors.

---

## Issue Closure Checklist

| Issue | Task | Status |
|-------|------|--------|
| #150 HooksSection Agent Hooks stub | Task 3 + 4 | Agent hooks now fully functional |
| #109 useHookStatus orphan | Task 5 | Deleted, replaced by useModuleHook |
| #108 App.tsx hook-status fetch | Task 5 | Removed |
| #103 useHookStatus silent errors | Task 3 + 4 | Error state exposed in UI |
| #142 Hook last trigger time | Task 3 + 4 + 7 | getLastTrigger + HookModuleCard display |
| #127 Agent label modelName lost | Task 6 | models map persists across events |
| #114 idle-with-background-subagents | N/A | Already implemented, close issue |
| #64 hook-driven CC status (FIFO) | N/A | Superseded by tbox hook + HTTP, close issue |
