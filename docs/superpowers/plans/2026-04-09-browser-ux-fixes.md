# Browser UX Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix mini window invisible toolbar + missing Cmd+W, add browser navigation shortcuts with tab-level handler registry.

**Architecture:** Menu accelerators dispatch action strings to focused window. SPA-side registry maps `PaneContent['kind']` → action → handler. Mini window uses a separate lightweight hook. Theme fix ensures CSS variables are available in mini window.

**Tech Stack:** Electron (menu, IPC, WebContentsView), React 19, Zustand 5, Vitest

---

### Task 1: Tab Shortcut Handler Registry

**Files:**
- Create: `spa/src/lib/tab-shortcut-registry.ts`
- Create: `spa/src/lib/tab-shortcut-registry.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
// spa/src/lib/tab-shortcut-registry.test.ts
import { describe, it, expect, vi } from 'vitest'
import {
  registerTabShortcuts,
  getTabShortcutHandler,
  clearTabShortcutRegistry,
} from './tab-shortcut-registry'

describe('tab-shortcut-registry', () => {
  afterEach(() => clearTabShortcutRegistry())

  it('registers and retrieves a handler', () => {
    const handler = vi.fn()
    registerTabShortcuts('browser', { reload: handler })
    expect(getTabShortcutHandler('browser', 'reload')).toBe(handler)
  })

  it('returns undefined for unregistered kind', () => {
    expect(getTabShortcutHandler('browser', 'reload')).toBeUndefined()
  })

  it('returns undefined for unregistered action', () => {
    registerTabShortcuts('browser', { reload: vi.fn() })
    expect(getTabShortcutHandler('browser', 'go-back')).toBeUndefined()
  })

  it('merges handlers when registering same kind twice', () => {
    const reload = vi.fn()
    const goBack = vi.fn()
    registerTabShortcuts('browser', { reload })
    registerTabShortcuts('browser', { 'go-back': goBack })
    expect(getTabShortcutHandler('browser', 'reload')).toBe(reload)
    expect(getTabShortcutHandler('browser', 'go-back')).toBe(goBack)
  })

  it('clearTabShortcutRegistry removes all handlers', () => {
    registerTabShortcuts('browser', { reload: vi.fn() })
    clearTabShortcutRegistry()
    expect(getTabShortcutHandler('browser', 'reload')).toBeUndefined()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/lib/tab-shortcut-registry.test.ts`
Expected: FAIL — module not found

- [ ] **Step 3: Write the implementation**

```ts
// spa/src/lib/tab-shortcut-registry.ts
import type { Tab, Pane } from '../types/tab'

export type TabShortcutHandler = (tab: Tab, pane: Pane) => void

const registry = new Map<string, Map<string, TabShortcutHandler>>()

export function registerTabShortcuts(
  kind: string,
  handlers: Record<string, TabShortcutHandler>,
): void {
  const existing = registry.get(kind) ?? new Map()
  for (const [action, handler] of Object.entries(handlers)) {
    existing.set(action, handler)
  }
  registry.set(kind, existing)
}

export function getTabShortcutHandler(
  kind: string,
  action: string,
): TabShortcutHandler | undefined {
  return registry.get(kind)?.get(action)
}

export function clearTabShortcutRegistry(): void {
  registry.clear()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/lib/tab-shortcut-registry.test.ts`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add spa/src/lib/tab-shortcut-registry.ts spa/src/lib/tab-shortcut-registry.test.ts
git commit -m "feat: add tab shortcut handler registry"
```

---

### Task 2: Keybindings Expansion

**Files:**
- Modify: `electron/keybindings.ts`

- [ ] **Step 1: Add `platform` field to `KeybindingDef` and `'browser'` to `MenuGroup`**

In `electron/keybindings.ts`, change line 3:

```ts
export type MenuGroup = 'tab-index' | 'tab-nav' | 'tab-action' | 'workspace-nav' | 'app' | 'view' | 'file' | 'browser'
```

Add `platform` to the interface (after `menuGroup`):

```ts
  platform?: 'darwin' | 'win32' | 'linux'
```

Add `'Browser'` to `menuCategory` union:

```ts
  menuCategory: 'App' | 'File' | 'Tab' | 'View' | 'Edit' | 'Browser'
