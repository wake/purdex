# Tab UX 改善 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 改善 tab 系統的 8 項 UX：rename session、new tab 頁面優化、URL 歷史、鍵盤導航、瀏覽紀錄回退、focus 保持

**Architecture:** 所有變更集中在 SPA 端（`spa/src/`）。Tab store 新增 visitHistory 追蹤瀏覽順序；新增 browser history store 持久化 URL 紀錄；新增 RenamePopover 元件；BrowserNewTabSection 改為帶 dropdown 的輸入欄位；SessionSection 加鍵盤導航。

**Tech Stack:** React 19 / Zustand 5 / Tailwind 4 / Vitest / Phosphor Icons

---

## File Map

| 動作 | 檔案 | 職責 |
|------|------|------|
| Create | `spa/src/components/RenamePopover.tsx` | Tab 下方 inline rename input |
| Create | `spa/src/components/RenamePopover.test.tsx` | RenamePopover 測試 |
| Create | `spa/src/stores/useBrowserHistoryStore.ts` | URL 瀏覽歷史 persist store |
| Create | `spa/src/stores/useBrowserHistoryStore.test.ts` | Browser history store 測試 |
| Modify | `spa/src/components/TabContextMenu.tsx` | 新增 rename action |
| Modify | `spa/src/components/TabContextMenu.test.tsx` | 新增 rename 測試 |
| Modify | `spa/src/features/workspace/hooks.ts` | 處理 rename action、rename popover state |
| Modify | `spa/src/App.tsx` | 渲染 RenamePopover |
| Modify | `spa/src/lib/register-panes.tsx` | 移除 memory-monitor provider、調整 browser order |
| Modify | `spa/src/components/BrowserNewTabSection.tsx` | ref focus、URL history dropdown |
| Modify | `spa/src/components/SessionSection.tsx` | 鍵盤導航 |
| Modify | `spa/src/stores/useTabStore.ts` | visitHistory 追蹤 + close 回退 |
| Modify | `spa/src/stores/useTabStore.test.ts` | visitHistory 測試 |
| Modify | `spa/src/components/SortableTab.tsx` | active tab onPointerDown |
| Modify | `spa/src/lib/storage/keys.ts` | 新增 BROWSER_HISTORY key |
| Modify | `spa/src/locales/en.json` | 新增 i18n keys |
| Modify | `spa/src/locales/zh-TW.json` | 新增 i18n keys |
| Delete | `spa/src/components/MemoryMonitorNewTabSection.tsx` | 不再使用 |

---

### Task 1: Tab Visit History（Item 7）

**Files:**
- Modify: `spa/src/stores/useTabStore.ts:68-145`
- Modify: `spa/src/stores/useTabStore.test.ts`

這是最底層的基礎設施，其他 task 不依賴它但越早做越好，因為要改 `closeTab` 的核心邏輯。

- [ ] **Step 1: 寫 visitHistory 的 failing tests**

在 `spa/src/stores/useTabStore.test.ts` 末尾（`describe('persist migration')` 之前）加入：

