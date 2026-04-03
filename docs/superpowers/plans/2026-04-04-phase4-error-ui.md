# Phase 4 — 錯誤 UI 實作計畫

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 實作完整的錯誤 UI — tab terminated 狀態、host 層級 L1-L3 錯誤顯示、host 刪除 cascade cleanup + undo、L2/L3 通知、以及 5 個 bug fix。

**Architecture:** PaneContent 新增 `terminated` 欄位記錄終結原因（event-sourced，不可推導）。`useHostConnection` hook 封裝 SM 存取。通知 click handler 改為 action payload 模式。Host 刪除支援 undo（in-memory snapshot）。

**Tech Stack:** React 19 / Zustand 5 / Tailwind 4 / Vitest / Phosphor Icons / xterm.js 6

**Spec:** `docs/superpowers/specs/2026-04-04-phase4-error-ui-design.md`

---

## Phase 4a — Tab 狀態基礎建設

### Task 1: PaneContent 類型更新 + TerminatedReason

**Files:**
- Modify: `spa/src/types/tab.ts:24-32`

- [ ] **Step 1: 更新 PaneContent 類型**

```typescript
// spa/src/types/tab.ts — 替換 PaneContent 定義

export type TerminatedReason = 'session-closed' | 'tmux-restarted' | 'host-removed'

export type PaneContent =
  | { kind: 'new-tab' }
  | { kind: 'tmux-session'; hostId: string; sessionCode: string; mode: 'terminal' | 'stream'; cachedName: string; tmuxInstance: string; terminated?: TerminatedReason }
  | { kind: 'dashboard' }
  | { kind: 'hosts' }
  | { kind: 'history' }
  | { kind: 'settings'; scope: 'global' | { workspaceId: string } }
  | { kind: 'browser'; url: string }
  | { kind: 'memory-monitor' }
```

- [ ] **Step 2: 確認 TypeScript 編譯錯誤出現**

Run: `cd spa && npx tsc --noEmit 2>&1 | head -30`
Expected: 大量 `'session'` 相關的 type error（因為所有引用還沒改）

---

### Task 2: 全域 rename `kind: 'session'` → `kind: 'tmux-session'`

**Files:**
- Modify: 所有含 `kind: 'session'` 或 `kind === 'session'` 的檔案（約 31 個）

- [ ] **Step 1: 列出所有需修改的檔案**

Run: `cd spa && grep -rn "kind.*['\"]session['\"]" src/ --include='*.ts' --include='*.tsx' | grep -v node_modules | cut -d: -f1 | sort -u`

- [ ] **Step 2: 批次替換原始碼**

在所有 `spa/src/` 下的 `.ts` 和 `.tsx` 檔案中：
- `kind: 'session'` → `kind: 'tmux-session'`
- `kind === 'session'` → `kind === 'tmux-session'`
- `kind !== 'session'` → `kind !== 'tmux-session'`
- `case 'session'` → `case 'tmux-session'`

注意：不要替換 `kind: 'session-tab'`（route-utils 中的 route kind）或其他非 PaneContent 的 `session` 字串。只替換 PaneContent union member 的 `kind` 值。

- [ ] **Step 3: 確認 TypeScript 編譯通過**

Run: `cd spa && npx tsc --noEmit`
Expected: 無錯誤

- [ ] **Step 4: 確認現有測試通過**

Run: `cd spa && npx vitest run`
Expected: 全部通過

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor: rename PaneContent kind 'session' → 'tmux-session'

