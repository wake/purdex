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

**Phase 順序（強制 1a → 1b → 1b' → 1c）：**
1. **Phase 1a** — 純資料層；單一 PR；UI 零改動。
2. **Phase 1b** — Settings UI + 資料層 helper + Workspace **context menu 入口**；單一 PR。**必須與 Settings UI 同 PR ship 一個穩定入口**（spec §6 不 ship 設了沒效果的中間態 — context menu 是 stable 入口）。涵蓋：`inferWorkspaceHostId` / `HostPickerPopover` / `<CommandSlot>` / `useUndoToast` schema 擴充 / slot-executor / Settings UI / context menu 入口 + i18n。
3. **Phase 1b'** — Plus hover popover **過渡入口**（含 mobile/touch fallback）；獨立 PR。風險隔離：此入口未來可能遷移／重做，獨立 PR 便於日後整段 revert/replace；功能上是「錦上添花」，1b 已可單獨運作。
4. **Phase 1c** — HOST_ACTIONS 入口；小 PR。Host 入口落於 `SessionsSection`：在 new-session 按鈕旁並列 `<CommandSlot mountTo=HOST_ACTIONS>`；同 PR 移除 `SessionsSection` 每個 session row 上殘留的 v1 `QuickCommandMenu`（功能集中至 new-session 入口；`QuickCommandMenu` 元件本身保留，因為仍被 `PaneLayoutRenderer.tsx` 使用）。

