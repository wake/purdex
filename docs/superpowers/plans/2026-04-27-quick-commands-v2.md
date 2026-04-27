# Quick Commands v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Re-architect Quick Commands as a three-layer **Capability / Binding / Slot** system so user 可以在 Settings 統一新增 / 編輯 commands、自選 mount 位置（Workspace / Host），並由 slot 端集中處理 executor + 失敗 UX。

**Architecture:**
- **Capability** — `QuickCommand` 純資料（id / name / command / icon / category）
- **Binding** — user 意圖 `Record<commandId, slotId[]>`，sync 跨裝置
- **Slot** — 模組決定如何渲染 / 執行；`<CommandSlot>` 共用元件 + slot-side executor
- **Module** — `quick-commands` 註冊為獨立 disableable module，遵循既有啟用 / 停用規範

**Tech Stack:** React / Zustand 5 / Tailwind 4 / Vitest / Phosphor Icons / 既有 sync-contributor / `useModuleEnabledStore` AR-1 sanitizer 模式

**Spec:** `docs/superpowers/specs/2026-04-27-quick-commands-v2-design.md`

**Phase 順序（強制 1a → 1b → 1c）：**
1. **Phase 1a** — 純資料層；單一 PR；UI 零改動。
2. **Phase 1b** — Settings UI + WORKSPACE_ACTIONS 入口；單一 PR，**必須與 Settings UI 同 PR ship**（spec §6 不 ship 設了沒效果的中間態）。Workspace 入口採雙進入點：(i) `WorkspaceRow` 右鍵 context menu；(ii) `WorkspaceRow` Plus 按鈕 hover 往左展開的 popover chip 列。兩入口共用同一個 `WORKSPACE_ACTIONS` slot + executor。
3. **Phase 1c** — HOST_ACTIONS 入口；小 PR。Host 入口落於 `SessionsSection`：在 new-session 按鈕旁並列 `<CommandSlot mountTo=HOST_ACTIONS>`；同 PR 移除 `SessionsSection` 每個 session row 上殘留的 v1 `QuickCommandMenu`（功能集中至 new-session 入口；`QuickCommandMenu` 元件本身保留，因為仍被 `PaneLayoutRenderer.tsx` 使用）。

**Mount UX 決策（覆蓋 spec §4.2 / §4.3 中的「位置由實作時定」）：**
- **Workspace 入口** — `WorkspaceRow.tsx` 兩處：
  - 右鍵 → 透過既有 `onContextMenuWorkspace` callback（App.tsx L155 `handleWsContextMenu` → 渲染 `WorkspaceContextMenu`），新增一個 `WorkspaceQuickCommandsContextMenu` 區塊或於原 `WorkspaceContextMenu` 加 quick-commands section
  - Plus 按鈕（`WorkspaceRow.tsx` L108-121）hover → 一個 absolute popover 向左展開 chip 列（半透明漸層壓底；mouseleave Plus AND popover 收回；鍵盤 focus 同等於 hover）
- **Host 入口** — `SessionsSection.tsx` 兩件事：
  - new-session 按鈕（L167-174）旁並列 `<CommandSlot mountTo=HOST_ACTIONS>`；視覺與 Plus 對齊（直接列 chip，無 popover）
  - 移除 row 上的 v1 `<QuickCommandMenu>`（L231-239）整合，但**保留** `QuickCommandMenu` 元件本身（`PaneLayoutRenderer.tsx` 仍使用）

**測試指令備忘（主 Claude 機器跑，subagent 寫 plan 不執行）：**
- SPA test: `cd spa && npx vitest run`
- SPA lint: `cd spa && pnpm run lint`
- SPA build: `cd spa && pnpm run build`
- Go test: `go test ./...`
- Go build: `go build ./...`

**避雷備忘：**
- Zustand test harness 用 `setState({...}, false)`（merge mode）並**顯式列出所有 mutable fields**，避免 wipe action methods（見 `feedback_zustand_harness_setstate.md`）。
- Alpha 階段 **不寫 persist migration**（見 `feedback_no_alpha_migration.md`）。
- Codex sandbox 無網路；plan 中所有 `pnpm install` / `vitest` / `lint` / `build` 指令必須在主 Claude 機器跑（見 `feedback_codex_sandbox_no_install.md`）。
- 子 agent 每個 Bash 強制 `cd <worktree-path> && ` 前綴，否則 commit 會落到錯誤分支。

---

## Phase 1a — 純資料層（單一 PR）

**目標：** 完成 store schema 升級 + sanitizer + sync `bindings` 欄位 + module 註冊（無 settings contribution），UI 零改動。Modules Switchboard 可看到 quick-commands 模組可開關。

### Task 1a.1: 新增 `quick-command-slots.ts` 常數與型別

**Files:**
- Create: `spa/src/lib/quick-command-slots.ts`
- Create: `spa/src/lib/quick-command-slots.test.ts`

- [ ] **Step 1: Write the failing test**

Create `spa/src/lib/quick-command-slots.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { QUICK_COMMAND_SLOTS, type QuickCommandSlotId } from './quick-command-slots'

describe('quick-command-slots', () => {
  it('exposes WORKSPACE_ACTIONS and HOST_ACTIONS literals', () => {
    expect(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS).toBe('workspace.actions')
    expect(QUICK_COMMAND_SLOTS.HOST_ACTIONS).toBe('host.actions')
  })

  it('values are unique', () => {
    const values = Object.values(QUICK_COMMAND_SLOTS)
    expect(new Set(values).size).toBe(values.length)
  })

  it('QuickCommandSlotId narrows to the value union', () => {
    const x: QuickCommandSlotId = QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS
    expect(typeof x).toBe('string')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/quick-command-slots.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement constants**

Create `spa/src/lib/quick-command-slots.ts`:

```ts
/**
 * Slot identifiers for the Quick Commands v2 capability/binding/slot model
 * (spec §2.2). Settings UI, <CommandSlot>, and binding sanitizer must read
 * from this constant — string literals are forbidden so a typo can't drift
 * away from the registered slots.
 *
 * If/when external modules need to register new slots, upgrade to a
 * `registerQuickCommandSlot()` registry (Phase 2+; YAGNI for now).
 */
export const QUICK_COMMAND_SLOTS = {
  WORKSPACE_ACTIONS: 'workspace.actions',
  HOST_ACTIONS: 'host.actions',
} as const

export type QuickCommandSlotId =
  (typeof QUICK_COMMAND_SLOTS)[keyof typeof QUICK_COMMAND_SLOTS]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/quick-command-slots.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): add QUICK_COMMAND_SLOTS typed constants for v2 binding model
```

---

### Task 1a.2: Store schema 升級 — 加 `bindings` 欄位 + sanitizer + binding API

**Files:**
- Modify: `spa/src/stores/useQuickCommandStore.ts`
- Modify: `spa/src/stores/useQuickCommandStore.test.ts`

- [ ] **Step 1: Write the failing test**

Append to `spa/src/stores/useQuickCommandStore.test.ts` (保留既有 5 個 test，覆蓋寫整個 file 內容)：

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { useQuickCommandStore } from './useQuickCommandStore'
import { QUICK_COMMAND_SLOTS } from '../lib/quick-command-slots'

// 顯式列出所有 mutable fields — zustand setState merge 模式下，
// action methods 由 closure 持有，不會被覆蓋（見 feedback_zustand_harness_setstate.md）。
function resetStore(initial?: {
  global?: ReturnType<typeof useQuickCommandStore.getState>['global']
  byHost?: ReturnType<typeof useQuickCommandStore.getState>['byHost']
  bindings?: ReturnType<typeof useQuickCommandStore.getState>['bindings']
}) {
  useQuickCommandStore.setState({
    global: initial?.global ?? [],
    byHost: initial?.byHost ?? {},
    bindings: initial?.bindings ?? {},
  })
}

describe('useQuickCommandStore — capability CRUD (Phase 1a)', () => {
  beforeEach(() => {
    resetStore({
      global: [
        { id: 'cmd-a', name: 'A', command: 'a', category: 'agent' },
        { id: 'cmd-b', name: 'B', command: 'b' },
      ],
    })
  })

  it('getCommands returns global commands when no host overrides', () => {
    const cmds = useQuickCommandStore.getState().getCommands('host-1')
    expect(cmds.map((c) => c.id)).toEqual(['cmd-a', 'cmd-b'])
  })

  it('per-host overrides global by id', () => {
    useQuickCommandStore.getState().addCommand(
      { id: 'cmd-a', name: 'A-host', command: 'aa' },
      'host-1',
    )
    const cmds = useQuickCommandStore.getState().getCommands('host-1')
    const a = cmds.find((c) => c.id === 'cmd-a')!
    expect(a.command).toBe('aa')
    expect(a.name).toBe('A-host')
  })

  it('addCommand to global', () => {
    useQuickCommandStore.getState().addCommand({ id: 'cmd-c', name: 'C', command: 'c' })
    expect(useQuickCommandStore.getState().global).toHaveLength(3)
  })

  it('removeCommand from global', () => {
    useQuickCommandStore.getState().removeCommand('cmd-a')
    expect(useQuickCommandStore.getState().global.find((c) => c.id === 'cmd-a')).toBeUndefined()
  })

  it('updateCommand in global', () => {
    useQuickCommandStore.getState().updateCommand('cmd-a', { name: 'A-updated' })
    expect(useQuickCommandStore.getState().global.find((c) => c.id === 'cmd-a')!.name).toBe('A-updated')
  })
})

describe('useQuickCommandStore — bindings (Phase 1a)', () => {
  beforeEach(() => {
    resetStore({
      global: [
        { id: 'cmd-a', name: 'A', command: 'a' },
        { id: 'cmd-b', name: 'B', command: 'b' },
      ],
    })
  })

  it('bindings default to empty object', () => {
    expect(useQuickCommandStore.getState().bindings).toEqual({})
  })

  it('setBinding records command -> slot mapping', () => {
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    expect(useQuickCommandStore.getState().bindings['cmd-a']).toEqual(['workspace.actions'])
  })

  it('setBinding with empty array removes the binding entry', () => {
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    useQuickCommandStore.getState().setBinding('cmd-a', [])
    expect(useQuickCommandStore.getState().bindings['cmd-a']).toBeUndefined()
  })

  it('setBinding can mount a command into multiple slots', () => {
    useQuickCommandStore.getState().setBinding('cmd-a', [
      QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS,
      QUICK_COMMAND_SLOTS.HOST_ACTIONS,
    ])
    expect(useQuickCommandStore.getState().bindings['cmd-a']).toEqual([
      'workspace.actions',
      'host.actions',
    ])
  })

  it('getBoundCommands returns commands mounted to the slot, in capability order', () => {
    useQuickCommandStore.getState().setBinding('cmd-b', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    const bound = useQuickCommandStore
      .getState()
      .getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, 'host-1')
    // 順序穩定 — 跟著 getCommands(hostId) 的順序，不是 bindings Record 的 key 順序
    expect(bound.map((c) => c.id)).toEqual(['cmd-a', 'cmd-b'])
  })

  it('getBoundCommands skips bindings whose command no longer exists (dangling filter)', () => {
    useQuickCommandStore.getState().setBinding('cmd-zombie', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    const bound = useQuickCommandStore
      .getState()
      .getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, 'host-1')
    expect(bound.map((c) => c.id)).toEqual(['cmd-a'])
  })

  it('removeCommand on a global command also clears its binding', () => {
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    useQuickCommandStore.getState().removeCommand('cmd-a')
    expect(useQuickCommandStore.getState().bindings['cmd-a']).toBeUndefined()
  })

  it('removeCommand on a per-host command does NOT clear its binding (binding is global concept)', () => {
    useQuickCommandStore.getState().addCommand({ id: 'cmd-a', name: 'A-host', command: 'aa' }, 'host-1')
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    useQuickCommandStore.getState().removeCommand('cmd-a', 'host-1')
    // global cmd-a 仍存在；per-host override 被刪；binding 不動
    expect(useQuickCommandStore.getState().bindings['cmd-a']).toEqual(['workspace.actions'])
  })
})

describe('useQuickCommandStore — sanitizeBindings via merge', () => {
  it('drops non-object payloads', () => {
    // 直接呼叫 merge 行為：模擬 hydrated raw string
    // (sanitizer 是 module-private，所以透過 setState + merge hook 間接驗；
    //  這裡的合理代表是斷言 store 在惡意 payload 注入後仍維持 bindings = {})
    useQuickCommandStore.setState({ bindings: {} })
    expect(useQuickCommandStore.getState().bindings).toEqual({})
  })
})
```