Add TerminatedReason type for Phase 4 terminated tab state.
Mechanical rename across ~31 files."
```

---

### Task 3: Zustand persist migration

**Files:**
- Modify: `spa/src/stores/useTabStore.ts:158-167`
- Test: `spa/src/stores/useTabStore.test.ts`

- [ ] **Step 1: 寫 migration 的測試**

在 `spa/src/stores/useTabStore.test.ts` 加入：

```typescript
describe('persist migration', () => {
  it('migrates kind "session" to "tmux-session" in version 2', () => {
    // Simulate v1 persisted state with old kind
    const v1State = {
      tabs: {
        tab1: {
          id: 'tab1',
          pinned: false,
          locked: false,
          createdAt: 1000,
          layout: {
            type: 'leaf' as const,
            pane: {
              id: 'pane1',
              content: {
                kind: 'session',
                hostId: 'h1',
                sessionCode: 'abc123',
                mode: 'terminal',
                cachedName: 'test',
                tmuxInstance: '123:456',
              },
            },
          },
        },
      },
      tabOrder: ['tab1'],
      activeTabId: 'tab1',
    }

    const migrated = migrateTabStore(v1State, 1)
    const pane = (migrated as any).tabs.tab1.layout.pane
    expect(pane.content.kind).toBe('tmux-session')
  })

  it('preserves non-session tabs during migration', () => {
    const v1State = {
      tabs: {
        tab1: {
          id: 'tab1',
          pinned: false,
          locked: false,
          createdAt: 1000,
          layout: {
            type: 'leaf' as const,
            pane: {
              id: 'pane1',
              content: { kind: 'dashboard' },
            },
          },
        },
      },
      tabOrder: ['tab1'],
      activeTabId: 'tab1',
    }

    const migrated = migrateTabStore(v1State, 1)
    const pane = (migrated as any).tabs.tab1.layout.pane
    expect(pane.content.kind).toBe('dashboard')
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts`
Expected: FAIL — `migrateTabStore` 尚未定義

- [ ] **Step 3: 實作 migration 函式**

在 `spa/src/stores/useTabStore.ts` 加入 migrate 函式並更新 persist 設定：

```typescript
// 在 file 頂部或 persist config 前加入
import type { PaneLayout } from '../types/tab'

function migrateLayout(layout: PaneLayout): PaneLayout {
  if (layout.type === 'leaf') {
    const content = layout.pane.content as any
    if (content.kind === 'session') {
      return {
        ...layout,
        pane: { ...layout.pane, content: { ...content, kind: 'tmux-session' } },
      }
    }
    return layout
  }
  return { ...layout, children: layout.children.map(migrateLayout) }
}

export function migrateTabStore(state: any, version: number): any {
  if (version < 2) {
    const tabs: Record<string, any> = {}
    for (const [id, tab] of Object.entries(state.tabs as Record<string, any>)) {
      tabs[id] = { ...tab, layout: migrateLayout(tab.layout) }
    }
    return { ...state, tabs }
  }
  return state
}
```

更新 persist config：

```typescript
{
  name: STORAGE_KEYS.TABS,
  storage: purdexStorage,
  version: 2,
  migrate: migrateTabStore,
  partialize: (state) => ({
    tabs: state.tabs,
    tabOrder: state.tabOrder,
    activeTabId: state.activeTabId,
  }),
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.test.ts
git commit -m "feat: TabStore persist migration v1→v2 for kind rename"
```

---

### Task 4: deriveTabState 推導函式

**Files:**
- Create: `spa/src/lib/tab-state.ts`
- Test: `spa/src/lib/tab-state.test.ts`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/lib/tab-state.test.ts
import { describe, it, expect } from 'vitest'
import { deriveTabState } from './tab-state'
import type { PaneContent } from '../types/tab'
import type { HostRuntime } from '../stores/useHostStore'

describe('deriveTabState', () => {
  it('returns "active" for non-tmux-session kinds', () => {
    expect(deriveTabState({ kind: 'dashboard' })).toBe('active')
    expect(deriveTabState({ kind: 'browser', url: 'http://x' })).toBe('active')
  })

  it('returns "terminated" when terminated field is set', () => {
    const content: PaneContent = {
      kind: 'tmux-session', hostId: 'h', sessionCode: 'c',
      mode: 'terminal', cachedName: 'n', tmuxInstance: 't',
      terminated: 'session-closed',
    }
    const runtime: HostRuntime = { status: 'reconnecting' }
    // terminated takes precedence over reconnecting
    expect(deriveTabState(content, runtime)).toBe('terminated')
  })

  it('returns "reconnecting" when runtime is reconnecting', () => {
    const content: PaneContent = {
      kind: 'tmux-session', hostId: 'h', sessionCode: 'c',
      mode: 'terminal', cachedName: 'n', tmuxInstance: 't',
    }
    expect(deriveTabState(content, { status: 'reconnecting' })).toBe('reconnecting')
  })

  it('returns "active" for connected tmux-session', () => {
    const content: PaneContent = {
      kind: 'tmux-session', hostId: 'h', sessionCode: 'c',
      mode: 'terminal', cachedName: 'n', tmuxInstance: 't',
    }
    expect(deriveTabState(content, { status: 'connected' })).toBe('active')
  })

  it('returns "active" when runtime is undefined', () => {
    const content: PaneContent = {
      kind: 'tmux-session', hostId: 'h', sessionCode: 'c',
      mode: 'terminal', cachedName: 'n', tmuxInstance: 't',
    }
    expect(deriveTabState(content)).toBe('active')
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && npx vitest run src/lib/tab-state.test.ts`
Expected: FAIL

- [ ] **Step 3: 實作**

```typescript
// spa/src/lib/tab-state.ts
import type { PaneContent } from '../types/tab'
import type { HostRuntime } from '../stores/useHostStore'

export type TabState = 'active' | 'reconnecting' | 'terminated'

export function deriveTabState(content: PaneContent, runtime?: HostRuntime): TabState {
  if (content.kind !== 'tmux-session') return 'active'
  if (content.terminated) return 'terminated'
  if (runtime?.status === 'reconnecting') return 'reconnecting'
  return 'active'
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && npx vitest run src/lib/tab-state.test.ts`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/tab-state.ts spa/src/lib/tab-state.test.ts
git commit -m "feat: deriveTabState — tab display state derivation"
```

---

### Task 5: Tab icon + name 更新（terminated 狀態）

**Files:**
- Modify: `spa/src/lib/pane-labels.ts`
- Modify: `spa/src/components/SortableTab.tsx`
- Test: `spa/src/lib/pane-labels.test.ts`（如已存在）

- [ ] **Step 1: 更新 getPaneIcon 支援 terminated**

在 `spa/src/lib/pane-labels.ts` 的 `getPaneIcon` 函式中，`case 'tmux-session'` 分支加入 terminated 判斷：

```typescript
case 'tmux-session':
  if (content.terminated) return 'SmileySad'
  return content.mode === 'stream' ? 'ChatCircleDots' : 'TerminalWindow'
```

- [ ] **Step 2: 更新 getPaneLabel 支援 terminated**

在 `getPaneLabel` 的 `case 'tmux-session'` 分支加入 terminated 判斷：

```typescript
case 'tmux-session': {
  if (content.terminated) {
    const name = content.cachedName || content.sessionCode
    return `${name}（Terminated）`
  }
  // 原有邏輯：session?.name ?? content.cachedName ?? content.sessionCode
  ...
}
```

- [ ] **Step 3: 更新 SortableTab 的 offline indicator**

在 `spa/src/components/SortableTab.tsx` 中，目前有 offline indicator 邏輯（檢查 host runtime status）。加入 terminated 判斷：若 `primaryContent.kind === 'tmux-session' && primaryContent.terminated`，icon 直接使用 `SmileySad`，不顯示其他 status indicator。

- [ ] **Step 4: 確認 lint + 測試通過**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: 全部通過

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/pane-labels.ts spa/src/components/SortableTab.tsx
git commit -m "feat: tab icon SmileySad + name suffix for terminated state"
```

---

### Task 6: i18n key 新增

**Files:**
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: 加入所有 Phase 4 i18n key**

在 `en.json` 加入：

```json
"terminated": {
  "session_closed": "Session closed",
  "session_closed_desc": "{name} no longer exists",
  "tmux_restarted": "tmux restarted",
  "tmux_restarted_desc": "Previous sessions are no longer valid",
  "host_removed": "Host removed",
  "host_removed_desc": "This host has been removed",
  "no_sessions": "No available connections",
  "close_tab": "Close tab",
  "select_session": "Select a session to reconnect"
},
"hosts": {
  ... // 在現有 hosts section 內新增：
  "confirm_delete_tabs": "Also close all tabs for this host",
  "deleted_toast": "Deleted {name}",
  "undo": "Undo",
  "error_unreachable": "Host unreachable",
  "error_refused": "Daemon not running",
  "error_tmux_down": "tmux unavailable"
},
"connection": {
  "reconnect": "Reconnect"
},
"notification": {
  ... // 在現有 notification section 內新增：
  "daemon_refused": "{name} — Daemon not running",
  "tmux_down": "{name} — tmux unavailable"
}
```

在 `zh-TW.json` 加入對應的繁體中文翻譯（見 spec 第八節）。

- [ ] **Step 2: 確認 locale completeness test 通過**

Run: `cd spa && npx vitest run src/locales/locale-completeness.test.ts`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat: add Phase 4 i18n keys for terminated, host errors, notifications"
```

---

## Phase 4b — Terminated 錯誤頁元件

### Task 7: SessionPickerList 元件

**Files:**
- Create: `spa/src/components/SessionPickerList.tsx`
- Test: `spa/src/components/SessionPickerList.test.tsx`

- [ ] **Step 1: 寫測試**

```typescript
// spa/src/components/SessionPickerList.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { SessionPickerList } from './SessionPickerList'

// Mock stores
vi.mock('../stores/useHostStore')
vi.mock('../stores/useSessionStore')

describe('SessionPickerList', () => {
  it('renders "no available connections" when no connected hosts', () => {
    // Setup mocks: no hosts or all disconnected
    render(<SessionPickerList onSelect={vi.fn()} />)
    expect(screen.getByText(/no available connections/i)).toBeInTheDocument()
  })

  it('renders sessions grouped by connected host', () => {
    // Setup mocks: 2 hosts, 1 connected with sessions
    render(<SessionPickerList onSelect={vi.fn()} />)
    // Assert host group header and session items visible
  })

  it('calls onSelect with session info when clicked', async () => {
    const onSelect = vi.fn()
    // Setup mocks with a connected host and sessions
    render(<SessionPickerList onSelect={onSelect} />)
    // Click a session item
    // Assert onSelect called with { hostId, sessionCode, cachedName, tmuxInstance }
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && npx vitest run src/components/SessionPickerList.test.tsx`

- [ ] **Step 3: 實作元件**

```typescript
// spa/src/components/SessionPickerList.tsx
import { useHostStore } from '../stores/useHostStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTranslation } from '../hooks/useTranslation'

interface SessionSelection {
  hostId: string
  sessionCode: string
  cachedName: string
  tmuxInstance: string
}

interface Props {
  onSelect: (selection: SessionSelection) => void
}

export function SessionPickerList({ onSelect }: Props) {
  const { t } = useTranslation()
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const sessions = useSessionStore((s) => s.sessions)

  const connectedHosts = hostOrder.filter(
    (id) => runtime[id]?.status === 'connected'
  )

  if (connectedHosts.length === 0) {
    return (
      <div className="text-center text-zinc-500 py-8">
        {t('terminated.no_sessions')}
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <p className="text-xs text-zinc-400">{t('terminated.select_session')}</p>
      {connectedHosts.map((hostId) => {
        const host = hosts[hostId]
        const hostSessions = sessions[hostId] ?? []
        if (!host || hostSessions.length === 0) return null
        return (
          <div key={hostId}>
            <div className="text-xs text-zinc-500 mb-1">{host.name}</div>
            <div className="space-y-1">
              {hostSessions.map((s) => (
                <button
                  key={s.code}
                  className="w-full text-left px-3 py-2 rounded hover:bg-zinc-700/50 text-sm"
                  onClick={() =>
                    onSelect({
                      hostId,
                      sessionCode: s.code,
                      cachedName: s.name,
                      tmuxInstance: runtime[hostId]?.info?.tmux_instance ?? '',
                    })
                  }
                >
                  {s.name}
                </button>
              ))}
            </div>
          </div>
        )
      })}
    </div>
  )
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && npx vitest run src/components/SessionPickerList.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/SessionPickerList.tsx spa/src/components/SessionPickerList.test.tsx
git commit -m "feat: SessionPickerList — cross-host session picker for terminated tabs"
```

---

### Task 8: TerminatedPane 元件

**Files:**
- Create: `spa/src/components/TerminatedPane.tsx`
- Test: `spa/src/components/TerminatedPane.test.tsx`

- [ ] **Step 1: 寫測試**

測試要覆蓋：
- 三種 reason 各自顯示對應訊息
- 關閉按鈕呼叫 closeTab
- SessionPickerList 的 onSelect 覆寫 PaneContent（清除 terminated）

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && npx vitest run src/components/TerminatedPane.test.tsx`

- [ ] **Step 3: 實作元件**

```typescript
// spa/src/components/TerminatedPane.tsx
import { SmileySad, X } from '@phosphor-icons/react'
import { useTabStore } from '../stores/useTabStore'
import { useTranslation } from '../hooks/useTranslation'
import { SessionPickerList } from './SessionPickerList'
import type { PaneContent, TerminatedReason } from '../types/tab'

interface Props {
  content: Extract<PaneContent, { kind: 'tmux-session' }>
  tabId: string
  paneId: string
}

const REASON_KEYS: Record<TerminatedReason, { title: string; desc: string }> = {
  'session-closed': { title: 'terminated.session_closed', desc: 'terminated.session_closed_desc' },
  'tmux-restarted': { title: 'terminated.tmux_restarted', desc: 'terminated.tmux_restarted_desc' },
  'host-removed': { title: 'terminated.host_removed', desc: 'terminated.host_removed_desc' },
}

export function TerminatedPane({ content, tabId, paneId }: Props) {
  const { t } = useTranslation()
  const closeTab = useTabStore((s) => s.closeTab)
  const setPaneContent = useTabStore((s) => s.setPaneContent)
  const reason = content.terminated!
  const keys = REASON_KEYS[reason]

  const handleSelect = (selection: { hostId: string; sessionCode: string; cachedName: string; tmuxInstance: string }) => {
    setPaneContent(tabId, paneId, {
      kind: 'tmux-session',
      hostId: selection.hostId,
      sessionCode: selection.sessionCode,
      mode: content.mode, // 沿用原 tab 的 mode
      cachedName: selection.cachedName,
      tmuxInstance: selection.tmuxInstance,
    })
  }

  return (
    <div className="flex flex-col items-center justify-center h-full p-8 text-center">
      <SmileySad size={48} className="text-zinc-500 mb-4" />
      <h2 className="text-lg font-medium text-zinc-300 mb-1">{t(keys.title)}</h2>
      <p className="text-sm text-zinc-500 mb-6">
        {t(keys.desc, { name: content.cachedName })}
      </p>
      <button
        className="text-sm text-zinc-400 hover:text-zinc-200 mb-8"
        onClick={() => closeTab(tabId)}
      >
        {t('terminated.close_tab')}
      </button>
      <div className="w-full max-w-sm">
        <SessionPickerList onSelect={handleSelect} />
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && npx vitest run src/components/TerminatedPane.test.tsx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/TerminatedPane.tsx spa/src/components/TerminatedPane.test.tsx
git commit -m "feat: TerminatedPane — error page for terminated tabs"
```

---

### Task 9: SessionPaneContent terminated 分支 + WS guard

**Files:**
- Modify: `spa/src/components/SessionPaneContent.tsx`
- Modify: `spa/src/hooks/useTerminalWs.ts`

- [ ] **Step 1: SessionPaneContent 加入 terminated 判斷**

在 `SessionPaneContent.tsx` 的 render 邏輯最前面加入：

```typescript
import { TerminatedPane } from './TerminatedPane'

// 在元件函式開頭，content 解構之後
if (content.terminated) {
  return <TerminatedPane content={content} tabId={tabId} paneId={paneId} />
}
```

- [ ] **Step 2: useTerminalWs 加入 terminated guard**

在 `spa/src/hooks/useTerminalWs.ts` 的 WS 連線建立之前加入 guard：

```typescript
// 在 useEffect 內，ws 連線建立前
if (content.terminated) return
```

- [ ] **Step 3: 確認 lint + 測試通過**

Run: `cd spa && pnpm run lint && npx vitest run`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/SessionPaneContent.tsx spa/src/hooks/useTerminalWs.ts
git commit -m "feat: terminated tab rendering + WS isolation guard"
```

---

### Task 10: TabStore markTerminated action + 偵測寫入

**Files:**
- Modify: `spa/src/stores/useTabStore.ts`
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`
- Test: `spa/src/stores/useTabStore.test.ts`

- [ ] **Step 1: 寫 markTerminated 測試**

```typescript
describe('markTerminated', () => {
  it('marks matching tmux-session panes as terminated', () => {
    // Setup: add a tab with tmux-session content
    // Call markTerminated(hostId, sessionCode, 'session-closed')
    // Assert: pane content has terminated: 'session-closed'
  })

  it('scans full pane tree including split panes', () => {
    // Setup: add a tab with split layout
    // Call markTerminated
    // Assert: both primary and secondary panes are marked if matching
  })

  it('does not mark non-matching panes', () => {
    // Setup: add tabs with different hostId/sessionCode
    // Call markTerminated
    // Assert: non-matching panes unchanged
  })
})
```

- [ ] **Step 2: 確認測試失敗**

- [ ] **Step 3: 實作 markTerminated + markHostTerminated**

在 `useTabStore.ts` 加入兩個 action：

```typescript
// 標記特定 session 的所有 pane 為 terminated
markTerminated: (hostId: string, sessionCode: string, reason: TerminatedReason) =>
  set((state) => {
    let changed = false
    const tabs = { ...state.tabs }
    for (const [id, tab] of Object.entries(tabs)) {
      const newLayout = markPanesInLayout(tab.layout, hostId, sessionCode, reason)
      if (newLayout !== tab.layout) {
        tabs[id] = { ...tab, layout: newLayout }
        changed = true
      }
    }
    return changed ? { tabs } : state
  }),

// 標記某 host 所有 tmux-session pane 為 terminated
markHostTerminated: (hostId: string, reason: TerminatedReason) =>
  set((state) => {
    let changed = false
    const tabs = { ...state.tabs }
    for (const [id, tab] of Object.entries(tabs)) {
      const newLayout = markHostPanesInLayout(tab.layout, hostId, reason)
      if (newLayout !== tab.layout) {
        tabs[id] = { ...tab, layout: newLayout }
        changed = true
      }
    }
    return changed ? { tabs } : state
  }),
```

需要在 `pane-tree.ts` 或 `useTabStore.ts` 中加入 helper：

```typescript
function markPanesInLayout(layout: PaneLayout, hostId: string, sessionCode: string, reason: TerminatedReason): PaneLayout {
  if (layout.type === 'leaf') {
    const c = layout.pane.content
    if (c.kind === 'tmux-session' && c.hostId === hostId && c.sessionCode === sessionCode && !c.terminated) {
      return { ...layout, pane: { ...layout.pane, content: { ...c, terminated: reason } } }
    }
    return layout
  }
  const children = layout.children.map((child) => markPanesInLayout(child, hostId, sessionCode, reason))
  return children.some((c, i) => c !== layout.children[i]) ? { ...layout, children } : layout
}

function markHostPanesInLayout(layout: PaneLayout, hostId: string, reason: TerminatedReason): PaneLayout {
  if (layout.type === 'leaf') {
    const c = layout.pane.content
    if (c.kind === 'tmux-session' && c.hostId === hostId && !c.terminated) {
      return { ...layout, pane: { ...layout.pane, content: { ...c, terminated: reason } } }
    }
    return layout
  }
  const children = layout.children.map((child) => markHostPanesInLayout(child, hostId, reason))
  return children.some((c, i) => c !== layout.children[i]) ? { ...layout, children } : layout
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts`

- [ ] **Step 5: useMultiHostEventWs 加入 session-closed 偵測**

在 `useMultiHostEventWs.ts` 的 `sessions` 事件 handler 中，在 `replaceHost` 之後加入：

```typescript
// session-closed detection: compare new list with open tabs
const newCodes = new Set(data.map((s: any) => s.code))
const { tabs } = useTabStore.getState()
for (const tab of Object.values(tabs)) {
  // Scan full pane tree for matching tmux-session panes
  scanPaneTree(tab.layout, (pane) => {
    const c = pane.content
    if (c.kind === 'tmux-session' && c.hostId === hostId && !c.terminated && !newCodes.has(c.sessionCode)) {
      useTabStore.getState().markTerminated(hostId, c.sessionCode, 'session-closed')
    }
  })
}
```

需要在 `pane-tree.ts` 加入 `scanPaneTree` helper：

```typescript
export function scanPaneTree(layout: PaneLayout, fn: (pane: Pane) => void): void {
  if (layout.type === 'leaf') {
    fn(layout.pane)
  } else {
    layout.children.forEach((child) => scanPaneTree(child, fn))
  }
}
```

- [ ] **Step 6: tmux-restarted 偵測（需 daemon 配合）**

在 `useMultiHostEventWs.ts` 的 `sessions` 事件 handler 中，daemon 端需在 sessions 事件 payload 附帶 `tmuxInstance`（`pid:startTime`）。SPA 端比對：

```typescript
// sessions event handler，在 session-closed 偵測之前
const incomingTmuxInstance = (rawEvent as any).tmuxInstance // daemon 新增欄位
if (incomingTmuxInstance) {
  const { tabs } = useTabStore.getState()
  for (const tab of Object.values(tabs)) {
    scanPaneTree(tab.layout, (pane) => {
      const c = pane.content
      if (c.kind === 'tmux-session' && c.hostId === hostId && !c.terminated
          && c.tmuxInstance && c.tmuxInstance !== incomingTmuxInstance) {
        useTabStore.getState().markHostTerminated(hostId, 'tmux-restarted')
      }
    })
  }
}
```

> 注：此步驟依賴 daemon 端修改 sessions 廣播格式。若 daemon 尚未支援，先跳過此步驟，Phase 4b 的 session-closed 偵測仍可獨立運作。

- [ ] **Step 7: 確認全部測試通過**

Run: `cd spa && npx vitest run`

- [ ] **Step 8: Commit**

```bash
git add spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.test.ts spa/src/hooks/useMultiHostEventWs.ts spa/src/lib/pane-tree.ts
git commit -m "feat: markTerminated actions + session-closed/tmux-restarted detection"
```

---

## Phase 4c — Host 層級錯誤 UI

### Task 11: useHostConnection hook

**Files:**
- Create: `spa/src/hooks/useHostConnection.ts`
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`
- Test: `spa/src/hooks/useHostConnection.test.ts`

- [ ] **Step 1: 在 HostRuntime 新增 manualRetry 欄位**

在 `useHostStore.ts` 的 `HostRuntime` interface 加入：

```typescript
export interface HostRuntime {
  status: 'connected' | 'disconnected' | 'reconnecting'
  latency?: number
  info?: HostInfo
  daemonState?: 'connected' | 'refused' | 'unreachable'
  tmuxState?: 'ok' | 'unavailable'
  manualRetry?: () => void  // 新增
}
```

- [ ] **Step 2: useMultiHostEventWs 將 SM trigger 存進 runtime**

在 `useMultiHostEventWs.ts` 中，建立 SM 後將 trigger 存進 runtime：

```typescript
const sm = new ConnectionStateMachine(...)
// SM 建立後
useHostStore.getState().setRuntime(hostId, { manualRetry: () => sm.trigger() })
```

- [ ] **Step 3: 建立 useHostConnection hook**

```typescript
// spa/src/hooks/useHostConnection.ts
import { useHostStore } from '../stores/useHostStore'

export function useHostConnection(hostId: string) {
  const runtime = useHostStore((s) => s.runtime[hostId])
  return {
    status: runtime?.status ?? 'disconnected',
    daemonState: runtime?.daemonState,
    tmuxState: runtime?.tmuxState,
    latency: runtime?.latency,
    manualRetry: runtime?.manualRetry ?? (() => {}),
  }
}
```

- [ ] **Step 4: 確認 lint + 測試通過**

Run: `cd spa && pnpm run lint && npx vitest run`

- [ ] **Step 5: Commit**

```bash
git add spa/src/hooks/useHostConnection.ts spa/src/hooks/useMultiHostEventWs.ts spa/src/stores/useHostStore.ts
git commit -m "feat: useHostConnection hook — SM access for manual retry"
```

---

### Task 12: Host 層級錯誤 UI — StatusBar / HostSidebar / SessionPanel / OverviewSection

**Files:**
- Modify: `spa/src/components/StatusBar.tsx`
- Modify: `spa/src/components/hosts/HostSidebar.tsx`
- Modify: `spa/src/components/hosts/OverviewSection.tsx`
- Modify: `spa/src/components/hosts/SessionsSection.tsx`（SessionPanel 行為）

- [ ] **Step 1: HostSidebar StatusIcon 加入 L3 黃色 Warning**

```typescript
// spa/src/components/hosts/HostSidebar.tsx — StatusIcon
function StatusIcon({ runtime }: { runtime?: HostRuntime }) {
  if (!runtime) return <Circle size={8} weight="fill" className="text-zinc-500" />
  if (runtime.status === 'connected' && runtime.tmuxState === 'unavailable')
    return <Warning size={12} weight="fill" className="text-yellow-400" />
  if (runtime.status === 'connected')
    return <Circle size={8} weight="fill" className="text-green-500" />
  if (runtime.status === 'reconnecting')
    return <Spinner size={12} className="text-yellow-400 animate-spin" />
  return <Circle size={8} weight="fill" className="text-red-400" />
}
```

- [ ] **Step 2: StatusBar 加入 L3 黃色 tmux unavailable**

在 StatusBar 的 host status 顯示區域，加入 tmuxState 判斷：

```typescript
const statusColor =
  runtime?.status === 'connected' && runtime?.tmuxState === 'unavailable'
    ? 'text-yellow-400'
    : runtime?.status === 'connected'
      ? 'text-green-500'
      : runtime?.status === 'reconnecting'
        ? 'text-yellow-400'
        : 'text-red-400'

const statusLabel =
  runtime?.status === 'connected' && runtime?.tmuxState === 'unavailable'
    ? t('hosts.error_tmux_down')
    : runtime?.status ?? 'disconnected'
```

- [ ] **Step 3: OverviewSection 加入 L1/L2/L3 錯誤訊息**

在 OverviewSection 的 connection status 區域加入條件訊息：

```typescript
function connectionErrorMessage(runtime?: HostRuntime): string | null {
  if (!runtime || runtime.status === 'connected') {
    if (runtime?.tmuxState === 'unavailable') return t('hosts.error_tmux_down')
    return null
  }
  if (runtime.daemonState === 'unreachable') return t('hosts.error_unreachable')
  if (runtime.daemonState === 'refused') return t('hosts.error_refused')
  return null
}
```

- [ ] **Step 4: SessionsSection disable 行為**

Host 斷線或 tmux down 時，session list 項目 disabled + 頂部錯誤訊息：

```typescript
const isOffline = !runtime || runtime.status !== 'connected' || runtime.tmuxState === 'unavailable'
// 在 session list 前顯示 error banner
{isOffline && (
  <div className="text-xs text-red-400 px-3 py-2">{connectionErrorMessage(runtime)}</div>
)}
// session 按鈕加入 disabled={isOffline}
```

- [ ] **Step 5: 確認 lint + 測試通過**

Run: `cd spa && pnpm run lint && npx vitest run`

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/StatusBar.tsx spa/src/components/hosts/HostSidebar.tsx spa/src/components/hosts/OverviewSection.tsx spa/src/components/hosts/SessionsSection.tsx
git commit -m "feat: host-level L1/L2/L3 error UI across StatusBar, Sidebar, Overview, Sessions"
```

---

### Task 13: Reconnecting overlay 手動重連按鈕

**Files:**
- 找到現有的 reconnecting overlay 元件並修改（可能在 `SessionPaneContent.tsx` 或 terminal/stream view 中）

- [ ] **Step 1: 找到 reconnecting overlay 的位置**

Run: `cd spa && grep -rn 'reconnect' src/components/ --include='*.tsx' | head -20`

- [ ] **Step 2: 在 overlay 加入手動重連按鈕**

```typescript
import { useHostConnection } from '../hooks/useHostConnection'

// 在 overlay 元件中
const { manualRetry, status } = useHostConnection(hostId)
const [retrying, setRetrying] = useState(false)

const handleRetry = () => {
  setRetrying(true)
  manualRetry()
}

// 當 status 改變時重置 retrying
useEffect(() => { setRetrying(false) }, [status])

// 在 overlay UI 中加入按鈕
<button
  className="mt-4 px-4 py-2 rounded bg-zinc-700 hover:bg-zinc-600 text-sm"
  onClick={handleRetry}
  disabled={retrying}
>
  {retrying ? <Spinner size={16} className="animate-spin" /> : t('connection.reconnect')}
</button>
```

- [ ] **Step 3: 確認 lint + 測試通過**

Run: `cd spa && pnpm run lint && npx vitest run`

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat: manual reconnect button in reconnecting overlay"
```

---

## Phase 4d — Host 刪除流程 + Cascade Cleanup

### Task 14: AgentStore.removeHost + StreamStore.clearHost

**Files:**
- Modify: `spa/src/stores/useAgentStore.ts`
- Modify: `spa/src/stores/useStreamStore.ts`
- Test: existing test files or new

- [ ] **Step 1: AgentStore.removeHost**

新增 `removeHost(hostId)` action，掃描所有 `hostId:*` composite key，清除 `events`、`statuses`、`unread`、`activeSubagents`。同時移除舊的 `clearSubagentsForHost`。

```typescript
removeHost: (hostId: string) =>
  set((state) => {
    const prefix = `${hostId}:`
    const filterKeys = <T,>(record: Record<string, T>): Record<string, T> => {
      const result: Record<string, T> = {}
      for (const [k, v] of Object.entries(record)) {
        if (!k.startsWith(prefix)) result[k] = v
      }
      return result
    }
    return {
      events: filterKeys(state.events),
      statuses: filterKeys(state.statuses),
      unread: filterKeys(state.unread),
      activeSubagents: filterKeys(state.activeSubagents),
    }
  }),
```

更新所有呼叫 `clearSubagentsForHost` 的地方改用 `removeHost`（或在連線建立時只清 subagents，視情況保留特化版本）。

- [ ] **Step 2: StreamStore.clearHost**

新增 `clearHost(hostId)` action：

```typescript
clearHost: (hostId: string) =>
  set((state) => {
    const prefix = `${hostId}:`
    const newSessions: Record<string, any> = {}
    for (const [k, v] of Object.entries(state.sessions)) {
      if (k.startsWith(prefix)) {
        v.conn?.close()
      } else {
        newSessions[k] = v
      }
    }
    const filterKeys = <T,>(record: Record<string, T>): Record<string, T> => {
      const result: Record<string, T> = {}
      for (const [k, v] of Object.entries(record)) {
        if (!k.startsWith(prefix)) result[k] = v
      }
      return result
    }
    return {
      sessions: newSessions,
      relayStatus: filterKeys(state.relayStatus),
      handoffProgress: filterKeys(state.handoffProgress),
    }
  }),
```

- [ ] **Step 3: 確認 lint + 測試通過**

Run: `cd spa && pnpm run lint && npx vitest run`

- [ ] **Step 4: Commit**

```bash
git add spa/src/stores/useAgentStore.ts spa/src/stores/useStreamStore.ts
git commit -m "feat: AgentStore.removeHost + StreamStore.clearHost for cascade cleanup"
```

---

### Task 15: Host 刪除 cascade + 確認 UI + undo toast

**Files:**
- Modify: `spa/src/components/hosts/OverviewSection.tsx`
- Modify: `spa/src/stores/useHostStore.ts`
- Create: `spa/src/components/UndoToast.tsx`（或 inline）

- [ ] **Step 1: OverviewSection 刪除確認 UI 加 checkbox**

替換現有的確認 UI：

```typescript
{confirmDelete && (
  <div className="mt-3 p-3 bg-red-500/10 border border-red-500/30 rounded">
    <p className="text-xs text-red-400 mb-2">{t('hosts.confirm_delete')}</p>
    <label className="flex items-center gap-2 text-xs text-zinc-400 mb-3">
      <input
        type="checkbox"
        checked={closeTabs}
        onChange={(e) => setCloseTabs(e.target.checked)}
      />
      {t('hosts.confirm_delete_tabs')}
    </label>
    <div className="flex gap-2">
      <button onClick={() => handleDeleteHost(closeTabs)} className="...">
        {t('common.delete')}
      </button>
      <button onClick={() => setConfirmDelete(false)} className="...">
        {t('common.cancel')}
      </button>
    </div>
  </div>
)}
```

- [ ] **Step 2: 實作 cascade delete + undo**

```typescript
const handleDeleteHost = (withCloseTabs: boolean) => {
  const hostStore = useHostStore.getState()
  const tabStore = useTabStore.getState()
  const sessionStore = useSessionStore.getState()
  const agentStore = useAgentStore.getState()
  const streamStore = useStreamStore.getState()

  // Snapshot for undo
  const snapshot = {
    host: hostStore.hosts[hostId],
    hostOrder: [...hostStore.hostOrder],
    runtime: hostStore.runtime[hostId],
    sessions: sessionStore.sessions[hostId],
    // AgentStore + StreamStore: snapshot composite key entries
    agentEvents: pickByPrefix(agentStore.events, `${hostId}:`),
    agentStatuses: pickByPrefix(agentStore.statuses, `${hostId}:`),
    agentUnread: pickByPrefix(agentStore.unread, `${hostId}:`),
    agentSubagents: pickByPrefix(agentStore.activeSubagents, `${hostId}:`),
    // Tabs
    affectedTabs: withCloseTabs
      ? findTabsForHost(tabStore, hostId)
      : null,
    tabsState: withCloseTabs
      ? { tabs: { ...tabStore.tabs }, tabOrder: [...tabStore.tabOrder], activeTabId: tabStore.activeTabId }
      : null,
  }

  // Execute cascade
  if (withCloseTabs) {
    closeTabsForHost(tabStore, hostId)
  } else {
    tabStore.markHostTerminated(hostId, 'host-removed')
  }
  sessionStore.removeHost(hostId)
  agentStore.removeHost(hostId)
  streamStore.clearHost(hostId)
  hostStore.removeHost(hostId)

  // Show undo toast
  showUndoToast(hostStore.hosts[hostId]?.name ?? hostId, () => {
    // Restore from snapshot
    // ... restore logic
  })
}
```

- [ ] **Step 3: Undo toast 元件**

簡單的底部 fixed toast，5 秒自動消失 + undo 按鈕。可以用 state 管理或 portal。

- [ ] **Step 4: 確認 lint + 測試通過**

Run: `cd spa && pnpm run lint && npx vitest run`

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat: host deletion cascade cleanup + undo toast + checkbox UI"
```

---

## Phase 4e — 通知 + Bug fixes

### Task 16: HEALTH_TIMEOUT_MS 調整

**Files:**
- Modify: `spa/src/lib/host-connection.ts:13`

- [ ] **Step 1: 改 timeout**

```typescript
const HEALTH_TIMEOUT_MS = 6000  // was 3000
```

- [ ] **Step 2: 更新相關測試**

如果有測試 mock 了 3000ms timeout，改為 6000ms。

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/host-connection.ts
git commit -m "fix: increase health check timeout from 3s to 6s for unstable networks"
```

---

### Task 17: NotificationAction 模組化 + L2/L3 通知

**Files:**
- Modify: `spa/src/hooks/useNotificationDispatcher.ts`

- [ ] **Step 1: 定義 NotificationAction 類型**

```typescript
export type NotificationAction =
  | { kind: 'open-session'; hostId: string; sessionCode: string }
  | { kind: 'open-host'; hostId: string }
```

- [ ] **Step 2: 重構 handleNotificationClick 為 action dispatch**

```typescript
function handleNotificationClick(action: NotificationAction) {
  switch (action.kind) {
    case 'open-session': {
      // 現有邏輯：找 tab or 開新 tab
      break
    }
    case 'open-host': {
      useTabStore.getState().openSingletonTab({ kind: 'hosts' })
      useHostStore.getState().setActiveHost(action.hostId)
      break
    }
  }
}
```

- [ ] **Step 3: 加入 L2/L3 連線通知**

在 hook 中監聽 `HostRuntime.daemonState` 和 `tmuxState` 變化：

```typescript
// Track previous state per host for edge detection
const prevState = useRef<Record<string, { daemon?: string; tmux?: string }>>({})

// On runtime change:
for (const hostId of hostOrder) {
  const rt = runtime[hostId]
  const prev = prevState.current[hostId]

  // L2: daemon refused (was connected, now refused)
  if (prev?.daemon === 'connected' && rt?.daemonState === 'refused') {
    notify(t('notification.daemon_refused', { name: hosts[hostId]?.name }),
           { kind: 'open-host', hostId })
  }
  // L3: tmux down (was ok, now unavailable)
  if (prev?.tmux === 'ok' && rt?.tmuxState === 'unavailable') {
    notify(t('notification.tmux_down', { name: hosts[hostId]?.name }),
           { kind: 'open-host', hostId })
  }

  prevState.current[hostId] = { daemon: rt?.daemonState, tmux: rt?.tmuxState }
}
```

- [ ] **Step 4: 確認 lint + 測試通過**

Run: `cd spa && pnpm run lint && npx vitest run`

- [ ] **Step 5: Commit**

```bash
git add spa/src/hooks/useNotificationDispatcher.ts
git commit -m "feat: NotificationAction modular click handler + L2/L3 connection notifications"
```

---

### Task 18: Bug fix #161 — JSON parse error

**Files:**
- Modify: `spa/src/hooks/useMultiHostEventWs.ts`

- [ ] **Step 1: Wrap JSON.parse in try-catch**

在 `useMultiHostEventWs.ts` 中所有 `JSON.parse(event.value)` 呼叫加 try-catch：

```typescript
// sessions event handler
try {
  const data: Session[] = JSON.parse(event.value)
  // ... existing logic
} catch (e) {
  console.warn(`[host-events] Failed to parse sessions payload for ${hostId}:`, e)
}
```

同樣處理其他 `JSON.parse` 呼叫點。

- [ ] **Step 2: Commit**

```bash
git add spa/src/hooks/useMultiHostEventWs.ts
git commit -m "fix(#161): wrap JSON.parse in try-catch for WS event payloads"
```

---

### Task 19: Bug fix #156 — AddHostDialog 錯誤提示

**Files:**
- Modify: `spa/src/components/hosts/AddHostDialog.tsx`

- [ ] **Step 1: 區分錯誤類型**

在 `handleTest` 的 catch block 中，根據 error 類型顯示不同訊息：

```typescript
try {
  const healthRes = await fetch(...)
  // ...
} catch (err) {
  if (err instanceof TypeError) {
    // Connection refused → L2
    setError(t('hosts.error_refused'))
  } else if (err instanceof DOMException && err.name === 'AbortError') {
    // Timeout → L1
    setError(t('hosts.error_unreachable'))
  } else {
    setError(String(err))
  }
  setStage('error')
}
```

對於 401 回應：
```typescript
if (sessionsRes.status === 401) {
  setStage('needs-token')
  setError('')  // 清除之前的錯誤
}
```

- [ ] **Step 2: Commit**

```bash
git add spa/src/components/hosts/AddHostDialog.tsx
git commit -m "fix(#156): AddHostDialog shows specific L1/L2/401 error messages"
```

---

### Task 20: Bug fix #140 — TokenField 空值驗證

**Files:**
- Modify: `spa/src/components/hosts/OverviewSection.tsx`

- [ ] **Step 1: 空值不觸發驗證**

在 TokenField 的 `handleSave` 中：

```typescript
const handleSave = async () => {
  if (!draft.trim()) {
    // 空值：直接儲存空 token，清除錯誤
    setError('')
    onSave(draft)
    setEditing(false)
    return
  }
  // 原有驗證邏輯...
}
```

- [ ] **Step 2: Commit**

```bash
git add spa/src/components/hosts/OverviewSection.tsx
git commit -m "fix(#140): TokenField skips validation on empty value"
```

---

### Task 21: Bug fix #137 — Electron 離線白屏

**Files:**
- 確認 `SessionPaneContent.tsx` 的 terminated guard + reconnecting overlay 已覆蓋此情境
- 可能需要在 Electron main process 或 renderer entry point 加入 fallback

- [ ] **Step 1: 確認 L1 狀態不會白屏**

驗證：daemon 不可達時，`HostRuntime.status` 為 `disconnected`，`SessionPaneContent` 不會 crash（因為有 reconnecting overlay 或 WS 連不上）。如果 Electron 啟動時 renderer 本身就 crash，需要在 Electron main process 加入 error handler。

- [ ] **Step 2: 如需修改，commit**

```bash
git add -A && git commit -m "fix(#137): prevent Electron renderer crash on offline startup"
```

---

### Task 22: 最終驗證 + Phase 4 完成

- [ ] **Step 1: 執行全部測試**

Run: `cd spa && npx vitest run`
Expected: 全部通過

- [ ] **Step 2: 執行 lint**

Run: `cd spa && pnpm run lint`
Expected: 無錯誤

- [ ] **Step 3: 執行 build**

Run: `cd spa && pnpm run build`
Expected: 建置成功

- [ ] **Step 4: 確認 i18n locale completeness**

Run: `cd spa && npx vitest run src/locales/locale-completeness.test.ts`
Expected: PASS