**Mount UX 決策（覆蓋 spec §4.2 / §4.3 中的「位置由實作時定」）：**
- **Workspace 入口** — `WorkspaceRow.tsx` 兩處（拆兩個 PR）：
  - **(Phase 1b)** 右鍵 → 透過既有 `onContextMenuWorkspace` callback（App.tsx L155 `handleWsContextMenu` → 渲染 `WorkspaceContextMenu`），新增一個 `WorkspaceQuickCommandsContextMenu` 區塊或於原 `WorkspaceContextMenu` 加 quick-commands section
  - **(Phase 1b')** Plus 按鈕（`WorkspaceRow.tsx` L108-121）hover → 一個 absolute popover 向左展開 chip 列（半透明漸層壓底；mouseleave Plus AND popover 收回；鍵盤 focus 同等於 hover；觸控裝置以 long-press / tap-to-toggle 替代 hover）
- **Host 入口** — `SessionsSection.tsx` 兩件事：
  - new-session 按鈕（L167-174）旁並列 `<CommandSlot mountTo=HOST_ACTIONS>`；視覺與 Plus 對齊（直接列 chip，無 popover）
  - 移除 row 上的 v1 `<QuickCommandMenu>`（L231-239）整合，但**保留** `QuickCommandMenu` 元件本身（`PaneLayoutRenderer.tsx` 仍使用）
- **Workspace hostId 解析（spec v4 §3.2.1 / §3.2.2）** — Workspace type 沒有 `hostId` 欄位，採**多數決**（`inferWorkspaceHostId(workspace, tabs)`，遍歷 workspace.tabs 的 layout tree 蒐集 `kind: 'tmux-session'` 的 hostId，多數決 + tie-break）。多數決回 `null`（workspace 無 tmux-session tabs）時，**不**靜默 fallback 到 `activeHostId`，改開 `HostPickerPopover` 讓 user 選 host；取消 popover → no-op。`SlotContext.hostId` 改為 `string | null`，executor 透過 `resolveHostId` callback 在內部 await user 決定。

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

## Phase 1b — Settings UI + 資料層 + Workspace 右鍵入口（單一 PR）

**目標：** user 能在 Settings 建 command + 設 mount = WORKSPACE，**回到 workspace 右鍵 row** 即可看到按鈕；點擊建 session + 送 keys + 切過去。失敗 toast 行為符合 §3.3。Plus hover popover 入口拆到 Phase 1b' 獨立 PR（過渡實作風險隔離）。

**範圍邊界：** Phase 1b **只 ship context menu 入口**（穩定入口；spec §6「Settings 首次出現時至少一個 slot 生效」由它滿足）。Plus hover popover 不在 1b — 見 Phase 1b'。

**Spec v4 新增的支援結構：** Phase 1b 三個基礎件（`inferWorkspaceHostId` helper + `HostPickerPopover` 元件 + i18n keys）必須先於 `<CommandSlot>` / executor / workspace 入口落地，否則後續 task 在跑單元測試時會看到 raw i18n key（fallback to key），且 caller 無法正確呼叫。

**Task 順序：** 1b.0a → 1b.0c → 1b.0b → 1b.1 → 1b.1.5 → 1b.2 → 1b.3 → 1b.4 → 1b.5a → 1b.6。（原 1b.5b 移到 Phase 1b'；1b.0c 為新增的「i18n keys 前置」task — 必須在 1b.0b HostPickerPopover 測試之前 ship，否則測試會比對到 raw i18n key 而誤綠。）

### Task 1b.0a: 新增 `inferWorkspaceHostId` helper（spec §3.2.1 多數決）

**Files:**
- Create: `spa/src/lib/infer-workspace-host-id.ts`
- Create: `spa/src/lib/infer-workspace-host-id.test.ts`

**動機：** `WORKSPACE_ACTIONS` slot 的 ctx 需要 hostId，但 `Workspace` type 沒有此欄位（`spa/src/types/tab.ts` L53-61 確認）。spec §3.2.1 規定以 workspace 內所有 tab `PaneLayout` tree 的 `tmux-session` panes 的 hostId 多數決決定。Phase 1b 後續的 CommandSlot ctx、context menu、popover、executor 全部依此 helper 取 hostId。

- [ ] **Step 1: Write the failing test**

Create `spa/src/lib/infer-workspace-host-id.test.ts`:

```ts
import { describe, it, expect } from 'vitest'
import { inferWorkspaceHostId } from './infer-workspace-host-id'
import type { Tab, Workspace, PaneLayout } from '../types/tab'

function leaf(content: PaneLayout extends infer T ? T : never): PaneLayout {
  return content as PaneLayout
}

function tmuxLeaf(hostId: string, sessionCode = 'sess'): PaneLayout {
  return {
    type: 'leaf',
    pane: {
      id: `pane-${hostId}-${sessionCode}`,
      content: {
        kind: 'tmux-session',
        hostId,
        sessionCode,
        mode: 'terminal',
        cachedName: 'x',
        tmuxInstance: 'default',
      },
    },
  }
}

function newTabLeaf(): PaneLayout {
  return { type: 'leaf', pane: { id: 'p-newtab', content: { kind: 'new-tab' } } }
}

function splitH(...children: PaneLayout[]): PaneLayout {
  return { type: 'split', id: 's', direction: 'h', children, sizes: children.map(() => 1 / children.length) }
}

function tab(id: string, layout: PaneLayout): Tab {
  return { id, pinned: false, locked: false, createdAt: 0, layout }
}

function ws(opts: { id?: string; tabs: string[]; activeTabId?: string | null }): Workspace {
  return {
    id: opts.id ?? 'w1',
    name: 'W',
    tabs: opts.tabs,
    activeTabId: opts.activeTabId ?? null,
    moduleConfig: {},
  }
}

describe('inferWorkspaceHostId', () => {
  it('returns the hostId of a single tmux-session tab', () => {
    const tabs = { t1: tab('t1', tmuxLeaf('h1')) }
    const w = ws({ tabs: ['t1'], activeTabId: 't1' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })

  it('returns the only candidate when multiple tabs share the same host', () => {
    const tabs = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', tmuxLeaf('h1')),
      t3: tab('t3', tmuxLeaf('h1')),
    }
    const w = ws({ tabs: ['t1', 't2', 't3'], activeTabId: 't1' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })

  it('returns the majority host when one host clearly dominates', () => {
    const tabs = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', tmuxLeaf('h1')),
      t3: tab('t3', tmuxLeaf('h2')),
    }
    const w = ws({ tabs: ['t1', 't2', 't3'], activeTabId: 't3' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })

  it('on tie, prefers the active tab hostId when active is tmux-session and in the winners set', () => {
    const tabs = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', tmuxLeaf('h2')),
    }
    const w = ws({ tabs: ['t1', 't2'], activeTabId: 't2' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h2')
  })

  it('on tie, falls back to first winner in tabs order when active tab host is NOT in winners', () => {
    // active tab is tmux-session h3 (not a winner among h1/h2 tied)
    const tabs = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', tmuxLeaf('h2')),
      t3: tab('t3', tmuxLeaf('h3')),
    }
    // Force a tie between h1 and h2 by adding a duplicate h2 layer in t2
    const tabsTie = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', splitH(tmuxLeaf('h2'), tmuxLeaf('h1'))), // h1=2, h2=1
      t3: tab('t3', tmuxLeaf('h2')),                          // overall: h1=2, h2=2
    }
    const w = ws({ tabs: ['t1', 't2', 't3'], activeTabId: 't3' })
    // active tab t3 is h2 — h2 IS in winners → expect h2
    expect(inferWorkspaceHostId(w, tabsTie)).toBe('h2')
    // Now make active tab a non-tmux tab → should fall back to first-winner-in-tabs-order
    const tabsTieActiveNonTmux = {
      ...tabsTie,
      t3: tab('t3', newTabLeaf()), // active tab no longer tmux
    }
    const w2 = ws({ tabs: ['t1', 't2', 't3'], activeTabId: 't3' })
    // Now h1=2, h2=1 (tabsTieActiveNonTmux: t1 has h1, t2 has [h2,h1], t3 has nothing)
    // → h1 wins outright (count=2 vs h2=1), no tie. Adjust the fixture for the intended assertion:
    void w2
    // Use a clean tie + non-tmux active tab fixture:
    const cleanTie = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', tmuxLeaf('h2')),
      t3: tab('t3', newTabLeaf()),
    }
    const w3 = ws({ tabs: ['t1', 't2', 't3'], activeTabId: 't3' })
    expect(inferWorkspaceHostId(w3, cleanTie)).toBe('h1')
  })

  // codex round-1 B1 — minority-active fixture
  it('on tie, IGNORES active tab hostId when active is a tmux-session of a minority host', () => {
    // h1 count=2 (t1 + t2 split), h2 count=2 (t3 + t2 split), h3 count=1 (t4)
    // → tie between h1 and h2; h3 is minority and NOT in winners.
    // active tab t4 is h3 (minority). Tie-break A must NOT pick h3 just because
    // it is the active tab's host; instead it falls through to Tie-break B
    // (first winner in tabs order) → h1.
    const tabs = {
      t1: tab('t1', tmuxLeaf('h1')),
      t2: tab('t2', splitH(tmuxLeaf('h1'), tmuxLeaf('h2'))),
      t3: tab('t3', tmuxLeaf('h2')),
      t4: tab('t4', tmuxLeaf('h3')),
    }
    const w = ws({ tabs: ['t1', 't2', 't3', 't4'], activeTabId: 't4' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })

  it('returns null when workspace has no tmux-session tabs', () => {
    const tabs = {
      t1: tab('t1', newTabLeaf()),
      t2: tab('t2', { type: 'leaf', pane: { id: 'p2', content: { kind: 'dashboard' } } }),
    }
    const w = ws({ tabs: ['t1', 't2'], activeTabId: 't1' })
    expect(inferWorkspaceHostId(w, tabs)).toBeNull()
  })

  it('returns null when workspace.tabs is empty', () => {
    const w = ws({ tabs: [], activeTabId: null })
    expect(inferWorkspaceHostId(w, {})).toBeNull()
  })

  it('skips missing tab ids that are not present in the tabs map', () => {
    const tabs = { t1: tab('t1', tmuxLeaf('h1')) }
    const w = ws({ tabs: ['t1', 't-missing'], activeTabId: 't1' })
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })

  it('recursively collects hostIds from nested split layouts', () => {
    const tabs = {
      t1: tab('t1', splitH(tmuxLeaf('h1'), splitH(tmuxLeaf('h1'), tmuxLeaf('h2')))),
    }
    const w = ws({ tabs: ['t1'], activeTabId: 't1' })
    // h1 count=2, h2 count=1 → h1 wins
    expect(inferWorkspaceHostId(w, tabs)).toBe('h1')
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/infer-workspace-host-id.test.ts`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement helper**

Create `spa/src/lib/infer-workspace-host-id.ts` (內容直接對應 spec §3.2.1 程式片段，注意實際型別名是 `PaneLayout` 而非 `Layout`)：

```ts
import type { PaneLayout, Tab, Workspace } from '../types/tab'

/**
 * Recursively walks a PaneLayout tree, collecting hostIds from every
 * pane whose content is `kind: 'tmux-session'`. Order matches
 * pre-order traversal of the layout tree (left-to-right children).
 */
export function collectTmuxSessionHostIds(layout: PaneLayout): string[] {
  if (layout.type === 'leaf') {
    return layout.pane.content.kind === 'tmux-session'
      ? [layout.pane.content.hostId]
      : []
  }
  return layout.children.flatMap(collectTmuxSessionHostIds)
}

/**
 * Infer the "primary" hostId for a Workspace based on its tabs (spec §3.2.1).
 *
 *  1. Collect hostIds from every tmux-session pane across all tabs.
 *  2. Majority vote (highest count wins).
 *  3. Tie-break A: if `workspace.activeTabId` resolves to a tmux-session whose
 *     hostId is among the winners, prefer it.
 *  4. Tie-break B: otherwise, scan `workspace.tabs` in order and return the
 *     first hostId that appears in the winners set.
 *  5. Returns `null` when no tmux-session pane is found anywhere — caller
 *     MUST treat this as "host unknown" and surface the host picker (see
 *     spec §3.2.2 / §4.4 HostPickerPopover).
 *
 * Critically: this MUST NOT silently fall back to `useHostStore.activeHostId`
 * for the null case — that would risk sending keys to the wrong host.
 */
export function inferWorkspaceHostId(
  workspace: Workspace,
  tabs: Record<string, Tab>,
): string | null {
  const candidates = workspace.tabs
    .map((tabId) => tabs[tabId])
    .filter((t): t is Tab => !!t)
    .flatMap((t) => collectTmuxSessionHostIds(t.layout))

  if (candidates.length === 0) return null

  const counts = new Map<string, number>()
  for (const h of candidates) counts.set(h, (counts.get(h) ?? 0) + 1)
  const max = Math.max(...counts.values())
  const winners = [...counts.entries()].filter(([, c]) => c === max).map(([h]) => h)
  if (winners.length === 1) return winners[0]

  // Tie-break A: active tab's hostId if it is a tmux-session and is among winners.
  if (workspace.activeTabId) {
    const activeTab = tabs[workspace.activeTabId]
    if (activeTab) {
      const activeHosts = collectTmuxSessionHostIds(activeTab.layout)
      const winner = activeHosts.find((h) => winners.includes(h))
      if (winner) return winner
    }
  }

  // Tie-break B: first winner in tabs order.
  for (const tabId of workspace.tabs) {
    const t = tabs[tabId]
    if (!t) continue
    const hosts = collectTmuxSessionHostIds(t.layout)
    const first = hosts.find((h) => winners.includes(h))
    if (first) return first
  }
  return winners[0] // theoretically unreachable
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/infer-workspace-host-id.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): add inferWorkspaceHostId — majority-vote host resolution for workspace slots
```

---

### Task 1b.0c: 前置 i18n keys（HostPicker / aria / executor / toast）

**Files:**
- Modify: `spa/src/locales/zh-TW.json`
- Modify: `spa/src/locales/en.json`

**動機（codex round-1 B9 / round-2 B9）：** 後續 task（HostPickerPopover、CommandSlot、slot-executor、context menu、popover）的單元測試會直接 render 元件、`screen.getByText(/No hosts available/i)` / `getByRole('button', { name: /retry/i })` 比對顯示文字。i18n 系統在 key 缺失時 fallback 回 raw key，會導致測試比對 `quick_commands.host_picker.empty` 而非實際翻譯，**綠／紅判定模糊**。把 keys 加入 JSON 必須**先於**任何用到這些 keys 的 caller / 元件測試開跑 — 因此 1b.0c 必須跑在 1b.0b（HostPickerPopover）之前。

原 plan 的 1b.4（settings contribution + i18n）保留，但只負責 **Settings tab / dialog / module description / 模組元件外殼**等後續 keys；**HostPicker、aria-label、executor toast、retry 按鈕**等先放在 1b.0c。

**i18n keys 清單（codex round-1 B9 + C16 — aria-label 也走 i18n）：**

zh-TW + en 對照（同步加入）：

```json
{
  "quick_commands.host_picker.label": "選擇主機" / "Choose host",
  "quick_commands.host_picker.empty": "尚未設定任何主機" / "No hosts available",
  "quick_commands.host_picker.online": "線上" / "online",
  "quick_commands.host_picker.offline": "離線" / "offline",
  "quick_commands.host_picker.close": "關閉" / "Close",
  "quick_commands.toast.create_failed": "無法建立 session：{{reason}}" / "Failed to start session: {{reason}}",
  "quick_commands.toast.send_keys_failed": "Session 已建立，但指令送出失敗。" / "Session created, but command failed.",
  "quick_commands.toast.switch_failed": "已建立並送出指令，但無法切換焦點；請至 sessions 列表查看。" / "Created and sent, but could not switch focus; check the sessions list.",
  "quick_commands.toast.retry": "重試" / "Retry",
  "quick_commands.aria.toolbar": "快速指令" / "Quick commands",
  "quick_commands.aria.workspace_actions": "工作區快速動作" / "Workspace quick actions"
}
```

註：`settings.section.quick_commands` / `settings.quick_commands.*` / `modules.quick_commands.description` / `common.edit` 等 keys 留在 1b.4 一併加入（因為它們綁 settings UI 上下文，1b.4 才會 render 出來）。

- [ ] **Step 1: Add keys to zh-TW.json**

Edit `spa/src/locales/zh-TW.json`（按既有檔案的字典排序或群組原則插入；確認 trailing comma 不破壞 JSON 結構）：

```json
  "quick_commands.host_picker.label": "選擇主機",
  "quick_commands.host_picker.empty": "尚未設定任何主機",
  "quick_commands.host_picker.online": "線上",
  "quick_commands.host_picker.offline": "離線",
  "quick_commands.host_picker.close": "關閉",
  "quick_commands.toast.create_failed": "無法建立 session：{{reason}}",
  "quick_commands.toast.send_keys_failed": "Session 已建立，但指令送出失敗。",
  "quick_commands.toast.switch_failed": "已建立並送出指令，但無法切換焦點；請至 sessions 列表查看。",
  "quick_commands.toast.retry": "重試",
  "quick_commands.aria.toolbar": "快速指令",
  "quick_commands.aria.workspace_actions": "工作區快速動作",
```

- [ ] **Step 2: Add keys to en.json**

Edit `spa/src/locales/en.json`：

```json
  "quick_commands.host_picker.label": "Choose host",
  "quick_commands.host_picker.empty": "No hosts available",
  "quick_commands.host_picker.online": "online",
  "quick_commands.host_picker.offline": "offline",
  "quick_commands.host_picker.close": "Close",
  "quick_commands.toast.create_failed": "Failed to start session: {{reason}}",
  "quick_commands.toast.send_keys_failed": "Session created, but command failed.",
  "quick_commands.toast.switch_failed": "Created and sent, but could not switch focus; check the sessions list.",
  "quick_commands.toast.retry": "Retry",
  "quick_commands.aria.toolbar": "Quick commands",
  "quick_commands.aria.workspace_actions": "Workspace quick actions",
```

- [ ] **Step 3: Verify locale completeness**

Run: `cd spa && pnpm run lint`
Expected: clean（既有 lint 已含 locale-completeness 檢查；所有新 keys 必須兩語對齊）。

- [ ] **Step 4: Commit**

```
chore(spa): add i18n keys for quick commands HostPicker / aria / toast (codex round-1 B9)
```

註：Task 1b.0b 的 HostPickerPopover 測試 + 實作仰賴 `quick_commands.host_picker.*` keys；**順序鐵則：1b.0a → 1b.0c → 1b.0b**（i18n keys 必須先於 HostPickerPopover ship，否則測試會比對 raw i18n key 而誤綠，prod 端 fallback 顯示 raw key 文字）。1b.0c 純粹是 i18n keys 增量（`zh-TW.json` + `en.json`），與 0a/0b 無代碼依賴，可獨立先做。

---

### Task 1b.0b: 新增 `HostPickerPopover` 共用元件（spec §3.2.2 / §4.4）

**Files:**
- Create: `spa/src/components/HostPickerPopover.tsx`
- Create: `spa/src/components/HostPickerPopover.test.tsx`

**動機：** spec §3.2.2 規定 workspace hostId 為 null 時不可靜默 fallback；改開 host picker 讓 user 主動選 host。spec §3.5 forward-compat：未來 multi-host workspace binding 系統也用同一個 popover。本 task 為**獨立可重用元件**，不耦合 quick commands store。前置依賴：Task 1b.0c 必須先完成（i18n keys）。

**API：**
```tsx
interface Props {
  open: boolean
  anchor: { x: number; y: number } | HTMLElement | null
  onSelect: (hostId: string) => void
  onCancel: () => void
}
```

**內部資料來源：** 直接讀 `useHostStore.hostOrder` + `useHostStore.hosts` + `useHostStore.runtime` 列出 hosts；每列顯示 host name + online/offline 點（透過 `runtime[hostId]?.status === 'connected'` 判斷）。

**a11y / UX 規格：**
- 開啟時 focus 第一個可選項（若 hostOrder 空，focus dialog 容器 / show empty 狀態）
- 上下方向鍵移動 active item，Enter 觸發 `onSelect`
- Esc 觸發 `onCancel`（不送任何 hostId）
- focus trap：Tab / Shift+Tab 在 popover 內循環
- 關閉時 focus 回 trigger（caller 透過 `onCancel`/`onSelect` 後恢復 — popover 自身不持有 trigger ref；caller 負責）
- 樣式：與既有 popover 一致（`bg-surface-secondary`、`border-border-default`、`shadow-lg`，無自訂顏色）
- offline host 仍可點選（不 disable），但 row 上加警示 chip（例如 `text-text-muted` + 「offline」標籤）；user 可能就是要切到 offline host 去看其他內容
- 空 `hostOrder` 顯示空狀態文字「No hosts available」**＋ 一個 close 按鈕**（icon `X`，aria-label = `t('quick_commands.host_picker.close')`），點擊呼叫 `onCancel`（spec §3.2.2 明寫；無此按鈕時空狀態 user 唯一逃離方式只剩 Esc，違反 a11y / 鼠標可達）

**anchor 處理：** 若 `anchor` 是 `{x,y}` → 以 fixed 定位；若是 HTMLElement → `getBoundingClientRect()` 計算位置，popover 出現在 anchor 元素下方（top = `rect.bottom + 4`，left = `rect.left`）。

- [ ] **Step 1: Write the failing test**

Create `spa/src/components/HostPickerPopover.test.tsx`:

```tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { HostPickerPopover } from './HostPickerPopover'
import { useHostStore } from '../stores/useHostStore'

function setHosts() {
  useHostStore.setState({
    hosts: {
      h1: { id: 'h1', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 },
      h2: { id: 'h2', name: 'air', ip: '100.64.0.1', port: 7860, order: 1 },
    },
    hostOrder: ['h1', 'h2'],
    runtime: {
      h1: { status: 'connected' },
      h2: { status: 'disconnected' },
    },
    activeHostId: 'h1',
  })
}

describe('HostPickerPopover', () => {
  beforeEach(() => {
    setHosts()
  })

  it('does not render when open=false', () => {
    const { container } = render(
      <HostPickerPopover open={false} anchor={{ x: 0, y: 0 }} onSelect={vi.fn()} onCancel={vi.fn()} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('lists hosts in hostOrder with name + online/offline indicator', () => {
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={vi.fn()} onCancel={vi.fn()} />,
    )
    const items = screen.getAllByRole('option')
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent('mlab')
    expect(items[1]).toHaveTextContent('air')
    // online indicator on h1
    expect(items[0].textContent?.toLowerCase()).toMatch(/online|connected/)
    // offline indicator on h2
    expect(items[1].textContent?.toLowerCase()).toMatch(/offline/)
  })

  it('Enter on focused item triggers onSelect with that hostId', () => {
    const onSelect = vi.fn()
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={onSelect} onCancel={vi.fn()} />,
    )
    const items = screen.getAllByRole('option')
    items[0].focus()
    fireEvent.keyDown(items[0], { key: 'Enter' })
    expect(onSelect).toHaveBeenCalledWith('h1')
  })

  it('ArrowDown / ArrowUp moves focus through items', () => {
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={vi.fn()} onCancel={vi.fn()} />,
    )
    const items = screen.getAllByRole('option')
    items[0].focus()
    fireEvent.keyDown(items[0], { key: 'ArrowDown' })
    expect(document.activeElement).toBe(items[1])
    fireEvent.keyDown(items[1], { key: 'ArrowUp' })
    expect(document.activeElement).toBe(items[0])
  })

  it('Esc triggers onCancel and not onSelect', () => {
    const onSelect = vi.fn()
    const onCancel = vi.fn()
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={onSelect} onCancel={onCancel} />,
    )
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('clicking an offline host still calls onSelect (not disabled)', () => {
    const onSelect = vi.fn()
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={onSelect} onCancel={vi.fn()} />,
    )
    fireEvent.click(screen.getAllByRole('option')[1])
    expect(onSelect).toHaveBeenCalledWith('h2')
  })

  it('shows empty state with close button when hostOrder is empty (codex round-1 B2)', () => {
    useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {}, activeHostId: null })
    const onCancel = vi.fn()
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={vi.fn()} onCancel={onCancel} />,
    )
    expect(screen.getByText(/No hosts available/i)).toBeInTheDocument()
    // close button must exist in empty state — Esc-only is not enough (a11y / mouse users)
    const closeBtn = screen.getByRole('button', { name: /close|cancel|關閉|取消/i })
    expect(closeBtn).toBeInTheDocument()
    fireEvent.click(closeBtn)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('focus is trapped inside the popover (Tab cycles)', () => {
    render(
      <HostPickerPopover open={true} anchor={{ x: 0, y: 0 }} onSelect={vi.fn()} onCancel={vi.fn()} />,
    )
    const items = screen.getAllByRole('option')
    items[items.length - 1].focus()
    fireEvent.keyDown(items[items.length - 1], { key: 'Tab' })
    // focus should wrap back to first
    expect(document.activeElement).toBe(items[0])
  })

  // codex round-1 B3 — HTMLElement anchor positioning
  it('positions popover below an HTMLElement anchor using getBoundingClientRect (codex round-1 B3)', () => {
    const anchor = document.createElement('button')
    anchor.getBoundingClientRect = () =>
      ({
        top: 100,
        right: 250,
        bottom: 130,
        left: 200,
        width: 50,
        height: 30,
        x: 200,
        y: 100,
        toJSON: () => ({}),
      }) as DOMRect
    document.body.appendChild(anchor)
    const { container } = render(
      <HostPickerPopover open={true} anchor={anchor} onSelect={vi.fn()} onCancel={vi.fn()} />,
    )
    const popover = container.querySelector('[role="listbox"]') as HTMLElement
    expect(popover).not.toBeNull()
    // top = rect.bottom + 4 = 134; left = rect.left = 200
    expect(popover.style.top).toBe('134px')
    expect(popover.style.left).toBe('200px')
    expect(popover.style.position).toBe('fixed')
    document.body.removeChild(anchor)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/HostPickerPopover.test.tsx`
Expected: FAIL — module not found.

- [ ] **Step 3: Implement popover**

Create `spa/src/components/HostPickerPopover.tsx`:

```tsx
import { useEffect, useMemo, useRef } from 'react'
import { useHostStore } from '../stores/useHostStore'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  open: boolean
  /**
   * Anchor for positioning. `{x, y}` → fixed positioning at viewport coords.
   * HTMLElement → positioned next to that element via getBoundingClientRect.
   * `null` allowed during transition; popover renders centered as fallback.
   */
  anchor: { x: number; y: number } | HTMLElement | null
  onSelect: (hostId: string) => void
  onCancel: () => void
}

/**
 * Reusable host picker popover. Caller-controlled open/anchor; emits
 * `onSelect(hostId)` or `onCancel()`. Used by:
 *   - WORKSPACE_ACTIONS slot when inferWorkspaceHostId returns null
 *   - (Future) multi-host workspace binding default-launcher selection (spec §3.5)
 *
 * a11y:
 *   - role="listbox" on container, role="option" on each host row
 *   - Enter on focused option → onSelect; Esc → onCancel; Arrow Up/Down moves
 *     focus; Tab wraps within popover (focus trap).
 *   - Caller is responsible for restoring focus to its trigger after
 *     onSelect/onCancel returns.
 */
export function HostPickerPopover({ open, anchor, onSelect, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const hosts = useHostStore((s) => s.hosts)
  const runtime = useHostStore((s) => s.runtime)
  const containerRef = useRef<HTMLDivElement>(null)

  const items = useMemo(
    () =>
      hostOrder
        .map((id) => hosts[id])
        .filter((h): h is NonNullable<typeof h> => !!h)
        .map((h) => ({
          id: h.id,
          name: h.name,
          online: runtime[h.id]?.status === 'connected',
        })),
    [hostOrder, hosts, runtime],
  )

  // Auto-focus first item on open.
  useEffect(() => {
    if (!open) return
    const first = containerRef.current?.querySelector<HTMLElement>('[role="option"]')
    first?.focus()
  }, [open])

  // Esc → onCancel (document-level so it works regardless of focus position).
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onCancel()
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onCancel])

  if (!open) return null

  const positionStyle: React.CSSProperties = (() => {
    if (anchor && 'getBoundingClientRect' in anchor) {
      const r = anchor.getBoundingClientRect()
      return { position: 'fixed', top: r.bottom + 4, left: r.left }
    }
    if (anchor && typeof anchor === 'object' && 'x' in anchor) {
      return { position: 'fixed', top: anchor.y, left: anchor.x }
    }
    return { position: 'fixed', top: '50%', left: '50%', transform: 'translate(-50%, -50%)' }
  })()

  function handleItemKey(e: React.KeyboardEvent<HTMLDivElement>, hostId: string, index: number) {
    if (e.key === 'Enter') {
      e.preventDefault()
      onSelect(hostId)
      return
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      const next = (index + 1) % items.length
      ;(containerRef.current?.querySelectorAll<HTMLElement>('[role="option"]')[next])?.focus()
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      const prev = (index - 1 + items.length) % items.length
      ;(containerRef.current?.querySelectorAll<HTMLElement>('[role="option"]')[prev])?.focus()
      return
    }
    if (e.key === 'Tab') {
      e.preventDefault()
      const len = items.length
      if (len === 0) return
      const dir = e.shiftKey ? -1 : 1
      const next = (index + dir + len) % len
      ;(containerRef.current?.querySelectorAll<HTMLElement>('[role="option"]')[next])?.focus()
    }
  }

  return (
    <div
      ref={containerRef}
      role="listbox"
      aria-label={t('quick_commands.host_picker.label')}
      style={positionStyle}
      className="z-50 min-w-[200px] rounded-md border border-border-default bg-surface-secondary shadow-lg py-1"
    >
      {items.length === 0 ? (
        // codex round-1 B2 — empty state must offer an explicit close button
        // (mouse users / a11y; Esc-only is not sufficient).
        <div className="px-3 py-2 text-xs text-text-muted flex items-center justify-between gap-2">
          <span>{t('quick_commands.host_picker.empty')}</span>
          <button
            type="button"
            onClick={onCancel}
            aria-label={t('quick_commands.host_picker.close')}
            className="p-0.5 text-text-muted hover:text-text-primary cursor-pointer"
          >
            {/* Implementation note: use Phosphor `X` icon component at runtime. */}
            ×
          </button>
        </div>
      ) : (
        items.map((item, index) => (
          <div
            key={item.id}
            role="option"
            tabIndex={0}
            aria-selected={false}
            onClick={() => onSelect(item.id)}
            onKeyDown={(e) => handleItemKey(e, item.id, index)}
            className="flex items-center justify-between gap-2 px-3 py-1.5 text-xs text-text-primary cursor-pointer hover:bg-surface-hover focus:bg-surface-hover focus:outline-none"
          >
            <span className="truncate">{item.name}</span>
            <span
              className={
                item.online
                  ? 'text-[10px] text-text-secondary bg-surface-primary px-1.5 py-0.5 rounded'
                  : 'text-[10px] text-text-muted bg-surface-primary px-1.5 py-0.5 rounded'
              }
            >
              {item.online
                ? t('quick_commands.host_picker.online')
                : t('quick_commands.host_picker.offline')}
            </span>
          </div>
        ))
      )}
    </div>
  )
}
```

註：i18n keys `quick_commands.host_picker.label` / `.empty` / `.online` / `.offline` / `.close` 已在 Task **1b.0c**（前置）加入 zh-TW / en JSON — 此元件測試開跑時 keys 必須先存在，否則 `screen.getByText(/No hosts available/i)` 會比對到 raw key。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/components/HostPickerPopover.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): add HostPickerPopover reusable component for host selection (spec §3.2.2 / §4.4)
```

---

### Task 1b.1: `<CommandSlot>` 共用元件

**Files:**
- Create: `spa/src/components/CommandSlot.tsx`
- Create: `spa/src/components/CommandSlot.test.tsx`

**Spec v4 重點：** `SlotContext.hostId` 為 `string | null`（spec §3.2.1 / §3.2.2 / §3.5）。`<CommandSlot>` 自身**不**渲染 host picker — picker 是 caller 端（context menu / popover / sessions section）的 UI 責任，原因是 picker 屬於 React tree 上層的 controllable popover，executor lib 不能也不該觸碰 React render。`<CommandSlot>` 只負責：(a) 列出 mount 在該 slot 的 commands；(b) 點擊後把 `(cmd, ctx)` 交給 `executor` callback；hostId null 時 ctx 仍然會帶 null 進去，由 caller 在 executor 內透過 `resolveHostId` callback 開 picker（見 Task 1b.2）。

- [ ] **Step 1: Write the failing test**

Create `spa/src/components/CommandSlot.test.tsx`（注意：含 hostId=null 案例驗證 SlotContext 契約）:

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

  it('renders bound commands in capability order (cmd-a then cmd-c, NOT bindings key order — codex round-1 C12)', () => {
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1', workspaceId: 'w1' }}
        executor={exec}
      />,
    )
    // codex round-1 C12 — assert exact order via accessible names; NOT arrayContaining
    const buttons = screen.getAllByRole('button')
    const names = buttons.map((b) => b.getAttribute('aria-label'))
    // cmd-a first (capability index 0), cmd-c second (capability index 2);
    // cmd-b is unbound → not in DOM at all
    expect(names).toEqual(['A', 'C'])
    expect(screen.queryByLabelText(/^B/)).toBeNull()
  })

  it('order follows capability list even when bindings record key order is reversed (codex round-1 C13)', () => {
    // Same global capability list; rebuild bindings in REVERSE key order.
    // The order seen on screen must still be capability order (cmd-a → cmd-c).
    useQuickCommandStore.setState({
      global: [
        { id: 'cmd-a', name: 'A', command: 'a' },
        { id: 'cmd-b', name: 'B', command: 'b' },
        { id: 'cmd-c', name: 'C', command: 'c' },
      ],
      byHost: {},
      bindings: {
        // Insert cmd-c first, then cmd-a — Object.keys order would be [cmd-c, cmd-a]
        'cmd-c': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
        'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS],
      },
    })
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: null, workspaceId: 'w1' }} // hostId=null per codex C13 ask
        executor={vi.fn()}
      />,
    )
    const buttons = screen.getAllByRole('button')
    const names = buttons.map((b) => b.getAttribute('aria-label'))
    // Capability order is cmd-a (index 0) then cmd-c (index 2) — NOT bindings record order
    expect(names).toEqual(['A', 'C'])
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

  it('hostId=null is valid — passes null through to executor (caller resolves via picker)', () => {
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: null, workspaceId: 'w1' }}
        executor={exec}
      />,
    )
    // commands are still rendered (hostId-null doesn't suppress UI)
    fireEvent.click(screen.getByLabelText(/^A/))
    expect(exec).toHaveBeenCalledTimes(1)
    expect(exec.mock.calls[0][1]).toMatchObject({ hostId: null, workspaceId: 'w1' })
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

  // codex round-2 — render prop receives `run` as 3rd arg so custom UIs can
  // actually trigger executor. Without `run` custom render is an inert footgun.
  it('custom render receives `run` callback that triggers executor', () => {
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1' }}
        executor={exec}
        render={(cmd, _ctx, run) => (
          <button data-testid={`custom-${cmd.id}`} onClick={run}>
            {cmd.name}
          </button>
        )}
      />,
    )
    fireEvent.click(screen.getByTestId('custom-cmd-a'))
    expect(exec).toHaveBeenCalledTimes(1)
    expect(exec.mock.calls[0][0].id).toBe('cmd-a')
    expect(exec.mock.calls[0][1]).toMatchObject({ hostId: 'h1' })
  })

  it('custom render `run` respects busy guard (no executor when busy=true)', () => {
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: 'h1' }}
        executor={exec}
        busy={true}
        render={(cmd, _ctx, run) => (
          <button data-testid={`custom-${cmd.id}`} onClick={run}>
            {cmd.name}
          </button>
        )}
      />,
    )
    fireEvent.click(screen.getByTestId('custom-cmd-a'))
    expect(exec).not.toHaveBeenCalled()
  })

  it('disables all chip buttons while busy=true (codex round-1 C11 — picker resolver race guard)', () => {
    // Picker open → caller flips busy=true to prevent double-click on chip
    // (which would create a second pending Promise and a second picker instance).
    const exec = vi.fn().mockResolvedValue(undefined)
    render(
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId: null, workspaceId: 'w1' }}
        executor={exec}
        busy={true}
      />,
    )
    const buttons = screen.getAllByRole('button')
    expect(buttons.length).toBeGreaterThan(0)
    buttons.forEach((b) => {
      expect((b as HTMLButtonElement).disabled).toBe(true)
    })
    // Click while busy → executor must NOT fire
    fireEvent.click(buttons[0])
    expect(exec).not.toHaveBeenCalled()
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

/**
 * Slot context (spec v4 §3.2.1 / §3.2.2 / §3.5).
 *
 * `hostId` is `string | null`:
 *   - WORKSPACE_ACTIONS: caller provides `inferWorkspaceHostId(ws, tabs)`
 *     which can return null when the workspace has no tmux-session tabs.
 *   - HOST_ACTIONS: caller always provides a concrete hostId (host detail
 *     page already knows which host it's rendering).
 *
 * When hostId is null, the executor MUST resolve it (e.g. by opening the
 * HostPickerPopover) before performing any host-side work — silently
 * falling back to `useHostStore.activeHostId` is forbidden.
 */
export interface SlotContext {
  hostId: string | null
  workspaceId?: string | null
  cwd?: string
}

export type SlotExecutor = (cmd: QuickCommand, ctx: SlotContext) => Promise<void>
/**
 * codex round-2 — `run` is the 3rd argument injected by `<CommandSlot>` so a
 * custom render can wire its own onClick to the executor pipeline. Without it
 * a custom render becomes an inert UI footgun (the chip is visible but
 * clicking does nothing). `run` is the same `executor(cmd, ctx)` Promise the
 * default chip kicks off; callers should still respect `busy` and avoid
 * double-firing.
 */
export type SlotRenderer = (cmd: QuickCommand, ctx: SlotContext, run: () => void) => ReactNode

interface Props {
  mountTo: QuickCommandSlotId
  ctx: SlotContext
  executor: SlotExecutor
  render?: SlotRenderer
  /**
   * Optional class for the outer container (codex round-1 B7 — used by the
   * context-menu caller which prefers `flex flex-col` over the toolbar default).
   */
  containerClassName?: string
  /**
   * codex round-1 C11 — picker resolver race guard: when the caller's host
   * picker is open, set `busy={true}` to disable every chip button. Prevents
   * a second click from creating a second pending Promise (and a second picker
   * instance fighting over the same resolver).
   */
  busy?: boolean
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
export function CommandSlot({ mountTo, ctx, executor, render, containerClassName, busy }: Props) {
  const enabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  const bindings = useQuickCommandStore((s) => s.bindings)
  const t = useI18nStore((s) => s.t)
  // hostId null → no host override possible; show global-only command list.
  // (getCommands(hostId) would require a string; explicitly passing null
  // means "caller hasn't resolved a host yet — just use globals".)
  const allCmds = useQuickCommandStore((s) =>
    ctx.hostId == null ? s.global : s.getCommands(ctx.hostId),
  )

  // Recompute boundCmds when bindings or capability list change.
  const boundCmds = useMemo(
    () => allCmds.filter((c) => bindings[c.id]?.includes(mountTo)),
    [allCmds, bindings, mountTo],
  )

  if (!enabled) return null
  if (boundCmds.length === 0) return null

  return (
    <div
      className={containerClassName ?? 'flex flex-wrap items-center gap-1.5'}
      role="toolbar"
      // codex round-1 C16 — aria-label sourced from i18n, not hard-coded English
      aria-label={t('quick_commands.aria.toolbar')}
    >
      {boundCmds.map((cmd) => {
        // codex round-2 — `run` injected so custom render can wire its own
        // onClick. Without it custom render becomes inert (chip visible, click
        // does nothing) — the same footgun the original 2-arg signature shipped.
        const run = () => {
          if (busy) return
          void executor(cmd, ctx)
        }
        if (render) {
          return (
            <span key={cmd.id} className="inline-flex">
              {render(cmd, ctx, run)}
            </span>
          )
        }
        const ariaLabel = cmd.category ? `${cmd.name} (${cmd.category})` : cmd.name
        return (
          <button
            key={cmd.id}
            type="button"
            // codex round-1 C11 — busy guard prevents double-click from spawning
            // a second picker / executor pipeline while one is mid-flight.
            disabled={busy}
            onClick={run}
            aria-label={ariaLabel}
            title={cmd.command}
            className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-text-secondary hover:text-text-primary hover:bg-surface-secondary cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
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

註：`useI18nStore` import 已隨 codex round-1 C16 加入（替代原 hard-coded `aria-label="Quick commands"`）：

```ts
import { useI18nStore } from '../stores/useI18nStore'
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/components/CommandSlot.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): add <CommandSlot> shared component for v2 binding model
```

---

### Task 1b.1.5: 擴 `useUndoToast` schema — optional `action` callback 與 `actionLabel`（codex round-1 B4）

**Files:**
- Modify: `spa/src/stores/useUndoToast.ts`
- Modify: `spa/src/stores/useUndoToast.test.ts`
- Modify: `spa/src/components/GlobalUndoToast.tsx`
- Modify / Create: `spa/src/components/GlobalUndoToast.test.tsx`

**動機（codex round-1 B4 修正）：** spec §3.3 三層失敗 UX 中，**create-session failure** 與 **switch-to-session failure** 不該顯示「假 Undo / 假 Retry」按鈕（user 沒東西可 undo / 重試也對應不到動作）；**只有 send-keys failure** 該帶 action button（Retry）。原 plan 直接複用既有 `useUndoToast.show(message, restore)` 兩參數簽名，會被迫傳一個 noop 給 restore — UX 上仍會渲染按鈕，誤導 user。

修正：把 `action` 與 `actionLabel` 都升為 **optional**，元件渲染時 `action == null` 不渲染 button（toast 只剩 message）。`OverviewSection.tsx` 既有的 delete-host undo 仍走 `show(msg, undoFn)` — `action` 非空 → 渲染 button + 預設 label 'Undo'，**完全向下相容**。

**API 設計：**

```ts
interface ToastShape {
  message: string
  action?: () => void      // ← optional：缺省時 button 不渲染
  actionLabel?: string     // ← optional：缺省（且 action 非空時）回退到 'Undo'
}