```typescript
  describe('visitHistory', () => {
    it('records previous tab when switching', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev002')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)
      useTabStore.getState().setActiveTab(tab2.id)
      expect(useTabStore.getState().visitHistory).toEqual([tab1.id])
    })

    it('does not record when switching to same tab', () => {
      const tab1 = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().setActiveTab(tab1.id)
      expect(useTabStore.getState().visitHistory).toEqual([])
    })

    it('does not record null activeTabId', () => {
      const tab1 = makeSessionTab('dev001')
      useTabStore.getState().addTab(tab1)
      // addTab auto-sets activeTabId to tab1.id, but from null — should not record null
      expect(useTabStore.getState().visitHistory).toEqual([])
    })

    it('closeTab activates last visited tab instead of adjacent', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev002')
      const tab3 = makeSessionTab('dev003')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)
      useTabStore.getState().addTab(tab3)
      // Visit order: tab1 → tab3 → tab2
      useTabStore.getState().setActiveTab(tab3.id)
      useTabStore.getState().setActiveTab(tab2.id)
      // Close tab2 → should go back to tab3 (last visited), not tab3 (adjacent)
      useTabStore.getState().closeTab(tab2.id)
      expect(useTabStore.getState().activeTabId).toBe(tab3.id)
    })

    it('closeTab skips closed tabs in visitHistory', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev002')
      const tab3 = makeSessionTab('dev003')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)
      useTabStore.getState().addTab(tab3)
      // Visit: tab1 → tab2 → tab3
      useTabStore.getState().setActiveTab(tab2.id)
      useTabStore.getState().setActiveTab(tab3.id)
      // Close tab2 first (not active, just remove from store)
      useTabStore.getState().closeTab(tab2.id)
      // Now close tab3 → should skip tab2 (gone) → go to tab1
      useTabStore.getState().closeTab(tab3.id)
      expect(useTabStore.getState().activeTabId).toBe(tab1.id)
    })

    it('closeTab falls back to adjacent when visitHistory empty', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev002')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)
      // No setActiveTab calls → visitHistory empty
      // addTab set activeTabId to tab1
      useTabStore.getState().closeTab(tab1.id)
      expect(useTabStore.getState().activeTabId).toBe(tab2.id)
    })

    it('closeTab removes closed tab id from visitHistory', () => {
      const tab1 = makeSessionTab('dev001')
      const tab2 = makeSessionTab('dev002')
      const tab3 = makeSessionTab('dev003')
      useTabStore.getState().addTab(tab1)
      useTabStore.getState().addTab(tab2)
      useTabStore.getState().addTab(tab3)
      useTabStore.getState().setActiveTab(tab2.id)
      useTabStore.getState().setActiveTab(tab3.id)
      // visitHistory: [tab1, tab2]
      useTabStore.getState().closeTab(tab2.id)
      // tab2 should be removed from visitHistory
      expect(useTabStore.getState().visitHistory).not.toContain(tab2.id)
    })
  })
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts`
Expected: FAIL — `visitHistory` 不存在

- [ ] **Step 3: 實作 visitHistory**

在 `spa/src/stores/useTabStore.ts` 中：

1. `TabState` interface 新增：
```typescript
  visitHistory: string[]
```

2. 初始值：
```typescript
      visitHistory: [],
```

3. `setActiveTab` 改為：
```typescript
      setActiveTab: (id) =>
        set((state) => {
          if (id === null) return { activeTabId: null }
          if (!state.tabs[id]) return state
          if (state.activeTabId === id) return state
          const newHistory = state.activeTabId !== null
            ? [...state.visitHistory.filter((h) => h !== id), state.activeTabId]
            : state.visitHistory
          return { activeTabId: id, visitHistory: newHistory }
        }),
```

4. `closeTab` 改為：
```typescript
      closeTab: (id) =>
        set((state) => {
          if (!state.tabs[id]) return state
          if (state.tabs[id].locked) return state
          const { [id]: _removed, ...remainingTabs } = state.tabs
          const newOrder = state.tabOrder.filter((tid) => tid !== id)
          const cleanedHistory = state.visitHistory.filter((h) => h !== id)

          let newActiveId = state.activeTabId
          if (state.activeTabId === id) {
            // Find last visited tab that still exists
            const lastVisited = [...cleanedHistory].reverse().find((h) => remainingTabs[h])
            if (lastVisited) {
              newActiveId = lastVisited
            } else {
              // Fallback to adjacent tab
              const oldIndex = state.tabOrder.indexOf(id)
              newActiveId = newOrder[Math.min(oldIndex, newOrder.length - 1)] ?? null
            }
          }
          return { tabs: remainingTabs, tabOrder: newOrder, activeTabId: newActiveId, visitHistory: cleanedHistory }
        }),
```

5. `partialize` 排除 `visitHistory`（保持不持久化）：
```typescript
      partialize: (state) => ({
        tabs: state.tabs,
        tabOrder: state.tabOrder,
        activeTabId: state.activeTabId,
      }),
```
（partialize 已經只包含三個 key，不用改）

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts`
Expected: ALL PASS

- [ ] **Step 5: 更新既有 closeTab 測試**

既有測試 `'closeTab activates adjacent tab when removing active'` 現在行為改變了（visitHistory 非空時會用 history）。檢查一下：

在那個測試中，`tab1` 被 addTab 後 auto-set 為 active，然後 `tab2` 被 addTab。接著 `setActiveTab(tab1.id)` — 但 tab1 已經是 active，所以 visitHistory 不記錄。然後 `closeTab(tab1.id)` — visitHistory 為空，fallback 到 adjacent → `tab2`。所以既有測試仍然正確，不需修改。

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.test.ts
git commit -m "feat: add visitHistory to tab store for close-tab navigation"
```

---

### Task 2: Active Tab Focus 保持（Item 8）

