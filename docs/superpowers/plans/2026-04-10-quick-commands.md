# Quick Commands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a pluggable quick command system — any module can contribute commands, any UI can consume and execute them via a tmux send-keys API.

**Architecture:** SPA-side store (global + per-host) + module registry extension point + `useCommands()` hook for consumption. Execution via `POST /api/sessions/{code}/send-keys` calling `tmux.SendKeysRaw`.

**Tech Stack:** Go (daemon handler), React/Zustand/purdex (store + hook), @dnd-kit not needed, Phosphor Icons

**Spec:** `docs/superpowers/specs/2026-04-10-quick-commands-design.md`

---

### Task 1: Backend — send-keys API endpoint

**Files:**
- Modify: `internal/module/session/handler.go` (add `handleSendKeys`)
- Modify: `internal/module/session/module.go` (register route)
- Modify: `internal/module/session/handler_test.go` (add test)

- [ ] **Step 1: Write the failing test**

In `internal/module/session/handler_test.go`, add after `TestHandlerRenameSessionDuplicate`:

```go
func TestHandlerSendKeys(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	fake.AddSession("target", "/tmp")

	sessions, err := mod.ListSessions()
	require.NoError(t, err)
	code := sessions[0].Code

	body := `{"keys":"echo hello\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+code+"/send-keys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify keys were sent via SendKeysRaw
	calls := fake.RawKeysSent()
	require.Len(t, calls, 1)
	assert.Equal(t, "target:", calls[0].Target)
	assert.Equal(t, []string{"echo hello\n"}, calls[0].Keys)
}