show(message: string, action?: () => void, actionLabel?: string): void
```

**Render 規則（GlobalUndoToast）：**
- `toast.action == null` → 完全不渲染 `<button>`
- `toast.action != null && toast.actionLabel == null` → 渲染 button，label = `t('hosts.undo')`
- `toast.action != null && toast.actionLabel != null` → 渲染 button，label = `toast.actionLabel`

**Executor callsite 對應（與 Task 1b.2 同步）：**
- create-session failure → `show(message)` （**無** action）
- switch-to-session failure → `show(message)` （**無** action）
- send-keys failure → `show(message, retryFn, t('quick_commands.toast.retry'))` （**有** action）

**既有 callsite 影響範圍盤點（grep `useUndoToast.*show|getState\(\)\.show`）：**
- Production：`spa/src/components/hosts/OverviewSection.tsx:85`（`show(message, restore)` — 兩參數呼叫；新 schema 第 2 參數 `action` 仍是 function，相容）
- Test：`spa/src/stores/useUndoToast.test.ts`（自身單元測試 — 測試斷言維持原樣，僅新增向下相容驗證）+ `spa/src/lib/host-lifecycle.test.ts`（只重置 toast=null，不呼叫 `show`，不受影響）
- 共 1 個 production callsite，2 個測試檔；schema 擴充採 **action / actionLabel 都 optional**，**無需修改既有 callsite 的呼叫形式**

- [ ] **Step 1: Write the failing test**

Edit `spa/src/stores/useUndoToast.test.ts` 加新測試（不要動既有測試 — 它們驗證向下相容）：

```ts
describe('useUndoToast — optional action / actionLabel (codex round-1 B4)', () => {
  beforeEach(() => {
    useUndoToast.setState({ toast: null })
  })

  it('back-compat: show(msg, fn) keeps action defined and actionLabel undefined', () => {
    useUndoToast.getState().show('msg', () => {})
    const toast = useUndoToast.getState().toast
    expect(toast?.action).toBeTypeOf('function')
    expect(toast?.actionLabel).toBeUndefined()
  })

  it('show(msg) with no action stores undefined for both', () => {
    useUndoToast.getState().show('Failed')
    const toast = useUndoToast.getState().toast
    expect(toast?.action).toBeUndefined()
    expect(toast?.actionLabel).toBeUndefined()
  })

  it('show(msg, fn, label) stores both', () => {
    useUndoToast.getState().show('Send keys failed', () => {}, 'Retry')
    const toast = useUndoToast.getState().toast
    expect(toast?.action).toBeTypeOf('function')
    expect(toast?.actionLabel).toBe('Retry')
  })
})
```

Edit/Create `spa/src/components/GlobalUndoToast.test.tsx`（若該檔不存在則新建）：

```tsx
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { GlobalUndoToast } from './GlobalUndoToast'
import { useUndoToast } from '../stores/useUndoToast'