```

- [ ] **Step 2: Add browser keybinding definitions**

Append these entries to the `DEFAULT_KEYBINDINGS` array, before the closing `]`:

```ts
  // Browser navigation
  { action: 'go-back', accelerator: 'CommandOrControl+[', label: 'Go Back', menuCategory: 'Browser', menuGroup: 'browser' },
  { action: 'go-forward', accelerator: 'CommandOrControl+]', label: 'Go Forward', menuCategory: 'Browser', menuGroup: 'browser' },
  { action: 'go-back', accelerator: 'Command+Left', label: 'Go Back', menuCategory: 'Browser', menuGroup: 'browser', platform: 'darwin' },
  { action: 'go-forward', accelerator: 'Command+Right', label: 'Go Forward', menuCategory: 'Browser', menuGroup: 'browser', platform: 'darwin' },
  { action: 'reload', accelerator: 'CommandOrControl+R', label: 'Reload', menuCategory: 'Browser', menuGroup: 'browser' },
  { action: 'focus-url', accelerator: 'CommandOrControl+L', label: 'Focus Address Bar', menuCategory: 'Browser', menuGroup: 'browser' },
  { action: 'print', accelerator: 'CommandOrControl+P', label: 'Print', menuCategory: 'Browser', menuGroup: 'browser' },
```

- [ ] **Step 3: Update `buildMenuTemplate` with platform filtering, dedup, and Browser submenu**

Replace the `buildMenuTemplate` function body:

```ts
export function buildMenuTemplate(
  bindings: KeybindingDef[],
  send: (action: string) => void,
  mainHandlers?: Record<string, () => void>,
): MenuItemConstructorOptions[] {
  // Platform filter
  const platform = process.platform
  const filtered = bindings.filter((b) => !b.platform || b.platform === platform)

  // Build menu items with dedup: first-seen action gets visible item, rest get hidden (accelerator-only)
  const byGroup = new Map<MenuGroup, MenuItemConstructorOptions[]>()
  const byCategory = new Map<string, MenuItemConstructorOptions[]>()
  const seenActions = new Set<string>()

  for (const b of filtered) {
    const handler = mainHandlers?.[b.action]
    const isDuplicate = seenActions.has(b.action)
    seenActions.add(b.action)

    const item: MenuItemConstructorOptions = {
      label: b.label,
      accelerator: b.accelerator,
      click: handler ?? (() => send(b.action)),
      ...(isDuplicate ? { visible: false } : {}),
    }

    const groupItems = byGroup.get(b.menuGroup) ?? []
    groupItems.push(item)
    byGroup.set(b.menuGroup, groupItems)

    const catItems = byCategory.get(b.menuCategory) ?? []
    catItems.push(item)
    byCategory.set(b.menuCategory, catItems)
  }

  const isMac = platform === 'darwin'

  const appMenu: MenuItemConstructorOptions = {
    label: 'tmux-box',
    submenu: [
      ...(isMac ? [{ role: 'about' as const }] : []),
      ...(byCategory.get('App') ?? []),
      { type: 'separator' as const },
      ...(isMac
        ? [
            { role: 'hide' as const },
            { role: 'hideOthers' as const },
            { role: 'unhide' as const },
            { type: 'separator' as const },
          ]
        : []),
      { role: 'quit' as const },
    ],
  }

  const tabMenu: MenuItemConstructorOptions = {
    label: 'Tab',
    submenu: [
      ...(byGroup.get('tab-index') ?? []),
      { type: 'separator' as const },
      ...(byGroup.get('tab-nav') ?? []),
      { type: 'separator' as const },
      ...(byGroup.get('tab-action') ?? []),
      { type: 'separator' as const },
      ...(byGroup.get('workspace-nav') ?? []),
    ],
  }

  const fileMenu: MenuItemConstructorOptions = {
    label: 'File',
    submenu: [...(byGroup.get('file') ?? [])],
  }

  const viewMenu: MenuItemConstructorOptions = {
    label: 'View',
    submenu: [...(byCategory.get('View') ?? [])],
  }

  const editMenu: MenuItemConstructorOptions = {
    label: 'Edit',
    submenu: [
      { role: 'undo' as const },
      { role: 'redo' as const },
      { type: 'separator' as const },
      { role: 'cut' as const },
      { role: 'copy' as const },
      { role: 'paste' as const },
      { role: 'selectAll' as const },
    ],
  }

  const browserMenu: MenuItemConstructorOptions = {
    label: 'Browser',
    submenu: [...(byGroup.get('browser') ?? [])],
  }

  return [appMenu, fileMenu, editMenu, tabMenu, browserMenu, viewMenu]
}
```

- [ ] **Step 4: Verify build**

Run: `cd spa && pnpm run build`
Expected: No TypeScript errors (keybindings.ts is in `electron/` — verify with `npx tsc --noEmit -p electron/tsconfig.json` or equivalent if available, otherwise visual check)

- [ ] **Step 5: Commit**

```bash
git add electron/keybindings.ts
git commit -m "feat: add browser navigation keybindings with platform support"
```

---

### Task 3: Print IPC

**Files:**
- Modify: `electron/preload.ts:26-34` (add after browserViewStop)
- Modify: `electron/browser-view-ipc.ts:51-57` (add after stop handler)
- Modify: `spa/src/types/electron.d.ts:67-68` (add after browserViewStop)

- [ ] **Step 1: Add IPC handler in `browser-view-ipc.ts`**

After the `browser-view:stop` handler (line 52), add:

```ts
  ipcMain.handle('browser-view:print', (_event, paneId: string) => {
    const entry = manager.getViewEntry(paneId)
    if (entry) entry.view.webContents.print()
  })