**Files:**
- Modify: `spa/src/components/SortableTab.tsx:124-151,159-212`

- [ ] **Step 1: 實作 onPointerDown 阻止 focus**

在 `spa/src/components/SortableTab.tsx` 中：

1. 在 pinned tab 的 `<button>`（約 line 126）加上 `onPointerDown`：
```typescript
        onPointerDown={(e) => { if (isActive) e.preventDefault() }}
```

2. 在 normal tab 的 `<div>`（約 line 159）加上 `onPointerDown`：
```typescript
      onPointerDown={(e) => { if (isActive) e.preventDefault() }}
```

注意：只在 `isActive` 時 preventDefault，不檢查 isDragging（因為 pointerDown 時 isDragging 還是 false）。dnd-kit 不依賴 focus 來啟動拖曳，所以不會衝突。

- [ ] **Step 2: 手動驗證**

這個改動很難用 unit test 驗證（涉及 focus 行為），以手動驗證為主：
- 開啟多個 tab
- 點擊已 active 的 tab → focus 應留在 content area
- 點擊不同 tab → 正常切換
- 拖曳 tab → 正常排序

Run: `cd spa && npx vitest run`
Expected: ALL PASS（不破壞既有測試）

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/SortableTab.tsx
git commit -m "fix: prevent focus theft when clicking active tab"
```

---

### Task 3: New Tab 頁面清理（Items 2, 4）

**Files:**
- Modify: `spa/src/lib/register-panes.tsx:76-94`
- Delete: `spa/src/components/MemoryMonitorNewTabSection.tsx`

- [ ] **Step 1: 移除 memory-monitor provider 並調整 browser order**

在 `spa/src/lib/register-panes.tsx` 中：

1. 移除 `import { MemoryMonitorNewTabSection } from '../components/MemoryMonitorNewTabSection'`（line 16）

2. 將 browser provider 的 `order` 從 `10` 改為 `-10`：
```typescript
  registerNewTabProvider({
    id: 'browser',
    label: 'browser.provider_label',
    icon: 'Globe',
    order: -10,
    component: BrowserNewTabSection,
    disabled: !caps.canBrowserPane,
    disabledReason: 'browser.requires_app',
  })
```

3. 移除 memory-monitor 的整個 `registerNewTabProvider` 區塊（lines 86-94）：
```typescript
  // 刪除這段
  registerNewTabProvider({
    id: 'memory-monitor',
    label: 'monitor.provider_label',
    icon: 'ChartBar',
    order: 20,
    component: MemoryMonitorNewTabSection,
    disabled: !caps.canSystemTray,
    disabledReason: 'monitor.requires_app',
  })
```

- [ ] **Step 2: 刪除 MemoryMonitorNewTabSection.tsx**

```bash
rm spa/src/components/MemoryMonitorNewTabSection.tsx
```

- [ ] **Step 3: 確認 build 和 lint 正常**

Run: `cd spa && pnpm run lint && pnpm run build`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/lib/register-panes.tsx
git rm spa/src/components/MemoryMonitorNewTabSection.tsx
git commit -m "refactor: move browser section to top, remove memory monitor from new tab"
```

---

### Task 4: Browser URL History Store（Item 5 store 層）

**Files:**
- Create: `spa/src/stores/useBrowserHistoryStore.ts`
- Create: `spa/src/stores/useBrowserHistoryStore.test.ts`
- Modify: `spa/src/lib/storage/keys.ts`

- [ ] **Step 1: 新增 STORAGE_KEY**

在 `spa/src/lib/storage/keys.ts` 中，在 `NOTIFICATION_SEEN` 之前加入：

```typescript
  BROWSER_HISTORY: 'purdex-browser-history',
```

- [ ] **Step 2: 寫 store 的 failing tests**

建立 `spa/src/stores/useBrowserHistoryStore.test.ts`：

