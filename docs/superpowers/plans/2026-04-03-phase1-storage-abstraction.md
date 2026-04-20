# Phase 1a: Storage 抽象層 + Key 遷移

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 StorageBackend 抽象層，實作 BrowserBackend（localStorage + BroadcastChannel 跨 tab 同步），並將所有 store key 從 `tbox-*` 遷移至 `purdex-*`。

**Architecture:** 以 Zustand `StateStorage` 介面為基礎建立 `browserStorage` 實作，搭配 BroadcastChannel 實現跨 tab 狀態同步。所有 10 個 persist store 統一使用新的 storage backend 與 `purdex-*` key。舊 `tbox-*` key 直接遺棄（alpha 階段不向下相容）。ElectronBackend（IPC hub + safeStorage）延至 Phase 1b 實作。

**Tech Stack:** Zustand 5 persist middleware / `createJSONStorage` / BroadcastChannel API / Vitest

**Scope note:** Phase 1b（ElectronBackend + safeStorage + 多視窗同步）將在本 PR merge 後另行規劃。本計畫產出獨立可用——瀏覽器環境完整支援，Electron 環境 fallback 至 localStorage（與現行行為相同）。

---

## 檔案結構

```
spa/src/lib/storage/          ← 新目錄
├── keys.ts                   — STORAGE_KEYS 常數（所有 key 的 single source of truth）
├── sync.ts                   — BroadcastChannel 跨 tab 同步管理器
├── browser-backend.ts        — StateStorage 實作（localStorage + sync 通知）
├── index.ts                  — barrel：purdexStorage, STORAGE_KEYS, registerStore
└── __tests__/
    ├── sync.test.ts
    └── browser-backend.test.ts

spa/src/stores/               ← 修改 10 個 persist store
├── useHostStore.ts           — key + storage + 移除 migrate
├── useSessionStore.ts        — key + storage + 移除 migrate + 重設 version
├── useTabStore.ts            — key + storage + 移除 migrate + addHostIdToLayout + 重設 version
├── useAgentStore.ts          — key + storage + 加 version
├── useNotificationSettingsStore.ts — key + storage + 加 version
├── useI18nStore.ts           — key + storage + 加 version
├── useThemeStore.ts          — key + storage + 加 version
├── useUISettingsStore.ts     — key + storage + 加 version
├── useWorkspaceStore.ts      — key + storage
└── useHistoryStore.ts        — key + storage

spa/src/hooks/
└── useNotificationDispatcher.ts — SEEN_KEY 改用 STORAGE_KEYS

spa/src/stores/useSessionStore.test.ts      — 更新 key 引用
spa/src/hooks/useNotificationDispatcher.test.ts — 更新 key 引用
```

---

### Task 1: Storage key 常數

**Files:**
- Create: `spa/src/lib/storage/keys.ts`

- [ ] **Step 1: 建立 key 常數檔**

```typescript
// spa/src/lib/storage/keys.ts

/** 所有 localStorage key 名稱 — single source of truth */
export const STORAGE_KEYS = {
  TABS: 'purdex-tabs',
  HOSTS: 'purdex-hosts',
  SESSIONS: 'purdex-sessions',
  AGENT: 'purdex-agent',
  WORKSPACES: 'purdex-workspaces',
  HISTORY: 'purdex-history',
  I18N: 'purdex-i18n',
  THEMES: 'purdex-themes',
  UI_SETTINGS: 'purdex-ui-settings',
  NOTIFICATION_SETTINGS: 'purdex-notification-settings',
  NOTIFICATION_SEEN: 'purdex-notification-seen',
} as const
```

- [ ] **Step 2: Commit**

```bash
git add spa/src/lib/storage/keys.ts
git commit -m "feat(storage): add STORAGE_KEYS constants for purdex-* key names"
```

---

### Task 2: Sync 模組（TDD）

**Files:**
- Create: `spa/src/lib/storage/__tests__/sync.test.ts`
- Create: `spa/src/lib/storage/sync.ts`