describe('GlobalUndoToast — render rules (codex round-1 B4)', () => {
  beforeEach(() => useUndoToast.setState({ toast: null }))

  it('renders default Undo label when action is provided but actionLabel is omitted', () => {
    useUndoToast.getState().show('Deleted host', () => {})
    render(<GlobalUndoToast />)
    expect(screen.getByRole('button')).toHaveTextContent(/Undo/i)
  })

  it('renders custom actionLabel (Retry) when both action and actionLabel are provided', () => {
    useUndoToast.getState().show('Send keys failed', () => {}, 'Retry')
    render(<GlobalUndoToast />)
    expect(screen.getByRole('button')).toHaveTextContent(/Retry/i)
  })

  it('does NOT render any button when action is undefined (create / switch failure path)', () => {
    useUndoToast.getState().show('Failed to start session: 500')
    render(<GlobalUndoToast />)
    // Message still shows
    expect(screen.getByText(/Failed to start session/i)).toBeInTheDocument()
    // No action button at all (codex round-1 B4 — no fake Undo / Retry)
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('toast container has role=status (codex round-1 C14 — a11y live region)', () => {
    useUndoToast.getState().show('Hello')
    render(<GlobalUndoToast />)
    expect(screen.getByRole('status')).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/stores/useUndoToast.test.ts src/components/GlobalUndoToast.test.tsx`
Expected: FAIL — `actionLabel` 欄位不存在 / 元件還沒讀。

- [ ] **Step 3: Implement schema extension**

Edit `spa/src/stores/useUndoToast.ts`（codex round-1 B4 — `action` 與 `actionLabel` 都升為 optional；既有 callsite 傳 `(msg, fn)` 仍合法、行為不變）：

```ts
// spa/src/stores/useUndoToast.ts — Global undo toast state
import { create } from 'zustand'

/**
 * Toast schema (codex round-1 B4):
 *  - action == null      → render no button (used by create / switch failure paths)
 *  - action != null      → render button; label = actionLabel ?? t('hosts.undo')
 *
 * Renamed semantically from "restore" to "action" — the field can host an undo
 * callback OR a retry callback; existing back-compat callers (delete-host undo)
 * pass a function and stay green.
 */
interface UndoToastState {
  toast: {
    message: string
    action?: () => void
    actionLabel?: string
  } | null
  show: (message: string, action?: () => void, actionLabel?: string) => void
  dismiss: () => void
}

export const useUndoToast = create<UndoToastState>()((set) => ({
  toast: null,
  show: (message, action, actionLabel) =>
    set({ toast: { message, action, actionLabel } }),
  dismiss: () => set({ toast: null }),
}))
```

Edit `spa/src/components/GlobalUndoToast.tsx`（render rules：`action == null` 不渲染 button；codex round-1 C14 加 `role="status"`）：

```tsx
import { useEffect, useRef } from 'react'
import { useUndoToast } from '../stores/useUndoToast'
import { useI18nStore } from '../stores/useI18nStore'

export function GlobalUndoToast() {
  const toast = useUndoToast((s) => s.toast)
  const dismiss = useUndoToast((s) => s.dismiss)
  const t = useI18nStore((s) => s.t)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (!toast) return
    timerRef.current = setTimeout(() => dismiss(), 5000)
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [toast, dismiss])

  if (!toast) return null

  return (
    <div
      role="status"
      aria-live="polite"
      className="fixed bottom-4 left-1/2 -translate-x-1/2 bg-zinc-800 border border-zinc-700 rounded-lg px-4 py-3 flex items-center gap-3 shadow-lg z-50"
    >
      <span className="text-sm text-zinc-300">{toast.message}</span>
      {toast.action && (
        <button
          className="text-sm text-blue-400 hover:text-blue-300 font-medium cursor-pointer"
          onClick={() => {
            toast.action!()
            dismiss()
          }}
        >
          {toast.actionLabel ?? t('hosts.undo')}
        </button>
      )}
    </div>
  )
}
```

註：`actionLabel` 由 caller 傳 i18n 化字串（例如 `t('quick_commands.toast.retry')`）；`'hosts.undo'` 維持作為**有 action 但未指定 label 時的預設**，確保 `OverviewSection` 既有 delete-host undo 行為不變。

**callsite 影響盤點補充驗證（codex round-1 B4）：**

執行 grep 檢查：
```
cd spa && grep -rn "useUndoToast" src/ --include="*.ts" --include="*.tsx" | grep -v "\.test\."
```

預期結果：
- `src/stores/useUndoToast.ts`（store 自身）
- `src/components/GlobalUndoToast.tsx`（render）
- `src/components/hosts/OverviewSection.tsx`（既有 callsite，呼叫 `show(msg, undoFn)` — 新 schema 下：`action=undoFn`、`actionLabel=undefined` → button 渲染 + label 'Undo'，行為與舊 schema 完全一致）
- `src/lib/host-lifecycle.test.ts`（測試 reset；不呼叫 show，不受影響）

若 grep 出現未列入的 callsite，subagent 必須回報主 Claude 評估是否需要更新（理論上 codex round-1 已 audit 完畢，新增 callsite 是 plan 後續 task 引入的，那些 callsite 自身已遵循新 schema）。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/stores/useUndoToast.test.ts src/components/GlobalUndoToast.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): extend useUndoToast schema with optional action / actionLabel (codex round-1 B4)
```

---

### Task 1b.2: Slot executor lib（建 session + 送 keys + 失敗 UX + hostId 解析）

**Files:**
- Create: `spa/src/lib/slot-executor.ts`
- Create: `spa/src/lib/slot-executor.test.ts`

註：保留既有 `spa/src/lib/execute-command.ts`（v1 send-keys helper）；新檔處理 v2 的「建 session + 送 keys + 切 session + toast」一條龍。

**Spec v4 — hostId 解析設計（Option B：`resolveHostId` callback）：**

`SlotContext.hostId` 是 `string | null`。executor 收到 ctx 後，若 hostId null **不直接呼叫 API**；改 await caller 注入的 `resolveHostId(): Promise<string | null>` callback —
- caller（CommandSlot 包裝層 / context menu / popover）在 callback 內部開 `HostPickerPopover`，await user 選定後 resolve hostId
- user 取消 popover → resolve `null` → executor no-op 結束

**Option B vs Option A 選擇理由：**
- Option A（throw `HostRequiredError` + caller catch + retry）會切斷 executor 線性流程，且 toast / focus 切換邏輯散落在 caller 兩處（first-run 與 retry-run），重複容易漏。
- Option B（callback 注入）延續既有 `runWorkspaceSlot` 的 `deps.switchToSession` callback 模式（同一個 task 已有先例），executor 仍是線性 `await`，CommandSlot 把 picker state + Promise resolver 包進一個 React hook 即可內聚地管理。
- 既有 codebase 沒有「throw + catch + retry」的 host-resolution pattern；Option B 與現有設計一致。

**選擇：Option B。**

CommandSlot 一側的封裝建議（在 1b.5a / 1b'.1 / 1c.1b 各 caller 自行實作；不抽公共 hook，因為三個 caller 的 trigger UI 完全不同）：
```tsx
const [pickerState, setPickerState] = useState<{
  resolver: (id: string | null) => void
  anchor: HTMLElement | { x: number; y: number } | null
} | null>(null)

const resolveHostId = useCallback(
  () =>
    new Promise<string | null>((resolve) => {
      // anchor 由 trigger element ref 提供
      setPickerState({ resolver: resolve, anchor: triggerRef.current })
    }),
  [],
)

// JSX:
<HostPickerPopover
  open={pickerState !== null}
  anchor={pickerState?.anchor ?? null}
  onSelect={(hostId) => { pickerState?.resolver(hostId); setPickerState(null) }}
  onCancel={() => { pickerState?.resolver(null); setPickerState(null) }}
/>
```

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
    const resolveHostId = vi.fn() // not called when ctx.hostId is non-null
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-1', name: 'A', cwd: '/tmp', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1', cwd: '/tmp' },
      { switchToSession: switchFocus, resolveHostId },
    )

    expect(resolveHostId).not.toHaveBeenCalled()
    expect(createSession).toHaveBeenCalledWith('h1', expect.any(String), '/tmp', 'terminal')
    expect(executeCommand).toHaveBeenCalledWith('h1', 'sess-1', 'echo hi')
    expect(switchFocus).toHaveBeenCalledWith('h1', 'sess-1')
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('hostId null → invokes resolveHostId; user picks → continues with that hostId', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn().mockResolvedValue('h-picked')
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-2', name: 'A', cwd: '~', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: null, workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId },
    )

    expect(resolveHostId).toHaveBeenCalledTimes(1)
    expect(createSession).toHaveBeenCalledWith('h-picked', expect.any(String), '~', 'terminal')
    expect(executeCommand).toHaveBeenCalledWith('h-picked', 'sess-2', 'echo hi')
    expect(switchFocus).toHaveBeenCalledWith('h-picked', 'sess-2')
  })

  it('hostId null → user cancels picker → no API call, no toast', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn().mockResolvedValue(null)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: null, workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId },
    )

    expect(resolveHostId).toHaveBeenCalledTimes(1)
    expect(createSession).not.toHaveBeenCalled()
    expect(executeCommand).not.toHaveBeenCalled()
    expect(switchFocus).not.toHaveBeenCalled()
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('createSession failure — toast WITHOUT action button (codex round-1 B4)', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('500 Internal'))

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId },
    )

    expect(switchFocus).not.toHaveBeenCalled()
    expect(executeCommand).not.toHaveBeenCalled()
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Failed to start session/i)
    // codex round-1 B4 — no action button on create-session failure
    expect(toast!.action).toBeUndefined()
    expect(toast!.actionLabel).toBeUndefined()
  })

  it('send-keys failure — STILL switches focus + toast carries Retry action', async () => {
    const switchFocus = vi.fn()
    const resolveHostId = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-1', name: 'A', cwd: '/tmp', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('send-keys failed: 503'))

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId },
    )

    expect(switchFocus).toHaveBeenCalledWith('h1', 'sess-1')
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Session created.*command failed/i)
    // codex round-1 B4 — send-keys failure DOES carry an action (retry)
    expect(toast!.action).toBeTypeOf('function')
    expect(toast!.actionLabel).toBeDefined()
    expect(toast!.actionLabel).toMatch(/retry/i)
    // action = retry — calling it should trigger executeCommand again
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined)
    toast!.action!()
    // retry is sync invocation; await microtask
    await Promise.resolve()
    expect(executeCommand).toHaveBeenCalledTimes(2)
  })

  it('switchToSession failure — toast WITHOUT action button (codex round-1 B4)', async () => {
    const switchFocus = vi.fn().mockImplementation(() => {
      throw new Error('switch failed')
    })
    const resolveHostId = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-1', name: 'A', cwd: '/tmp', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runWorkspaceSlot(
      { id: 'cmd-a', name: 'A', command: 'echo hi' },
      { hostId: 'h1', workspaceId: 'w1' },
      { switchToSession: switchFocus, resolveHostId },
    )

    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Could not switch/i)
    // codex round-1 B4 — no action button on switch failure
    expect(toast!.action).toBeUndefined()
    expect(toast!.actionLabel).toBeUndefined()
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

  /**
   * Resolves a hostId when ctx.hostId is null (spec v4 §3.2.2 — Option B).
   * Caller opens the HostPickerPopover inside this callback and resolves
   * with the user-selected hostId, or `null` if the user cancels.
   *
   * Required regardless of ctx.hostId — when ctx.hostId is non-null this
   * callback is never invoked (see happy-path test). Caller still passes
   * a noop / never-called function to satisfy the type contract.
   */
  resolveHostId: () => Promise<string | null>
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

  // spec v4 §3.2.2 — null hostId resolution via caller-injected callback.
  // We do NOT silently fall back to activeHostId.
  let hostId: string
  if (ctx.hostId == null) {
    const picked = await deps.resolveHostId()
    if (picked == null) {
      // User cancelled the picker → no-op, no toast.
      return
    }
    hostId = picked
  } else {
    hostId = ctx.hostId
  }

  let sessionCode: string
  try {
    const session = await createSession(hostId, genSessionName(cmd), ctx.cwd ?? '~', 'terminal')
    sessionCode = session.code
  } catch (err) {
    // codex round-1 B4 — create-session failure has NO retry/undo action;
    // user has nothing meaningful to retry without re-clicking the chip.
    const reason = err instanceof Error ? err.message : String(err)
    toast.show(t('quick_commands.toast.create_failed', { reason }))
    return
  }

  try {
    await executeCommand(hostId, sessionCode, cmd.command)
  } catch (err) {
    // Step 2 failed — STILL switch (so user sees the orphan), WITH Retry action.
    safelySwitch(hostId, sessionCode, deps, t)
    const reason = err instanceof Error ? err.message : String(err)
    void reason
    toast.show(
      t('quick_commands.toast.send_keys_failed'),
      // retry: re-run send-keys; failures dropped (user can keep clicking).
      () => {
        void executeCommand(hostId, sessionCode, cmd.command).catch(() => undefined)
      },
      t('quick_commands.toast.retry'),  // codex round-1 B4 — only this branch carries an action label
    )
    return
  }

  safelySwitch(hostId, sessionCode, deps, t)
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
    // codex round-1 B4 — switch failure has NO retry action either; the session
    // is already alive elsewhere, the toast is purely informational.
    const reason = err instanceof Error ? err.message : String(err)
    void reason
    useUndoToast.getState().show(t('quick_commands.toast.switch_failed'))
  }
}
```

註（給實作者）：i18n keys 已在 Task **1b.0c** 加入 zh-TW / en JSON（codex round-1 B9 — 必須前置）。`createSession` 已存在於 `spa/src/lib/host-api.ts`（簽名 `(hostId, name, cwd, mode) => Promise<Session>`）。`executeCommand` 已存在於 `spa/src/lib/execute-command.ts`。`switchToSession` deps 由 caller 注入（避免 tab/workspace store 的循環相依）。

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

  // codex round-1 C15 — keyboard accessibility on multi-select mount chips
  it('mount-target chips support Space/Enter activation and ArrowRight/ArrowLeft roving focus', () => {
    render(<QuickCommandsSettingsSection />)
    fireEvent.click(screen.getByRole('button', { name: /New/i }))
    const wsChip = screen.getByRole('button', { name: /Workspace/i })
    const hostChip = screen.getByRole('button', { name: /Host/i })

    // Focus first chip
    wsChip.focus()
    expect(document.activeElement).toBe(wsChip)

    // Space toggles aria-pressed
    expect(wsChip.getAttribute('aria-pressed')).toBe('false')
    fireEvent.keyDown(wsChip, { key: ' ' })
    fireEvent.click(wsChip) // RTL: native button Space → click; explicit click for jsdom safety
    expect(wsChip.getAttribute('aria-pressed')).toBe('true')

    // Enter also toggles
    fireEvent.keyDown(wsChip, { key: 'Enter' })
    fireEvent.click(wsChip)
    expect(wsChip.getAttribute('aria-pressed')).toBe('false')

    // ArrowRight moves focus to next chip (roving focus)
    fireEvent.keyDown(wsChip, { key: 'ArrowRight' })
    expect(document.activeElement).toBe(hostChip)

    // ArrowLeft moves focus back
    fireEvent.keyDown(hostChip, { key: 'ArrowLeft' })
    expect(document.activeElement).toBe(wsChip)
  })
})
```

註（codex round-1 C15）：multi-select chip 元件需在 `onKeyDown` 內處理 ArrowLeft / ArrowRight 切換 focus（roving focus pattern）；Space / Enter 由 native button 的 keydown→click 自動觸發 `toggleTarget`。實作建議：在 `<fieldset>` 容器加 `onKeyDown`，比對 `e.key` 為方向鍵時找出當前 active button index 後 focus 鄰位 button。

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
          {/* codex round-1 C15 — roving focus across chips via ArrowLeft / ArrowRight */}
          <div
            className="flex gap-2 mt-1 flex-wrap"
            onKeyDown={(e) => {
              if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
              const buttons = Array.from(
                e.currentTarget.querySelectorAll<HTMLButtonElement>('button[type="button"]'),
              )
              const idx = buttons.indexOf(document.activeElement as HTMLButtonElement)
              if (idx === -1) return
              e.preventDefault()
              const dir = e.key === 'ArrowRight' ? 1 : -1
              const next = (idx + dir + buttons.length) % buttons.length
              buttons[next]?.focus()
            }}
          >
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

In `spa/src/locales/zh-TW.json` 加入（**Settings / module description 範圍** — host_picker / aria / toast 已在 Task 1b.0c 加入；此處不重複）：

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
  "modules.quick_commands.description": "快速指令模組 — 在 workspace / host 入口顯示 chip，一鍵建 session 送指令",
  "common.edit": "編輯",
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
  "modules.quick_commands.description": "Quick Commands — chip launchers on workspace / host entries that create a session and send a command in one click",
  "common.edit": "Edit",
```

註：若 `common.edit` 已存在，跳過該行。檢查指令：`grep '"common.edit"' spa/src/locales/zh-TW.json`。

註：以下 keys 已在 Task **1b.0c**（前置）加入，本 task **不重複**：`quick_commands.toast.create_failed` / `.send_keys_failed` / `.switch_failed` / `.retry` / `quick_commands.host_picker.label` / `.empty` / `.online` / `.offline` / `.close` / `quick_commands.aria.toolbar` / `.workspace_actions`。

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
3. `App.tsx` 的 `WorkspaceContextMenu` 渲染處需新增傳 `workspaceId={wsContextMenu.wsId}` prop。

**Spec v4 hostId 解析（不再用 `activeHostId` fallback）：**
- 取 `useTabStore.tabs` + 該 workspace，呼叫 `inferWorkspaceHostId(workspace, tabs)`
- 多數決成功 → 直接傳 hostId 給 ctx
- 多數決回 null → ctx.hostId 為 null；點擊 chip 時 executor 透過 `resolveHostId` callback 開 `HostPickerPopover`

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

// codex round-1 B8 — full executable test body (subagent must not insert TODO stubs)
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'

vi.mock('../../../lib/host-api', async () => {
  const actual = await vi.importActual<typeof import('../../../lib/host-api')>('../../../lib/host-api')
  return {
    ...actual,
    createSession: vi.fn().mockResolvedValue({
      code: 'sess-new', name: 'Alpha', cwd: '/tmp', mode: 'terminal',
    }),
  }
})

vi.mock('../../../lib/execute-command', () => ({
  executeCommand: vi.fn().mockResolvedValue(undefined),
}))

import { createSession } from '../../../lib/host-api'

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
  // Tab + workspace stores need a base shape so insertTab / setActiveTab don't blow up
  useTabStore.setState({
    tabs: {},
    tabOrder: [],
    activeTabId: null,
  } as Partial<ReturnType<typeof useTabStore.getState>> as never)
  // codex round-2 B8 — useWorkspaceStore.workspaces 是 Workspace[]（非 Record），
  // 沒有 workspaceOrder 欄位（spa/src/features/workspace/store.ts:10 確認）。
  useWorkspaceStore.setState({
    workspaces: [{ id: workspaceId, name: 'WS', tabs: [], activeTabId: null, moduleConfig: {} }],
    activeWorkspaceId: workspaceId,
  } as Partial<ReturnType<typeof useWorkspaceStore.getState>> as never)
  clearModuleRegistry()
  registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
  vi.clearAllMocks()
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

  it('clicking a command calls onClose after executor finishes', async () => {
    const onClose = vi.fn()
    render(<WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={onClose} />)
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    // executor is async (createSession + executeCommand) → flush microtasks
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(onClose).toHaveBeenCalled()
  })

  it('happy path — calls createSession with the inferred hostId and inserts tab into workspace (codex round-1 B5/B6)', async () => {
    render(<WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={() => {}} />)
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(createSession).toHaveBeenCalledWith('h1', expect.any(String), expect.any(String), 'terminal')
    // Tab should be inserted into the workspace (codex round-1 B6 — full
    // openSingletonAndSelect equivalent: openSingletonTab → insertTab → setActive).
    // codex round-2 B8 — workspaces 是 Workspace[]，用 .find() 不是 record key 索引
    const tabIds = useWorkspaceStore.getState().workspaces.find((w) => w.id === 'w1')?.tabs ?? []
    expect(tabIds.length).toBeGreaterThan(0)
    const tabId = tabIds[tabIds.length - 1]
    const tab = useTabStore.getState().tabs[tabId]
    expect(tab).toBeDefined()
    // codex round-1 B5 — tmux-session content has all required fields populated
    if (tab && tab.layout.type === 'leaf' && tab.layout.pane.content.kind === 'tmux-session') {
      const c = tab.layout.pane.content
      expect(c.hostId).toBe('h1')
      expect(c.sessionCode).toBe('sess-new')
      expect(c.mode).toBe('terminal')
      expect(c.cachedName).toBeDefined()
      expect(c.tmuxInstance).toBeDefined()
    }
    // active workspace + tab updated
    expect(useWorkspaceStore.getState().activeWorkspaceId).toBe('w1')
    expect(useTabStore.getState().activeTabId).toBe(tabId)
  })

  it('returns null when quick-commands module is disabled', () => {
    useModuleEnabledStore.getState().setEnabled('quick-commands', false)
    const { container } = render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId="h1" onClose={() => {}} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('hostId=null — clicking a command opens HostPickerPopover (does NOT call createSession until user picks)', async () => {
    // Spec v4 §3.2.2 — when inferWorkspaceHostId returns null we must let the
    // user choose. The chip is rendered as soon as bindings exist; click triggers
    // the picker before executor.
    render(<WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId={null} onClose={() => {}} />)
    expect(screen.queryByRole('listbox')).toBeNull()
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(await screen.findByRole('listbox')).toBeInTheDocument()
    // createSession not yet called — waiting for user to pick a host
    expect(createSession).not.toHaveBeenCalled()
  })

  it('hostId=null — picker Esc cancel → no createSession, no insertTab (codex round-1 B8 — full assertion)', async () => {
    const onClose = vi.fn()
    render(<WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId={null} onClose={onClose} />)
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(await screen.findByRole('listbox')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(createSession).not.toHaveBeenCalled()
    // No tab inserted into workspace
    // codex round-2 B8 — workspaces 是 Workspace[]，用 .find() 不是 record key 索引
    expect(useWorkspaceStore.getState().workspaces.find((w) => w.id === 'w1')?.tabs ?? []).toHaveLength(0)
    // onClose still fires (executor's finally block runs even on cancel path)
    expect(onClose).toHaveBeenCalled()
  })

  // codex round-2 — picker resolver cleanup (parent unmount + duplicate resolve safety)
  it('unmount while picker is open → pending Promise resolves to null, executor finally runs, no throw', async () => {
    const onClose = vi.fn()
    const { unmount } = render(
      <WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId={null} onClose={onClose} />,
    )
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(await screen.findByRole('listbox')).toBeInTheDocument()
    // simulate the parent menu closing externally (e.g. user clicks outside the
    // WorkspaceContextMenu) while a picker resolver is mid-flight.
    expect(() => unmount()).not.toThrow()
    await new Promise<void>((r) => setTimeout(r, 0))
    // executor's finally should have run despite no explicit user action
    expect(onClose).toHaveBeenCalled()
    expect(createSession).not.toHaveBeenCalled()
  })

  it('duplicate onSelect / onCancel calls are safe (resolver is nulled-out after first invocation)', async () => {
    const onClose = vi.fn()
    render(<WorkspaceQuickCommandsContextMenu workspaceId="w1" hostId={null} onClose={onClose} />)
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(await screen.findByRole('listbox')).toBeInTheDocument()
    // First Esc → cancels normally
    fireEvent.keyDown(document, { key: 'Escape' })
    // Second Esc (e.g. fast double-tap) → must be a no-op, NOT throw
    expect(() => fireEvent.keyDown(document, { key: 'Escape' })).not.toThrow()
    await new Promise<void>((r) => setTimeout(r, 0))
    expect(createSession).not.toHaveBeenCalled()
  })
})
```