```typescript
import { describe, it, expect, beforeEach } from 'vitest'
import { useBrowserHistoryStore } from './useBrowserHistoryStore'

describe('useBrowserHistoryStore', () => {
  beforeEach(() => {
    useBrowserHistoryStore.setState({ urls: [] })
  })

  it('addUrl adds to head', () => {
    useBrowserHistoryStore.getState().addUrl('https://example.com')
    expect(useBrowserHistoryStore.getState().urls[0]).toBe('https://example.com')
  })

  it('addUrl deduplicates (moves existing to head)', () => {
    useBrowserHistoryStore.getState().addUrl('https://a.com')
    useBrowserHistoryStore.getState().addUrl('https://b.com')
    useBrowserHistoryStore.getState().addUrl('https://a.com')
    const urls = useBrowserHistoryStore.getState().urls
    expect(urls).toEqual(['https://a.com', 'https://b.com'])
  })

  it('addUrl caps at 100 entries', () => {
    for (let i = 0; i < 110; i++) {
      useBrowserHistoryStore.getState().addUrl(`https://${i}.com`)
    }
    expect(useBrowserHistoryStore.getState().urls).toHaveLength(100)
    // Most recent should be first
    expect(useBrowserHistoryStore.getState().urls[0]).toBe('https://109.com')
  })
})
```

- [ ] **Step 3: 確認測試失敗**

Run: `cd spa && npx vitest run src/stores/useBrowserHistoryStore.test.ts`
Expected: FAIL — module not found

- [ ] **Step 4: 實作 store**

建立 `spa/src/stores/useBrowserHistoryStore.ts`：

```typescript
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { purdexStorage, STORAGE_KEYS } from '../lib/storage'

const MAX_URLS = 100

interface BrowserHistoryState {
  urls: string[]
  addUrl: (url: string) => void
}

export const useBrowserHistoryStore = create<BrowserHistoryState>()(
  persist(
    (set) => ({
      urls: [],

      addUrl: (url) =>
        set((state) => {
          const filtered = state.urls.filter((u) => u !== url)
          return { urls: [url, ...filtered].slice(0, MAX_URLS) }
        }),
    }),
    {
      name: STORAGE_KEYS.BROWSER_HISTORY,
      storage: purdexStorage,
    },
  ),
)
```

- [ ] **Step 5: 確認測試通過**

Run: `cd spa && npx vitest run src/stores/useBrowserHistoryStore.test.ts`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/lib/storage/keys.ts spa/src/stores/useBrowserHistoryStore.ts spa/src/stores/useBrowserHistoryStore.test.ts
git commit -m "feat: add browser URL history store with persist"
```

---

### Task 5: URL History Dropdown UI（Items 3, 5 UI 層）

> 此 task 完整改寫 BrowserNewTabSection，同時實現 Item 3（auto-focus）和 Item 5（URL history dropdown）。

**Files:**
- Modify: `spa/src/components/BrowserNewTabSection.tsx`

- [ ] **Step 1: 改寫 BrowserNewTabSection 為帶 dropdown 的輸入欄**

將 `spa/src/components/BrowserNewTabSection.tsx` 完整改寫：