- [ ] **Step 1: 寫 sync 測試**

```typescript
// spa/src/lib/storage/__tests__/sync.test.ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

let instances: Array<{
  name: string
  onmessage: ((event: MessageEvent) => void) | null
  postMessage: ReturnType<typeof vi.fn>
  close: ReturnType<typeof vi.fn>
}>

class MockBroadcastChannel {
  name: string
  onmessage: ((event: MessageEvent) => void) | null = null
  postMessage = vi.fn()
  close = vi.fn()
  constructor(name: string) {
    this.name = name
    instances.push(this)
  }
}

beforeEach(() => {
  instances = []
  vi.stubGlobal('BroadcastChannel', MockBroadcastChannel)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.resetModules()
})

describe('createSyncManager', () => {
  it('register creates BroadcastChannel with correct name', async () => {
    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }

    manager.register('purdex-tabs', store)

    expect(instances).toHaveLength(1)
    expect(instances[0].name).toBe('purdex-sync')
    manager.destroy()
  })

  it('notify posts key to channel', async () => {
    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }
    manager.register('purdex-tabs', store)

    manager.notify('purdex-tabs')

    expect(instances[0].postMessage).toHaveBeenCalledWith({ key: 'purdex-tabs' })
    manager.destroy()
  })

  it('incoming message triggers rehydrate on matching store', async () => {
    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }
    manager.register('purdex-tabs', store)

    // Simulate message from another tab
    instances[0].onmessage!({ data: { key: 'purdex-tabs' } } as MessageEvent)

    expect(store.persist.rehydrate).toHaveBeenCalledOnce()
    manager.destroy()
  })

  it('incoming message for unregistered key does nothing', async () => {
    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }
    manager.register('purdex-tabs', store)

    instances[0].onmessage!({ data: { key: 'purdex-unknown' } } as MessageEvent)

    expect(store.persist.rehydrate).not.toHaveBeenCalled()
    manager.destroy()
  })

  it('gracefully handles missing BroadcastChannel', async () => {
    vi.unstubAllGlobals()
    // BroadcastChannel undefined (e.g., old browser, SSR)
    vi.stubGlobal('BroadcastChannel', undefined)

    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }

    // Should not throw
    manager.register('purdex-tabs', store)
    manager.notify('purdex-tabs')
    manager.destroy()
  })

  it('destroy closes channel and clears registry', async () => {
    const { createSyncManager } = await import('../sync')
    const manager = createSyncManager()
    const store = { persist: { rehydrate: vi.fn() } }
    manager.register('purdex-tabs', store)

    manager.destroy()

    expect(instances[0].close).toHaveBeenCalledOnce()
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/lib/storage/__tests__/sync.test.ts`
Expected: FAIL — module `../sync` not found

- [ ] **Step 3: 實作 sync 模組**

```typescript
// spa/src/lib/storage/sync.ts

type SyncableStore = {
  persist: { rehydrate: () => void | Promise<void> }
}

const CHANNEL_NAME = 'purdex-sync'

export function createSyncManager() {
  const registry = new Map<string, SyncableStore>()
  let channel: BroadcastChannel | null = null

  function ensureChannel(): BroadcastChannel | null {
    if (channel) return channel
    if (typeof BroadcastChannel === 'undefined') return null
    channel = new BroadcastChannel(CHANNEL_NAME)
    channel.onmessage = (event: MessageEvent<{ key: string }>) => {
      registry.get(event.data.key)?.persist.rehydrate()
    }
    return channel
  }

  return {
    register(key: string, store: SyncableStore) {
      registry.set(key, store)
      ensureChannel()
    },
    notify(key: string) {
      ensureChannel()?.postMessage({ key })
    },
    destroy() {
      channel?.close()
      channel = null
      registry.clear()
    },
  }
}

/** Default singleton — 生產用 */
export const syncManager = createSyncManager()
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/lib/storage/__tests__/sync.test.ts`
Expected: All 6 tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/storage/sync.ts spa/src/lib/storage/__tests__/sync.test.ts
git commit -m "feat(storage): add BroadcastChannel sync manager with TDD"
```

---

### Task 3: BrowserBackend（TDD）

**Files:**
- Create: `spa/src/lib/storage/__tests__/browser-backend.test.ts`
- Create: `spa/src/lib/storage/browser-backend.ts`

- [ ] **Step 1: 寫 BrowserBackend 測試**

```typescript
// spa/src/lib/storage/__tests__/browser-backend.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'