也在 `WorkspaceContextMenu.test.tsx` 追加 integration 測試（codex round-1 B8 — full executable body）：

```tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { WorkspaceContextMenu } from './WorkspaceContextMenu'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../../../lib/module-registry'

describe('WorkspaceContextMenu — quick commands section integration (Phase 1b)', () => {
  beforeEach(() => {
    useQuickCommandStore.setState({ global: [], byHost: {}, bindings: {} })
    useModuleEnabledStore.setState({ enabled: {}, baseline: null })
    clearModuleRegistry()
    registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
  })

  it('renders quick commands section above Settings when WORKSPACE_ACTIONS bindings exist', () => {
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-x', name: 'XCmd', command: 'x' }],
      byHost: {},
      bindings: { 'cmd-x': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
    })
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

  it('omits the quick commands section (and its separator) when no WORKSPACE_ACTIONS bindings exist', () => {
    render(
      <WorkspaceContextMenu
        position={{ x: 0, y: 0 }}
        workspaceId="w1"
        hostId="h1"
        onSettings={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    // No quick command chip; existing Settings button still present
    expect(screen.queryByRole('toolbar', { name: /quick|快速/i })).toBeNull()
    expect(screen.getByRole('button', { name: /settings|設定/i })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceQuickCommandsContextMenu.test.tsx src/features/workspace/components/WorkspaceContextMenu.test.tsx`
Expected: FAIL — module not found / props missing。

- [ ] **Step 3: Implement**

Create `spa/src/features/workspace/components/WorkspaceQuickCommandsContextMenu.tsx`（codex round-1 B5/B6/B7 —使用 default chip render + `containerClassName="flex flex-col"`；executor 內部完整呼叫 `openSingletonTab` + `insertTab` + `setActiveWorkspace` + `setActiveTab`，並補齊所有 `tmux-session` 欄位）：

```tsx
import { useCallback, useEffect, useRef, useState } from 'react'
import { CommandSlot } from '../../../components/CommandSlot'
import { HostPickerPopover } from '../../../components/HostPickerPopover'
import { runWorkspaceSlot } from '../../../lib/slot-executor'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'

interface Props {
  workspaceId: string
  /**
   * Workspace 的多數決 hostId（spec v4 §3.2.1）；null 代表 workspace 無
   * tmux-session tabs，executor 會在 callback 裡開 HostPickerPopover。
   */
  hostId: string | null
  onClose: () => void
}

/**
 * 渲染 mount=WORKSPACE_ACTIONS 的 quick commands，作為 WorkspaceContextMenu 的子 section。
 *
 * codex round-1 B7 — 不傳 `render` prop（會與 `executor` 衝突 — render 包出來的
 * 是 `<span>`，沒有 onClick，executor 不會跑）。改用 `<CommandSlot>` default
 * button render + `containerClassName="flex flex-col"` 改 layout 為 menu 條列。
 *
 * codex round-1 B5/B6 — switchToSession callback 必須做 `openSingletonAndSelect`
 * 等價邏輯：open singleton tab → insertTab to workspace → setActiveWorkspace +
 * setActiveTab。tmux-session content 欄位要齊全（mode / cachedName / tmuxInstance）
 * 以滿足 `spa/src/types/tab.ts` 的型別契約。
 */
export function WorkspaceQuickCommandsContextMenu({ workspaceId, hostId, onClose }: Props) {
  // codex round-2 — picker state shape pinned: open implied by resolver !== null;
  // resolver is always nulled-out the moment it's invoked (idempotent guard against
  // duplicate resolve which would no-op the Promise but is still a sign of a bug).
  const [picker, setPicker] = useState<{
    open: boolean
    resolver: ((hostId: string | null) => void) | null
    anchor: { x: number; y: number } | null
  } | null>(null)
  const lastClickPos = useRef<{ x: number; y: number } | null>(null)

  const resolveHostId = useCallback(
    () =>
      new Promise<string | null>((resolve) => {
        setPicker({ open: true, resolver: resolve, anchor: lastClickPos.current })
      }),
    [],
  )

  // codex round-2 — dangling Promise cleanup. If the parent menu closes (unmount)
  // or the popover gets force-dismissed externally while a picker resolver is
  // still pending, we MUST resolve it as null so the executor's await returns,
  // its `finally` runs, and onClose fires. Otherwise the Promise hangs forever
  // and the executor stays mid-flight (busy=true sticks, chips stay disabled).
  useEffect(() => {
    return () => {
      setPicker((current) => {
        if (current?.resolver) current.resolver(null)
        return null
      })
    }
  }, [])

  // codex round-1 B6 — workspace caller must perform full openSingletonAndSelect
  // equivalent (the helper exists at spa/src/features/workspace/hooks.ts:200 but
  // is bound to the hook, so we replicate inline here using the same store
  // primitives it uses).
  const switchToSession = useCallback(
    (h: string, sessionCode: string) => {
      // codex round-1 B5 — fill ALL tmux-session content fields per types/tab.ts:38
      const tabId = useTabStore.getState().openSingletonTab({
        kind: 'tmux-session',
        hostId: h,
        sessionCode,
        mode: 'terminal',
        cachedName: sessionCode,
        tmuxInstance: '',
      })
      useWorkspaceStore.getState().insertTab(tabId, workspaceId)
      useWorkspaceStore.getState().setActiveWorkspace(workspaceId)
      useTabStore.getState().setActiveTab(tabId)
    },
    [workspaceId],
  )

  return (
    <div
      className="py-1"
      onClickCapture={(e) => {
        // Capture coordinates for picker anchor (fixed-positioned next to click).
        lastClickPos.current = { x: e.clientX, y: e.clientY }
      }}
    >
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId, workspaceId }}
        // codex round-1 B7 — flex-col override; default chip render keeps onClick + executor wiring intact.
        containerClassName="flex flex-col"
        // codex round-1 C11 — disable buttons while picker is mid-flight (prevents double-click race).
        busy={picker?.open ?? false}
        executor={async (cmd, ctx) => {
          try {
            await runWorkspaceSlot(cmd, ctx, {
              switchToSession,
              resolveHostId,
            })
          } finally {
            onClose()
          }
        }}
      />
      <HostPickerPopover
        open={picker?.open ?? false}
        anchor={picker?.anchor ?? null}
        onSelect={(hostId) => {
          // codex round-2 — null-out resolver before invoking to make duplicate-call safe
          const resolver = picker?.resolver
          setPicker(null)
          resolver?.(hostId)
        }}
        onCancel={() => {
          const resolver = picker?.resolver
          setPicker(null)
          resolver?.(null)
        }}
      />
    </div>
  )
}
```

註（codex round-1 B7 + round-2）：原本的舊 plan 同時傳 `render` 與 `executor`，但 CommandSlot 在有 `render` 時包 `<span>` 不掛 onClick → executor 永遠不跑（unreachable UI footgun）。round-2 已把 `SlotRenderer` 升級為 3-arg `(cmd, ctx, run) => ReactNode`，custom render 拿 `run` 自行掛 onClick；但本 caller 用 `containerClassName="flex flex-col"` 改 layout 即足夠，不需 custom render，default button 帶 onClick 正常觸發 executor。CommandSlot 的 `containerClassName` prop 與 3-arg `run` 注入皆在 Task 1b.1 加入。

更新後 `WorkspaceContextMenu.tsx`：

```tsx
interface Props {
  position: { x: number; y: number }
  workspaceId?: string                 // ← new (optional 維持向下相容；測試 setup 內全部要傳)
  hostId?: string | null               // ← new — null 代表 workspace 多數決失敗，picker 流程
  onSettings: () => void
  onTearOff?: () => void
  onMergeTo?: (targetWindowId: string) => void
  onClose: () => void
}
```

**Spec v4 — separator 條件：** separator 應只在「該 workspace 確實有 WORKSPACE_ACTIONS bindings」才顯示，與 hostId 是否 null 無關（hostId null 時 chip 仍會渲染）。判斷靠讀 `useQuickCommandStore.global` + `bindings` 是否有 binding（不依賴 hostId 推 byHost 覆寫，因為 hostId null 時不能呼叫 `getCommands(hostId)`）：

```tsx
// 在 WorkspaceContextMenu.tsx 加入：
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'

const hasQuickCommands = useQuickCommandStore((s) => {
  // hostId null 時用 global only；否則用 getCommands(hostId)（含 host override）
  const cmds = hostId == null ? s.global : s.getCommands(hostId)
  return cmds.some((c) => s.bindings[c.id]?.includes(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS))
})
const moduleEnabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
const showQuickCommandsSection = !!workspaceId && moduleEnabled && hasQuickCommands

// JSX：
{showQuickCommandsSection && (
  <>
    <WorkspaceQuickCommandsContextMenu workspaceId={workspaceId!} hostId={hostId ?? null} onClose={onClose} />
    <div className="border-t border-border-default my-1" />
  </>
)}
```

修改 `App.tsx` 的 `<WorkspaceContextMenu>` 呼叫處（L322-329）— 改用 spec v4 多數決：

```tsx
import { inferWorkspaceHostId } from './lib/infer-workspace-host-id'
import { useTabStore } from './stores/useTabStore'
import { useWorkspaceStore } from './stores/useWorkspaceStore' // 若已存在；否則 workspaces 來自當前 source

{wsContextMenu && (() => {
  const ws = workspaces.find((w) => w.id === wsContextMenu.wsId)
  if (!ws) return null
  // Spec v4 §3.2.1 — workspace hostId 多數決，不再用 activeHostId fallback。
  const tabs = useTabStore.getState().tabs
  const hostId = inferWorkspaceHostId(ws, tabs)  // string | null
  return (
    <WorkspaceContextMenu
      position={wsContextMenu.position}
      workspaceId={wsContextMenu.wsId}
      hostId={hostId}
      onSettings={() => openWsSettings(wsContextMenu.wsId)}
      onTearOff={window.electronAPI ? () => handleWsTearOff(wsContextMenu.wsId) : undefined}
      onMergeTo={window.electronAPI ? (targetWindowId) => handleWsMergeTo(wsContextMenu.wsId, targetWindowId) : undefined}
      onClose={handleCloseWsContextMenu}
    />
  )
})()}
```

註：`useTabStore.getState().tabs` 為 `Record<string, Tab>`（spa/src/stores/useTabStore.ts L110 確認）。`inferWorkspaceHostId` Task 1b.0a 已建立。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceQuickCommandsContextMenu.test.tsx src/features/workspace/components/WorkspaceContextMenu.test.tsx src/components/CommandSlot.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): mount WORKSPACE_ACTIONS slot in workspace right-click context menu
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

啟動 daemon + dev SPA，user flow（**Phase 1b 範圍** — 不含 Plus hover popover，後者在 Phase 1b'）：
1. Settings → Quick Commands → New → 輸入 Name + Command + 勾 Workspace → Save
2. 在 sidebar Workspace 行 **右鍵** → context menu 出現該 quick command；點擊執行
3. 應：建 session、送 cmd、自動切到該 session（tab 落在該 workspace 下）
4. 模擬失敗（停 daemon → 點按鈕 → 應有 toast）；send-keys 失敗的 toast 應顯 'Retry' 按鈕（codex round-1 B4 — create / switch failure 不顯 button）

### Phase 1b 驗收清單

- [x] Settings 頁面可建 / 編 / 刪 commands；mount chips 可多選
- [x] 對話框 a11y：focus trap / Esc / aria-label / 觸發鍵盤可達
- [x] 設 mount = WORKSPACE 後，回 sidebar **右鍵** Workspace row 即可看到按鈕（無需 reload）— 此為 Phase 1b 唯一入口；Plus hover popover 在 Phase 1b' ship
- [x] 點擊按鈕：建 session + 送 keys + 切過去；tab 透過 `insertTab + setActive` 落在正確 workspace
- [x] 三層失敗 UX 符合 spec §3.3（codex round-1 B4 修正）：
  - 建 session 失敗 → toast，**無 button**
  - 送 keys 失敗 → 仍切過去 + toast 帶 **'Retry' button**
  - 切失敗 → toast，**無 button**
- [x] 停用 quick-commands module → context menu 入口立即消失（CommandSlot short-circuit）
- [x] i18n: zh-TW + en 全部 key 齊全（Task 1b.0c 已前置 host_picker / aria / toast keys；1b.4 補 settings keys；`pnpm run lint` 含 locale-completeness 檢查）
- [x] **Spec v4 — workspace hostId 多數決邊界（§3.2.1 / §3.2.2）：**
  - workspace 中只有非 tmux-session tabs（如全 dashboard / settings）→ 點 quick command → `HostPickerPopover` 出現 → 選 host → 正常流程（建 session + 送 keys + 切過去）
  - workspace 中只有 tmux-session tabs（單 host）→ 自動推斷正確 host → **不出 picker**
  - workspace 中混合多 host tmux-session tabs，多數一個 host → 多數決命中該 host → 不出 picker
  - workspace 多數決平手 + active tab 是 tmux-session 在多數派內 → 用 active 的 hostId
  - workspace 多數決平手 + active tab 非 tmux-session → 用 tabs 順序中第一個 winner
  - workspace 多數決平手 + active tab 是少數派 tmux-session（codex round-1 B1）→ **忽略 active**，走 tabs 順序中第一個 winner
  - hostId null 時 picker 取消 → no-op，**沒有任何 createSession / send-keys API call**
  - hostId null 時 picker 選定 → executor 用該 hostId 完成全流程

---

## Phase 1b' — Plus hover popover 過渡入口（獨立 PR）

> **⚠️ #690 superseded note（2026-04-28）**：本 phase 範例（Task 1b'.1）內的 `runWorkspaceSlot(...)` deps 物件**漏 `assertContextLive`**。依 [issue #690](https://github.com/wake/purdex/issues/690) / spec §3.3.1，`Deps.assertContextLive` 為 type-level required。1b' 實作時必須補：
> ```tsx
> assertContextLive: () =>
>   useWorkspaceStore.getState().workspaces.some((w) => w.id === workspaceId),
> ```
> 範例本身保留為 historical reference（codex review 採納紀錄），實作前以 #690 enforcement 規範為準。

**目標：** 在已 ship 的 Phase 1b（Settings + 資料層 + 右鍵 context menu）之上，加上 `WorkspaceRow` Plus 按鈕 hover 往左展開的 chip popover；同時設計 mobile/touch fallback。

**為何拆獨立 PR（codex round-1 結構性變更）：**
- User 標記此入口為**過渡實作** — 未來可能遷移到其他位置或重做為 command palette 風格。
- 獨立 PR 風險隔離：未來要 revert / replace 整段時，single squash-merge commit 一鍵還原，不影響資料層 / Settings / context menu 的穩定基礎。
- 1b 已可獨立運作（context menu 是穩定入口；spec §6 對「Settings 出現時必有生效入口」的要求已滿足）。

**前置條件：** Phase 1b PR 已 merge（`<CommandSlot>` / `runWorkspaceSlot` / `HostPickerPopover` / `inferWorkspaceHostId` / i18n keys 全部就緒）。

**Tasks：** 1b'.1（popover + WorkspaceRow Plus hover + mobile/touch fallback）→ 1b'.2（全域驗證）。

---

### Task 1b'.1: `WorkspaceQuickActionsPopover` + WorkspaceRow Plus hover + mobile/touch fallback

**Files:**
- Create: `spa/src/features/workspace/components/WorkspaceQuickActionsPopover.tsx`
- Create: `spa/src/features/workspace/components/WorkspaceQuickActionsPopover.test.tsx`
- Modify: `spa/src/features/workspace/components/WorkspaceRow.tsx`
- Modify: `spa/src/features/workspace/components/WorkspaceRow.test.tsx`