```tsx
import { useState, useRef, useEffect, useCallback } from 'react'
import { Globe } from '@phosphor-icons/react'
import { useI18nStore } from '../stores/useI18nStore'
import { useBrowserHistoryStore } from '../stores/useBrowserHistoryStore'
import type { NewTabProviderProps } from '../lib/new-tab-registry'

export function BrowserNewTabSection({ onSelect }: NewTabProviderProps) {
  const t = useI18nStore((s) => s.t)
  const urls = useBrowserHistoryStore((s) => s.urls)
  const addUrl = useBrowserHistoryStore((s) => s.addUrl)
  const [url, setUrl] = useState('')
  const [showDropdown, setShowDropdown] = useState(false)
  const [highlightIndex, setHighlightIndex] = useState(-1)
  const inputRef = useRef<HTMLInputElement>(null)
  const dropdownRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  const filtered = url.trim()
    ? urls.filter((u) => u.toLowerCase().includes(url.toLowerCase()))
    : urls

  const submit = useCallback((value: string) => {
    if (!value.trim()) return
    const finalUrl = value.includes('://') ? value : `https://${value}`
    try {
      const parsed = new URL(finalUrl)
      if (!['http:', 'https:'].includes(parsed.protocol)) return
    } catch {
      return
    }
    addUrl(finalUrl)
    onSelect({ kind: 'browser', url: finalUrl })
  }, [addUrl, onSelect])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (!showDropdown || filtered.length === 0) {
      if (e.key === 'ArrowDown' && filtered.length > 0) {
        setShowDropdown(true)
        setHighlightIndex(0)
        e.preventDefault()
        return
      }
      return
    }
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        setHighlightIndex((prev) => Math.min(prev + 1, filtered.length - 1))
        break
      case 'ArrowUp':
        e.preventDefault()
        setHighlightIndex((prev) => {
          if (prev <= 0) { setShowDropdown(false); return -1 }
          return prev - 1
        })
        break
      case 'Enter':
        e.preventDefault()
        if (highlightIndex >= 0 && highlightIndex < filtered.length) {
          submit(filtered[highlightIndex])
        } else {
          submit(url)
        }
        break
      case 'Escape':
        e.preventDefault()
        setShowDropdown(false)
        setHighlightIndex(-1)
        break
    }
  }

  const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setUrl(e.target.value)
    setShowDropdown(true)
    setHighlightIndex(-1)
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (highlightIndex >= 0 && highlightIndex < filtered.length) {
      submit(filtered[highlightIndex])
    } else {
      submit(url)
    }
  }

  // Close dropdown on outside click
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (
        dropdownRef.current && !dropdownRef.current.contains(e.target as Node) &&
        inputRef.current && !inputRef.current.contains(e.target as Node)
      ) {
        setShowDropdown(false)
      }
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  // Scroll highlighted item into view
  useEffect(() => {
    if (highlightIndex < 0 || !dropdownRef.current) return
    const items = dropdownRef.current.querySelectorAll('[data-dropdown-item]')
    items[highlightIndex]?.scrollIntoView({ block: 'nearest' })
  }, [highlightIndex])

  return (
    <div className="relative px-2">
      <form onSubmit={handleSubmit} className="flex items-center gap-2">
        <Globe size={16} className="text-text-muted flex-shrink-0" />
        <input
          ref={inputRef}
          type="text"
          value={url}
          onChange={handleInputChange}
          onFocus={() => { if (urls.length > 0) setShowDropdown(true) }}
          onKeyDown={handleKeyDown}
          placeholder={t('browser.url_placeholder')}
          className="flex-1 bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 focus:border-border-active focus:outline-none"
        />
      </form>
      {showDropdown && filtered.length > 0 && (
        <div
          ref={dropdownRef}
          className="absolute left-2 right-2 mt-1 bg-surface-elevated border border-border-default rounded-md shadow-lg max-h-48 overflow-y-auto z-10"
        >
          {filtered.map((historyUrl, i) => (
            <button
              key={historyUrl}
              data-dropdown-item
              type="button"
              onMouseDown={(e) => { e.preventDefault(); submit(historyUrl) }}
              onMouseEnter={() => setHighlightIndex(i)}
              className={`w-full text-left text-xs px-3 py-1.5 truncate cursor-pointer transition-colors ${
                i === highlightIndex ? 'bg-surface-hover text-text-primary' : 'text-text-secondary hover:bg-surface-hover'
              }`}
            >
              {historyUrl}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: 確認測試和 build 通過**

Run: `cd spa && npx vitest run && pnpm run build`
Expected: ALL PASS

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/BrowserNewTabSection.tsx
git commit -m "feat: URL history dropdown with auto-filter in new tab page"
```

---

### Task 6: Session List 鍵盤導航（Item 6）

**Files:**
- Modify: `spa/src/components/SessionSection.tsx:49-63`

- [ ] **Step 1: 加入 onKeyDown 鍵盤導航**

在 `spa/src/components/SessionSection.tsx` 中，修改 session button 的 render：

1. 在 `<button>` 上加入 `onKeyDown` handler：

```tsx
              <button
                key={`${hostId}:${session.code}`}
                className="flex items-center gap-3 px-3 py-2 rounded-md hover:bg-white/10 text-left text-sm text-text-primary cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed focus:outline-none focus:ring-1 focus:ring-accent-muted"
                disabled={!!isOffline}
                tabIndex={0}
                onClick={() =>
                  onSelect({ kind: 'tmux-session', hostId, sessionCode: session.code, mode: 'terminal', cachedName: session.name, tmuxInstance: '' })
                }
                onKeyDown={(e) => {
                  const container = e.currentTarget.closest('[data-session-list]')
                  if (!container) return
                  const buttons = Array.from(container.querySelectorAll('button:not(:disabled)')) as HTMLElement[]
                  const currentIndex = buttons.indexOf(e.currentTarget)
                  if (currentIndex === -1) return

                  switch (e.key) {
                    case 'ArrowDown':
                    case 'j':
                      e.preventDefault()
                      buttons[Math.min(currentIndex + 1, buttons.length - 1)]?.focus()
                      break
                    case 'ArrowUp':
                    case 'k':
                      e.preventDefault()
                      buttons[Math.max(currentIndex - 1, 0)]?.focus()
                      break
                    case 'Enter':
                      // Default button behavior handles this
                      break
                  }
                }}
              >
```

2. 在外層 `<div>` 加 `data-session-list` 屬性：

```tsx
    <div className="flex flex-col gap-1" data-session-list>
```

- [ ] **Step 2: 連接 Tab 鍵從 URL input 到 session list**

URL input 的 Tab 鍵預設就會往下個 focusable element 移動。因為 session button 已有 `tabIndex={0}`，瀏覽器原生 Tab 行為就能從 URL input Tab 到第一個 session button。不需要攔截 Tab 鍵。

確認 `Shift+Tab` 從 session list 自然回到 URL input。

- [ ] **Step 3: 確認測試通過**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/SessionSection.tsx
git commit -m "feat: keyboard navigation (arrows/jk) in session list"
```

---

### Task 7: Tab Context Menu Rename（Item 1 — menu 層）

**Files:**
- Modify: `spa/src/components/TabContextMenu.tsx:8-81`
- Modify: `spa/src/components/TabContextMenu.test.tsx`
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: 新增 i18n keys**

在 `spa/src/locales/en.json` 的 `"tab.close_right"` 之後加入：

```json
  "tab.rename_session": "Rename Session",
```

在 `spa/src/locales/zh-TW.json` 的 `"tab.close_right"` 之後加入：

```json
  "tab.rename_session": "重新命名 Session",
```

- [ ] **Step 2: 寫 failing test**

在 `spa/src/components/TabContextMenu.test.tsx` 的 `describe('TabContextMenu')` 內加入：

```typescript
  // --- Rename section ---
  it('shows "Rename Session" for non-terminated session tab', () => {
    renderMenu()
    expect(screen.getByText('Rename Session')).toBeInTheDocument()
  })

  it('hides "Rename Session" for non-session tab', () => {
    renderMenu({ tab: makeNonSessionTab() })
    expect(screen.queryByText('Rename Session')).not.toBeInTheDocument()
  })

  it('hides "Rename Session" for terminated session tab', () => {
    const tab = createTab({ kind: 'tmux-session', hostId: 'h', sessionCode: 'c', mode: 'terminal', cachedName: '', tmuxInstance: '', terminated: 'session-closed' })
    renderMenu({ tab })
    expect(screen.queryByText('Rename Session')).not.toBeInTheDocument()
  })

  it('calls onAction with "rename" when clicking Rename Session', () => {
    const props = renderMenu()
    fireEvent.click(screen.getByText('Rename Session'))
    expect(props.onAction).toHaveBeenCalledWith('rename')
  })
```

- [ ] **Step 3: 確認測試失敗**

Run: `cd spa && npx vitest run src/components/TabContextMenu.test.tsx`
Expected: FAIL — "Rename Session" 不存在

- [ ] **Step 4: 實作 — 加入 rename 到 ContextMenuAction 和 menu items**

在 `spa/src/components/TabContextMenu.tsx` 中：

1. 更新 `ContextMenuAction` type（line 8）：
```typescript
export type ContextMenuAction =
  | 'viewMode-terminal' | 'viewMode-stream'
  | 'lock' | 'unlock' | 'pin' | 'unpin'
  | 'close' | 'closeOthers' | 'closeRight'
  | 'tearOff' | 'mergeTo'
  | 'rename'
```

2. 在 items 陣列中，在 viewMode section 之後、lock/pin section 之前插入 rename：

```typescript
    // Rename section (session only, non-terminated)
    ...(isSession && !isTerminated ? [{ label: t('tab.rename_session'), action: 'rename' as const, show: true }] : []),
```

3. 在 `const isSession` 下方加入 terminated 檢查：
```typescript
  const isTerminated = isSession && !!(primary.content as { terminated?: string }).terminated
```

- [ ] **Step 5: 確認測試通過**

Run: `cd spa && npx vitest run src/components/TabContextMenu.test.tsx`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/TabContextMenu.tsx spa/src/components/TabContextMenu.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat: add Rename Session to tab context menu"
```

---

### Task 8: RenamePopover 元件（Item 1 — UI 層）

**Files:**
- Create: `spa/src/components/RenamePopover.tsx`
- Create: `spa/src/components/RenamePopover.test.tsx`
- Modify: `spa/src/locales/en.json`
- Modify: `spa/src/locales/zh-TW.json`

- [ ] **Step 1: 新增 i18n keys**

在 `spa/src/locales/en.json` 的 `"tab.rename_session"` 之後加入：

```json
  "tab.rename_placeholder": "Session name",
```

在 `spa/src/locales/zh-TW.json` 的 `"tab.rename_session"` 之後加入：

```json
  "tab.rename_placeholder": "Session 名稱",
```

- [ ] **Step 2: 寫 failing tests**

建立 `spa/src/components/RenamePopover.test.tsx`：

```typescript
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { RenamePopover } from './RenamePopover'

describe('RenamePopover', () => {
  const defaultProps = {
    anchorRect: { left: 100, top: 30, width: 120, height: 26, bottom: 56, right: 220 } as DOMRect,
    currentName: 'my-session',
    onConfirm: vi.fn().mockResolvedValue(undefined),
    onCancel: vi.fn(),
  }

  beforeEach(() => { cleanup(); vi.clearAllMocks() })
  afterEach(() => cleanup())

  it('renders input with current name', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    expect(input).toBeInTheDocument()
  })

  it('selects all text on mount', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session') as HTMLInputElement
    expect(input.selectionStart).toBe(0)
    expect(input.selectionEnd).toBe('my-session'.length)
  })

  it('calls onConfirm with new name on Enter', async () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.change(input, { target: { value: 'new-name' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).toHaveBeenCalledWith('new-name')
  })

  it('calls onCancel on Escape', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.keyDown(input, { key: 'Escape' })
    expect(defaultProps.onCancel).toHaveBeenCalled()
  })

  it('does not call onConfirm with empty name', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.change(input, { target: { value: '' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).not.toHaveBeenCalled()
  })

  it('does not call onConfirm when name unchanged', () => {
    render(<RenamePopover {...defaultProps} />)
    const input = screen.getByDisplayValue('my-session')
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(defaultProps.onConfirm).not.toHaveBeenCalled()
  })

  it('shows error message when provided', () => {
    render(<RenamePopover {...defaultProps} error="Rename failed" />)
    expect(screen.getByText('Rename failed')).toBeInTheDocument()
  })
})
```

- [ ] **Step 3: 確認測試失敗**

Run: `cd spa && npx vitest run src/components/RenamePopover.test.tsx`
Expected: FAIL — module not found

- [ ] **Step 4: 實作 RenamePopover**

建立 `spa/src/components/RenamePopover.tsx`：

```tsx
import { useState, useRef, useEffect, useLayoutEffect } from 'react'
import { useClickOutside } from '../hooks/useClickOutside'
import { useI18nStore } from '../stores/useI18nStore'

interface Props {
  anchorRect: DOMRect
  currentName: string
  onConfirm: (name: string) => Promise<void>
  onCancel: () => void
  error?: string
}

const POPOVER_WIDTH = 240
const PADDING = 4

export function RenamePopover({ anchorRect, currentName, onConfirm, onCancel, error }: Props) {
  const t = useI18nStore((s) => s.t)
  const [draft, setDraft] = useState(currentName)
  const [submitting, setSubmitting] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  useClickOutside(containerRef, onCancel)

  // Focus + select all on mount
  useEffect(() => {
    const input = inputRef.current
    if (input) {
      input.focus()
      input.select()
    }
  }, [])

  // Position: centered below anchor, clamped to viewport
  useLayoutEffect(() => {
    const el = containerRef.current
    if (!el) return
    let left = anchorRect.left + anchorRect.width / 2 - POPOVER_WIDTH / 2
    left = Math.max(PADDING, Math.min(left, window.innerWidth - POPOVER_WIDTH - PADDING))
    const top = anchorRect.bottom + PADDING
    el.style.left = `${left}px`
    el.style.top = `${top}px`
  }, [anchorRect])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      onCancel()
    }
    if (e.key === 'Enter') {
      e.preventDefault()
      const trimmed = draft.trim()
      if (!trimmed || trimmed === currentName || submitting) return
      setSubmitting(true)
      onConfirm(trimmed).finally(() => setSubmitting(false))
    }
  }

  return (
    <div
      ref={containerRef}
      className="fixed z-50 bg-surface-elevated border border-border-default rounded-lg shadow-xl p-2"
      style={{ width: POPOVER_WIDTH }}
    >
      <input
        ref={inputRef}
        type="text"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={handleKeyDown}
        disabled={submitting}
        placeholder={t('tab.rename_placeholder')}
        className="w-full bg-surface-input border border-border-default rounded-md text-text-primary text-xs px-3 py-1.5 focus:border-border-active focus:outline-none disabled:opacity-50"
      />
      {error && (
        <p className="text-xs text-red-400 mt-1 px-1">{error}</p>
      )}
    </div>
  )
}
```

- [ ] **Step 5: 確認測試通過**

Run: `cd spa && npx vitest run src/components/RenamePopover.test.tsx`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/RenamePopover.tsx spa/src/components/RenamePopover.test.tsx spa/src/locales/en.json spa/src/locales/zh-TW.json
git commit -m "feat: add RenamePopover component for inline session rename"
```

---

### Task 9: Rename 流程整合（Item 1 — 接線）

**Files:**
- Modify: `spa/src/features/workspace/hooks.ts:1-152`
- Modify: `spa/src/App.tsx:329-337`

- [ ] **Step 1: hooks.ts 增加 rename state + action handler**

在 `spa/src/features/workspace/hooks.ts` 中：

1. 新增 import：
```typescript
import { renameSession } from '../../lib/host-api'
```

2. 在 `useTabWorkspaceActions` 內新增 state：
```typescript
  const [renameTarget, setRenameTarget] = useState<{ tabId: string; hostId: string; sessionCode: string; currentName: string; anchorRect: DOMRect } | null>(null)
  const [renameError, setRenameError] = useState<string | undefined>()
```

3. 在 `handleContextAction` 的 switch 中加入 `'rename'` case：
```typescript
      case 'rename': {
        const primary = getPrimaryPane(tab.layout)
        const c = primary.content
        if (c.kind !== 'tmux-session' || c.terminated) break
        // Find the tab element for anchor positioning
        const tabEl = document.querySelector(`[data-tab-id="${tab.id}"]`)
        if (!tabEl) break
        const rect = tabEl.getBoundingClientRect()
        setRenameTarget({
          tabId: tab.id,
          hostId: c.hostId,
          sessionCode: c.sessionCode,
          currentName: c.cachedName || c.sessionCode,
          anchorRect: rect,
        })
        setRenameError(undefined)
        break
      }
```

4. 新增 rename confirm/cancel handlers：
```typescript
  const handleRenameConfirm = useCallback(async (name: string) => {
    if (!renameTarget) return
    try {
      const res = await renameSession(renameTarget.hostId, renameTarget.sessionCode, name)
      if (!res.ok) {
        const text = await res.text().catch(() => 'Unknown error')
        setRenameError(text)
        return
      }
      setRenameTarget(null)
      setRenameError(undefined)
    } catch (err) {
      setRenameError(err instanceof Error ? err.message : 'Unknown error')
    }
  }, [renameTarget])

  const handleRenameCancel = useCallback(() => {
    setRenameTarget(null)
    setRenameError(undefined)
  }, [])
```

5. 在 return 中加入新的值：
```typescript
  return {
    // ... existing
    renameTarget,
    renameError,
    handleRenameConfirm,
    handleRenameCancel,
  }
```

- [ ] **Step 2: SortableTab 加上 data-tab-id**

在 `spa/src/components/SortableTab.tsx` 中，兩個 render path 的最外層元素加上 `data-tab-id={tab.id}`：

Pinned tab（`<button>`）：
```typescript
        data-tab-id={tab.id}
```

Normal tab（`<div>`）：
```typescript
      data-tab-id={tab.id}
```

- [ ] **Step 3: App.tsx 渲染 RenamePopover**

在 `spa/src/App.tsx` 中：

1. 新增 import：
```typescript
import { RenamePopover } from './components/RenamePopover'
```

2. 解構 hooks 回傳值時加入新 fields：
```typescript
    renameTarget,
    renameError,
    handleRenameConfirm,
    handleRenameCancel,
```

3. 在 `TabContextMenu` 渲染之後加入：
```tsx
        {renameTarget && (
          <RenamePopover
            anchorRect={renameTarget.anchorRect}
            currentName={renameTarget.currentName}
            onConfirm={handleRenameConfirm}
            onCancel={handleRenameCancel}
            error={renameError}
          />
        )}
```

- [ ] **Step 4: 確認全部測試通過**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 5: 確認 lint + build 通過**

Run: `cd spa && pnpm run lint && pnpm run build`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/features/workspace/hooks.ts spa/src/components/SortableTab.tsx spa/src/App.tsx
git commit -m "feat: wire up rename session flow from context menu to popover"
```

---

### Task 10: 最終驗證

- [ ] **Step 1: 全部測試**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 2: Lint**

Run: `cd spa && pnpm run lint`
Expected: PASS

- [ ] **Step 3: Build**

Run: `cd spa && pnpm run build`
Expected: PASS