// Mock sync module before importing browser-backend
vi.mock('../sync', () => ({
  syncManager: { notify: vi.fn(), register: vi.fn(), destroy: vi.fn() },
}))

import { browserStorage } from '../browser-backend'
import { syncManager } from '../sync'

beforeEach(() => {
  localStorage.clear()
  vi.clearAllMocks()
})

describe('browserStorage', () => {
  it('getItem returns null for missing key', () => {
    expect(browserStorage.getItem('missing')).toBeNull()
  })

  it('setItem stores value in localStorage', () => {
    browserStorage.setItem('key', '"value"')
    expect(localStorage.getItem('key')).toBe('"value"')
  })

  it('setItem notifies sync manager', () => {
    browserStorage.setItem('purdex-tabs', '{}')
    expect(syncManager.notify).toHaveBeenCalledWith('purdex-tabs')
  })

  it('getItem retrieves stored value', () => {
    localStorage.setItem('key', '"hello"')
    expect(browserStorage.getItem('key')).toBe('"hello"')
  })

  it('removeItem deletes from localStorage', () => {
    localStorage.setItem('key', '"value"')
    browserStorage.removeItem('key')
    expect(localStorage.getItem('key')).toBeNull()
  })

  it('removeItem notifies sync manager', () => {
    browserStorage.removeItem('purdex-tabs')
    expect(syncManager.notify).toHaveBeenCalledWith('purdex-tabs')
  })
})
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd spa && npx vitest run src/lib/storage/__tests__/browser-backend.test.ts`
Expected: FAIL — module `../browser-backend` not found

- [ ] **Step 3: 實作 BrowserBackend**

```typescript
// spa/src/lib/storage/browser-backend.ts
import type { StateStorage } from 'zustand/middleware'
import { syncManager } from './sync'