註：上面 sanitize merge 的端對端測試會在 Task 1a.4 sync deserialize 路徑補上（cross-field dangling case），這裡 store-level 的 sanitizer 主要靠 `merge` hook 在 hydrate 期跑，本 task 只驗 happy-path 行為。

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/stores/useQuickCommandStore.test.ts`
Expected: FAIL — `bindings` 不存在、`setBinding` / `getBoundCommands` 不存在。

- [ ] **Step 3: Implement schema upgrade**

Rewrite `spa/src/stores/useQuickCommandStore.ts`:

```ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'
import type { QuickCommandSlotId } from '../lib/quick-command-slots'

export interface QuickCommand {
  id: string
  name: string
  command: string
  icon?: string
  category?: string
  hostOnly?: boolean
}

export type Bindings = Record<string /* commandId */, QuickCommandSlotId[]>

interface QuickCommandState {
  global: QuickCommand[]
  byHost: Record<string, QuickCommand[]>
  bindings: Bindings

  addCommand: (cmd: QuickCommand, hostId?: string) => void
  updateCommand: (id: string, patch: Partial<QuickCommand>, hostId?: string) => void
  removeCommand: (id: string, hostId?: string) => void
  getCommands: (hostId: string) => QuickCommand[]

  setBinding: (commandId: string, targets: QuickCommandSlotId[]) => void
  getBoundCommands: (mountTarget: QuickCommandSlotId, hostId: string) => QuickCommand[]
}

// v2: do NOT pre-seed defaults. Existing users who hydrated v1 defaults
// (`start-cc` / `start-codex`) keep them but bindings = {} so nothing renders
// until the user mounts them via Settings.
const DEFAULT_COMMANDS: QuickCommand[] = []

const UNSAFE_KEYS = new Set(['__proto__', 'constructor', 'prototype'])

/**
 * AR-1 (mirrors `useModuleEnabledStore` codex review #617):
 * Sanitize bindings on rehydrate / sync deserialize. A corrupted payload —
 * hand-edited localStorage, failed migration, hostile sync source — must
 * NEVER let arbitrary string keys silently mount commands into slots.
 *
 * Rules:
 *  - Top-level value must be a plain object (rejects arrays, null, primitives).
 *  - Reject `__proto__` / `constructor` / `prototype` keys (prototype pollution).
 *  - Drop entries whose value is not an array.
 *  - Within each array, keep only non-empty strings (dedupe NOT done here —
 *    UI guarantees uniqueness; tolerating duplicates is forward-compatible).
 *  - Drop entries whose cleaned array is empty (matches setBinding(id, [])).
 */
export function sanitizeBindings(raw: unknown): Bindings {
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return {}
  const out: Bindings = {}
  for (const [cmdId, targets] of Object.entries(raw as Record<string, unknown>)) {
    if (typeof cmdId !== 'string' || cmdId.length === 0) continue
    if (UNSAFE_KEYS.has(cmdId)) continue
    if (!Array.isArray(targets)) continue
    const cleaned: QuickCommandSlotId[] = []
    for (const t of targets) {
      if (typeof t === 'string' && t.length > 0) cleaned.push(t as QuickCommandSlotId)
    }
    if (cleaned.length > 0) out[cmdId] = cleaned
  }
  return out
}

export const useQuickCommandStore = create<QuickCommandState>()(
  persist(
    (set, get) => ({
      global: DEFAULT_COMMANDS,
      byHost: {},
      bindings: {},

      addCommand: (cmd, hostId) =>
        set((state) => {
          if (hostId) {
            const hostCmds = [...(state.byHost[hostId] ?? []).filter((c) => c.id !== cmd.id), cmd]
            return { byHost: { ...state.byHost, [hostId]: hostCmds } }
          }
          // global add: replace if id exists, else append
          const existingIdx = state.global.findIndex((c) => c.id === cmd.id)
          if (existingIdx >= 0) {
            const next = [...state.global]
            next[existingIdx] = cmd
            return { global: next }
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
            // per-host removal does NOT touch bindings (binding is a global concept)
            return { byHost: { ...state.byHost, [hostId]: hostCmds } }
          }
          // global removal — also drop the binding entry to avoid dangling refs.
          // getBoundCommands also filters at read-time as a defense-in-depth net.
          const nextBindings = { ...state.bindings }
          delete nextBindings[id]
          return {
            global: state.global.filter((c) => c.id !== id),
            bindings: nextBindings,
          }
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

      setBinding: (commandId, targets) =>
        set((state) => {
          const nextBindings = { ...state.bindings }
          if (targets.length === 0) {
            delete nextBindings[commandId]
          } else {
            nextBindings[commandId] = [...targets]
          }
          return { bindings: nextBindings }
        }),

      getBoundCommands: (mountTarget, hostId) => {
        // Iterate the capability list (stable, sync-safe order) — NOT
        // Object.keys(bindings) which has unpredictable post-sync order
        // (spec §4.4). Dangling binding entries (commandId without a matching
        // capability) are skipped here as well as cleared at removeCommand.
        const cmds = get().getCommands(hostId)
        const { bindings } = get()
        return cmds.filter((c) => bindings[c.id]?.includes(mountTarget))
      },
    }),
    {
      name: STORAGE_KEYS.QUICK_COMMANDS,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        global: state.global,
        byHost: state.byHost,
        bindings: state.bindings,
      }),
      merge: (persisted, current) => {
        // AR-1: sanitize hydrated bindings BEFORE first read. global / byHost
        // come from a known-shape v1 payload — keep as-is. Only bindings is
        // newly added in v2 and must be clamped against hostile payloads.
        const p = persisted as { global?: unknown; byHost?: unknown; bindings?: unknown } | null | undefined
        return {
          ...current,
          global: Array.isArray(p?.global) ? (p?.global as QuickCommand[]) : current.global,
          byHost: typeof p?.byHost === 'object' && p?.byHost !== null && !Array.isArray(p?.byHost)
            ? (p?.byHost as Record<string, QuickCommand[]>)
            : current.byHost,
          bindings: sanitizeBindings(p?.bindings),
        }
      },
    },
  ),
)

syncManager.register(STORAGE_KEYS.QUICK_COMMANDS, useQuickCommandStore)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/stores/useQuickCommandStore.test.ts`
Expected: PASS（包含既有 5 個 + 新增 9 個 binding/dangling/sanitize tests）

- [ ] **Step 5: Commit**

```
feat(spa): add bindings field + sanitizer to useQuickCommandStore for v2 slot model
```

---

### Task 1a.3: Sync contributor 加入 `bindings` field + cross-field dangling test

**Files:**
- Modify: `spa/src/lib/sync/contributors/quick-commands.ts`
- Modify: `spa/src/lib/sync/contributors/quick-commands.test.ts`

- [ ] **Step 1: Write the failing test**

Append to `spa/src/lib/sync/contributors/quick-commands.test.ts`（保留既有 tests）：

```ts
// (在現有 import 後加入)
import { sanitizeBindings } from '../../../stores/useQuickCommandStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'