```

Wait — `manager` doesn't expose `getViewEntry` by paneId directly. Check: `browser-view-manager.ts` has `views` as private Map. We need a method. Let me adjust — use the pattern from `reload`/`stop` which call `manager.reload(paneId)` etc.

Add to `BrowserViewManager` in `browser-view-manager.ts` (after the `stop` method):

```ts
  print(paneId: string): void {
    const entry = this.views.get(paneId)
    entry?.view.webContents.print()
  }
```

Then in `browser-view-ipc.ts` after the stop handler:

```ts
  ipcMain.handle('browser-view:print', (_event, paneId: string) => {
    manager.print(paneId)
  })
```

- [ ] **Step 2: Add preload bridge in `preload.ts`**

After `browserViewStop` (line 34), add:

```ts
  browserViewPrint: (paneId: string) =>
    ipcRenderer.invoke('browser-view:print', paneId),
```

- [ ] **Step 3: Add type declaration in `electron.d.ts`**

After `browserViewStop` (line 67), add:

```ts
    browserViewPrint: (paneId: string) => Promise<void>
```

- [ ] **Step 4: Commit**

```bash
git add electron/browser-view-manager.ts electron/browser-view-ipc.ts electron/preload.ts spa/src/types/electron.d.ts
git commit -m "feat: add browser-view:print IPC for Cmd+P"
```

---

### Task 4: Browser Shortcuts Registration + useShortcuts Integration

**Files:**
- Create: `spa/src/lib/browser-shortcuts.ts`
- Modify: `spa/src/hooks/useShortcuts.ts:1-2,126-128`
- Modify: `spa/src/App.tsx:16` (add import)
- Modify: `spa/src/hooks/useShortcuts.test.ts` (add registry dispatch test)

- [ ] **Step 1: Write the failing test for registry dispatch in useShortcuts**

Append to `spa/src/hooks/useShortcuts.test.ts`, inside the outer `describe('useShortcuts', ...)` block, before the closing `})`:

```ts
  describe('tab shortcut registry dispatch', () => {
    afterEach(() => {
      // Clean up registry
      const { clearTabShortcutRegistry } = await import('../lib/tab-shortcut-registry')
      clearTabShortcutRegistry()
    })

    it('dispatches to registered handler for active tab kind', async () => {
      const { registerTabShortcuts, clearTabShortcutRegistry } = await import('../lib/tab-shortcut-registry')
      const handler = vi.fn()
      registerTabShortcuts('browser', { reload: handler })

      const { fire } = mockElectronAPI()
      const tab = createTab({ kind: 'browser', url: 'https://example.com' })
      useTabStore.getState().addTab(tab)
      useTabStore.getState().setActiveTab(tab.id)
      const wsId = useWorkspaceStore.getState().activeWorkspaceId!
      useWorkspaceStore.getState().addTabToWorkspace(wsId, tab.id)
      renderHook(() => useShortcuts())

      fire('reload')
      expect(handler).toHaveBeenCalledOnce()
      expect(handler).toHaveBeenCalledWith(tab, expect.objectContaining({ content: { kind: 'browser', url: 'https://example.com' } }))
      clearTabShortcutRegistry()
    })

    it('does not dispatch when active tab kind has no handler', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(1) // kind: 'new-tab'
      renderHook(() => useShortcuts())

      // Should not throw — just falls through to unknown action
      fire('reload')
    })
  })