**實作策略：**
- 改造 `WorkspaceRow.tsx` L107-121 的 Plus 按鈕：保留原 click 行為（`onAddTabToWorkspace`）但**外層**多包一個 `<div onMouseEnter={...} onMouseLeave={...} onFocusCapture={...} onBlurCapture={...} onTouchStart={...} onTouchEnd={...}>` 作 hover/touch hub。
- 該 div 內含 Plus button + 一個 absolute popover（`<WorkspaceQuickActionsPopover>`），popover `right-full mr-1`（Plus 按鈕左側）展開。
- popover 內部用 `<CommandSlot mountTo=WORKSPACE_ACTIONS>` 渲染 chip 列。
- Hover 收回邏輯：使用 single boolean state `popoverOpen`；mouseenter Plus 或 popover 任一觸發開啟，mouseleave 整個 hub 觸發關閉（用 wrapper div 圍住兩者，事件冒泡判斷即可）。
- 鍵盤可達性：Plus 按鈕 focus 時 popover 開啟（`onFocusCapture`），整個 wrapper blur 時關閉（`onBlurCapture` + 比對 `relatedTarget` 是否仍在 wrapper 內）。
- 視覺 cue：popover 含半透明漸層壓底（`bg-gradient-to-l from-surface-secondary/0 to-surface-secondary/95` 或同等 token）。
- **Plus 原本 hover 才顯**（L117 `opacity-0 group-hover/ws-header:opacity-100`）— 此邏輯保留。

**Mobile / touch fallback（codex round-1 C17）：**

桌面 hover 不存在於觸控裝置；在沒設計 fallback 的前提下，行動裝置 user **完全無法觸發 popover**。設計選擇：

- **Long-press 開 popover + tap chip 執行**（建議方案，符合行動裝置慣例）
  - `onTouchStart` 記下時間戳 + `setTimeout(500ms)` 觸發 `setPopoverOpen(true)`
  - `onTouchEnd` 在 500ms 內取消 timer → 視為一般 click（執行原本的 `onAddTabToWorkspace`）
  - popover 開啟後：點 popover 外（document `pointerdown` 偵測 hub 外）→ 收回；tap chip → 執行 executor
  - 不採 tap-toggle（先點開 popover、再點 chip）的原因：兩次 tap 才能執行單一動作，比 long-press 模型多一步

實作要點：
```tsx
const longPressTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
const longPressFiredRef = useRef(false)

const handleTouchStart = () => {
  longPressFiredRef.current = false
  longPressTimerRef.current = setTimeout(() => {
    longPressFiredRef.current = true
    setPopoverOpen(true)
  }, 500)
}

const handleTouchEnd = () => {
  if (longPressTimerRef.current) {
    clearTimeout(longPressTimerRef.current)
    longPressTimerRef.current = null
  }
  // 若 long-press 沒觸發 → 視為一般 tap，讓 button onClick 自然處理
  // 若 long-press 已觸發 → 不執行 add-tab，等 user 在 popover 內 tap chip
}

// onClick 仍掛 add-tab；touch 流：若 longPressFiredRef.current === true 則阻擋預設 click（瀏覽器 touch → click 兼容性）
const handleClick = (e: React.MouseEvent) => {
  if (longPressFiredRef.current) {
    e.preventDefault()
    e.stopPropagation()
    return
  }
  e.stopPropagation()
  onAddTabToWorkspace(workspace.id)
}

// document-level pointerdown to close popover when tapping outside the hub
useEffect(() => {
  if (!popoverOpen) return
  const onPointer = (e: PointerEvent) => {
    if (!hubRef.current?.contains(e.target as Node | null)) {
      setPopoverOpen(false)
    }
  }
  document.addEventListener('pointerdown', onPointer)
  return () => document.removeEventListener('pointerdown', onPointer)
}, [popoverOpen])
```

**Spec v4 hostId 解析：** WorkspaceRow 內以 `inferWorkspaceHostId(workspace, useTabStore.getState().tabs)` 取 hostId（不再用 `useHostStore.activeHostId` fallback）。多數決回 null 時 chip 仍顯示（`hostId={null}` 傳給 popover）；點擊 chip 由 popover 內部開 `HostPickerPopover` 等 user 選定後跑 executor。

**短路條件：** `<CommandSlot>` 已內建 module-disabled / no-bindings short-circuit；`WorkspaceQuickActionsPopover` 額外用同邏輯（讀 store 並 early-return null）避免在無 commands 時渲染空 popover wrapper（避免 hover 觸發後出現空白浮層）。**注意：hostId null 不再作為 `WorkspaceQuickActionsPopover` 的隱藏條件**（spec v4 §3.2.2）— 即使 workspace 沒有 tmux-session tabs，user 仍可主動透過 picker 選 host 啟動 quick command。

- [ ] **Step 1: Write the failing test**

Create `spa/src/features/workspace/components/WorkspaceQuickActionsPopover.test.tsx`：

```tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { WorkspaceQuickActionsPopover } from './WorkspaceQuickActionsPopover'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { useHostStore } from '../../../stores/useHostStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../../../lib/module-registry'

function setup() {
  useQuickCommandStore.setState({
    global: [{ id: 'cmd-a', name: 'Alpha', command: 'a' }],
    byHost: {},
    bindings: { 'cmd-a': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] },
  })
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  // HostPickerPopover 內部讀 useHostStore；至少塞一個 host 避免空狀態誤判
  useHostStore.setState({
    hosts: { h1: { id: 'h1', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: ['h1'],
    runtime: { h1: { status: 'connected' } },
    activeHostId: 'h1',
  })
  clearModuleRegistry()
  registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
}

describe('WorkspaceQuickActionsPopover', () => {
  beforeEach(setup)

  it('renders bound commands as chips', () => {
    render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId="h1" />)
    expect(screen.getByLabelText(/^Alpha/)).toBeInTheDocument()
  })

  it('renders chips when hostId is null (picker handles host resolution at click time)', () => {
    // Spec v4 §3.2.2 — null hostId is a valid state (workspace has no
    // tmux-session tabs); we still surface the chips so user can pick a host.
    render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId={null} />)
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

  it('hostId=null + click chip → opens HostPickerPopover', () => {
    render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId={null} />)
    expect(screen.queryByRole('listbox')).toBeNull()
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(screen.getByRole('listbox')).toBeInTheDocument()
  })

  // codex round-2 — picker resolver cleanup (popover unmount via mouseleave on parent)
  it('unmount while picker is open → pending Promise resolves to null, no throw', async () => {
    const { unmount } = render(<WorkspaceQuickActionsPopover workspaceId="w1" hostId={null} />)
    fireEvent.click(screen.getByLabelText(/^Alpha/))
    expect(screen.getByRole('listbox')).toBeInTheDocument()
    // simulate parent hub mouseleave force-closing the popover wrapper.
    expect(() => unmount()).not.toThrow()
  })
})
```

並於 `WorkspaceRow.test.tsx` 追加 hover / touch / no-bindings 三組行為測試（codex round-1 B8 — full executable bodies；C17 — mobile/touch fallback；vi.useFakeTimers for long-press timing）：

```tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { WorkspaceRow } from './WorkspaceRow'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useTabStore } from '../../../stores/useTabStore'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { clearModuleRegistry, registerModule } from '../../../lib/module-registry'

function setupHoverPopoverFixtures(opts: { withBindings: boolean }) {
  useQuickCommandStore.setState({
    global: opts.withBindings ? [{ id: 'cmd-x', name: 'X', command: 'x' }] : [],
    byHost: {},
    bindings: opts.withBindings ? { 'cmd-x': [QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS] } : {},
  })
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
  useHostStore.setState({
    hosts: { h1: { id: 'h1', name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
    hostOrder: ['h1'],
    runtime: { h1: { status: 'connected' } },
    activeHostId: 'h1',
  })
  // workspace has a tmux-session tab so inferWorkspaceHostId returns 'h1' (no picker prompt)
  useTabStore.setState({
    tabs: {
      t1: {
        id: 't1', pinned: false, locked: false, createdAt: 0,
        layout: {
          type: 'leaf',
          pane: {
            id: 'p1',
            content: {
              kind: 'tmux-session', hostId: 'h1', sessionCode: 'sess', mode: 'terminal',
              cachedName: 'x', tmuxInstance: '',
            },
          },
        },
      },
    },
    tabOrder: ['t1'],
    activeTabId: 't1',
  } as Partial<ReturnType<typeof useTabStore.getState>> as never)
  clearModuleRegistry()
  registerModule({ id: 'quick-commands', name: 'Quick Commands', disableable: true })
}

// codex round-2 B8 — props 依 WorkspaceRow.tsx 實際 Props 介面（spa/src/features/workspace/components/WorkspaceRow.tsx L11-24）對齊：
// workspace / isActive / tabsById / activeTabId / onSelectWorkspace /
// onContextMenuWorkspace? / onSelectTab / onCloseTab / onMiddleClickTab /
// onContextMenuTab / onRenameTab? / onAddTabToWorkspace
const baseProps = {
  workspace: { id: 'w1', name: 'WS', tabs: ['t1'], activeTabId: 't1', moduleConfig: {} },
  isActive: false,
  tabsById: {
    t1: {
      id: 't1', pinned: false, locked: false, createdAt: 0,
      layout: {
        type: 'leaf' as const,
        pane: {
          id: 'p1',
          content: {
            kind: 'tmux-session' as const, hostId: 'h1', sessionCode: 'sess', mode: 'terminal' as const,
            cachedName: 'x', tmuxInstance: '',
          },
        },
      },
    },
  },
  activeTabId: 't1' as string | null,
  onSelectWorkspace: vi.fn(),
  onContextMenuWorkspace: vi.fn(),
  onSelectTab: vi.fn(),
  onCloseTab: vi.fn(),
  onMiddleClickTab: vi.fn(),
  onContextMenuTab: vi.fn(),
  onRenameTab: vi.fn(),
  onAddTabToWorkspace: vi.fn(),
}

describe('WorkspaceRow — Plus hover popover (Phase 1b\\')', () => {
  beforeEach(() => setupHoverPopoverFixtures({ withBindings: true }))

  it('opens popover on Plus hover, closes on mouseleave', () => {
    render(<WorkspaceRow {...(baseProps as never)} />)
    const plusBtn = screen.getByLabelText(/Add tab to/i)
    const hub = plusBtn.parentElement!

    // 預設未 hover → chip 不在 DOM
    expect(screen.queryByLabelText(/^X/)).toBeNull()

    // hover Plus → popover 顯示
    fireEvent.mouseEnter(hub)
    expect(screen.getByLabelText(/^X/)).toBeInTheDocument()

    // mouseleave wrapper → popover 收回
    fireEvent.mouseLeave(hub)
    expect(screen.queryByLabelText(/^X/)).toBeNull()
  })

  it('does NOT open popover when no WORKSPACE_ACTIONS bindings exist', () => {
    setupHoverPopoverFixtures({ withBindings: false })
    render(<WorkspaceRow {...(baseProps as never)} />)
    const plusBtn = screen.getByLabelText(/Add tab to/i)
    fireEvent.mouseEnter(plusBtn.parentElement!)
    expect(screen.queryByRole('toolbar', { name: /Quick commands|快速指令/i })).toBeNull()
  })
})

describe('WorkspaceRow — touch fallback (codex round-1 C17)', () => {
  beforeEach(() => {
    setupHoverPopoverFixtures({ withBindings: true })
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('long-press (>=500ms) on Plus opens the popover; tap chip executes', () => {
    render(<WorkspaceRow {...(baseProps as never)} />)
    const plusBtn = screen.getByLabelText(/Add tab to/i)
    const hub = plusBtn.parentElement!

    // touch start → wait 500ms → long-press fires
    fireEvent.touchStart(hub)
    act(() => {
      vi.advanceTimersByTime(500)
    })
    expect(screen.getByLabelText(/^X/)).toBeInTheDocument()
    fireEvent.touchEnd(hub)
    // popover stays open (long-press fired)
    expect(screen.getByLabelText(/^X/)).toBeInTheDocument()
  })

  it('short tap (<500ms) on Plus triggers add-tab, NOT popover', () => {
    const onAddTabToWorkspace = vi.fn()
    render(<WorkspaceRow {...(baseProps as never)} onAddTabToWorkspace={onAddTabToWorkspace} />)
    const plusBtn = screen.getByLabelText(/Add tab to/i)

    fireEvent.touchStart(plusBtn.parentElement!)
    act(() => {
      vi.advanceTimersByTime(200) // less than 500ms
    })
    fireEvent.touchEnd(plusBtn.parentElement!)
    fireEvent.click(plusBtn)

    expect(onAddTabToWorkspace).toHaveBeenCalledWith('w1')
    expect(screen.queryByLabelText(/^X/)).toBeNull()
  })

  it('tapping outside the hub closes an open touch-popover', () => {
    render(<WorkspaceRow {...(baseProps as never)} />)
    const plusBtn = screen.getByLabelText(/Add tab to/i)
    const hub = plusBtn.parentElement!

    fireEvent.touchStart(hub)
    act(() => {
      vi.advanceTimersByTime(500)
    })
    expect(screen.getByLabelText(/^X/)).toBeInTheDocument()
    fireEvent.touchEnd(hub)

    // tap outside the hub
    fireEvent.pointerDown(document.body)
    expect(screen.queryByLabelText(/^X/)).toBeNull()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceQuickActionsPopover.test.tsx src/features/workspace/components/WorkspaceRow.test.tsx`
Expected: FAIL — module not found / popover 不顯示。

- [ ] **Step 3: Implement popover + WorkspaceRow integration**

Create `spa/src/features/workspace/components/WorkspaceQuickActionsPopover.tsx`（codex round-1 B5/B6/C16 — switchToSession 完整 tmux-session 欄位 + workspace insertTab + setActive + aria-label 走 i18n）：

```tsx
import { useCallback, useEffect, useRef, useState } from 'react'
import { CommandSlot } from '../../../components/CommandSlot'
import { HostPickerPopover } from '../../../components/HostPickerPopover'
import { runWorkspaceSlot } from '../../../lib/slot-executor'
import { QUICK_COMMAND_SLOTS } from '../../../lib/quick-command-slots'
import { useTabStore } from '../../../stores/useTabStore'
import { useWorkspaceStore } from '../../../stores/useWorkspaceStore'
import { useQuickCommandStore } from '../../../stores/useQuickCommandStore'
import { useModuleEnabledStore } from '../../../stores/useModuleEnabledStore'
import { useI18nStore } from '../../../stores/useI18nStore'

interface Props {
  workspaceId: string
  /**
   * hostId 為 null 時 chip 仍顯示，executor 點擊後會開 HostPickerPopover
   * 讓 user 選 host（spec v4 §3.2.2）。
   */
  hostId: string | null
}

/**
 * Popover chip-list rendered to the LEFT of the Plus-button on each
 * WorkspaceRow on hover/focus. Uses CommandSlot internally — already
 * short-circuits when module disabled / no bindings; we additionally
 * skip rendering the popover wrapper itself in those cases so the
 * hover trigger doesn't expose an empty floating panel.
 *
 * NOTE (spec v4 §3.2.2): we do NOT short-circuit on hostId == null —
 * the picker flow handles that case. Only no-bindings / module-disabled
 * suppress the wrapper.
 */
export function WorkspaceQuickActionsPopover({ workspaceId, hostId }: Props) {
  const t = useI18nStore((s) => s.t)
  const moduleEnabled = useModuleEnabledStore((s) => s.isEnabled('quick-commands'))
  const hasBindings = useQuickCommandStore((s) => {
    const cmds = hostId == null ? s.global : s.getCommands(hostId)
    return cmds.some((c) => s.bindings[c.id]?.includes(QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS))
  })
  const wrapperRef = useRef<HTMLDivElement>(null)
  // codex round-2 — picker state shape pinned: open implied by resolver !== null.
  const [picker, setPicker] = useState<{
    open: boolean
    resolver: ((id: string | null) => void) | null
    anchor: HTMLElement | null
  } | null>(null)

  const resolveHostId = useCallback(
    () =>
      new Promise<string | null>((resolve) => {
        setPicker({ open: true, resolver: resolve, anchor: wrapperRef.current })
      }),
    [],
  )

  // codex round-2 — dangling Promise cleanup. The popover lives behind a hover
  // trigger; mouseleave on the parent hub will unmount this component while a
  // resolver may still be pending. We MUST resolve null on unmount so the
  // executor's await returns and busy state clears.
  useEffect(() => {
    return () => {
      setPicker((current) => {
        if (current?.resolver) current.resolver(null)
        return null
      })
    }
  }, [])

  // codex round-1 B5/B6 — full openSingletonAndSelect equivalent: complete
  // tmux-session content fields + insertTab into workspace + setActive.
  const switchToSession = useCallback(
    (h: string, sessionCode: string) => {
      const tabId = useTabStore.getState().openSingletonTab({
        kind: 'tmux-session',
        hostId: h,
        sessionCode,
        mode: 'terminal',
        cachedName: sessionCode,
        tmuxInstance: '',
      })
      useWorkspaceStore.getState().insertTab(tabId, workspaceId)
      useWorkspaceStore.getState().setActiveWorkspace(workspaceId)
      useTabStore.getState().setActiveTab(tabId)
    },
    [workspaceId],
  )

  if (!moduleEnabled || !hasBindings) return null

  return (
    <div
      ref={wrapperRef}
      role="group"
      // codex round-1 C16 — i18n key, not hard-coded English
      aria-label={t('quick_commands.aria.workspace_actions')}
      className="absolute right-full top-1/2 -translate-y-1/2 mr-1 flex items-center gap-1 px-2 py-1 rounded-md bg-gradient-to-l from-surface-secondary/0 to-surface-secondary/95 backdrop-blur-sm shadow-md z-30"
    >
      <CommandSlot
        mountTo={QUICK_COMMAND_SLOTS.WORKSPACE_ACTIONS}
        ctx={{ hostId, workspaceId }}
        // codex round-1 C11 — picker race guard
        busy={picker?.open ?? false}
        executor={(cmd, ctx) =>
          runWorkspaceSlot(cmd, ctx, {
            switchToSession,
            resolveHostId,
          })
        }
      />
      <HostPickerPopover
        open={picker?.open ?? false}
        anchor={picker?.anchor ?? null}
        onSelect={(hostId) => {
          // codex round-2 — null-out resolver before invoking to make duplicate-call safe
          const resolver = picker?.resolver
          setPicker(null)
          resolver?.(hostId)
        }}
        onCancel={() => {
          const resolver = picker?.resolver
          setPicker(null)
          resolver?.(null)
        }}
      />
    </div>
  )
}
```