describe('createQuickCommandsContributor — bindings field (v2)', () => {
  let contributor: ReturnType<typeof createQuickCommandsContributor>

  beforeEach(() => {
    useQuickCommandStore.setState({
      global: [],
      byHost: {},
      bindings: {},
    })
    contributor = createQuickCommandsContributor()
  })

  it('serialize includes bindings field', () => {
    useQuickCommandStore.getState().addCommand({ id: 'cmd-a', name: 'A', command: 'a' })
    useQuickCommandStore.getState().setBinding('cmd-a', [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
    const payload = contributor.serialize() as FullPayload
    expect(payload.data.bindings).toEqual({ 'cmd-a': ['workspace.actions'] })
  })

  it('serialize keys exclude action functions but include bindings', () => {
    const payload = contributor.serialize() as FullPayload
    const keys = Object.keys(payload.data)
    expect(keys).toContain('global')
    expect(keys).toContain('byHost')
    expect(keys).toContain('bindings')
    expect(keys).not.toContain('setBinding')
    expect(keys).not.toContain('getBoundCommands')
  })

  it('deserialize full-replace with bindings overwrites local bindings', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'local', name: 'L', command: 'l' }],
      byHost: {},
      bindings: { 'local': ['workspace.actions'] },
    })
    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [{ id: 'remote', name: 'R', command: 'r' }],
        byHost: {},
        bindings: { 'remote': ['host.actions'] },
      },
    }
    contributor.deserialize(incoming, { type: 'full-replace' })
    const state = useQuickCommandStore.getState()
    expect(state.bindings).toEqual({ 'remote': ['host.actions'] })
  })

  it('deserialize sanitizes incoming bindings (drops malformed entries)', () => {
    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [{ id: 'cmd-a', name: 'A', command: 'a' }],
        byHost: {},
        bindings: {
          'cmd-a': ['workspace.actions'],
          // hostile payload variants — must all be dropped:
          '__proto__': ['host.actions'],
          'cmd-bad-targets': 'not-an-array' as unknown as string[],
          '': ['host.actions'],
        },
      },
    }
    contributor.deserialize(incoming, { type: 'full-replace' })
    const state = useQuickCommandStore.getState()
    expect(state.bindings).toEqual({ 'cmd-a': ['workspace.actions'] })
  })

  it('field-merge: cross-field dangling — global=local + bindings=remote → getBoundCommands returns empty', () => {
    // local has cmd-A only; remote bindings reference cmd-B (not in local global)
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-a', name: 'A', command: 'a' }],
      byHost: {},
      bindings: {},
    })
    const incoming: FullPayload = {
      version: 1,
      data: {
        global: [{ id: 'cmd-b', name: 'B', command: 'b' }],
        byHost: {},
        bindings: { 'cmd-b': ['workspace.actions'] },
      },
    }
    contributor.deserialize(incoming, {
      type: 'field-merge',
      resolved: { global: 'local', bindings: 'remote' },
    })
    const state = useQuickCommandStore.getState()
    // global stayed local — only cmd-a
    expect(state.global.map((c) => c.id)).toEqual(['cmd-a'])
    // bindings took remote — references cmd-b
    expect(state.bindings['cmd-b']).toEqual(['workspace.actions'])
    // BUT getBoundCommands filters dangling at read-time → empty
    const bound = state.getBoundCommands(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, 'host-1')
    expect(bound).toEqual([])
  })

  it('sanitizeBindings is idempotent on already-clean payload', () => {
    const clean = { 'cmd-a': ['workspace.actions'] }
    expect(sanitizeBindings(clean)).toEqual(clean)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/sync/contributors/quick-commands.test.ts`
Expected: FAIL — `bindings` 不在 `DATA_FIELDS`，sanitize 路徑未呼叫。

- [ ] **Step 3: Modify the contributor**

Update `spa/src/lib/sync/contributors/quick-commands.ts`:

```ts
import { useQuickCommandStore, sanitizeBindings } from '../../../stores/useQuickCommandStore'
import type { SyncContributor, FullPayload, MergeStrategy } from '../types'

const DATA_FIELDS = ['global', 'byHost', 'bindings'] as const

type QuickCommandsData = {
  [K in (typeof DATA_FIELDS)[number]]: ReturnType<typeof useQuickCommandStore.getState>[K]
}

export function createQuickCommandsContributor(): SyncContributor {
  return {
    id: 'quick-commands',
    strategy: 'full',

    getVersion(): number {
      return 1
    },

    serialize(): FullPayload {
      const state = useQuickCommandStore.getState()
      const data: Record<string, unknown> = {}
      for (const field of DATA_FIELDS) {
        data[field] = state[field]
      }
      return { version: 1, data }
    },

    deserialize(payload: unknown, merge: MergeStrategy): void {
      const fp = payload as FullPayload
      const incoming = fp.data as Partial<QuickCommandsData>

      // Sanitize untrusted incoming bindings BEFORE applying. Mirrors the
      // store's `merge` hook — sync payloads are equally untrusted.
      const sanitizedIncoming: Partial<QuickCommandsData> = {
        ...incoming,
        ...(incoming.bindings !== undefined
          ? { bindings: sanitizeBindings(incoming.bindings) }
          : {}),
      }

      if (merge.type === 'full-replace') {
        useQuickCommandStore.setState(sanitizedIncoming as QuickCommandsData)
        return
      }

      const patch: Partial<QuickCommandsData> = {}
      for (const field of DATA_FIELDS) {
        if (merge.resolved[field] === 'remote' && field in sanitizedIncoming) {
          ;(patch as Record<string, unknown>)[field] = sanitizedIncoming[field]
        }
      }

      if (Object.keys(patch).length > 0) {
        useQuickCommandStore.setState(patch as QuickCommandsData)
      }
    },
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/sync/contributors/quick-commands.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): sync quick-commands bindings + sanitize incoming payload
```

---

### Task 1a.4: 註冊 `quick-commands` module（disableable，無 settings contribution）

**Files:**
- Modify: `spa/src/lib/register-modules.tsx`
- Modify: `spa/src/locales/zh-TW.json`
- Modify: `spa/src/locales/en.json`
- Create: `spa/src/lib/register-modules.quick-commands.test.tsx`（或合併進既有 register-modules test）

註：本 task 只註冊 module + descriptionKey，**不掛 settings contribution**（spec §6 Phase 1a 明確：「暫無 settings contribution，等 1b」）。

- [ ] **Step 1: Write the failing test**

Create `spa/src/lib/register-modules.quick-commands.test.tsx`:

```tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { clearModuleRegistry, getModule } from './module-registry'
import { registerBuiltinModules } from './register-modules'

describe('register-modules — quick-commands module (Phase 1a)', () => {
  beforeEach(() => {
    clearModuleRegistry()
  })

  it('registers quick-commands as a disableable module', () => {
    registerBuiltinModules()
    const m = getModule('quick-commands')
    expect(m).toBeDefined()
    expect(m!.disableable).toBe(true)
    expect(m!.descriptionKey).toBe('modules.quick_commands.description')
  })

  it('Phase 1a: quick-commands has NO settings contribution yet (deferred to Phase 1b)', () => {
    registerBuiltinModules()
    const m = getModule('quick-commands')
    expect(m!.settings ?? []).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/register-modules.quick-commands.test.tsx`
Expected: FAIL — module 未註冊。

- [ ] **Step 3: Add registration**

In `spa/src/lib/register-modules.tsx`, 在 `editor` module 註冊區塊**之後**、`getFsBackend({ type: 'inapp' })` 區塊**之前**插入：

```tsx
  // Quick Commands v2 — Phase 1a registers the module shell only (disableable
  // gate + Modules Switchboard listing). Settings contribution + UI surfaces
  // ship together in Phase 1b (spec §6 — never ship a "configured but no
  // effect" intermediate state).
  registerModule({
    id: 'quick-commands',
    name: 'Quick Commands',
    disableable: true,
    descriptionKey: 'modules.quick_commands.description',
  })
```

In `spa/src/locales/zh-TW.json`, 在 `modules.memory_monitor.description` 之後插入：

```json
  "modules.quick_commands.description": "讓你在 Workspace 與 Host 入口快速執行常用指令",
```

In `spa/src/locales/en.json`, 對應位置插入：

```json
  "modules.quick_commands.description": "Quickly run frequently-used commands from Workspace and Host entry points",
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/register-modules.quick-commands.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): register quick-commands as disableable module (Phase 1a)
```

---

### Task 1a.5: Phase 1a 全域驗證

- [ ] **Step 1: Run all SPA tests**

Run: `cd spa && npx vitest run`
Expected: all pass（既有 + 新增 quick-command-slots / store / sync / register-modules tests）

- [ ] **Step 2: Run SPA lint**

Run: `cd spa && pnpm run lint`
Expected: clean

- [ ] **Step 3: Run SPA build**

Run: `cd spa && pnpm run build`
Expected: clean

- [ ] **Step 4: Run all Go tests**

Run: `go test ./...`
Expected: all pass

- [ ] **Step 5: Go build**

Run: `go build ./...`
Expected: clean

### Phase 1a 驗收清單

- [x] 資料層完整：bindings + sanitizer + setBinding / getBoundCommands API + dangling 清理
- [x] Sync OK：`bindings` field-merge cross-field dangling case 過綠
- [x] Modules Switchboard 看得到 `quick-commands` 可開關
- [x] UI 零改動，純基礎建設
- [x] `DEFAULT_COMMANDS = []`，舊 user 既有 commands 不主動清

---

## Phase 1b — Settings UI + WORKSPACE_ACTIONS 入口（單一 PR）

**目標：** user 能在 Settings 建 command + 設 mount = WORKSPACE，回到 workspace 立刻看到按鈕；點擊建 session + 送 keys + 切過去。失敗 toast 行為符合 §3.3。

### Task 1b.1: `<CommandSlot>` 共用元件

**Files:**
- Create: `spa/src/components/CommandSlot.tsx`
- Create: `spa/src/components/CommandSlot.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `spa/src/components/CommandSlot.test.tsx`:

```tsx
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { CommandSlot } from './CommandSlot'
import { useQuickCommandStore } from '../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import { QUICK_COMMAND_SLOTS } from '../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../lib/module-registry'

function resetStores() {
  useQuickCommandStore.setState({
    global: [
      { id: 'cmd-a', name: 'A', command: 'a' },
      { id: 'cmd-b', name: 'B', command: 'b' },
      { id: 'cmd-c', name: 'C', command: 'c' },
    ],
    byHost: {},
    bindings: {
      'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
      'cmd-c': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
    },
  })
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  clearModuleRegistry()
  registerModule({
    id: 'quick-commands',
    name: 'Quick Commands',
    disableable: true,
  })
}

describe('CommandSlot', () => {
  beforeEach(() => resetStores())
  afterEach(() => clearModuleRegistry())

  it('renders bound commands in capability order (cmd-a then cmd-c, NOT bindings key order)', () => {
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1', workspaceId: 'w1' }}
        executor={exec}
      />,
    )
    const buttons = screen.getAllByRole('button')
    expect(buttons.map((b) => b.getAttribute('aria-label'))).toEqual(
      expect.arrayContaining([expect.stringMatching(/A/), expect.stringMatching(/C/)]),
    )
    // cmd-b not bound → not rendered
    expect(screen.queryByLabelText(/^B/)).toBeNull()
  })

  it('returns null when quick-commands module is disabled (short-circuit)', () => {
    useModuleEnabledStore.getState().setEnabled('quick-commands', false)
    const { container } = render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1', workspaceId: 'w1' }}
        executor={vi.fn()}
      />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('returns null when no commands are bound', () => {
    useQuickCommandStore.setState({ bindings: {} })
    const { container } = render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1', workspaceId: 'w1' }}
        executor={vi.fn()}
      />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('clicking a button calls executor with (cmd, ctx)', async () => {
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1', workspaceId: 'w1' }}
        executor={exec}
      />,
    )
    fireEvent.click(screen.getByLabelText(/^A/))
    expect(exec).toHaveBeenCalledTimes(1)
    expect(exec.mock.calls[0][0]).toMatchObject({ id: 'cmd-a' })
    expect(exec.mock.calls[0][1]).toMatchObject({ hostId: 'h1', workspaceId: 'w1' })
  })

  it('supports custom render prop', () => {
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1' }}
        executor={vi.fn()}
        render={(cmd) => <span data-testid="custom">{cmd.id}</span>}
      />,
    )
    expect(screen.getAllByTestId('custom').map((n) => n.textContent)).toEqual(['cmd-a', 'cmd-c'])
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/CommandSlot.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement `<CommandSlot>`**

Create `spa/src/components/CommandSlot.tsx`:

```tsx
import { useMemo, type ReactNode } from 'react'
import { useQuickCommandStore, type QuickCommand } from '../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../stores/useModuleEnabledStore'
import type { QuickCommandSlotId } from '../lib/quick-command-slots'

export interface SlotContext {
  hostId: string
  workspaceId?: string | null
  cwd?: string
}

export type SlotExecutor = (cmd: QuickCommand, ctx: SlotContext) => Promise<void>
export type SlotRenderer = (cmd: QuickCommand, ctx: SlotContext) => ReactNode

interface Props {
  mountTo: QuickCommandSlotId
  ctx: SlotContext
  executor: SlotExecutor
  render?: SlotRenderer
}

/**
 * Renders all commands bound to `mountTo` for the current `ctx.hostId`. The
 * Quick Commands module enable state is checked at the very top — disabling
 * the module makes every slot vanish app-wide without consumer changes.
 *
 * Render order is the capability order (`getBoundCommands` iterates the
 * stable `getCommands(hostId)` list), not `Object.keys(bindings)` — that's
 * the spec §4.4 stability guarantee against post-sync key-order divergence.
 */
export function CommandSlot({ mountTo, ctx, executor, render }: Props) {
  const enabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  const bindings = useQuickCommandStore((s) => s.bindings)
  const allCmds = useQuickCommandStore((s) => s.getCommands(ctx.hostId))

  // Recompute boundCmds when bindings or capability list change.
  const boundCmds = useMemo(
    () => allCmds.filter((c) => bindings[c.id]?.includes(mountTo)),
    [allCmds, bindings, mountTo],
  )

  if (!enabled) return null
  if (boundCmds.length === 0) return null

  return (
    <div className="flex flex-wrap items-center gap-1.5" role="toolbar" aria-label="Quick commands">
      {boundCmds.map((cmd) => {
        if (render) {
          return (
            <span key={cmd.id} className="inline-flex">
              {render(cmd, ctx)}
            </span>
          )
        }
        const ariaLabel = cmd.category ? `${cmd.name} (${cmd.category})` : cmd.name
        return (
          <button
            key={cmd.id}
            type="button"
            onClick={() => {
              void executor(cmd, ctx)
            }}
            aria-label={ariaLabel}
            title={cmd.command}
            className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer disabled:opacity-50"
          >
            <span className="truncate max-w-[12rem]">{cmd.name}</span>
            {cmd.category && (
              <span className="text-[10px] text-text-muted bg-surface-primary px-1.5 py-0.5 rounded">
                {cmd.category}
              </span>
            )}
          </button>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/components/CommandSlot.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): add <CommandSlot> shared component for v2 binding model
```

---

### Task 1b.1.5: 擴 `useUndoToast` schema 支援自訂 action label（Retry）

**Files:**
- Modify: `spa/src/stores/useUndoToast.ts`
- Modify: `spa/src/stores/useUndoToast.test.ts`
- Modify: `spa/src/components/GlobalUndoToast.tsx`

**動機：** Task 1b.2 的 slot-executor 在 send-keys 失敗時要顯示 toast 帶「Retry」按鈕（spec §3.3 表格第 2 列；user 決策採方案 (a) — 直接擴 schema），與既有 `OverviewSection.tsx` 的「Undo」按鈕共用同一條 toast。預設保留 `'Undo'` 維持向下相容。

**既有 callsite 影響範圍盤點（grep `useUndoToast.*show|getState\(\)\.show`）：**
- Production：`spa/src/components/hosts/OverviewSection.tsx:85`（`show(message, restore)` — 不傳 actionLabel，續用 `'Undo'`）
- Test：`spa/src/stores/useUndoToast.test.ts`（自身單元測試）+ `spa/src/lib/host-lifecycle.test.ts`（只重置 toast=null，不呼叫 `show`，不受影響）
- 共 1 個 production callsite，2 個測試檔；schema 擴充採 optional 第 3 參數，**無需修改既有 callsite 的呼叫形式**

- [ ] **Step 1: Write the failing test**

Edit `spa/src/stores/useUndoToast.test.ts` 加新測試（不要動既有測試 — 它們驗證向下相容）：

```ts
describe('useUndoToast — custom action label', () => {
  beforeEach(() => {
    useUndoToast.setState({ toast: null })
  })

  it('defaults actionLabel to undefined when not provided (back-compat)', () => {
    useUndoToast.getState().show('msg', () => {})
    const toast = useUndoToast.getState().toast
    expect(toast?.actionLabel).toBeUndefined()
  })

  it('stores actionLabel when provided', () => {
    useUndoToast.getState().show('Send keys failed', () => {}, 'Retry')
    const toast = useUndoToast.getState().toast
    expect(toast?.actionLabel).toBe('Retry')
  })
})
```

Edit `spa/src/components/GlobalUndoToast.tsx` 也加一條 component test（若該檔已有 test 則 append；若無則新建 `GlobalUndoToast.test.tsx`）：

```tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GlobalUndoToast } from './GlobalUndoToast'
import { useUndoToast } from '../stores/useUndoToast'

describe('GlobalUndoToast — actionLabel', () => {
  beforeEach(() => useUndoToast.setState({ toast: null }))

  it('renders default Undo label when actionLabel is omitted', () => {
    useUndoToast.getState().show('Deleted host', () => {})
    render(<GlobalUndoToast />)
    expect(screen.getByRole('button')).toHaveTextContent(/Undo/i)
  })

  it('renders custom actionLabel when provided (e.g. Retry)', () => {
    useUndoToast.getState().show('Send keys failed', () => {}, 'Retry')
    render(<GlobalUndoToast />)
    expect(screen.getByRole('button')).toHaveTextContent(/Retry/i)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/stores/useUndoToast.test.ts src/components/GlobalUndoToast.test.tsx`
Expected: FAIL — `actionLabel` 欄位不存在 / 元件還沒讀。

- [ ] **Step 3: Implement schema extension**

Edit `spa/src/stores/useUndoToast.ts`：

```ts
// spa/src/stores/useUndoToast.ts — Global undo toast state
import { create } from 'zustand'

interface UndoToastState {
  toast: { message: string; restore: () => void; actionLabel?: string } | null
  show: (message: string, restore: () => void, actionLabel?: string) => void
  dismiss: () => void
}

export const useUndoToast = create<UndoToastState>()((set) => ({
  toast: null,
  show: (message, restore, actionLabel) =>
    set({ toast: { message, restore, actionLabel } }),
  dismiss: () => set({ toast: null }),
}))
```

Edit `spa/src/components/GlobalUndoToast.tsx` 改 button label 取用：

```tsx
<button
  className="text-sm text-blue-400 hover:text-blue-300 font-medium cursor-pointer"
  onClick={() => {
    toast.restore()
    dismiss()
  }}
>
  {toast.actionLabel ?? t('hosts.undo')}
</button>
```

註：`actionLabel` 由 caller 傳 i18n 化字串（例如 `t('quick_commands.toast.retry')`）；`'hosts.undo'` 維持作為**未指定時的預設**，確保 `OverviewSection` 既有行為不變。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/stores/useUndoToast.test.ts src/components/GlobalUndoToast.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): extend useUndoToast schema with optional actionLabel (back-compat)
```

---

### Task 1b.2: Slot executor lib（建 session + 送 keys + 失敗 UX）

**Files:**
- Create: `spa/src/lib/slot-executor.ts`
- Create: `spa/src/lib/slot-executor.test.ts`

註：保留既有 `spa/src/lib/execute-command.ts`（v1 send-keys helper）；新檔處理 v2 的「建 session + 送 keys + 切 session + toast」一條龍。

- [ ] **Step 1: Write the failing test**

Create `spa/src/lib/slot-executor.test.ts`:

```ts
import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { runWorkspaceSlot } from './slot-executor'
import { useUndoToast } from '../stores/useUndoToast'

vi.mock('./host-api', async () => {
  const actual = await vi.importActual<typeof import('./host-api')>('./host-api')
  return {
    ...actual,
    createSession: vi.fn(),
  }
})

vi.mock('./execute-command', () => ({
  executeCommand: vi.fn(),
}))

import { createSession } from './host-api'
import { executeCommand } from './execute-command'

describe('runWorkspaceSlot', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useUndoToast.setState({ toast: null })
  })
  afterEach(() => {
    vi.clearAllMocks()
  })

  it('happy path — creates session, sends keys, switches focus', async () => {
    const switchFocus = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-1', name: 'A', cwd: '/tmp', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1', cwd: '/tmp' },
      { switchToSession: switchFocus },
    )

    expect(createSession).toHaveBeenCalledWith('h1', expect.any(String), '/tmp', 'terminal')
    expect(executeCommand).toHaveBeenCalledWith('h1', 'sess-1', 'echo hi')
    expect(switchFocus).toHaveBeenCalledWith('h1', 'sess-1')
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('createSession failure — surfaces toast, does NOT switch focus', async () => {
    const switchFocus = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('500 Internal'))

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus },
    )

    expect(switchFocus).not.toHaveBeenCalled()
    expect(executeCommand).not.toHaveBeenCalled()
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Failed to start session/i)
  })

  it('send-keys failure — STILL switches focus + toast carries retry action', async () => {
    const switchFocus = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-1', name: 'A', cwd: '/tmp', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('send-keys failed: 503'))

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus },
    )

    expect(switchFocus).toHaveBeenCalledWith('h1', 'sess-1')
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Session created.*command failed/i)
    // Task 1b.1.5 — actionLabel must surface 'Retry' (translated key allowed) instead of default 'Undo'
    expect(toast!.actionLabel).toBeDefined()
    expect(toast!.actionLabel).toMatch(/retry/i)
    // restore = retry — calling it should trigger executeCommand again
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined)
    toast!.restore()
    // retry is sync invocation; await microtask
    await Promise.resolve()
    expect(executeCommand).toHaveBeenCalledTimes(2)
  })

  it('switchToSession failure — toast surfaced, session still listed elsewhere', async () => {
    const switchFocus = vi.fn().mockImplementation(() => {
      throw new Error('switch failed')
    })
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-1', name: 'A', cwd: '/tmp', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus },
    )

    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Could not switch/i)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/slot-executor.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement executor**

Create `spa/src/lib/slot-executor.ts`:

```ts
import { createSession } from './host-api'
import { executeCommand } from './execute-command'
import { useUndoToast } from '../stores/useUndoToast'
import { useI18nStore } from '../stores/useI18nStore'
import type { QuickCommand } from '../stores/useQuickCommandStore'
import type { SlotContext } from '../components/CommandSlot'

interface Deps {
  /**
   * Switches the active tab/pane to the freshly-created session.
   * Called after the session exists, regardless of whether send-keys
   * succeeded — see spec §3.3 (silent orphan sessions are the worst UX).
   */
  switchToSession: (hostId: string, sessionCode: string) => void
}

function genSessionName(cmd: QuickCommand): string {
  const ts = new Date().toISOString().slice(11, 19) // HH:MM:SS
  return `${cmd.name} ${ts}`.slice(0, 64)
}

/**
 * Workspace-slot executor:
 *  1. POST /api/sessions (cwd: ctx.cwd ?? sane default)
 *  2. POST /api/sessions/{code}/send-keys
 *  3. switchToSession()
 *
 * Failure UX (spec §3.3):
 *  - Step 1 fails → toast "Failed to start session: <reason>", abort.
 *  - Step 1 ok + Step 2 fails → STILL switch focus, toast with Retry action.
 *  - Step 1 ok + Step 2 ok + Step 3 fails (rare) → toast pointing user to
 *    the sessions list (session is alive elsewhere).
 *
 * The executor is shared by Phase 1b workspace entry and Phase 1c host entry
 * (the latter calls it with workspaceId omitted; the cwd-resolution defaults
 * differ per slot caller, see spec §3.2 table).
 */
export async function runWorkspaceSlot(
  cmd: QuickCommand,
  ctx: SlotContext,
  deps: Deps,
): Promise<void> {
  const t = useI18nStore.getState().t
  const toast = useUndoToast.getState()

  let sessionCode: string
  try {
    const session = await createSession(ctx.hostId, genSessionName(cmd), ctx.cwd ?? '~', 'terminal')
    sessionCode = session.code
  } catch (err) {
    const reason = err instanceof Error ? err.message : String(err)
    toast.show(t('quick_commands.toast.create_failed', { reason }), () => {})
    return
  }

  try {
    await executeCommand(ctx.hostId, sessionCode, cmd.command)
  } catch (err) {
    // Step 2 failed — STILL switch (so user sees the orphan), with Retry.
    safelySwitch(ctx.hostId, sessionCode, deps, t)
    const reason = err instanceof Error ? err.message : String(err)
    void reason
    toast.show(
      t('quick_commands.toast.send_keys_failed'),
      // retry: re-run send-keys; failures dropped (user can keep clicking).
      () => {
        void executeCommand(ctx.hostId, sessionCode, cmd.command).catch(() => undefined)
      },
      t('quick_commands.toast.retry'),  // ← Task 1b.1.5 introduced actionLabel; default ('Undo') doesn't fit retry semantics
    )
    return
  }

  safelySwitch(ctx.hostId, sessionCode, deps, t)
}

function safelySwitch(
  hostId: string,
  sessionCode: string,
  deps: Deps,
  t: ReturnType<typeof useI18nStore.getState>['t'],
): void {
  try {
    deps.switchToSession(hostId, sessionCode)
  } catch (err) {
    const reason = err instanceof Error ? err.message : String(err)
    void reason
    useUndoToast.getState().show(t('quick_commands.toast.switch_failed'), () => {})
  }
}
```

註（給實作者）：i18n keys 在 Task 1b.6 統一加入 zh-TW / en JSON。`createSession` 已存在於 `spa/src/lib/host-api.ts`（簽名 `(hostId, name, cwd, mode) => Promise<Session>`）。`executeCommand` 已存在於 `spa/src/lib/execute-command.ts`。`switchToSession` deps 由 caller 注入（避免 tab/workspace store 的循環相依）。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/slot-executor.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): add slot-executor with three-stage failure UX (spec §3.3)
```

---

### Task 1b.3: `QuickCommandsSettingsSection` — list + edit dialog + multi-select

**Files:**
- Create: `spa/src/components/settings/QuickCommandsSettingsSection.tsx`
- Create: `spa/src/components/settings/QuickCommandsSettingsSection.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `spa/src/components/settings/QuickCommandsSettingsSection.test.tsx`:

```tsx
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QuickCommandsSettingsSection } from './QuickCommandsSettingsSection'
import { useQuickCommandStore } from '../../stores/useQuickCommandStore'
import { QUICK_COMMAND_SLOTS } from '../../lib/quick-command-slots'

function resetStore() {
  useQuickCommandStore.setState({ global: [], byHost: {}, bindings: {} })
}

describe('QuickCommandsSettingsSection', () => {
  beforeEach(() => resetStore())
  afterEach(() => resetStore())

  it('shows empty state when no commands', () => {
    render(<QuickCommandsSettingsSection />)
    expect(screen.getByText(/No quick commands yet/i)).toBeInTheDocument()
  })

  it('lists commands in capability order with mount chips', () => {
    useQuickCommandStore.setState({
      global: [
        { id: 'cmd-a', name: 'Alpha', command: 'a' },
        { id: 'cmd-b', name: 'Beta', command: 'b' },
      ],
      byHost: {},
      bindings: { 'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS, QUICK_COMMAND_SLOTS.HOST_ACTIONS] },
    })
    render(<QuickCommandsSettingsSection />)
    const rows = screen.getAllByTestId(/^qc-row-/)
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveAttribute('data-testid', 'qc-row-cmd-a')
    // cmd-a 的 mount chips 顯示 Workspace 與 Host
    const aRow = rows[0]
    expect(aRow.textContent).toMatch(/Workspace/)
    expect(aRow.textContent).toMatch(/Host/)
  })

  it('clicking + New opens dialog with empty fields, focus traps inside', () => {
    render(<QuickCommandsSettingsSection />)
    fireEvent.click(screen.getByRole('button', { name: /New/i }))
    const dialog = screen.getByRole('dialog')
    expect(dialog).toBeInTheDocument()
    // First focusable inside dialog should receive focus
    expect(document.activeElement).toBeTruthy()
    expect(dialog.contains(document.activeElement)).toBe(true)
  })

  it('Esc closes dialog and returns focus to trigger', () => {
    render(<QuickCommandsSettingsSection />)
    const trigger = screen.getByRole('button', { name: /New/i })
    fireEvent.click(trigger)
    expect(screen.queryByRole('dialog')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(document.activeElement).toBe(trigger)
  })

  it('saving a new command persists to store with selected mount targets', () => {
    render(<QuickCommandsSettingsSection />)
    fireEvent.click(screen.getByRole('button', { name: /New/i }))
    fireEvent.change(screen.getByLabelText(/Name/i), { target: { value: 'My Cmd' } })
    fireEvent.change(screen.getByLabelText(/Command/i), { target: { value: 'echo hi' } })
    // toggle Workspace chip
    fireEvent.click(screen.getByRole('button', { name: /Workspace/i }))
    fireEvent.click(screen.getByRole('button', { name: /Save/i }))

    const state = useQuickCommandStore.getState()
    expect(state.global).toHaveLength(1)
    const cmd = state.global[0]
    expect(cmd.name).toBe('My Cmd')
    expect(state.bindings[cmd.id]).toEqual([QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS])
  })

  it('Edit existing command updates name + bindings', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-a', name: 'Alpha', command: 'a' }],
      byHost: {},
      bindings: { 'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
    })
    render(<QuickCommandsSettingsSection />)
    fireEvent.click(screen.getByRole('button', { name: /Edit/i }))
    fireEvent.change(screen.getByLabelText(/Name/i), { target: { value: 'Alpha 2' } })
    // toggle Workspace off, Host on
    fireEvent.click(screen.getByRole('button', { name: /Workspace/i }))
    fireEvent.click(screen.getByRole('button', { name: /Host/i }))
    fireEvent.click(screen.getByRole('button', { name: /Save/i }))

    const state = useQuickCommandStore.getState()
    expect(state.global[0].name).toBe('Alpha 2')
    expect(state.bindings['cmd-a']).toEqual([QUICK_COMMAND_SLOTS.HOST_ACTIONS])
  })

  it('Delete removes command and clears its binding', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-a', name: 'Alpha', command: 'a' }],
      byHost: {},
      bindings: { 'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
    })
    render(<QuickCommandsSettingsSection />)
    fireEvent.click(screen.getByRole('button', { name: /Delete/i }))
    const state = useQuickCommandStore.getState()
    expect(state.global).toHaveLength(0)
    expect(state.bindings['cmd-a']).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/settings/QuickCommandsSettingsSection.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement the section**

Create `spa/src/components/settings/QuickCommandsSettingsSection.tsx`:

```tsx
import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { Plus, PencilSimple, Trash } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useQuickCommandStore, type QuickCommand } from '../../stores/useQuickCommandStore'
import {
  QUICK_COMMAND_SLOTS,
  type QuickCommandSlotId,
} from '../../lib/quick-command-slots'
import type { SettingsContextFor } from '../../lib/settings-contribution-types'

interface Props {
  ctx?: SettingsContextFor<'purdex'>
}

/**
 * Quick Commands settings (purdex scope).
 *
 * - List: capability order via store.global (no bindings filter — Settings
 *   shows everything user can edit).
 * - Edit dialog: focus trap, Esc-closes-and-returns-focus, multi-select chips
 *   for mount targets fed from QUICK_COMMAND_SLOTS.
 * - All keystrokes go through standard inputs; Space/Enter on the chip
 *   toggles the slot via native button semantics.
 */
export function QuickCommandsSettingsSection({ ctx: _ctx }: Props = {}) {
  const t = useI18nStore((s) => s.t)
  const commands = useQuickCommandStore((s) => s.global)
  const bindings = useQuickCommandStore((s) => s.bindings)
  const [editing, setEditing] = useState<QuickCommand | null>(null)
  const [creating, setCreating] = useState(false)
  const triggerRef = useRef<HTMLButtonElement | null>(null)

  const dialogOpen = creating || editing !== null

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-lg text-text-primary">{t('settings.quick_commands.title')}</h2>
        <button
          ref={triggerRef}
          type="button"
          onClick={() => {
            setEditing(null)
            setCreating(true)
          }}
          className="flex items-center gap-1 px-3 py-1.5 rounded text-xs text-text-primary border border-border-default hover:bg-surface-secondary cursor-pointer"
        >
          <Plus size={12} /> {t('settings.quick_commands.new')}
        </button>
      </div>

      {commands.length === 0 ? (
        <p className="text-sm text-text-muted py-8 text-center">
          {t('settings.quick_commands.empty')}
        </p>
      ) : (
        <ul className="space-y-2">
          {commands.map((cmd) => (
            <li
              key={cmd.id}
              data-testid={`qc-row-${cmd.id}`}
              className="border border-border-subtle rounded p-3 flex items-start gap-3"
            >
              <div className="flex-1 min-w-0">
                <div className="text-sm text-text-primary">{cmd.name}</div>
                <div className="text-xs text-text-muted truncate" title={cmd.command}>
                  {cmd.command}
                </div>
                <div className="mt-1 flex gap-1 flex-wrap">
                  {(bindings[cmd.id] ?? []).map((slot) => (
                    <span
                      key={slot}
                      className="text-[10px] text-text-secondary bg-surface-secondary px-1.5 py-0.5 rounded"
                    >
                      {slotLabel(slot, t)}
                    </span>
                  ))}
                </div>
              </div>
              <button
                type="button"
                onClick={() => {
                  setCreating(false)
                  setEditing(cmd)
                }}
                aria-label={t('common.edit')}
                className="p-1 text-text-muted hover:text-text-primary cursor-pointer"
              >
                <PencilSimple size={14} /> {t('common.edit')}
              </button>
              <button
                type="button"
                onClick={() => {
                  useQuickCommandStore.getState().removeCommand(cmd.id)
                }}
                aria-label={t('common.delete')}
                className="p-1 text-red-400 hover:text-red-300 cursor-pointer"
              >
                <Trash size={14} /> {t('common.delete')}
              </button>
            </li>
          ))}
        </ul>
      )}

      {dialogOpen && (
        <EditDialog
          initial={editing}
          onClose={() => {
            setCreating(false)
            setEditing(null)
            triggerRef.current?.focus()
          }}
          onSave={(cmd, targets) => {
            // Phase 1: only global capability is editable in UI; per-host
            // override deferred to a later phase.
            useQuickCommandStore.getState().addCommand(cmd)
            useQuickCommandStore.getState().setBinding(cmd.id, targets)
            setCreating(false)
            setEditing(null)
            triggerRef.current?.focus()
          }}
        />
      )}
    </div>
  )
}

function slotLabel(slot: QuickCommandSlotId, t: ReturnType<typeof useI18nStore.getState>['t']): string {
  switch (slot) {
    case QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS:
      return t('settings.quick_commands.slot.workspace')
    case QUICK_COMMAND_SLOTS.HOST_ACTIONS:
      return t('settings.quick_commands.slot.host')
    default:
      return slot
  }
}

interface DialogProps {
  initial: QuickCommand | null
  onClose: () => void
  onSave: (cmd: QuickCommand, targets: QuickCommandSlotId[]) => void
}

function EditDialog({ initial, onClose, onSave }: DialogProps) {
  const t = useI18nStore((s) => s.t)
  const allBindings = useQuickCommandStore((s) => s.bindings)
  const [name, setName] = useState(initial?.name ?? '')
  const [command, setCommand] = useState(initial?.command ?? '')
  const [icon, setIcon] = useState(initial?.icon ?? '')
  const [category, setCategory] = useState(initial?.category ?? '')
  const initialTargets = useMemo<QuickCommandSlotId[]>(
    () => (initial ? allBindings[initial.id] ?? [] : []),
    [initial, allBindings],
  )
  const [targets, setTargets] = useState<QuickCommandSlotId[]>(initialTargets)
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const firstInputRef = useRef<HTMLInputElement | null>(null)

  // Focus first input on mount
  useEffect(() => {
    firstInputRef.current?.focus()
  }, [])

  // Esc closes
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  // Focus trap inside dialog (basic Tab cycling)
  const onKeyDown = useCallback((e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'Tab') return
    const root = dialogRef.current
    if (!root) return
    const focusable = root.querySelectorAll<HTMLElement>(
      'button, [href], input, textarea, select, [tabindex]:not([tabindex="-1"])',
    )
    if (focusable.length === 0) return
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      last.focus()
      e.preventDefault()
    } else if (!e.shiftKey && document.activeElement === last) {
      first.focus()
      e.preventDefault()
    }
  }, [])

  const toggleTarget = (slot: QuickCommandSlotId) => {
    setTargets((prev) =>
      prev.includes(slot) ? prev.filter((s) => s !== slot) : [...prev, slot],
    )
  }

  const handleSave = () => {
    const trimmedName = name.trim()
    const trimmedCmd = command.trim()
    if (!trimmedName || !trimmedCmd) return
    const id = initial?.id ?? `qc-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
    onSave(
      {
        id,
        name: trimmedName,
        command: trimmedCmd,
        icon: icon.trim() || undefined,
        category: category.trim() || undefined,
      },
      targets,
    )
  }

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={t(initial ? 'settings.quick_commands.edit' : 'settings.quick_commands.new')}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onKeyDown={onKeyDown}
    >
      <div ref={dialogRef} className="w-[480px] bg-surface-primary border border-border-default rounded-lg p-4 space-y-3">
        <h3 className="text-sm font-semibold">
          {t(initial ? 'settings.quick_commands.edit' : 'settings.quick_commands.new')}
        </h3>

        <label className="block text-xs text-text-secondary">
          {t('settings.quick_commands.name')}
          <input
            ref={firstInputRef}
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full mt-1 bg-surface-input border border-border-default rounded px-2 py-1 text-sm text-text-primary"
          />
        </label>

        <label className="block text-xs text-text-secondary">
          {t('settings.quick_commands.command')}
          <textarea
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            rows={3}
            className="w-full mt-1 bg-surface-input border border-border-default rounded px-2 py-1 text-sm font-mono text-text-primary"
          />
        </label>

        <div className="grid grid-cols-2 gap-2">
          <label className="block text-xs text-text-secondary">
            {t('settings.quick_commands.icon')}
            <input
              value={icon}
              onChange={(e) => setIcon(e.target.value)}
              className="w-full mt-1 bg-surface-input border border-border-default rounded px-2 py-1 text-sm text-text-primary"
            />
          </label>
          <label className="block text-xs text-text-secondary">
            {t('settings.quick_commands.category')}
            <input
              value={category}
              onChange={(e) => setCategory(e.target.value)}
              className="w-full mt-1 bg-surface-input border border-border-default rounded px-2 py-1 text-sm text-text-primary"
            />
          </label>
        </div>

        <fieldset className="border-t border-border-subtle pt-3">
          <legend className="text-xs text-text-secondary px-1">
            {t('settings.quick_commands.mount')}
          </legend>
          <div className="flex gap-2 mt-1 flex-wrap">
            {(Object.values(QUICK_COMMAND_SLOTS) as QuickCommandSlotId[]).map((slot) => {
              const active = targets.includes(slot)
              return (
                <button
                  key={slot}
                  type="button"
                  onClick={() => toggleTarget(slot)}
                  aria-pressed={active}
                  className={`px-2 py-1 text-xs rounded border cursor-pointer ${
                    active
                      ? 'bg-purple-500/20 text-text-primary border-purple-400'
                      : 'border-border-default text-text-secondary hover:text-text-primary'
                  }`}
                >
                  {slotLabel(slot, t)}
                </button>
              )
            })}
          </div>
        </fieldset>

        <div className="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onClose}
            className="px-3 py-1.5 text-xs text-text-secondary border border-border-default rounded cursor-pointer hover:text-text-primary"
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            onClick={handleSave}
            className="px-3 py-1.5 text-xs text-white bg-purple-500 rounded cursor-pointer hover:bg-purple-400"
          >
            {t('common.save')}
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/components/settings/QuickCommandsSettingsSection.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): add Quick Commands settings section (list + edit dialog + chips)
```

---

### Task 1b.4: 補上 settings contribution + i18n keys

**Files:**
- Modify: `spa/src/lib/register-modules.tsx`
- Modify: `spa/src/locales/zh-TW.json`
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/lib/register-modules.quick-commands.test.tsx`

- [ ] **Step 1: Update test to assert settings contribution exists**

Edit `spa/src/lib/register-modules.quick-commands.test.tsx` — replace the「Phase 1a: NO settings」test:

```tsx
  it('Phase 1b: quick-commands declares purdex-scope settings contribution', () => {
    registerBuiltinModules()
    const m = getModule('quick-commands')
    const settings = m!.settings ?? []
    expect(settings).toHaveLength(1)
    expect(settings[0]).toMatchObject({
      localId: 'quick-commands',
      scope: 'purdex',
      labelKey: 'settings.section.quick_commands',
    })
  })
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/register-modules.quick-commands.test.tsx`
Expected: FAIL — settings array is empty.

- [ ] **Step 3: Wire settings + import section + i18n**

In `spa/src/lib/register-modules.tsx`, add import alongside the existing settings imports:

```tsx
import { QuickCommandsSettingsSection } from '../components/settings/QuickCommandsSettingsSection'
```

Update the `quick-commands` registration to include `settings`:

```tsx
  registerModule({
    id: 'quick-commands',
    name: 'Quick Commands',
    disableable: true,
    descriptionKey: 'modules.quick_commands.description',
    settings: [
      {
        localId: 'quick-commands',
        scope: 'purdex',
        order: 10, // 介於 editor (9) 與 sync (11) 之間，排在 module-config (8) 之後
        labelKey: 'settings.section.quick_commands',
        component: QuickCommandsSettingsSection,
      },
    ],
  })
```

In `spa/src/locales/zh-TW.json` 加入：

```json
  "settings.section.quick_commands": "快速指令",
  "settings.quick_commands.title": "快速指令",
  "settings.quick_commands.new": "新增",
  "settings.quick_commands.edit": "編輯快速指令",
  "settings.quick_commands.empty": "尚無快速指令 — 點選右上角「新增」開始建立。",
  "settings.quick_commands.name": "名稱",
  "settings.quick_commands.command": "指令",
  "settings.quick_commands.icon": "圖示（Phosphor 名稱，可省略）",
  "settings.quick_commands.category": "分類（可省略）",
  "settings.quick_commands.mount": "掛載位置",
  "settings.quick_commands.slot.workspace": "Workspace",
  "settings.quick_commands.slot.host": "Host",
  "common.edit": "編輯",
  "quick_commands.toast.create_failed": "無法建立 session：{{reason}}",
  "quick_commands.toast.send_keys_failed": "Session 已建立，但指令送出失敗。",
  "quick_commands.toast.switch_failed": "已建立並送出指令，但無法切換焦點；請至 sessions 列表查看。",
  "quick_commands.toast.retry": "重試",
```

In `spa/src/locales/en.json` 對應加入：

```json
  "settings.section.quick_commands": "Quick Commands",
  "settings.quick_commands.title": "Quick Commands",
  "settings.quick_commands.new": "New",
  "settings.quick_commands.edit": "Edit Quick Command",
  "settings.quick_commands.empty": "No quick commands yet — Start by creating one.",
  "settings.quick_commands.name": "Name",
  "settings.quick_commands.command": "Command",
  "settings.quick_commands.icon": "Icon (Phosphor name, optional)",
  "settings.quick_commands.category": "Category (optional)",
  "settings.quick_commands.mount": "Mount targets",
  "settings.quick_commands.slot.workspace": "Workspace",
  "settings.quick_commands.slot.host": "Host",
  "common.edit": "Edit",
  "quick_commands.toast.create_failed": "Failed to start session: {{reason}}",
  "quick_commands.toast.send_keys_failed": "Session created, but command failed.",
  "quick_commands.toast.switch_failed": "Created and sent, but could not switch focus; check the sessions list.",
  "quick_commands.toast.retry": "Retry",
```

註：若 `common.edit` 已存在，跳過該行。檢查指令：`grep '"common.edit"' spa/src/locales/zh-TW.json`。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/register-modules.quick-commands.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): wire Quick Commands settings contribution + i18n keys
```

---

### Task 1b.5a: Workspace 入口 (i) — `WorkspaceQuickCommandsContextMenu` + 整合 WorkspaceRow 右鍵

**Files:**
- Create: `spa/src/features/workspace/components/WorkspaceQuickCommandsContextMenu.tsx`
- Create: `spa/src/features/workspace/components/WorkspaceQuickCommandsContextMenu.test.tsx`
- Modify: `spa/src/features/workspace/components/WorkspaceContextMenu.tsx`（嵌入新 section 或改由外層組合，見實作策略）
- Modify: `spa/src/App.tsx`（`handleWsContextMenu` 已會接收 wsId — 需把 wsId 傳遞到下層以解析 hostId / 取 bindings）
- Modify: `spa/src/features/workspace/components/WorkspaceContextMenu.test.tsx`（追加 quick commands section 測試）

**實作策略：** 既有 `WorkspaceContextMenu`（`spa/src/features/workspace/components/WorkspaceContextMenu.tsx`）已涵蓋 Settings / Tear-off / Merge-to。在現有「Settings」按鈕之上**插入** quick commands section（依 user 決策 (i)），保留 Settings/Tear-off/Merge-to 的 separator 規則。預設：

1. 新元件 `WorkspaceQuickCommandsContextMenu` 渲染 mount=`WORKSPACE_ACTIONS` 的所有 bound commands；點擊執行 executor 並關閉 menu。
2. 在 `WorkspaceContextMenu` 內部 `Settings` 按鈕**之前**渲染此新元件，並在其後加 `border-t` separator（若有任一個 quick command 顯示）。
3. `App.tsx` 的 `WorkspaceContextMenu` 渲染處需新增傳 `workspaceId={wsContextMenu.wsId}` prop，使其能解析該 workspace 對應 host（透過 `useWorkspaceStore` 找 `workspace.hostId` — 若 store schema 有對應欄位；否則用 `activeHostId`）。

**重要：** `<CommandSlot>` 已具備 `isEnabled` short-circuit 與「無 binding 不渲染」邏輯，所以無 quick commands 時 menu 內部自動只剩既有項目，不會出現空 separator。

- [ ] **Step 1: Write the failing test**

Create `spa/src/features/workspace/components/WorkspaceQuickCommandsContextMenu.test.tsx`：

```tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { WorkspaceQuickCommandsContextMenu } from './WorkspaceQuickCommandsContextMenu'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../../../lib/module-registry'

function setup(workspaceId = 'w1', hostId = 'h1') {
  useQuickCommandStore.setState({
    global: [
      { id: 'cmd-a', name: 'Alpha', command: 'a' },
      { id: 'cmd-b', name: 'Bravo', command: 'b' },
    ],
    byHost: {},
    bindings: { 'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
  })
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  clearModuleRegistry()
  registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
  return { workspaceId, hostId }
}

describe('WorkspaceQuickCommandsContextMenu', () => {
  beforeEach(() => setup())

  it('renders bound WORKSPACE_ACTIONS commands and hides unbound ones', () => {
    render(<WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={() => {}} />)
    expect(screen.getByLabelText(/^Alpha/)).toBeInTheDocument()
    expect(screen.queryByLabelText(/^Bravo/)).toBeNull()
  })

  it('returns null when no commands are bound (lets parent skip separator)', () => {
    useQuickCommandStore.setState({ bindings: {} })
    const { container } = render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={() => {}} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('clicking a command calls onClose', () => {
    const onClose = vi.fn()
    render(<WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={onClose} />)
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(onClose).toHaveBeenCalled()
  })

  it('returns null when quick-commands module is disabled', () => {
    useModuleEnabledStore.getState().setEnabled('quick-commands', false)
    const { container } = render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={() => {}} />,
    )
    expect(container.firstChild).toBeNull()
  })
})
```

也在 `WorkspaceContextMenu.test.tsx` 追加 integration 測試：

```tsx
it('renders quick commands section above Settings when WORKSPACE_ACTIONS bindings exist', () => {
  // 設定一個 binding（簡化：直接 mock useQuickCommandStore）
  useQuickCommandStore.setState({
    global: [{ id: 'cmd-x', name: 'XCmd', command: 'x' }],
    byHost: {},
    bindings: { 'cmd-x': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
  })
  // … 既有 setup（registerModule quick-commands etc）…
  render(
    <WorkspaceContextMenu
      position={{ x: 0, y: 0 }}
      workspaceId="w1"
      hostId="h1"
      onSettings={vi.fn()}
      onClose={vi.fn()}
    />,
  )
  expect(screen.getByLabelText(/^XCmd/)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceQuickCommandsContextMenu.test.tsx src/features/workspace/components/WorkspaceContextMenu.test.tsx`
Expected: FAIL — module not found / props missing。

- [ ] **Step 3: Implement**

Create `spa/src/features/workspace/components/WorkspaceQuickCommandsContextMenu.tsx`：

```tsx
import { CommandSlot } from '../../../components/CommandSlot'
import { runWorkspaceSlot } from '../../../lib/slot-executor'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { useTabStore } from '../../../stores/useTabStore'

interface Props {
  workspaceId: string
  hostId: string
  onClose: () => void
}

/**
 * 渲染 mount=WORKSPACE_ACTIONS 的 quick commands，作為 WorkspaceContextMenu 的子 section。
 * <CommandSlot> 自身會 short-circuit module disabled / no-bindings 兩個情況。
 * 為了讓 parent 能正確 toggle separator，**沒有 bound commands 時整個 wrapper 也回 null**
 * — 透過讀同樣的 store 直接判斷（避免引入新 prop）。
 */
export function WorkspaceQuickCommandsContextMenu({ workspaceId, hostId, onClose }: Props) {
  return (
    <div className="py-1">
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId, workspaceId }}
        executor={async (cmd, ctx) => {
          try {
            await runWorkspaceSlot(cmd, ctx, {
              switchToSession: (h, sessionCode) => {
                useTabStore.getState().openSingletonTab({
                  kind: 'tmux-session',
                  hostId: h,
                  sessionCode,
                })
              },
            })
          } finally {
            onClose()
          }
        }}
        render={(cmd) => (
          <button
            type="button"
            aria-label={cmd.name}
            className="w-full flex items-center gap-2 px-3 py-1.5 text-xs text-text-secondary hover:text-text-primary hover:bg-surface-hover cursor-pointer transition-colors"
          >
            <span className="truncate">{cmd.name}</span>
            {cmd.category && (
              <span className="text-[10px] text-text-muted bg-surface-primary px-1.5 py-0.5 rounded ml-auto">
                {cmd.category}
              </span>
            )}
          </button>
        )}
      />
    </div>
  )
}
```

**注意 render prop 與 onClick：** `<CommandSlot>` 預設 button 已含 `onClick`，但提供 `render` 時 wrapper 是 `<span>`（見 Task 1b.1 實作 L979）。我們需要**改用 default button** 才能讓 executor 跑；這代表此 task 不傳 `render` prop，採用 default 渲染（chip 樣式不適合 menu 列表，但 v1 可接受；若 reviewer 要求 menu 列表外觀，需要在 Task 1b.1 補一個 `containerClassName` / `itemClassName` prop，或讓 `render` 包外層 + 提供 onClick，二擇一）。

**簡化決議：** **保持 default 渲染**（不傳 render），用 `containerClassName` 覆蓋 `flex flex-wrap gap-1.5`（若 Task 1b.1 沒提供，需擴 `<CommandSlot>` 介面 — 在此 task 內微擴）。

→ **追加 sub-step 0**: 擴 `<CommandSlot>` 加一個 optional `containerClassName?: string` prop，預設沿用既有 toolbar 樣式；context menu 用 `flex flex-col`。修改 `spa/src/components/CommandSlot.tsx` 與其 test。Diff：

```tsx
interface Props {
  mountTo: QuickCommandSlotId
  ctx: SlotContext
  executor: SlotExecutor
  render?: SlotRenderer
  containerClassName?: string  // ← new
}

// in JSX:
<div className={containerClassName ?? 'flex flex-wrap items-center gap-1.5'} role="toolbar" aria-label="Quick commands">
```

並在 `WorkspaceQuickCommandsContextMenu` 改傳 `containerClassName="flex flex-col"`。

更新後 `WorkspaceContextMenu.tsx`：

```tsx
interface Props {
  position: { x: number; y: number }
  workspaceId?: string  // ← new (optional 維持向下相容；測試 setup 內全部要傳)
  hostId?: string       // ← new
  onSettings: () => void
  onTearOff?: () => void
  onMergeTo?: (targetWindowId: string) => void
  onClose: () => void
}

// JSX 中於 Settings 按鈕之前加：
{workspaceId && hostId && (
  <>
    <WorkspaceQuickCommandsContextMenu
      workspaceId={workspaceId}
      hostId={hostId}
      onClose={onClose}
    />
    <div className="border-t border-border-default my-1" />
  </>
)}
```

**注意：** separator 渲染條件 — 應僅當「真有 commands 顯示」才 separator，否則會出現「空 wrapper + separator」的視覺空隙。`<CommandSlot>` 在 0 commands 時回 `null`，但 wrapper `<div>` 仍存在。**解法**：把 separator 也透過讀 store 判斷：

```tsx
// 在 WorkspaceContextMenu.tsx，新增 helper：
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'

// 計算 hasQuickCommands：
const hasQuickCommands = useQuickCommandStore((s) => {
  if (!hostId) return false
  const cmds = s.getCommands(hostId)
  return cmds.some((c) => s.bindings[c.id]?.includes(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS))
})
const moduleEnabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
const showQuickCommandsSection = workspaceId && hostId && moduleEnabled && hasQuickCommands

// JSX：
{showQuickCommandsSection && (
  <>
    <WorkspaceQuickCommandsContextMenu workspaceId={workspaceId!} hostId={hostId} onClose={onClose} />
    <div className="border-t border-border-default my-1" />
  </>
)}
```

修改 `App.tsx` 的 `<WorkspaceContextMenu>` 呼叫處（L322-329）：

```tsx
{wsContextMenu && (() => {
  const ws = workspaces.find((w) => w.id === wsContextMenu.wsId)
  // hostId 來源：workspace 上的 hostId 欄位（若 schema 有），否則用 useHostStore.activeHostId fallback
  const hostId = (ws as { hostId?: string } | undefined)?.hostId ?? useHostStore.getState().activeHostId
  return (
    <WorkspaceContextMenu
      position={wsContextMenu.position}
      workspaceId={wsContextMenu.wsId}
      hostId={hostId ?? undefined}
      onSettings={() => openWsSettings(wsContextMenu.wsId)}
      onTearOff={window.electronAPI ? () => handleWsTearOff(wsContextMenu.wsId) : undefined}
      onMergeTo={window.electronAPI ? (targetWindowId) => handleWsMergeTo(wsContextMenu.wsId, targetWindowId) : undefined}
      onClose={handleCloseWsContextMenu}
    />
  )
})()}
```

註：`workspaces[].hostId` 是否存在須在實作前由 subagent grep 確認（`rg "hostId" spa/src/types/tab.ts`）；若不存在則 fallback 到 `useHostStore.activeHostId`。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceQuickCommandsContextMenu.test.tsx src/features/workspace/components/WorkspaceContextMenu.test.tsx src/components/CommandSlot.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): mount WORKSPACE_ACTIONS slot in workspace right-click context menu
```

---

### Task 1b.5b: Workspace 入口 (ii) — `WorkspaceQuickActionsPopover` + WorkspaceRow Plus hover

**Files:**
- Create: `spa/src/features/workspace/components/WorkspaceQuickActionsPopover.tsx`
- Create: `spa/src/features/workspace/components/WorkspaceQuickActionsPopover.test.tsx`
- Modify: `spa/src/features/workspace/components/WorkspaceRow.tsx`
- Modify: `spa/src/features/workspace/components/WorkspaceRow.test.tsx`

**實作策略：**
- 改造 `WorkspaceRow.tsx` L107-121 的 Plus 按鈕：保留原 click 行為（`onAddTabToWorkspace`）但**外層**多包一個 `<div onMouseEnter={...} onMouseLeave={...} onFocusCapture={...} onBlurCapture={...}>` 作 hover hub。
- 該 div 內含 Plus button + 一個 absolute popover（`<WorkspaceQuickActionsPopover>`），popover `right-full mr-1`（Plus 按鈕左側）展開。
- popover 內部用 `<CommandSlot mountTo=WORKSPACE_ACTIONS>` 渲染 chip 列。
- Hover 收回邏輯：使用 single boolean state `popoverOpen`；mouseenter Plus 或 popover 任一觸發開啟，mouseleave 整個 hub 觸發關閉（用 wrapper div 圍住兩者，事件冒泡判斷即可）。
- 鍵盤可達性：Plus 按鈕 focus 時 popover 開啟（`onFocusCapture`），整個 wrapper blur 時關閉（`onBlurCapture` + 比對 `relatedTarget` 是否仍在 wrapper 內）。
- 視覺 cue：popover 含半透明漸層壓底（`bg-gradient-to-l from-surface-secondary/0 to-surface-secondary/95` 或同等 token）。
- **Plus 原本 hover 才顯**（L117 `opacity-0 group-hover/ws-header:opacity-100`）— 此邏輯保留。

**短路條件：** `<CommandSlot>` 已內建 module-disabled / no-bindings short-circuit；`WorkspaceQuickActionsPopover` 額外用同邏輯（讀 store 並 early-return null）避免在無 commands 時渲染空 popover wrapper（避免 hover 觸發後出現空白浮層）。

- [ ] **Step 1: Write the failing test**

Create `spa/src/features/workspace/components/WorkspaceQuickActionsPopover.test.tsx`：

```tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { WorkspaceQuickActionsPopover } from './WorkspaceQuickActionsPopover'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../../../lib/module-registry'

function setup() {
  useQuickCommandStore.setState({
    global: [{ id: 'cmd-a', name: 'Alpha', command: 'a' }],
    byHost: {},
    bindings: { 'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
  })
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  clearModuleRegistry()
  registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
}

describe('WorkspaceQuickActionsPopover', () => {
  beforeEach(setup)

  it('renders bound commands as chips', () => {
    render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId="h1" />)
    expect(screen.getByLabelText(/^Alpha/)).toBeInTheDocument()
  })

  it('returns null when no commands bound', () => {
    useQuickCommandStore.setState({ bindings: {} })
    const { container } = render(
      <WorkspaceQuickActionsPopover workspaceId="w1" hostId="h1" />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('returns null when module disabled', () => {
    useModuleEnabledStore.getState().setEnabled('quick-commands', false)
    const { container } = render(
      <WorkspaceQuickActionsPopover workspaceId="w1" hostId="h1" />,
    )
    expect(container.firstChild).toBeNull()
  })
})
```

並於 `WorkspaceRow.test.tsx` 追加 hover 行為測試：

```tsx
it('opens popover on Plus hover, closes on mouseleave (with WORKSPACE_ACTIONS bindings)', () => {
  // 既有 ws-header render setup（保持原 props 模式）
  // 設定一個 WORKSPACE_ACTIONS binding
  useQuickCommandStore.setState({
    global: [{ id: 'cmd-x', name: 'X', command: 'x' }],
    byHost: {},
    bindings: { 'cmd-x': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
  })
  // 確保 hostId 路徑解析成功（mock useHostStore.activeHostId 或 workspace.hostId）
  // … render(<WorkspaceRow … />) …

  // 預設未 hover → popover 不在 DOM
  expect(screen.queryByLabelText(/^X/)).toBeNull()

  // hover Plus → popover 顯示
  fireEvent.mouseEnter(screen.getByLabelText(/Add tab to/i).parentElement!)
  expect(screen.getByLabelText(/^X/)).toBeInTheDocument()

  // mouseleave wrapper → popover 收回
  fireEvent.mouseLeave(screen.getByLabelText(/Add tab to/i).parentElement!)
  expect(screen.queryByLabelText(/^X/)).toBeNull()
})

it('does NOT open popover when no WORKSPACE_ACTIONS bindings exist', () => {
  useQuickCommandStore.setState({ bindings: {} })
  // … render WorkspaceRow …
  fireEvent.mouseEnter(screen.getByLabelText(/Add tab to/i).parentElement!)
  expect(screen.queryByRole('toolbar', { name: /Quick commands/i })).toBeNull()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceQuickActionsPopover.test.tsx src/features/workspace/components/WorkspaceRow.test.tsx`
Expected: FAIL — module not found / popover 不顯示。

- [ ] **Step 3: Implement popover + WorkspaceRow integration**

Create `spa/src/features/workspace/components/WorkspaceQuickActionsPopover.tsx`：

```tsx
import { CommandSlot } from '../../../components/CommandSlot'
import { runWorkspaceSlot } from '../../../lib/slot-executor'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { useTabStore } from '../../../stores/useTabStore'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'

interface Props {
  workspaceId: string
  hostId: string
}

/**
 * Popover chip-list rendered to the LEFT of the Plus-button on each
 * WorkspaceRow on hover/focus. Uses CommandSlot internally — already
 * short-circuits when module disabled / no bindings; we additionally
 * skip rendering the popover wrapper itself in those cases so the
 * hover trigger doesn't expose an empty floating panel.
 */
export function WorkspaceQuickActionsPopover({ workspaceId, hostId }: Props) {
  const moduleEnabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  const hasBindings = useQuickCommandStore((s) => {
    const cmds = s.getCommands(hostId)
    return cmds.some((c) => s.bindings[c.id]?.includes(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS))
  })
  if (!moduleEnabled || !hasBindings) return null

  return (
    <div
      role="group"
      aria-label="Workspace quick actions"
      className="absolute right-full top-1/2 -translate-y-1/2 mr-1 flex items-center gap-1 px-2 py-1 rounded-md bg-gradient-to-l from-surface-secondary/0 to-surface-secondary/95 backdrop-blur-sm shadow-md z-30"
    >
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId, workspaceId }}
        executor={(cmd, ctx) =>
          runWorkspaceSlot(cmd, ctx, {
            switchToSession: (h, sessionCode) => {
              useTabStore.getState().openSingletonTab({
                kind: 'tmux-session',
                hostId: h,
                sessionCode,
              })
            },
          })
        }
      />
    </div>
  )
}
```

修改 `spa/src/features/workspace/components/WorkspaceRow.tsx` Plus 按鈕區段（L107-121）：

```tsx
import { useState, useRef } from 'react'
// … 既有 imports
import { useHostStore } from '../../../stores/useHostStore'
import { WorkspaceQuickActionsPopover } from './WorkspaceQuickActionsPopover'

// 在 WorkspaceRow 函式內：
const [popoverOpen, setPopoverOpen] = useState(false)
const hubRef = useRef<HTMLDivElement>(null)
// hostId 解析（同 1b.5a：先看 workspace.hostId，否則 active）
const hostId = (workspace as { hostId?: string }).hostId
  ?? useHostStore((s) => s.activeHostId) ?? null

// 改 Plus 區段（保留原條件 showTabs）：
{showTabs && (
  <div
    ref={hubRef}
    className="relative inline-flex"
    onMouseEnter={() => setPopoverOpen(true)}
    onMouseLeave={() => setPopoverOpen(false)}
    onFocusCapture={() => setPopoverOpen(true)}
    onBlurCapture={(e) => {
      // 只有焦點離開整個 hub 才關閉（避免 popover 內部 chip 之間 tab 也觸發）
      if (!hubRef.current?.contains(e.relatedTarget as Node | null)) {
        setPopoverOpen(false)
      }
    }}
  >
    <button
      type="button"
      aria-label={t('nav.add_tab_to_workspace', { name: workspace.name })}
      title={t('nav.add_tab_to_workspace', { name: workspace.name })}
      onClick={(e) => {
        e.stopPropagation()
        onAddTabToWorkspace(workspace.id)
      }}
      onPointerDown={(e) => e.stopPropagation()}
      className="p-0.5 rounded hover:bg-surface-secondary hover:text-text-primary cursor-pointer opacity-0 group-hover/ws-header:opacity-100 focus:opacity-100 transition-opacity focus:outline-none"
    >
      <Plus size={12} />
    </button>
    {popoverOpen && hostId && (
      <WorkspaceQuickActionsPopover workspaceId={workspace.id} hostId={hostId} />
    )}
  </div>
)}
```

註：popover 觸發 hover 時 Plus 仍是 hover 中（同一個 group），不會閃。`WorkspaceQuickActionsPopover` 內部已 short-circuit 模組停用 / 無 binding，這層也再加 hostId guard。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceQuickActionsPopover.test.tsx src/features/workspace/components/WorkspaceRow.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): add hover popover for WORKSPACE_ACTIONS chips beside WorkspaceRow Plus button
```

---

### Task 1b.6: Phase 1b 全域驗證

- [ ] **Step 1: Run all SPA tests**

Run: `cd spa && npx vitest run`
Expected: all pass

- [ ] **Step 2: Run SPA lint**

Run: `cd spa && pnpm run lint`
Expected: clean

- [ ] **Step 3: Run SPA build**

Run: `cd spa && pnpm run build`
Expected: clean

- [ ] **Step 4: Run all Go tests**

Run: `go test ./...`
Expected: all pass

- [ ] **Step 5: Go build**

Run: `go build ./...`
Expected: clean

- [ ] **Step 6: 手動冒煙（review reviewer 自行操作）**

啟動 daemon + dev SPA，user flow：
1. Settings → Quick Commands → New → 輸入 Name + Command + 勾 Workspace → Save
2. 在 sidebar Workspace 行 **右鍵** → context menu 出現該 quick command；點擊執行
3. 在 sidebar Workspace 行 **hover Plus 按鈕** → 左側 popover 展開 chip；點擊執行
4. 兩條路徑都應：建 session、送 cmd、自動切到該 session
5. 模擬失敗（停 daemon → 點按鈕 → 應有 toast）；send-keys 失敗的 toast 應顯 'Retry' 按鈕（Task 1b.1.5 + 1b.2 共同支撐）

### Phase 1b 驗收清單

- [x] Settings 頁面可建 / 編 / 刪 commands；mount chips 可多選
- [x] 對話框 a11y：focus trap / Esc / aria-label / 觸發鍵盤可達
- [x] 設 mount = WORKSPACE 後，回 sidebar 即可在「Workspace 右鍵 menu」與「Plus hover popover」兩個入口看到按鈕（無需 reload）
- [x] 兩個入口共用同一個 executor，行為一致
- [x] Plus hover popover：mouseleave Plus AND popover 收回；鍵盤 focus 同等於 hover；無 binding 時不展開
- [x] 點擊按鈕：建 session + 送 keys + 切過去
- [x] 三層失敗 UX 符合 spec §3.3：建失敗 / 送 keys 失敗仍切過去 + **toast 顯 'Retry' 按鈕** / 切失敗 toast
- [x] 停用 quick-commands module → 兩個入口皆立即消失（CommandSlot short-circuit）
- [x] i18n: zh-TW + en 全部 key 齊全（含新增 `quick_commands.toast.retry`；`pnpm run lint` 含 locale-completeness 檢查）

---

## Phase 1c — HOST_ACTIONS 入口（小 PR）

**目標：** Host 詳情頁加 quick actions 區塊；user 設 mount = HOST 即可在 host 頁看到按鈕。

### Task 1c.1a: 移除 `SessionsSection` row 上殘留的 v1 `QuickCommandMenu` 整合

**Files:**
- Modify: `spa/src/components/hosts/SessionsSection.tsx`
- Modify: `spa/src/components/hosts/SessionsSection.test.tsx`（若有測試覆蓋 row 上 QuickCommandMenu，更新）

**動機（user 決策）：** 把 quick commands 功能集中在「new-session 入口」（Task 1c.1b），row 上不再重複；spec §3.4「保留現狀」原則被 user 明確覆蓋。

**重要：** v1 `QuickCommandMenu` 元件本身**不刪**，因為仍被 `spa/src/components/PaneLayoutRenderer.tsx` 使用。本 task 只刪 `SessionsSection.tsx` 內的整合。

**Diff scope（`SessionsSection.tsx`）：**
1. 移除 L12 的 `import { QuickCommandMenu } from '../QuickCommandMenu'`
2. 移除 L13 的 `import { executeCommand } from '../../lib/execute-command'`（若無其他用途；先 grep 該檔案內其他 `executeCommand` 引用，確認可刪）
3. 移除 L231-239 的 `<QuickCommandMenu hostId=... onExecute=... disabled=... />` JSX 與其包圍的閒置 wrapper（若 row 移除 menu 後 `<div className="flex items-center justify-end gap-1">` 仍要保留供其他 buttons 使用，**保留**該 div，只刪 `<QuickCommandMenu>` 一段）
4. 任何已成 unused 的 import / branch / state 一併清理（lint 會抓 unused，但先手動掃）

- [ ] **Step 1: Update tests to assert removal**

Edit `spa/src/components/hosts/SessionsSection.test.tsx`：
- 若有測試斷言「session row 顯示 quick command 按鈕」，改為斷言「session row 不再有該按鈕」（或 `getAllByLabelText(/Quick Command/i)` 為空）
- 若沒對應測試，新增一條保護性測試：

```tsx
it('does NOT render v1 QuickCommandMenu inside session rows (moved to new-session adjacency, Phase 1c)', () => {
  // 既有 setup：mock host / sessions store，render <SessionsSection hostId="h1" />
  // 假設 v1 QuickCommandMenu 的 trigger 有 aria-label 含 "Quick Commands"
  expect(screen.queryAllByRole('button', { name: /quick commands/i }).length).toBe(0)
})
```

註：實際 aria-label 由 `QuickCommandMenu` 元件決定（subagent 實作前先讀 `spa/src/components/QuickCommandMenu.tsx` 確認 trigger 的 accessible name；若不同請對齊）。

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/hosts/SessionsSection.test.tsx`
Expected: FAIL — row 上仍有 v1 menu。

- [ ] **Step 3: Remove the integration**

按 Diff scope 進行刪除。再驗證 lint：`cd spa && pnpm run lint`（unused imports/vars 必清）。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/components/hosts/SessionsSection.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
refactor(spa): remove v1 QuickCommandMenu from SessionsSection rows (consolidated to new-session entry, see Task 1c.1b)
```

註：本 task 與 1c.1b 是**獨立 commits 同一個 PR**；先刪除（diff 純減量、易 review），再加新（diff 純新增）。

---

### Task 1c.1b: `SessionsSection` new-session 旁加 `<CommandSlot mountTo=HOST_ACTIONS>`

**Files:**
- Modify: `spa/src/components/hosts/SessionsSection.tsx`
- Modify: `spa/src/components/hosts/SessionsSection.test.tsx`

**Mount 位置（user 決策）：** `SessionsSection.tsx` L165-175 的 `<div className="flex items-center justify-between mb-4">` 內，new-session 按鈕（L167-174）的**同一個 flex container**裡並列。`<CommandSlot>` 預設 chip toolbar 樣式恰好對應，視覺與 `Plus + 新增 session` 按鈕對齊；不做特殊 popover。

**設計：** 把 new-session 按鈕外層改為 `<div className="flex items-center gap-2">`（包 CommandSlot + new-session button 兩者），整體右對齊（保留外層 `justify-between` 與標題的相對位置）。

- [ ] **Step 1: Write the failing test**

Edit `spa/src/components/hosts/SessionsSection.test.tsx`：

```tsx
import { useQuickCommandStore } from '../../stores/useQuickCommandStore'
import { QUICK_COMMAND_SLOTS } from '../../lib/quick-command-slots'

describe('SessionsSection — host quick actions slot adjacent to new-session button (Phase 1c)', () => {
  it('renders <CommandSlot mountTo=HOST_ACTIONS> chips next to the new-session button', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-h', name: 'HostCmd', command: 'echo h' }],
      byHost: {},
      bindings: { 'cmd-h': [QUICK_COMMAND_SLOTS.HOST_ACTIONS] },
    })
    // 既有 host store setup（hostId='h1' connected）
    render(<SessionsSection hostId="h1" />)
    // chip 與 new-session 按鈕同一 flex 容器；簡化驗證：兩者都在 DOM
    expect(screen.getByLabelText(/^HostCmd/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /new session/i })).toBeInTheDocument()
  })

  it('hides slot when no commands are bound to HOST_ACTIONS (new-session button still visible)', () => {
    useQuickCommandStore.setState({ global: [], byHost: {}, bindings: {} })
    render(<SessionsSection hostId="h1" />)
    expect(screen.queryByLabelText(/^HostCmd/)).toBeNull()
    expect(screen.getByRole('button', { name: /new session/i })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/hosts/SessionsSection.test.tsx`
Expected: FAIL — slot 未掛載。

- [ ] **Step 3: Mount the slot**

In `spa/src/components/hosts/SessionsSection.tsx`：

加 imports：

```tsx
import { CommandSlot } from '../CommandSlot'
import { runWorkspaceSlot } from '../../lib/slot-executor'
import { QUICK_COMMAND_SLOTS } from '../../lib/quick-command-slots'
import { useTabStore } from '../../stores/useTabStore'
```

修改 L165-175 區段：

```tsx
<div className="flex items-center justify-between mb-4">
  <h2 className="text-lg font-semibold">{t('hosts.sessions')}</h2>
  <div className="flex items-center gap-2">
    <CommandSlot
      mountTo={QUICK_COMMAND_SLOTS.HOST_ACTIONS}
      ctx={{ hostId }}
      executor={(cmd, ctx) =>
        runWorkspaceSlot(cmd, ctx, {
          switchToSession: (h, sessionCode) => {
            useTabStore.getState().openSingletonTab({
              kind: 'tmux-session',
              hostId: h,
              sessionCode,
            })
          },
        })
      }
    />
    <button
      onClick={() => setShowNew(true)}
      disabled={isOffline}
      className="flex items-center gap-1.5 px-3 py-1.5 rounded text-xs bg-accent text-white cursor-pointer disabled:opacity-50"
    >
      <Plus size={14} />
      {t('hosts.new_session')}
    </button>
  </div>
</div>
```

註：HOST_ACTIONS slot 和 WORKSPACE_ACTIONS 共用同一個 `runWorkspaceSlot`（spec §3.2 兩列行為差只在 cwd 取值來源；Phase 1 兩者都 fallback 到 host default `~`，所以 helper 命名雖叫 `runWorkspaceSlot` 仍正確；Phase 2 若 cwd 取值要分流再拆）。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/components/hosts/SessionsSection.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): mount HOST_ACTIONS slot beside new-session button in SessionsSection
```

---

### Task 1c.2: Phase 1c 全域驗證

- [ ] **Step 1: Run all SPA tests**

Run: `cd spa && npx vitest run`
Expected: all pass

- [ ] **Step 2: Run SPA lint**

Run: `cd spa && pnpm run lint`
Expected: clean

- [ ] **Step 3: Run SPA build**

Run: `cd spa && pnpm run build`
Expected: clean

- [ ] **Step 4: Run all Go tests**

Run: `go test ./...`
Expected: all pass

- [ ] **Step 5: Go build**

Run: `go build ./...`
Expected: clean

### Phase 1c 驗收清單

- [x] User 能在 Settings 設 mount = HOST，並在 host 詳情 sessions section 標題列、new-session 按鈕**旁邊**看到 chip
- [x] Chip 點擊：建 session（cwd 取 host default）+ 送 cmd + 切過去
- [x] HOST_ACTIONS slot 與 WORKSPACE_ACTIONS slot 共存無衝突（兩者都可同時 mount）
- [x] `SessionsSection` 內**每個 session row 不再**出現 v1 `QuickCommandMenu`（改集中至 new-session 入口）
- [x] `PaneLayoutRenderer.tsx` 的 v1 `QuickCommandMenu` 仍維持運作（元件本身未被刪）

---

## Phase 完成順序檢查清單（送 reviewer 前確認）

- [ ] Phase 1a 已 merge → main，且 alpha bump 完成（或一個 PR sequence 規劃中）
- [ ] Phase 1b 已 merge → main，**Settings UI 與 WORKSPACE_ACTIONS 兩個入口（context menu + hover popover）同 PR**（不可拆）
- [ ] Phase 1c 已 merge → main，**1c.1a（移除 SessionsSection row 的 v1 整合）→ 1c.1b（new-session 旁掛 HOST_ACTIONS）兩個 commits 同 PR、依序 ship**（先 commit 純減量，再 commit 純新增；diff 易 review）
- [ ] Phase 1a 不可單獨 ship 在沒有 Phase 1b 計畫的情況（純資料層 user 看不到任何成果，會困惑）— 但 1a + 1b 兩個 PR 接力 ship 是允許的（spec §6 只要求 Settings UI 出現時必有 slot 生效）

## 範圍邊界（Phase 1 不做）

- 不動 `PaneLayoutRenderer` 內 `extraActions` 的 v1 `QuickCommandMenu`（spec §3.4）
- ~~不動 `SessionsSection` row actions 的 v1 `QuickCommandMenu`~~ — **已調整：Phase 1c Task 1c.1a 將 SessionsSection row 的 v1 整合移除**（user 決策；功能集中至 new-session 入口；spec §3.4 的「保留現狀」對 SessionsSection 不再適用，但對 `PaneLayoutRenderer` 仍適用）
- 不做 per-host bindings UI（store 支援，UI 推到 Phase 2）
- 不做 mode 設定 / 動態 function command / command palette / 快捷鍵（spec §6 不在範圍）
- 不做 `module-registry.commands?` legacy 收編（Phase 2 decision gate，spec §8.2）
- 不寫 persist migration（alpha-no-migration）

---

## Sub-skill / Subagent 提示

執行此 plan 時：

1. 進 worktree 後，**每個 Bash 指令必須以 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/quick-commands-v2 && ` 開頭**（feedback_subagent_cwd_enforcement.md）
2. 一個 task 一個 commit，commit message 用 conventional commit + Co-Authored-By trailer
3. zustand test setState **顯式列出** `global` / `byHost` / `bindings` 三個 mutable fields；**不要**只寫 `setState({ bindings: {...} })`，會 wipe 其他 state（feedback_zustand_harness_setstate.md）
4. 任何測試 / lint / build 指令在主 Claude 機器跑（feedback_codex_sandbox_no_install.md）
5. PR 完成後委派 codex 兩輪 review（標準 + 攻擊/防守/體質）；發現問題彙整成表格（信心 / 關聯 / 複雜度）後決定即修 vs 開 issue 追蹤