```

Wait — `afterEach` with `await import` won't work cleanly in vitest with static imports. Let me use a synchronous approach. The test file already imports modules at top level. Let me adjust:

Add at the top of the test file (after existing imports):

```ts
import { registerTabShortcuts, clearTabShortcutRegistry } from '../lib/tab-shortcut-registry'
```

Then the test block:

```ts
  describe('tab shortcut registry dispatch', () => {
    afterEach(() => clearTabShortcutRegistry())

    it('dispatches to registered handler for active tab kind', () => {
      const handler = vi.fn()
      registerTabShortcuts('browser', { reload: handler })

      const { fire } = mockElectronAPI()
      const tab = createTab({ kind: 'browser', url: 'https://example.com' })
      useTabStore.getState().addTab(tab)
      useTabStore.getState().setActiveTab(tab.id)
      const wsId = useWorkspaceStore.getState().activeWorkspaceId!
      useWorkspaceStore.getState().addTabToWorkspace(wsId, tab.id)
      renderHook(() => useShortcuts())

      fire('reload')
      expect(handler).toHaveBeenCalledOnce()
      expect(handler).toHaveBeenCalledWith(
        tab,
        expect.objectContaining({
          content: { kind: 'browser', url: 'https://example.com' },
        }),
      )
    })

    it('does not dispatch when active tab kind has no handler', () => {
      const { fire } = mockElectronAPI()
      seedTabs(1) // kind: 'new-tab'
      renderHook(() => useShortcuts())

      fire('reload')
      // Should not throw — falls through to unknown action log
    })
  })
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/hooks/useShortcuts.test.ts`
Expected: FAIL — `reload` action not dispatched to handler

- [ ] **Step 3: Add registry dispatch to `useShortcuts.ts`**

Add import at top of `spa/src/hooks/useShortcuts.ts`:

```ts
import { getTabShortcutHandler } from '../lib/tab-shortcut-registry'
import { getPrimaryPane } from '../lib/pane-tree'
```

Replace the `unknown action` block at the end (lines 126-128):

```ts
      // Tab-level shortcut dispatch via registry
      const { activeTabId, tabs } = tabState
      if (activeTabId) {
        const tab = tabs[activeTabId]
        if (tab) {
          const pane = getPrimaryPane(tab.layout)
          const handler = getTabShortcutHandler(pane.content.kind, action)
          if (handler) {
            handler(tab, pane)
            return
          }
        }
      }

      if (import.meta.env.DEV) {
        console.warn(`[useShortcuts] unknown action: ${action}`)
      }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd spa && npx vitest run src/hooks/useShortcuts.test.ts`
Expected: ALL PASS

- [ ] **Step 5: Create `browser-shortcuts.ts`**

```ts
// spa/src/lib/browser-shortcuts.ts
import { registerTabShortcuts } from './tab-shortcut-registry'

registerTabShortcuts('browser', {
  'go-back': (_tab, pane) => window.electronAPI?.browserViewGoBack(pane.id),
  'go-forward': (_tab, pane) => window.electronAPI?.browserViewGoForward(pane.id),
  'reload': (_tab, pane) => window.electronAPI?.browserViewReload(pane.id),
  'focus-url': () => document.dispatchEvent(new CustomEvent('browser:focus-url')),
  'print': (_tab, pane) => window.electronAPI?.browserViewPrint(pane.id),
})
```

- [ ] **Step 6: Import in `App.tsx`**

Add after line 16 (`import { openBrowserTab }...`):

```ts
import './lib/browser-shortcuts'
```

- [ ] **Step 7: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 8: Commit**

```bash
git add spa/src/lib/browser-shortcuts.ts spa/src/hooks/useShortcuts.ts spa/src/hooks/useShortcuts.test.ts spa/src/App.tsx
git commit -m "feat: browser tab shortcuts via registry dispatch"
```

---

### Task 5: BrowserToolbar `focus-url` Event Listener

**Files:**
- Modify: `spa/src/components/BrowserToolbar.tsx:1,46-48`

- [ ] **Step 1: Add `useEffect` import and event listener**

In `spa/src/components/BrowserToolbar.tsx`, add `useEffect` to the import (line 1):

```ts
import { useState, useRef, useCallback, useEffect } from 'react'
```

Inside the `BrowserToolbar` component, after the `handleKeyDown` callback (after line 68), add:

```ts
  useEffect(() => {
    const handler = () => {
      inputRef.current?.focus()
      inputRef.current?.select()
    }
    document.addEventListener('browser:focus-url', handler)
    return () => document.removeEventListener('browser:focus-url', handler)
  }, [])
```

- [ ] **Step 2: Verify build**

Run: `cd spa && pnpm run build`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/BrowserToolbar.tsx
git commit -m "feat: BrowserToolbar listens for focus-url custom event"
```

---

### Task 6: Mini Window Theme Fix

**Files:**
- Modify: `spa/src/mini-browser.tsx:1-13`
- Modify: `spa/src/components/MiniBrowserApp.tsx:1-10`

- [ ] **Step 1: Add `ThemeInjector` to `mini-browser.tsx`**