export const browserStorage: StateStorage = {
  getItem(name: string) {
    return localStorage.getItem(name)
  },
  setItem(name: string, value: string) {
    localStorage.setItem(name, value)
    syncManager.notify(name)
  },
  removeItem(name: string) {
    localStorage.removeItem(name)
    syncManager.notify(name)
  },
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd spa && npx vitest run src/lib/storage/__tests__/browser-backend.test.ts`
Expected: All 6 tests PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/storage/browser-backend.ts spa/src/lib/storage/__tests__/browser-backend.test.ts
git commit -m "feat(storage): add BrowserBackend wrapping localStorage + sync"
```

---

### Task 4: Storage barrel export

**Files:**
- Create: `spa/src/lib/storage/index.ts`

- [ ] **Step 1: 建立 barrel export**

```typescript
// spa/src/lib/storage/index.ts
import { createJSONStorage } from 'zustand/middleware'
import { browserStorage } from './browser-backend'

export { STORAGE_KEYS } from './keys'
export { syncManager } from './sync'

/**
 * Zustand persist storage backend.
 * Phase 1a: BrowserBackend（localStorage + BroadcastChannel）
 * Phase 1b: 將加入 ElectronBackend 自動偵測
 */
export const purdexStorage = createJSONStorage(() => browserStorage)
```

- [ ] **Step 2: 執行全部 storage 測試確認通過**

Run: `cd spa && npx vitest run src/lib/storage/`
Expected: All 12 tests PASS

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/storage/index.ts
git commit -m "feat(storage): add barrel export with purdexStorage factory"
```

---

### Task 5: 遷移有 migrate 的 store（useHostStore, useSessionStore, useTabStore）

**Files:**
- Modify: `spa/src/stores/useHostStore.ts:155-186` — persist config
- Modify: `spa/src/stores/useSessionStore.ts:43-68` — persist config
- Modify: `spa/src/stores/useTabStore.ts:27-35,148-170` — addHostIdToLayout + persist config

- [ ] **Step 1: 遷移 useHostStore**

`spa/src/stores/useHostStore.ts` — 移除 `migrate`，更新 persist config：

```typescript
// 加入 import（檔案頂部）
import { purdexStorage, STORAGE_KEYS, syncManager } from '@/lib/storage'
```

將整個 persist config 替換為：

```typescript
{
  name: STORAGE_KEYS.HOSTS,
  storage: purdexStorage,
  version: 1,
  partialize: (state) => ({
    hosts: state.hosts,
    hostOrder: state.hostOrder,
    activeHostId: state.activeHostId,
  }),
},
```

移除舊的 `migrate` 函式。在 store 建立後加入 sync 註冊：

```typescript
// 檔案末尾，export 後
syncManager.register(STORAGE_KEYS.HOSTS, useHostStore)
```

- [ ] **Step 2: 遷移 useSessionStore**

`spa/src/stores/useSessionStore.ts` — 移除 `migrate`，version 重設為 1：

```typescript
// 加入 import
import { purdexStorage, STORAGE_KEYS, syncManager } from '@/lib/storage'
```

將 persist config 替換為：

```typescript
{
  name: STORAGE_KEYS.SESSIONS,
  storage: purdexStorage,
  version: 1,
  partialize: (state) => ({
    activeHostId: state.activeHostId,
    activeCode: state.activeCode,
  }),
},
```

移除舊的 `migrate` 函式（含 `useHostStore.getState()` 跨 store 引用）。如果 `useHostStore` import 僅被 migrate 使用，一併移除該 import。

在 store 建立後加入：

```typescript
syncManager.register(STORAGE_KEYS.SESSIONS, useSessionStore)
```

- [ ] **Step 3: 遷移 useTabStore**

`spa/src/stores/useTabStore.ts` — 移除 `addHostIdToLayout` 函式（lines 27-35）和 `migrate`，version 重設為 1：

```typescript
// 加入 import
import { purdexStorage, STORAGE_KEYS, syncManager } from '@/lib/storage'
```

**刪除** `addHostIdToLayout` 函式（lines 27-35）。

**刪除** persist config 中的 `migrate` 函式。如果 `useHostStore` import 僅被 migrate 使用，一併移除。

將 persist config 替換為：

```typescript
{
  name: STORAGE_KEYS.TABS,
  storage: purdexStorage,
  version: 1,
  partialize: (state) => ({
    tabs: state.tabs,
    tabOrder: state.tabOrder,
    activeTabId: state.activeTabId,
  }),
},
```

在 store 建立後加入：

```typescript
syncManager.register(STORAGE_KEYS.TABS, useTabStore)
```

- [ ] **Step 4: 執行相關測試**

Run: `cd spa && npx vitest run src/stores/useHostStore.test.ts src/stores/useSessionStore.test.ts src/stores/useTabStore.test.ts`
Expected: 可能有 2 個 test 失敗（useSessionStore.test.ts 引用舊 key）— 下個 Task 處理

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useHostStore.ts spa/src/stores/useSessionStore.ts spa/src/stores/useTabStore.ts
git commit -m "refactor(stores): migrate host/session/tab to purdex-* keys + remove dead migrations"
```

---

### Task 6: 遷移剩餘 7 個 persist store

**Files:**
- Modify: `spa/src/stores/useAgentStore.ts:192-195`
- Modify: `spa/src/stores/useNotificationSettingsStore.ts:47`
- Modify: `spa/src/stores/useWorkspaceStore.ts:89-96`
- Modify: `spa/src/stores/useHistoryStore.ts:97-100`
- Modify: `spa/src/stores/useI18nStore.ts:105-126`
- Modify: `spa/src/stores/useThemeStore.ts:105-122`
- Modify: `spa/src/stores/useUISettingsStore.ts:56`

所有 7 個 store 統一修改模式：

1. 加入 `import { purdexStorage, STORAGE_KEYS, syncManager } from '@/lib/storage'`
2. `name` 改為對應的 `STORAGE_KEYS.*`
3. 加入 `storage: purdexStorage`
4. 無 `version` 的加入 `version: 1`
5. 保留 `partialize`、`onRehydrateStorage` 不動
6. 檔案末尾加 `syncManager.register(STORAGE_KEYS.*, useXxxStore)`

- [ ] **Step 1: 遷移 useAgentStore**

```typescript
// persist config 改為：
{
  name: STORAGE_KEYS.AGENT,
  storage: purdexStorage,
  version: 1,
  partialize: (state) => ({ tabIndicatorStyle: state.tabIndicatorStyle }),
}

// 檔案末尾：
syncManager.register(STORAGE_KEYS.AGENT, useAgentStore)
```

- [ ] **Step 2: 遷移 useNotificationSettingsStore**

```typescript
// persist config 改為：
{
  name: STORAGE_KEYS.NOTIFICATION_SETTINGS,
  storage: purdexStorage,
  version: 1,
  partialize: (state) => ({ agents: state.agents }),
}

// 檔案末尾：
syncManager.register(STORAGE_KEYS.NOTIFICATION_SETTINGS, useNotificationSettingsStore)
```

- [ ] **Step 3: 遷移 useWorkspaceStore**

```typescript
// persist config 改為：
{
  name: STORAGE_KEYS.WORKSPACES,
  storage: purdexStorage,
  version: 1,
  partialize: (state) => ({
    workspaces: state.workspaces,
    activeWorkspaceId: state.activeWorkspaceId,
  }),
}

// 檔案末尾：
syncManager.register(STORAGE_KEYS.WORKSPACES, useWorkspaceStore)
```

- [ ] **Step 4: 遷移 useHistoryStore**

```typescript
// persist config 改為：
{
  name: STORAGE_KEYS.HISTORY,
  storage: purdexStorage,
  version: 1,
}

// 檔案末尾：
syncManager.register(STORAGE_KEYS.HISTORY, useHistoryStore)
```

- [ ] **Step 5: 遷移 useI18nStore**

```typescript
// persist config 改為（保留 partialize + onRehydrateStorage）：
{
  name: STORAGE_KEYS.I18N,
  storage: purdexStorage,
  version: 1,
  partialize: (state) => ({
    activeLocaleId: state.activeLocaleId,
    customLocales: state.customLocales,
  }),
  onRehydrateStorage: () => (state) => {
    // ... 保持現有內容不變 ...
  },
}

// 檔案末尾：
syncManager.register(STORAGE_KEYS.I18N, useI18nStore)
```

- [ ] **Step 6: 遷移 useThemeStore**

```typescript
// persist config 改為（保留 partialize + onRehydrateStorage）：
{
  name: STORAGE_KEYS.THEMES,
  storage: purdexStorage,
  version: 1,
  partialize: (state) => ({
    activeThemeId: state.activeThemeId,
    customThemes: state.customThemes,
  }),
  onRehydrateStorage: () => (state) => {
    // ... 保持現有內容不變 ...
  },
}

// 檔案末尾：
syncManager.register(STORAGE_KEYS.THEMES, useThemeStore)
```

- [ ] **Step 7: 遷移 useUISettingsStore**

```typescript
// persist config 改為：
{
  name: STORAGE_KEYS.UI_SETTINGS,
  storage: purdexStorage,
  version: 1,
}

// 檔案末尾：
syncManager.register(STORAGE_KEYS.UI_SETTINGS, useUISettingsStore)
```

- [ ] **Step 8: Commit**

```bash
git add spa/src/stores/useAgentStore.ts spa/src/stores/useNotificationSettingsStore.ts \
  spa/src/stores/useWorkspaceStore.ts spa/src/stores/useHistoryStore.ts \
  spa/src/stores/useI18nStore.ts spa/src/stores/useThemeStore.ts \
  spa/src/stores/useUISettingsStore.ts
git commit -m "refactor(stores): migrate 7 remaining stores to purdex-* keys"
```

---

### Task 7: 遷移 notification-seen key

**Files:**
- Modify: `spa/src/hooks/useNotificationDispatcher.ts:17`

- [ ] **Step 1: 更新 SEEN_KEY**

```typescript
// 移除舊的常數：
// const SEEN_KEY = 'tbox-notification-seen'

// 改為 import：
import { STORAGE_KEYS } from '@/lib/storage'

// 所有 SEEN_KEY 用法改為 STORAGE_KEYS.NOTIFICATION_SEEN
```

搜尋檔案中所有 `SEEN_KEY` 引用，替換為 `STORAGE_KEYS.NOTIFICATION_SEEN`。

- [ ] **Step 2: Commit**

```bash
git add spa/src/hooks/useNotificationDispatcher.ts
git commit -m "refactor: migrate notification-seen key to STORAGE_KEYS constant"
```

---

### Task 8: 更新測試中的舊 key 引用

**Files:**
- Modify: `spa/src/stores/useSessionStore.test.ts:110,120`
- Modify: `spa/src/hooks/useNotificationDispatcher.test.ts:57`

- [ ] **Step 1: 更新 useSessionStore.test.ts**

```typescript
// 加入 import
import { STORAGE_KEYS } from '@/lib/storage'

// Line 110: 改為
const stored = JSON.parse(localStorage.getItem(STORAGE_KEYS.SESSIONS) || '{}')

// Line 120: 改為
const stored = JSON.parse(localStorage.getItem(STORAGE_KEYS.SESSIONS) || '{}')
```

- [ ] **Step 2: 更新 useNotificationDispatcher.test.ts**

```typescript
// 加入 import
import { STORAGE_KEYS } from '@/lib/storage'

// Line 57: 改為
localStorage.removeItem(STORAGE_KEYS.NOTIFICATION_SEEN)
```

- [ ] **Step 3: Commit**

```bash
git add spa/src/stores/useSessionStore.test.ts spa/src/hooks/useNotificationDispatcher.test.ts
git commit -m "test: update test references from tbox-* to STORAGE_KEYS constants"
```

---

### Task 9: 全面驗證

- [ ] **Step 1: 執行完整測試**

Run: `cd spa && npx vitest run`
Expected: All tests PASS

- [ ] **Step 2: Lint 檢查**

Run: `cd spa && pnpm run lint`
Expected: No errors

- [ ] **Step 3: Build 檢查**

Run: `cd spa && pnpm run build`
Expected: Build successful

- [ ] **Step 4: 確認無殘留 tbox- 引用**

Run: `grep -r "tbox-" spa/src/ --include="*.ts" --include="*.tsx" -l`
Expected: No files — 所有 `tbox-` 引用已遷移

- [ ] **Step 5: Commit（如有修正）**

---

## Phase 1b 延後項目

以下內容不在本 PR 範圍，將在 Phase 1a merge 後另行規劃：

| 項目 | 說明 |
|------|------|
| `electron/storage-hub.ts` | Main process 狀態管理 hub |
| IPC handlers | `storage:get` / `storage:set` / `storage:remove` / `storage:sync` |
| preload 擴充 | `storageGet` / `storageSet` / `storageRemove` / `onStorageSync` |
| `electron-backend.ts` | Renderer 側 IPC wrapper（StateStorage 實作） |
| safeStorage | Token 加密存儲（Phase 5 前置） |
| `index.ts` 更新 | 平台偵測自動選擇 backend |

Phase 1b 完成後，`purdexStorage` factory 將自動偵測 Electron 環境並切換到 `ElectronBackend`，SPA store 程式碼不需再改動。