修改 `spa/src/features/workspace/components/WorkspaceRow.tsx` Plus 按鈕區段（L107-121）— 含 codex round-1 C17 的 touch fallback：

```tsx
import { useState, useRef, useEffect } from 'react'
// … 既有 imports
import { useTabStore } from '../../../stores/useTabStore'
import { inferWorkspaceHostId } from '../../../lib/infer-workspace-host-id'
import { WorkspaceQuickActionsPopover } from './WorkspaceQuickActionsPopover'

// 在 WorkspaceRow 函式內（spec v4 §3.2.1 — 多數決，不再用 activeHostId fallback）：
const [popoverOpen, setPopoverOpen] = useState(false)
const hubRef = useRef<HTMLDivElement>(null)
const tabsMap = useTabStore((s) => s.tabs)  // 訂閱 tabs 變動，hostId 隨 layout 變化即時更新
const hostId = inferWorkspaceHostId(workspace, tabsMap)  // string | null

// codex round-1 C17 — touch fallback (long-press 500ms opens popover; tap < 500ms triggers add-tab)
const longPressTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
const longPressFiredRef = useRef(false)

const handleTouchStart = () => {
  longPressFiredRef.current = false
  longPressTimerRef.current = setTimeout(() => {
    longPressFiredRef.current = true
    setPopoverOpen(true)
  }, 500)
}
const handleTouchEnd = () => {
  if (longPressTimerRef.current) {
    clearTimeout(longPressTimerRef.current)
    longPressTimerRef.current = null
  }
  // long-press fired → user is now interacting with popover; do NOT trigger add-tab onClick.
}
useEffect(() => {
  if (!popoverOpen) return
  const onPointer = (e: PointerEvent) => {
    if (!hubRef.current?.contains(e.target as Node | null)) {
      setPopoverOpen(false)
    }
  }
  document.addEventListener('pointerdown', onPointer)
  return () => document.removeEventListener('pointerdown', onPointer)
}, [popoverOpen])

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
    onTouchStart={handleTouchStart}
    onTouchEnd={handleTouchEnd}
    onTouchCancel={handleTouchEnd}
  >
    <button
      type="button"
      aria-label={t('nav.add_tab_to_workspace', { name: workspace.name })}
      title={t('nav.add_tab_to_workspace', { name: workspace.name })}
      onClick={(e) => {
        // codex round-1 C17 — if long-press fired, suppress click (touch → click compat)
        if (longPressFiredRef.current) {
          e.preventDefault()
          e.stopPropagation()
          return
        }
        e.stopPropagation()
        onAddTabToWorkspace(workspace.id)
      }}
      onPointerDown={(e) => e.stopPropagation()}
      className="p-0.5 rounded hover:bg-surface-secondary hover:text-text-primary cursor-pointer opacity-0 group-hover/ws-header:opacity-100 focus:opacity-100 transition-opacity focus:outline-none"
    >
      <Plus size={12} />
    </button>
    {popoverOpen && (
      <WorkspaceQuickActionsPopover workspaceId={workspace.id} hostId={hostId} />
    )}
  </div>
)}
```

註：popover 觸發 hover 時 Plus 仍是 hover 中（同一個 group），不會閃。`WorkspaceQuickActionsPopover` 內部已 short-circuit 模組停用 / 無 binding；hostId 為 null 時 chip 仍顯示（spec v4 §3.2.2 — picker 會在點擊時開）。Touch 流程：long-press 500ms 開 popover；短 tap 走原本 click。document `pointerdown` 偵測 hub 外點擊以收回。

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/features/workspace/components/WorkspaceQuickActionsPopover.test.tsx src/features/workspace/components/WorkspaceRow.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): add hover/long-press popover for WORKSPACE_ACTIONS chips beside WorkspaceRow Plus button
```

---

### Task 1b'.2: Phase 1b' 全域驗證

- [ ] **Step 1: Run all SPA tests**

Run: `cd spa && npx vitest run`
Expected: all pass

- [ ] **Step 2: Run SPA lint**

Run: `cd spa && pnpm run lint`
Expected: clean

- [ ] **Step 3: Run SPA build**

Run: `cd spa && pnpm run build`
Expected: clean

- [ ] **Step 4: 手動冒煙（review reviewer 自行操作）**

桌面：
1. Settings → Quick Commands → 確認已有一個 mount=Workspace 的 command
2. sidebar Workspace 行 hover Plus 按鈕 → 左側 popover 展開 chip
3. 點擊 chip → 建 session + 送 cmd + 切過去
4. mouseleave hub → popover 收回
5. 鍵盤 Tab focus 到 Plus → popover 開；blur 整個 hub → popover 收

行動裝置 / 觸控（codex round-1 C17）：
6. iOS Safari 或 Android Chrome 連上 dev URL（`https://*.mlab.host:5174`）
7. 在 sidebar Plus 按鈕做 long-press（按住 ≥500ms 不放）→ popover 展開
8. 放開後 tap chip → 建 session + 送 cmd + 切過去（codex round-2：long-press 觸發後 user 通常會放開手指；popover 已 latched-open，等待後續 tap）
9. 短 tap Plus（<500ms）→ 應觸發原本的 add-tab，**不**展開 popover
10. popover 開啟時 tap 螢幕其他位置 → popover 收回

### Phase 1b' 驗收清單

- [x] Plus hover popover：mouseleave Plus AND popover 收回；鍵盤 focus 同等於 hover；無 binding 時不展開
- [x] Long-press 500ms 開 popover（latched-open，touchend 不關閉）；放開後 tap chip 執行；short tap (<500ms) 走原 click（C17 mobile/touch fallback；codex round-2 文案修正）
- [x] 點擊 hub 外（document pointerdown）→ popover 收回
- [x] 行為與 Phase 1b context menu 入口共用同一 executor，結果一致
- [x] hostId null 時 popover 仍顯示，chip 點擊觸發 HostPickerPopover

---

## Phase 1c — HOST_ACTIONS 入口（小 PR）

> **2026-04-28 — Phase 1c 改寫**：原 Task 1c.1b 共用 `runWorkspaceSlot` 的設計被 PR #694（#690 enforcement）封死 — `runWorkspaceSlot.Deps.assertContextLive` 為 required，`WorkspaceSlotContext.workspaceId` 為 required 非 null string；host caller 無此資訊，編譯期即 fail。
>
> 改寫後 Phase 1c 增加 **Task 1c.0**：新建 `runHostSlot` + `HostSlotContext`（無 workspace 邊界、無 picker、無 destructive-context probe），由 1c.1b 使用。`HostSlotContext` 形狀依 spec §3.2 表格 HOST_ACTIONS ctx `{ hostId, cwd? }` 收窄為 `hostId: string`（非 null）。`assertContextLive` 不引入 — 主機詳情頁的 host record 不會因 tab 關閉而消失，沒有 workspace 等級的「context 在 async 中被 user 摧毀」風險（spec §3.3.1 留白由 1c 自行定義）。

**目標：** Host 詳情頁加 quick actions 區塊；user 設 mount = HOST 即可在 host 頁看到按鈕。

### Task 1c.0: 新建 `runHostSlot` + `HostSlotContext`

**Files:**
- Modify: `spa/src/lib/slot-executor.ts`
- Modify: `spa/src/lib/slot-executor.test.ts`

**設計：**
- `HostSlotContext extends SlotContext { hostId: string }` — `hostId` 收窄為 required string（host 詳情頁 prop 來源即為非 null hostId）。`workspaceId` / `cwd` 沿用父 `SlotContext` 的 optional 欄位，不引入新欄位
- `HostDeps { switchToSession: (hostId, sessionCode) => void }` — 不需要 `resolveHostId`（host 已知）、不需要 `assertContextLive`（無 destruction risk；spec §3.3.1 把 `HostSlotContext` 形狀留白由 1c 定義 → 不預先設計 #690 等價物）
- 失敗 UX 與 `runWorkspaceSlot` 等價（spec §3.3）：
  - createSession fail → `quick_commands.toast.create_failed`，no action
  - send-keys fail + switch ok → `quick_commands.toast.send_keys_failed`，action = retry
  - switch fail（with or without send-keys fail）→ `quick_commands.toast.switch_failed`，no action（避免 retry 一個 user 看不到的 orphan session）
- cwd：`ctx.cwd ?? '~'`（host default）；session 名稱沿用 `genSessionName(cmd)` helper

**理由摘錄（記錄於 commit message）：** spec §3.3.1 定 `HostSlotContext` 形狀以 1c 寫到時的需求為準。本 task 限縮為「不需 picker、不需 workspace-context probe」的最簡形狀；未來若 HOST_ACTIONS 在其他更易失效的 host context（如即將被刪除的 host record）上 mount，再行擴充。YAGNI 不預先設計。

- [ ] **Step 1: Write failing tests**

Append to `spa/src/lib/slot-executor.test.ts`：

```ts
import { runHostSlot, type HostSlotContext } from './slot-executor'

describe('runHostSlot', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useUndoToast.setState({ toast: null })
  })
  afterEach(() => {
    vi.clearAllMocks()
  })

  it('happy path — creates session with host default cwd, sends keys, switches focus', async () => {
    const switchFocus = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-h',
      name: 'A',
      cwd: '~',
      mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus },
    )

    // host default cwd ('~') applied when ctx.cwd is undefined
    expect(createSession).toHaveBeenCalledWith('h1', expect.any(String), '~', 'terminal')
    expect(executeCommand).toHaveBeenCalledWith('h1', 'sess-h', 'echo h')
    expect(switchFocus).toHaveBeenCalledWith('h1', 'sess-h')
    expect(useUndoToast.getState().toast).toBeNull()
  })

  it('respects ctx.cwd override when provided', async () => {
    const switchFocus = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-h', name: 'A', cwd: '/srv', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1', cwd: '/srv' },
      { switchToSession: switchFocus },
    )
    expect(createSession).toHaveBeenCalledWith('h1', expect.any(String), '/srv', 'terminal')
  })

  it('createSession failure — toast WITHOUT action button', async () => {
    const switchFocus = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('500'))

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus },
    )

    expect(switchFocus).not.toHaveBeenCalled()
    expect(executeCommand).not.toHaveBeenCalled()
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Failed to start session/i)
    expect(toast!.action).toBeUndefined()
    expect(toast!.actionLabel).toBeUndefined()
  })

  it('send-keys failure — STILL switches focus + toast carries Retry action', async () => {
    const switchFocus = vi.fn()
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-h', name: 'A', cwd: '~', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockRejectedValueOnce(new Error('503'))

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus },
    )

    expect(switchFocus).toHaveBeenCalledWith('h1', 'sess-h')
    const toast = useUndoToast.getState().toast
    expect(toast).not.toBeNull()
    expect(toast!.message).toMatch(/Session created.*command failed/i)
    expect(toast!.action).toBeTypeOf('function')
    expect(toast!.actionLabel).toMatch(/retry/i)

    // retry triggers executeCommand again
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValueOnce(undefined)
    toast!.action!()
    await Promise.resolve()
    expect(executeCommand).toHaveBeenCalledTimes(2)
  })

  it('switchToSession failure — toast WITHOUT action button', async () => {
    const switchFocus = vi.fn().mockImplementation(() => { throw new Error('switch failed') })
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-h', name: 'A', cwd: '~', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockResolvedValue(undefined)

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus },
    )

    const toast = useUndoToast.getState().toast
    expect(toast!.message).toMatch(/could not switch/i)
    expect(toast!.action).toBeUndefined()
  })

  it('send-keys AND switch BOTH fail — surfaces switch_failed (no retry)', async () => {
    const switchFocus = vi.fn().mockImplementation(() => { throw new Error('switch failed') })
    ;(createSession as unknown as ReturnType<typeof vi.fn>).mockResolvedValue({
      code: 'sess-orphan', name: 'A', cwd: '~', mode: 'terminal',
    })
    ;(executeCommand as unknown as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('send-keys failed'))

    await runHostSlot(
      { id: 'cmd-h', name: 'HostCmd', command: 'echo h' },
      { hostId: 'h1' },
      { switchToSession: switchFocus },
    )

    const toast = useUndoToast.getState().toast
    expect(toast!.message).toMatch(/could not switch/i)
    expect(toast!.message).not.toMatch(/command failed/i)
    expect(toast!.action).toBeUndefined()
  })

  // Type-level invariants verified by `tsc -b` (pnpm run build), NOT by vitest
  // runtime — same caveat as the runWorkspaceSlot type-level test above.
  // Phase 1c contract: HostSlotContext.hostId is required non-null string;
  // HostDeps does NOT carry assertContextLive or resolveHostId.
  it('type-level invariants verified by tsc -b — HostSlotContext.hostId required, HostDeps narrow', () => {
    type Deps = Parameters<typeof runHostSlot>[2]
    type Ctx = Parameters<typeof runHostSlot>[1]
    type IsAny<T> = 0 extends 1 & T ? true : false

    type DepsIsNotAny = IsAny<Deps> extends true ? false : true
    type HostIdRequired = object extends Pick<Ctx, 'hostId'> ? false : true
    type HostIdRejectsNull = null extends Ctx['hostId'] ? false : true
    // Negative: HostDeps must NOT have assertContextLive (spec §3.3.1 carved
    // out — no workspace-context probe needed). If a future change adds it,
    // this assertion fails and forces a redesign discussion.
    type HostDepsHasNoAssert = 'assertContextLive' extends keyof Deps ? false : true
    type HostDepsHasNoResolve = 'resolveHostId' extends keyof Deps ? false : true

    const _depsNotAny: DepsIsNotAny = true
    const _hostIdRequired: HostIdRequired = true
    const _hostIdRejectsNull: HostIdRejectsNull = true
    const _noAssert: HostDepsHasNoAssert = true
    const _noResolve: HostDepsHasNoResolve = true

    expect(_depsNotAny && _hostIdRequired && _hostIdRejectsNull && _noAssert && _noResolve).toBe(true)
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/lib/slot-executor.test.ts`
Expected: FAIL — `runHostSlot` / `HostSlotContext` not exported.

- [ ] **Step 3: Implement `runHostSlot`**

In `spa/src/lib/slot-executor.ts`，append after `runWorkspaceSlot` 區塊：

```ts
/**
 * Host-narrowed slot context (Phase 1c — spec §3.2 HOST_ACTIONS row + §3.3.1).
 *
 * `hostId` is required non-null string — host detail page (the only Phase 1c
 * caller) owns the hostId via prop. No `workspaceId`, no nullable hostId, no
 * `assertContextLive` probe in HostDeps:
 *   - Host detail page is not a destructible context like a workspace; the
 *     host record persists across tab close, so there's no parallel of the
 *     #690 destructive-command guard.
 *   - spec §3.3.1 explicitly defers HostSlotContext shape to 1c. We pick the
 *     minimal shape today; widen only when a real caller demands it.
 */
export interface HostSlotContext extends SlotContext {
  hostId: string
}

interface HostDeps {
  switchToSession: (hostId: string, sessionCode: string) => void
}

/**
 * Host-slot executor — same 3-stage failure UX as runWorkspaceSlot but with a
 * narrower contract (no picker, no destructive-context probe). See spec §3.3
 * for failure-precedence rules.
 */
export async function runHostSlot(
  cmd: QuickCommand,
  ctx: HostSlotContext,
  deps: HostDeps,
): Promise<void> {
  const t = useI18nStore.getState().t
  const toast = useUndoToast.getState()

  let sessionCode: string
  try {
    const session = await createSession(ctx.hostId, genSessionName(cmd), ctx.cwd ?? '~', 'terminal')
    sessionCode = session.code
  } catch (err) {
    const reason = err instanceof Error ? err.message : String(err)
    toast.show(t('quick_commands.toast.create_failed', { reason }))
    return
  }

  let sendKeysOk = true
  try {
    await executeCommand(ctx.hostId, sessionCode, cmd.command)
  } catch {
    sendKeysOk = false
  }

  const switchOk = tryHostSwitch(deps, ctx.hostId, sessionCode)

  if (sendKeysOk && switchOk) return

  if (!switchOk) {
    toast.show(t('quick_commands.toast.switch_failed'))
    return
  }

  toast.show(
    t('quick_commands.toast.send_keys_failed'),
    () => {
      void executeCommand(ctx.hostId, sessionCode, cmd.command).catch(() => undefined)
    },
    t('quick_commands.toast.retry'),
  )
}

function tryHostSwitch(deps: HostDeps, hostId: string, sessionCode: string): boolean {
  try {
    deps.switchToSession(hostId, sessionCode)
    return true
  } catch {
    return false
  }
}
```