Replace the full content of `spa/src/mini-browser.tsx`:

```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { ThemeInjector } from './components/ThemeInjector'
import { MiniBrowserApp } from './components/MiniBrowserApp'

const params = new URLSearchParams(window.location.search)
const paneId = params.get('paneId') || ''

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeInjector />
    <MiniBrowserApp paneId={paneId} />
  </StrictMode>,
)
```

- [ ] **Step 2: Add theme sync hook to `MiniBrowserApp.tsx`**

Add imports at top of `spa/src/components/MiniBrowserApp.tsx`:

```ts
import { useRef, useCallback, useEffect } from 'react'
import { useThemeStore } from '../stores/useThemeStore'
```

Inside the `MiniBrowserApp` component, after `const contentRef = ...` (after line 11), add:

```ts
  const activeThemeId = useThemeStore((s) => s.activeThemeId)
  useEffect(() => {
    document.documentElement.dataset.theme = activeThemeId
  }, [activeThemeId])
```

- [ ] **Step 3: Verify build**

Run: `cd spa && pnpm run build`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add spa/src/mini-browser.tsx spa/src/components/MiniBrowserApp.tsx
git commit -m "fix: initialize theme in mini window to make toolbar visible"
```

---

### Task 7: Mini Window Shortcuts

**Files:**
- Create: `spa/src/hooks/useMiniWindowShortcuts.ts`
- Modify: `spa/src/components/MiniBrowserApp.tsx` (add hook call)

- [ ] **Step 1: Create `useMiniWindowShortcuts.ts`**

```ts
// spa/src/hooks/useMiniWindowShortcuts.ts
import { useEffect } from 'react'

export function useMiniWindowShortcuts(paneId: string): void {
  useEffect(() => {
    if (!window.electronAPI?.onShortcut) return

    const cleanup = window.electronAPI.onShortcut(({ action }) => {
      switch (action) {
        case 'close-tab':
          window.close()
          break
        case 'go-back':
          window.electronAPI?.browserViewGoBack(paneId)
          break
        case 'go-forward':
          window.electronAPI?.browserViewGoForward(paneId)
          break
        case 'reload':
          window.electronAPI?.browserViewReload(paneId)
          break
        case 'focus-url':
          document.dispatchEvent(new CustomEvent('browser:focus-url'))
          break
        case 'print':
          window.electronAPI?.browserViewPrint(paneId)
          break
      }
    })

    return cleanup
  }, [paneId])
}
```

- [ ] **Step 2: Wire up in `MiniBrowserApp.tsx`**

Add import in `spa/src/components/MiniBrowserApp.tsx`:

```ts
import { useMiniWindowShortcuts } from '../hooks/useMiniWindowShortcuts'
```

Inside the component, after the `useBrowserViewResize` call, add:

```ts
  useMiniWindowShortcuts(paneId)
```

- [ ] **Step 3: Verify build**

Run: `cd spa && pnpm run build`
Expected: No errors

- [ ] **Step 4: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/hooks/useMiniWindowShortcuts.ts spa/src/components/MiniBrowserApp.tsx
git commit -m "feat: mini window shortcuts (Cmd+W close, browser nav, print)"
```

---

### Task 8: Lint + Final Verification

**Files:** None (verification only)

- [ ] **Step 1: Run lint**

Run: `cd spa && pnpm run lint`
Expected: No errors

- [ ] **Step 2: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 3: Run build**

Run: `cd spa && pnpm run build`
Expected: Clean build

- [ ] **Step 4: Fix any issues found, commit if needed**

---

### Task 9: Create `Cmd+F` Follow-up Issue

**Files:** None (gh issue only)

- [ ] **Step 1: Create GitHub issue**

```bash
gh issue create \
  --title "feat: browser tab Cmd+F find-in-page" \
  --label "feature,spa" \
  --body "## Summary

Browser tabs currently lack Cmd+F find-in-page functionality.

## Details

Electron does not provide a built-in Chromium find bar. Needs custom implementation:
- \`webContents.findInPage(text)\` / \`webContents.stopFindInPage('clearSelection')\`
- Find bar UI: input + prev/next buttons + match count + close button
- IPC: \`browser-view:find\`, \`browser-view:find-next\`, \`browser-view:find-stop\`
- Keybinding: \`Cmd+F\` → \`find-in-page\` (browser tab only via registry)
- Keybinding: \`Cmd+G\` / \`Cmd+Shift+G\` → next/prev match
- Keybinding: \`Escape\` → close find bar

## Context

Identified during browser UX fixes spec (2026-04-09). Deferred due to UI complexity."
```