func TestHandlerSendKeysNotFound(t *testing.T) {
	mod, _, _ := newTestModule(t)
	mux := http.NewServeMux()
	mod.RegisterRoutes(mux)

	body := `{"keys":"echo hello\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/zzzzzz/send-keys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/module/session/ -run TestHandlerSendKeys -v`
Expected: FAIL — route not registered, handler not defined.

- [ ] **Step 3: Write the handler**

In `internal/module/session/handler.go`, add at the end:

```go
type sendKeysRequest struct {
	Keys string `json:"keys"`
}

func (m *SessionModule) handleSendKeys(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")

	var req sendKeysRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Keys == "" {
		http.Error(w, "keys must not be empty", http.StatusBadRequest)
		return
	}

	info, err := m.GetSession(code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if info == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	if err := m.tmux.SendKeysRaw(info.Name+":", req.Keys); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
```

In `internal/module/session/module.go`, inside `RegisterRoutes`, add:

```go
mux.HandleFunc("POST /api/sessions/{code}/send-keys", m.handleSendKeys)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/module/session/ -run TestHandlerSendKeys -v`
Expected: PASS (both tests)

- [ ] **Step 5: Commit**

```bash
git add internal/module/session/handler.go internal/module/session/module.go internal/module/session/handler_test.go
git commit -m "feat: POST /api/sessions/{code}/send-keys endpoint"
```

---

### Task 2: Module registry — add commands extension point

**Files:**
- Modify: `spa/src/lib/module-registry.ts` (extend types + add query)
- Create: `spa/src/lib/module-registry.test.ts` (test commands query)

- [ ] **Step 1: Write the failing test**

Create `spa/src/lib/module-registry.test.ts`:

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { registerModule, clearModuleRegistry, getModulesWithCommands } from './module-registry'

describe('module-registry commands', () => {
  beforeEach(() => clearModuleRegistry())

  it('getModulesWithCommands returns modules that have commands', () => {
    registerModule({ id: 'no-cmds', name: 'No Commands' })
    registerModule({
      id: 'has-cmds',
      name: 'Has Commands',
      commands: [{ id: 'test', name: 'Test', command: 'echo test' }],
    })
    const result = getModulesWithCommands()
    expect(result).toHaveLength(1)
    expect(result[0].id).toBe('has-cmds')
  })

  it('supports function commands', () => {
    registerModule({
      id: 'dynamic',
      name: 'Dynamic',
      commands: [{ id: 'dyn', name: 'Dynamic', command: (ctx) => `cd ${ctx.moduleConfig?.path ?? '~'}` }],
    })
    const result = getModulesWithCommands()
    expect(result).toHaveLength(1)
    const cmd = result[0].commands![0]
    expect(typeof cmd.command).toBe('function')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/module-registry.test.ts`
Expected: FAIL — `getModulesWithCommands` not exported, `commands` not in type.

- [ ] **Step 3: Extend ModuleDefinition and add query**

In `spa/src/lib/module-registry.ts`, add the types after `ConfigDef`:

```ts
export interface CommandContribution {
  id: string
  name: string
  command: string | ((ctx: CommandContext) => string)
  icon?: string
  category?: string
}

export interface CommandContext {
  hostId: string
  workspaceId?: string | null
  moduleConfig?: Record<string, unknown>
}
```

Add `commands` to `ModuleDefinition`:

```ts
export interface ModuleDefinition {
  id: string
  name: string
  pane?: PaneDefinition
  views?: ViewDefinition[]
  workspaceConfig?: ConfigDef[]
  globalConfig?: ConfigDef[]
  commands?: CommandContribution[]
}
```

Add the query function after `getModulesWithGlobalConfig`:

```ts
export function getModulesWithCommands(): ModuleDefinition[] {
  return [...modules.values()].filter((m) => m.commands && m.commands.length > 0)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/module-registry.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/module-registry.ts spa/src/lib/module-registry.test.ts
git commit -m "feat: module registry commands extension point"
```

---

### Task 3: QuickCommandStore — purdex persist store

**Files:**
- Create: `spa/src/stores/useQuickCommandStore.ts`
- Create: `spa/src/stores/useQuickCommandStore.test.ts`
- Modify: `spa/src/lib/storage/keys.ts` (add key)

- [ ] **Step 1: Write the failing test**

Create `spa/src/stores/useQuickCommandStore.test.ts`:

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useQuickCommandStore } from './useQuickCommandStore'

describe('useQuickCommandStore', () => {
  beforeEach(() => {
    useQuickCommandStore.setState({
      global: [
        { id: 'start-cc', name: 'Start Claude Code', command: 'claude -p --verbose --output-format stream-json', category: 'agent' },
        { id: 'start-codex', name: 'Start Codex', command: 'codex', category: 'agent' },
      ],
      byHost: {},
    })
  })

  it('getCommands returns global commands when no host overrides', () => {
    const cmds = useQuickCommandStore.getState().getCommands('host-1')
    expect(cmds).toHaveLength(2)
    expect(cmds[0].id).toBe('start-cc')
  })

  it('per-host overrides global by id', () => {
    useQuickCommandStore.getState().addCommand(
      { id: 'start-cc', name: 'CC Custom', command: 'claude --custom', category: 'agent' },
      'host-1',
    )
    const cmds = useQuickCommandStore.getState().getCommands('host-1')
    const cc = cmds.find((c) => c.id === 'start-cc')!
    expect(cc.command).toBe('claude --custom')
    expect(cc.name).toBe('CC Custom')
  })

  it('addCommand to global', () => {
    useQuickCommandStore.getState().addCommand({ id: 'custom', name: 'Custom', command: 'ls -la' })
    expect(useQuickCommandStore.getState().global).toHaveLength(3)
  })

  it('removeCommand from global', () => {
    useQuickCommandStore.getState().removeCommand('start-codex')
    expect(useQuickCommandStore.getState().global).toHaveLength(1)
  })

  it('updateCommand in global', () => {
    useQuickCommandStore.getState().updateCommand('start-cc', { name: 'CC Updated' })
    const cmd = useQuickCommandStore.getState().global.find((c) => c.id === 'start-cc')!
    expect(cmd.name).toBe('CC Updated')
    expect(cmd.command).toBe('claude -p --verbose --output-format stream-json')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/stores/useQuickCommandStore.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Add storage key**

In `spa/src/lib/storage/keys.ts`, add:

```ts
  QUICK_COMMANDS: 'purdex-quick-commands',
```

after the `MODULE_CONFIG` line.

- [ ] **Step 4: Implement the store**

Create `spa/src/stores/useQuickCommandStore.ts`:

```ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

export interface QuickCommand {
  id: string
  name: string
  command: string
  icon?: string
  category?: string
  hostOnly?: boolean
}

interface QuickCommandState {
  global: QuickCommand[]
  byHost: Record<string, QuickCommand[]>

  addCommand: (cmd: QuickCommand, hostId?: string) => void
  updateCommand: (id: string, patch: Partial<QuickCommand>, hostId?: string) => void
  removeCommand: (id: string, hostId?: string) => void
  getCommands: (hostId: string) => QuickCommand[]
}

const DEFAULT_COMMANDS: QuickCommand[] = [
  { id: 'start-cc', name: 'Start Claude Code', command: 'claude -p --verbose --output-format stream-json', category: 'agent' },
  { id: 'start-codex', name: 'Start Codex', command: 'codex', category: 'agent' },
]

export const useQuickCommandStore = create<QuickCommandState>()(
  persist(
    (set, get) => ({
      global: DEFAULT_COMMANDS,
      byHost: {},

      addCommand: (cmd, hostId) =>
        set((state) => {
          if (hostId) {
            const hostCmds = [...(state.byHost[hostId] ?? []), cmd]
            return { byHost: { ...state.byHost, [hostId]: hostCmds } }
          }
          return { global: [...state.global, cmd] }
        }),

      updateCommand: (id, patch, hostId) =>
        set((state) => {
          const update = (cmds: QuickCommand[]) =>
            cmds.map((c) => (c.id === id ? { ...c, ...patch } : c))
          if (hostId) {
            const hostCmds = update(state.byHost[hostId] ?? [])
            return { byHost: { ...state.byHost, [hostId]: hostCmds } }
          }
          return { global: update(state.global) }
        }),

      removeCommand: (id, hostId) =>
        set((state) => {
          if (hostId) {
            const hostCmds = (state.byHost[hostId] ?? []).filter((c) => c.id !== id)
            return { byHost: { ...state.byHost, [hostId]: hostCmds } }
          }
          return { global: state.global.filter((c) => c.id !== id) }
        }),

      getCommands: (hostId) => {
        const { global, byHost } = get()
        const hostCmds = byHost[hostId] ?? []
        if (hostCmds.length === 0) return global

        const merged = [
          ...global.map((g) => {
            const override = hostCmds.find((h) => h.id === g.id)
            return override ?? g
          }),
          ...hostCmds.filter((h) => !global.some((g) => g.id === h.id)),
        ]
        return merged
      },
    }),
    {
      name: STORAGE_KEYS.QUICK_COMMANDS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        global: state.global,
        byHost: state.byHost,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.QUICK_COMMANDS, useQuickCommandStore)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd spa && npx vitest run src/stores/useQuickCommandStore.test.ts`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/stores/useQuickCommandStore.ts spa/src/stores/useQuickCommandStore.test.ts spa/src/lib/storage/keys.ts
git commit -m "feat: QuickCommandStore with global + per-host persist"
```

---

### Task 4: useCommands hook + executeCommand helper

**Files:**
- Create: `spa/src/hooks/useCommands.ts`
- Create: `spa/src/hooks/useCommands.test.ts`
- Create: `spa/src/lib/execute-command.ts`

- [ ] **Step 1: Write the failing test**

Create `spa/src/hooks/useCommands.test.ts`:

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { renderHook } from '@testing-library/react'
import { useCommands } from './useCommands'
import { useQuickCommandStore } from '../stores/useQuickCommandStore'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'

describe('useCommands', () => {
  beforeEach(() => {
    clearModuleRegistry()
    useQuickCommandStore.setState({
      global: [{ id: 'g1', name: 'Global 1', command: 'echo global' }],
      byHost: {},
    })
  })

  it('returns store commands with source "store"', () => {
    const { result } = renderHook(() => useCommands({ hostId: 'h1' }))
    expect(result.current).toHaveLength(1)
    expect(result.current[0].source).toBe('store')
    expect(result.current[0].command).toBe('echo global')
  })

  it('includes module contributions with source = module id', () => {
    registerModule({
      id: 'test-mod',
      name: 'Test',
      commands: [{ id: 'mc1', name: 'Module Cmd', command: 'echo module' }],
    })
    const { result } = renderHook(() => useCommands({ hostId: 'h1' }))
    expect(result.current).toHaveLength(2)
    expect(result.current[1].source).toBe('test-mod')
  })

  it('resolves function commands with context', () => {
    registerModule({
      id: 'dyn-mod',
      name: 'Dynamic',
      commands: [{ id: 'dyn', name: 'Dynamic', command: (ctx) => `cd ${ctx.hostId}` }],
    })
    const { result } = renderHook(() => useCommands({ hostId: 'my-host' }))
    const dyn = result.current.find((c) => c.id === 'dyn')!
    expect(dyn.command).toBe('cd my-host')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/hooks/useCommands.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Create executeCommand helper**

Create `spa/src/lib/execute-command.ts`:

```ts
import { hostFetch } from './host-api'

export async function executeCommand(hostId: string, sessionCode: string, command: string): Promise<void> {
  const res = await hostFetch(hostId, `/api/sessions/${sessionCode}/send-keys`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ keys: command + '\n' }),
  })
  if (!res.ok) throw new Error(`send-keys failed: ${res.status}`)
}
```

- [ ] **Step 4: Create useCommands hook**

Create `spa/src/hooks/useCommands.ts`:

```ts
import { useMemo } from 'react'
import { useQuickCommandStore } from '../stores/useQuickCommandStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { getModulesWithCommands, type CommandContext } from '../lib/module-registry'

export interface ResolvedCommand {
  id: string
  name: string
  command: string
  icon?: string
  category?: string
  source: string // 'store' or module id
}

export function useCommands(filter: { hostId: string; workspaceId?: string | null }): ResolvedCommand[] {
  const storeCmds = useQuickCommandStore((s) => s.getCommands(filter.hostId))
  const workspaces = useWorkspaceStore((s) => s.workspaces)

  return useMemo(() => {
    // 1. Store commands
    const resolved: ResolvedCommand[] = storeCmds.map((c) => ({
      id: c.id,
      name: c.name,
      command: c.command,
      icon: c.icon,
      category: c.category,
      source: 'store',
    }))

    // 2. Module contributions
    const ws = filter.workspaceId
      ? workspaces.find((w) => w.id === filter.workspaceId)
      : undefined

    const modulesWithCmds = getModulesWithCommands()
    for (const mod of modulesWithCmds) {
      if (!mod.commands) continue
      const ctx: CommandContext = {
        hostId: filter.hostId,
        workspaceId: filter.workspaceId,
        moduleConfig: ws?.moduleConfig?.[mod.id],
      }
      for (const contrib of mod.commands) {
        const command = typeof contrib.command === 'function'
          ? contrib.command(ctx)
          : contrib.command
        resolved.push({
          id: contrib.id,
          name: contrib.name,
          command,
          icon: contrib.icon,
          category: contrib.category,
          source: mod.id,
        })
      }
    }

    return resolved
  }, [storeCmds, workspaces, filter.hostId, filter.workspaceId])
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd spa && npx vitest run src/hooks/useCommands.test.ts`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/hooks/useCommands.ts spa/src/hooks/useCommands.test.ts spa/src/lib/execute-command.ts
git commit -m "feat: useCommands hook + executeCommand helper"
```

---

### Task 5: QuickCommandMenu shared component

**Files:**
- Create: `spa/src/components/QuickCommandMenu.tsx`

- [ ] **Step 1: Create the dropdown menu component**

Create `spa/src/components/QuickCommandMenu.tsx`:

```tsx
import { useState, useRef, useEffect } from 'react'
import { Lightning, CaretDown } from '@phosphor-icons/react'
import { useCommands, type ResolvedCommand } from '../hooks/useCommands'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  hostId: string
  workspaceId?: string | null
  onExecute: (cmd: ResolvedCommand) => void
  disabled?: boolean
}

export function QuickCommandMenu({ hostId, workspaceId, onExecute, disabled }: Props) {
  const t = useI18nStore((s) => s.t)
  const commands = useCommands({ hostId, workspaceId })
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const handle = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handle)
    return () => document.removeEventListener('mousedown', handle)
  }, [open])

  if (commands.length === 0) return null

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(!open)}
        disabled={disabled}
        className="flex items-center gap-1 px-2 py-1 rounded text-xs text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer disabled:opacity-50"
        title="Quick Commands"
      >
        <Lightning size={14} />
        <CaretDown size={10} />
      </button>
      {open && (
        <div className="absolute top-full left-0 mt-1 w-56 bg-surface-secondary border border-border-default rounded-lg shadow-lg z-50 py-1">
          {commands.map((cmd) => (
            <button
              key={`${cmd.source}-${cmd.id}`}
              onClick={() => { onExecute(cmd); setOpen(false) }}
              className="w-full text-left px-3 py-1.5 text-xs text-text-primary hover:bg-surface-tertiary cursor-pointer flex items-center gap-2"
            >
              <span className="flex-1 truncate">{cmd.name}</span>
              {cmd.category && (
                <span className="text-[10px] text-text-muted bg-surface-primary px-1.5 py-0.5 rounded">{cmd.category}</span>
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`
Expected: clean

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/QuickCommandMenu.tsx
git commit -m "feat: QuickCommandMenu shared dropdown component"
```

---

### Task 6: Integrate into SessionsSection

**Files:**
- Modify: `spa/src/components/hosts/SessionsSection.tsx`

- [ ] **Step 1: Add QuickCommandMenu to session row actions**

In `spa/src/components/hosts/SessionsSection.tsx`, add import at top:

```ts
import { QuickCommandMenu } from '../QuickCommandMenu'
import { executeCommand } from '../../lib/execute-command'
import type { ResolvedCommand } from '../../hooks/useCommands'
```

In the session row action buttons (`<div className="flex items-center justify-end gap-1">`), add the QuickCommandMenu before the existing Play button:

```tsx
<QuickCommandMenu
  hostId={hostId}
  onExecute={async (cmd: ResolvedCommand) => {
    try {
      await executeCommand(hostId, session.code, cmd.command)
    } catch { /* ignore */ }
  }}
  disabled={isOffline}
/>
```

- [ ] **Step 2: Run lint + test**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: all pass

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/hosts/SessionsSection.tsx
git commit -m "feat: QuickCommandMenu in SessionsSection"
```

---

### Task 7: Integrate into terminal PaneHeader

**Files:**
- Modify: `spa/src/components/PaneLayoutRenderer.tsx` (pane header rendering)

- [ ] **Step 1: Add QuickCommandMenu to PaneHeader area**

In `spa/src/components/PaneLayoutRenderer.tsx`, add imports:

```tsx
import { QuickCommandMenu } from './QuickCommandMenu'
import { executeCommand } from '../lib/execute-command'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
```

In the `showHeader` branch (around L47-68), after `<PaneHeader ... />` and before the pane content `<Component .../>`, add conditionally for tmux-session panes:

```tsx
{layout.pane.content.kind === 'tmux-session' && (() => {
  const content = layout.pane.content as { hostId: string; sessionCode: string }
  return (
    <QuickCommandMenu
      hostId={content.hostId}
      workspaceId={useWorkspaceStore.getState().activeWorkspaceId}
      onExecute={async (cmd) => {
        try { await executeCommand(content.hostId, content.sessionCode, cmd.command) } catch { /* ignore */ }
      }}
    />
  )
})()}
```

Note: Read the actual `PaneLayoutRenderer.tsx` to determine the exact insertion point within the header area. The `content` fields (`hostId`, `sessionCode`) are available on `tmux-session` pane content objects (defined in `spa/src/types/tab.ts`).

- [ ] **Step 2: Run lint + test**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: all pass

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/PaneLayoutRenderer.tsx
git commit -m "feat: QuickCommandMenu in terminal PaneHeader"
```

---

### Task 8: Full verification

- [ ] **Step 1: Run all SPA tests**

Run: `cd spa && npx vitest run`
Expected: all pass (127+ test files)

- [ ] **Step 2: Run SPA lint**

Run: `cd spa && pnpm run lint`
Expected: clean

- [ ] **Step 3: Run all Go tests**

Run: `go test ./...`
Expected: all pass

- [ ] **Step 4: Go build**

Run: `go build ./...`
Expected: clean