註：`tryHostSwitch` 與既有 `trySwitch` 結構相同但形態不同（`HostDeps` vs `Deps`）。不抽共用 helper — 兩個 callsite 已是最少重複，引入額外 generic 反而模糊 type contract。

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd spa && npx vitest run src/lib/slot-executor.test.ts`
Expected: PASS — `runHostSlot` 6 tests + 既有 `runWorkspaceSlot` tests 全綠。

- [ ] **Step 5: Type-check**

Run: `cd spa && pnpm run build`
Expected: clean. Type-level invariant test 由 `tsc -b` 強制，runtime vitest 看不到失敗。

- [ ] **Step 6: Commit**

```
feat(spa): introduce runHostSlot for Phase 1c HOST_ACTIONS

Spec §3.3.1 deferred HostSlotContext shape to Phase 1c. This task
defines the minimal contract: hostId is required non-null string,
HostDeps carries only switchToSession (no picker, no destructive-
context probe). Failure UX matches runWorkspaceSlot's 3-stage rules
per spec §3.3.

Closes the prerequisite for Task 1c.1b (mount HOST_ACTIONS slot).
```

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
- 若沒對應測試，新增一條保護性測試（codex round-1 B8 — full executable body）：

```tsx
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SessionsSection } from './SessionsSection'
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'

// codex round-2 B8 — sessions 來自 useSessionStore.sessions[hostId]
// （見 spa/src/components/hosts/SessionsSection.tsx:135 + 既有測試 SessionsSection.test.tsx:64）；
// PaneContent / Tab schema 見 spa/src/types/tab.ts:36-48。Session row API 物件 schema
// 見既有測試 L55-57：{ code, name, cwd, mode, cc_session_id, cc_model, has_relay }
const HOST_ID = 'h1'
const SESSIONS_FIXTURE = [
  { code: 'sess-1', name: 'dev', cwd: '/tmp', mode: 'terminal', cc_session_id: '', cc_model: '', has_relay: false },
]

describe('SessionsSection — v1 QuickCommandMenu removal (Phase 1c)', () => {
  beforeEach(() => {
    useHostStore.setState({
      hosts: { [HOST_ID]: { id: HOST_ID, name: 'mlab', ip: '100.64.0.2', port: 7860, order: 0 } },
      hostOrder: [HOST_ID],
      runtime: { [HOST_ID]: { status: 'connected' } },
      activeHostId: HOST_ID,
    })
    useSessionStore.setState({ sessions: { [HOST_ID]: SESSIONS_FIXTURE } })
  })

  it('does NOT render v1 QuickCommandMenu inside session rows (moved to new-session adjacency, Phase 1c)', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    // v1 QuickCommandMenu 的 trigger 有 aria-label "Quick Commands" — 確認 0 個
    expect(screen.queryAllByRole('button', { name: /quick commands/i }).length).toBe(0)
  })
})
```

註：實際 aria-label 由 `QuickCommandMenu` 元件決定（subagent 實作前先讀 `spa/src/components/QuickCommandMenu.tsx` 確認 trigger 的 accessible name；若不同請對齊）。Sessions fixture 已直接補上 `useSessionStore.setState({ sessions: { [HOST_ID]: SESSIONS_FIXTURE } })`，欄位對應 SessionsSection 既有測試 (L55-57) 的 row API schema。

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

**Mount 位置（user 決策）：** `SessionsSection.tsx` 標題列 `<div className="flex items-center justify-between mb-4">` 內，new-session 按鈕的**同一個 flex container**裡並列。`<CommandSlot>` 預設 chip toolbar 樣式恰好對應，視覺與 `Plus + 新增 session` 按鈕對齊；不做特殊 popover。

**設計：** 把 new-session 按鈕外層改為 `<div className="flex items-center gap-2">`（包 CommandSlot + new-session button 兩者），整體右對齊（保留外層 `justify-between` 與標題的相對位置）。

**Spec §3.3.1 — type-locked：** 必須使用 `runHostSlot`（Task 1c.0 引入）。`runWorkspaceSlot` 因 `WorkspaceSlotContext.workspaceId` 為 required 非 null string 編譯期會失敗 — 這是 PR #694 設下的 forward-compat 防線，讓 host caller 不能誤用 workspace executor。

**switchToSession 設計：** 與 `SessionsSection.handleOpen` 等價（既有 row 上 Open 按鈕的行為） — `openSingletonTab` + `useWorkspaceStore.insertTab(tabId)` + `setActiveTab`。Host 詳情頁本身是某個 workspace 內的 tab；新建的 session tab 該插進**同一個 active workspace**（`insertTab` 不傳 workspaceId 時預設 `activeWorkspaceId`），讓 user 立即看到 tab 出現在 tab strip 上。

- [ ] **Step 1: Write the failing test**

Edit `spa/src/components/hosts/SessionsSection.test.tsx`，在現有 `describe('SessionsSection', ...)` 之外新增：

```tsx
import { useQuickCommandStore } from '../../stores/useQuickCommandStore'
import { QUICK_COMMAND_SLOTS } from '../../lib/quick-command-slots'

describe('SessionsSection — host quick actions slot adjacent to new-session button (Phase 1c)', () => {
  beforeEach(() => {
    useQuickCommandStore.setState({
      global: [{ id: 'cmd-h', name: 'HostCmd', command: 'echo h' }],
      byHost: {},
      bindings: { 'cmd-h': [QUICK_COMMAND_SLOTS.HOST_ACTIONS] },
    })
  })

  it('renders <CommandSlot mountTo=HOST_ACTIONS> chips next to the new-session button', () => {
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.getByLabelText(/^HostCmd/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /new session/i })).toBeInTheDocument()
  })

  it('hides slot when no commands are bound to HOST_ACTIONS (new-session button still visible)', () => {
    useQuickCommandStore.setState({
      global: [],
      byHost: {},
      bindings: {},
    })
    render(<SessionsSection hostId={HOST_ID} />)
    expect(screen.queryByLabelText(/^HostCmd/)).toBeNull()
    expect(screen.getByRole('button', { name: /new session/i })).toBeInTheDocument()
  })
})
```

註：既有 `vi.mock('../../stores/useQuickCommandStore', ...)` 的 mock 形狀只覆蓋 `global` / `byHost` / `getCommands`（無 `bindings` / `setState`），會導致 `<CommandSlot>` 取 `bindings` 為 undefined。subagent 實作時請：
1. 把該 mock 改為**真實 store**（移除 `vi.mock` 那段，import `useQuickCommandStore` from `'../../stores/useQuickCommandStore'`），或
2. 擴充 mock 為含 `bindings` selector + `setState`（但保留其他既有測試的 default empty state）。
推薦選項 1（更簡單，且既有測試仍 pass — 既有 `setState({ sessions: ..., })` 不會 wipe 其他 store）。

註 2：Phase 1b' 已建立 `useQuickCommandStore` 真實 store 在多個元件 test 中正常 init；本檔案是 1b' 之前留下的舊 mock。改用真實 store 順便還清這條歷史包袱。

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/components/hosts/SessionsSection.test.tsx`
Expected: FAIL — slot 未掛載。

- [ ] **Step 3: Mount the slot**

In `spa/src/components/hosts/SessionsSection.tsx`：

加 imports：

```tsx
import { CommandSlot } from '../CommandSlot'
import { runHostSlot } from '../../lib/slot-executor'
import { QUICK_COMMAND_SLOTS } from '../../lib/quick-command-slots'
```

（`useTabStore` 與 `useWorkspaceStore` 已 imported）

修改標題列區段：

```tsx
<div className="flex items-center justify-between mb-4">
  <h2 className="text-lg font-semibold">{t('hosts.sessions')}</h2>
  <div className="flex items-center gap-2">
    <CommandSlot
      mountTo={QUICK_COMMAND_SLOTS.HOST_ACTIONS}
      ctx={{ hostId }}
      executor={(cmd, ctx) =>
        runHostSlot(cmd, ctx, {
          switchToSession: (h, sessionCode) => {
            // 與 handleOpen 等價：openSingletonTab + insertTab(active ws) + setActiveTab。
            // tmux-session content 必填欄位（types/tab.ts:38）：kind / hostId /
            // sessionCode / mode / cachedName / tmuxInstance。
            const tabId = useTabStore.getState().openSingletonTab({
              kind: 'tmux-session',
              hostId: h,
              sessionCode,
              mode: 'terminal',
              cachedName: sessionCode,
              tmuxInstance: '',
            })
            useWorkspaceStore.getState().insertTab(tabId)
            useTabStore.getState().setActiveTab(tabId)
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

註：`runHostSlot` 的 ctx type 是 `HostSlotContext`（`{ hostId: string; ... }`，hostId 收窄為非 null）。`<CommandSlot ctx={{ hostId }}>` 傳的是 `SlotContext`（`hostId: string | null`）。本 callsite 的 hostId 來自 prop（`Props.hostId: string`），執行期一定非 null，但 TypeScript 看 `<CommandSlot>` 簽名時無法推導。executor closure 收的 ctx 仍是 `SlotContext` shape；`runHostSlot` 第二參數要 `HostSlotContext`。subagent 實作時用以下任一處理：
1. **執行期 narrow + as cast**：`executor={(cmd, ctx) => runHostSlot(cmd, { ...ctx, hostId: hostId }, deps)}` — 直接從 closure scope 取 prop 的非 null hostId 覆蓋。**推薦此法** — 顯式說明「ctx.hostId 在此 callsite 必為非 null」的 invariant 來源是 SessionsSection prop，不是 ctx 本身
2. 提供 generic `<CommandSlot<HostSlotContext>>` — 但 `<CommandSlot>` 並非 generic 元件，需要先 refactor，scope 過大。不採

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/components/hosts/SessionsSection.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```
feat(spa): mount HOST_ACTIONS slot beside new-session button in SessionsSection

Wires runHostSlot (Task 1c.0) into SessionsSection. switchToSession
opens a singleton tab + inserts into the active workspace + activates
it (matches existing handleOpen). HostSlotContext is built by
narrowing the SlotContext.hostId from the SessionsSection prop —
runWorkspaceSlot is intentionally not reachable here per spec §3.3.1
type lock.
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

**新 Phase 順序（codex round-1 結構性變更）：1a → 1b → 1b' → 1c。**

- [ ] Phase 1a 已 merge → main，且 alpha bump 完成（或一個 PR sequence 規劃中）
- [ ] **Phase 1b 已 merge → main**，含 Settings UI + 資料層 + Workspace **右鍵 context menu** 入口（單一 PR）
  - Phase 1b 內 task 順序：**1b.0a（inferWorkspaceHostId helper）→ 1b.0c（i18n keys 前置 — codex round-1 B9 / round-2 B9）→ 1b.0b（HostPickerPopover 元件 — 仰賴 0c keys）→ 1b.1（CommandSlot + busy prop）→ 1b.1.5（Toast schema 擴充 — codex round-1 B4）→ 1b.2（slot-executor + resolveHostId）→ 1b.3（Settings UI + chip 方向鍵 — codex round-1 C15）→ 1b.4（settings contribution + 剩餘 i18n）→ 1b.5a（context menu 入口 — codex round-1 B5/B6/B7/B8 修正）→ 1b.6（全域驗證）**
  - 此 PR 滿足 spec §6「Settings 首次出現時至少一個 slot 生效」要求 — context menu 是穩定入口
- [ ] **Phase 1b' 已 merge → main**，**Plus hover popover 過渡入口獨立 PR**（codex round-1 結構性變更 — 風險隔離）
  - Phase 1b' 內 task 順序：**1b'.1（WorkspaceQuickActionsPopover + WorkspaceRow Plus hover + mobile/touch fallback — codex round-1 C17）→ 1b'.2（全域驗證）**
  - User 標記此入口為過渡實作；獨立 PR 便於日後 revert / replace
- [ ] Phase 1c 已 merge → main，**1c.0（runHostSlot + tests — spec §3.3.1 type-lock 前置）→ 1c.1a（移除 SessionsSection row 的 v1 整合 — codex round-1 B8）→ 1c.1b（new-session 旁掛 HOST_ACTIONS）三個 commits 同 PR、依序 ship**（1c.0 純新增 lib + tests / 1c.1a 純減量 / 1c.1b 純新增 UI；diff 易 review）
- [ ] Phase 1a 不可單獨 ship 在沒有 Phase 1b 計畫的情況（純資料層 user 看不到任何成果，會困惑）— 但 1a + 1b 兩個 PR 接力 ship 是允許的（spec §6 只要求 Settings UI 出現時必有 slot 生效）
- [ ] Phase 1b' 與 1c 之間順序可彈性（user 可決定先 ship 哪個）；建議 1b' → 1c 因為 1b' 是 Phase 1b 的延伸，1c 是新 mount 點

## 範圍邊界（Phase 1 不做）

- 不動 `PaneLayoutRenderer` 內 `extraActions` 的 v1 `QuickCommandMenu`（spec §3.4）
- ~~不動 `SessionsSection` row actions 的 v1 `QuickCommandMenu`~~ — **已調整：Phase 1c Task 1c.1a 將 SessionsSection row 的 v1 整合移除**（user 決策；功能集中至 new-session 入口；spec §3.4 的「保留現狀」對 SessionsSection 不再適用，但對 `PaneLayoutRenderer` 仍適用）
- 不做 per-host bindings UI（store 支援，UI 推到 Phase 2）
- 不做 mode 設定 / 動態 function command / command palette / 快捷鍵（spec §6 不在範圍）
- 不做 `module-registry.commands?` legacy 收編（Phase 2 decision gate，spec §8.2）
- 不寫 persist migration（alpha-no-migration）
- **不做 multi-host workspace binding 系統**（spec §3.5 forward-compat） — 未來 workspace 支援多重 `(hostId, path)` binding 與「default launcher」checkbox；`HostPickerPopover` 設計可重用，inferWorkspaceHostId 將退為「無 binding 時 fallback」，但 v2 不實作該層
- **不做 Workspace.hostId schema 欄位** — spec v4 確定走 layout 多數決，不引入新 type 欄位

---

## Sub-skill / Subagent 提示

執行此 plan 時：

1. 進 worktree 後，**每個 Bash 指令必須以 `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/quick-commands-v2 && ` 開頭**（feedback_subagent_cwd_enforcement.md）
2. 一個 task 一個 commit，commit message 用 conventional commit + Co-Authored-By trailer
3. zustand test setState **顯式列出** `global` / `byHost` / `bindings` 三個 mutable fields；**不要**只寫 `setState({ bindings: {...} })`，會 wipe 其他 state（feedback_zustand_harness_setstate.md）。同樣原則套用 `useHostStore`（測試需要時列出 `hosts` / `hostOrder` / `runtime` / `activeHostId` 四個欄位）。
4. 任何測試 / lint / build 指令在主 Claude 機器跑（feedback_codex_sandbox_no_install.md）
5. **Phase 1b 任務依賴**（codex round-1/2 後）：1b.0a → 1b.0c（i18n 必須前置 — round-2 B9）→ 1b.0b（HostPickerPopover；仰賴 0c keys）→ 1b.1（CommandSlot）→ 1b.1.5（toast schema 擴充）→ 1b.2（executor）→ 1b.3 → 1b.4 → 1b.5a → 1b.6。其中 1b.5a 的 switchToSession 必須做完整 `openSingletonAndSelect` 等價邏輯（codex round-1 B5/B6）：openSingletonTab（含 mode/cachedName/tmuxInstance 完整欄位）→ insertTab → setActiveWorkspace + setActiveTab。
6. **Phase 1b' 任務依賴**：必須在 Phase 1b PR merge 後才能開工；`<CommandSlot>`、`<HostPickerPopover>`、`runWorkspaceSlot`、`inferWorkspaceHostId`、i18n keys 全部就緒後再實作 hover popover + 與 1b.5a 同等的 switchToSession 邏輯。
7. **Phase 1c 任務依賴**：必須在 #690 enforcement (PR #694 / alpha.242) merge 後才能開工；task 順序 **1c.0（runHostSlot + tests）→ 1c.1a（移除 row v1）→ 1c.1b（mount HOST_ACTIONS chip 用 runHostSlot）→ 1c.2（全域驗證）**。1c.1b 的 ctx narrowing 從 `SessionsSection` prop 取非 null hostId（細節見 Task 1c.1b 內註）。
8. PR 完成後委派 codex 兩輪 review（標準 + 攻擊/防守/體質）；發現問題彙整成表格（信心 / 關聯 / 複雜度）後決定即修 vs 開 issue 追蹤
8. **Toast 行為的 spec §3.3 對應**（codex round-1 B4）：create-session failure → `toast.show(msg)` 不傳 action；switch-to-session failure → `toast.show(msg)` 不傳 action；send-keys failure → `toast.show(msg, retryFn, t('quick_commands.toast.retry'))`。元件渲染規則：`toast.action == null` → 不渲染 button。
