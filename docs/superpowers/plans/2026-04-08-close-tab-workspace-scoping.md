# Close Tab Workspace Scoping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the hook-layer close-tab patching with a single `closeTabInWorkspace` composite action in the workspace store, fixing 6 issues from PR #208 review.

**Architecture:** Add `closeTabInWorkspace(tabId)` to `useWorkspaceStore` that pre-computes the workspace-scoped adjacent tab before any mutation, then executes recordClose → removeTabFromWorkspace → closeTab → sync activeTabId in one call. Simplify `closeTab` in `useTabStore` to only remove the tab (no auto-select). Extract `destroyBrowserViewIfNeeded` helper. Migrate all 5 callers.

**Tech Stack:** React 19 / Zustand 5 / Vitest

---

### Task 1: Simplify `useTabStore.closeTab` — remove auto-select

**Files:**
- Modify: `spa/src/stores/useTabStore.ts:125-138`
- Modify: `spa/src/stores/useTabStore.test.ts:54-62`

- [ ] **Step 1: Update the failing test**

In `spa/src/stores/useTabStore.test.ts`, change the test at line 54 to expect `null` instead of adjacent tab:

```ts
  it('closeTab sets activeTabId to null when removing active tab', () => {
    const tab1 = makeSessionTab('dev001')
    const tab2 = makeSessionTab('cld001')
    useTabStore.getState().addTab(tab1)
    useTabStore.getState().addTab(tab2)
    useTabStore.getState().setActiveTab(tab1.id)
    useTabStore.getState().closeTab(tab1.id)
    expect(useTabStore.getState().activeTabId).toBeNull()
  })
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts -t "closeTab sets activeTabId to null"`
Expected: FAIL — currently returns `tab2.id`, not `null`

- [ ] **Step 3: Simplify closeTab implementation**

In `spa/src/stores/useTabStore.ts`, replace lines 125-138:

```ts
      closeTab: (id) =>
        set((state) => {
          if (!state.tabs[id]) return state
          if (state.tabs[id].locked) return state
          // eslint-disable-next-line @typescript-eslint/no-unused-vars
          const { [id]: _removed, ...remainingTabs } = state.tabs
          const newOrder = state.tabOrder.filter((tid) => tid !== id)
          return {
            tabs: remainingTabs,
            tabOrder: newOrder,
            activeTabId: state.activeTabId === id ? null : state.activeTabId,
          }
        }),
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/stores/useTabStore.test.ts`
Expected: ALL PASS (including the updated test and the existing "closeTab sets null when removing last tab" which is unchanged)

- [ ] **Step 5: Commit**

```bash
git add spa/src/stores/useTabStore.ts spa/src/stores/useTabStore.test.ts
git commit -m "refactor: simplify closeTab to only remove tab, no auto-select"
```

---

### Task 2: Add `destroyBrowserViewIfNeeded` helper

**Files:**
- Create: `spa/src/lib/browser-cleanup.ts`

- [ ] **Step 1: Create the helper**

```ts
import type { Tab } from '../types/tab'
import { getPrimaryPane } from './pane-tree'

export function destroyBrowserViewIfNeeded(tab: Tab): void {
  const primary = getPrimaryPane(tab.layout)
  if (primary.content.kind === 'browser') {
    window.electronAPI?.destroyBrowserView(primary.id)
  }
}
```

- [ ] **Step 2: Verify build**

Run: `cd spa && npx tsc --noEmit`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/browser-cleanup.ts
git commit -m "refactor: extract destroyBrowserViewIfNeeded helper"
```

---

### Task 3: Add `closeTabInWorkspace` composite action — TDD

**Files:**
- Modify: `spa/src/features/workspace/store.ts:6-22`
- Modify: `spa/src/features/workspace/store.test.ts`

- [ ] **Step 1: Write failing tests**

Add a new `describe('closeTabInWorkspace')` block at the end of `spa/src/features/workspace/store.test.ts`:

```ts
import { useTabStore } from '../../stores/useTabStore'
import { useHistoryStore } from '../../stores/useHistoryStore'
import { createTab } from '../../types/tab'

function makeTab() {
  return createTab({ kind: 'new-tab' })
}

// ... inside the outer describe, after all existing tests:

  describe('closeTabInWorkspace', () => {
    beforeEach(() => {
      useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
      useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
    })

    it('closes middle tab and selects right-adjacent tab', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tabs = [makeTab(), makeTab(), makeTab()]
      tabs.forEach((t) => {
        useTabStore.getState().addTab(t)
        useWorkspaceStore.getState().addTabToWorkspace(ws.id, t.id)
      })
      useTabStore.getState().setActiveTab(tabs[1].id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabs[1].id)

      useWorkspaceStore.getState().closeTabInWorkspace(tabs[1].id)

      expect(useTabStore.getState().tabs[tabs[1].id]).toBeUndefined()
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
      const updatedWs = useWorkspaceStore.getState().workspaces[0]
      expect(updatedWs.tabs).toEqual([tabs[0].id, tabs[2].id])
      expect(updatedWs.activeTabId).toBe(tabs[2].id)
    })

    it('closes last-index tab and selects left-adjacent tab', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tabs = [makeTab(), makeTab(), makeTab()]
      tabs.forEach((t) => {
        useTabStore.getState().addTab(t)
        useWorkspaceStore.getState().addTabToWorkspace(ws.id, t.id)
      })
      useTabStore.getState().setActiveTab(tabs[2].id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tabs[2].id)

      useWorkspaceStore.getState().closeTabInWorkspace(tabs[2].id)

      expect(useTabStore.getState().activeTabId).toBe(tabs[1].id)
      const updatedWs = useWorkspaceStore.getState().workspaces[0]
      expect(updatedWs.activeTabId).toBe(tabs[1].id)
    })

    it('closes only tab in workspace → activeTabId null', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tab = makeTab()
      useTabStore.getState().addTab(tab)
      useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab.id)
      useTabStore.getState().setActiveTab(tab.id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(ws.id, tab.id)

      useWorkspaceStore.getState().closeTabInWorkspace(tab.id)

      expect(useTabStore.getState().activeTabId).toBeNull()
      const updatedWs = useWorkspaceStore.getState().workspaces[0]
      expect(updatedWs.tabs).toEqual([])
      expect(updatedWs.activeTabId).toBeNull()
    })

    it('does not close locked tab', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tab = makeTab()
      useTabStore.getState().addTab(tab)
      useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab.id)
      useTabStore.getState().toggleLock(tab.id)
      useTabStore.getState().setActiveTab(tab.id)

      useWorkspaceStore.getState().closeTabInWorkspace(tab.id)

      expect(useTabStore.getState().tabs[tab.id]).toBeDefined()
    })

    it('no-op for nonexistent tab', () => {
      useWorkspaceStore.getState().addWorkspace('Test')
      useWorkspaceStore.getState().closeTabInWorkspace('nonexistent')
      // Should not throw
    })

    it('records close in history store', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tab = makeTab()
      useTabStore.getState().addTab(tab)
      useWorkspaceStore.getState().addTabToWorkspace(ws.id, tab.id)
      useTabStore.getState().setActiveTab(tab.id)

      useWorkspaceStore.getState().closeTabInWorkspace(tab.id)

      const { closedTabs } = useHistoryStore.getState()
      expect(closedTabs).toHaveLength(1)
      expect(closedTabs[0].tab.id).toBe(tab.id)
      expect(closedTabs[0].fromWorkspaceId).toBe(ws.id)
    })

    it('does not change activeTabId when closing non-active tab', () => {
      const ws = useWorkspaceStore.getState().addWorkspace('Test')
      const tabs = [makeTab(), makeTab()]
      tabs.forEach((t) => {
        useTabStore.getState().addTab(t)
        useWorkspaceStore.getState().addTabToWorkspace(ws.id, t.id)
      })
      useTabStore.getState().setActiveTab(tabs[0].id)

      useWorkspaceStore.getState().closeTabInWorkspace(tabs[1].id)

      expect(useTabStore.getState().activeTabId).toBe(tabs[0].id)
    })

    it('closes standalone tab with global tabOrder adjacency', () => {
      // No workspace — standalone tab
      const tabs = [makeTab(), makeTab(), makeTab()]
      tabs.forEach((t) => useTabStore.getState().addTab(t))
      useTabStore.getState().setActiveTab(tabs[1].id)

      useWorkspaceStore.getState().closeTabInWorkspace(tabs[1].id)

      expect(useTabStore.getState().tabs[tabs[1].id]).toBeUndefined()
      expect(useTabStore.getState().activeTabId).toBe(tabs[2].id)
    })
  })
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd spa && npx vitest run src/features/workspace/store.test.ts -t "closeTabInWorkspace"`
Expected: FAIL — `closeTabInWorkspace` does not exist yet

- [ ] **Step 3: Add closeTabInWorkspace to store interface and implementation**

In `spa/src/features/workspace/store.ts`, add imports at the top:

```ts
import { useTabStore } from '../../stores/useTabStore'
import { useHistoryStore } from '../../stores/useHistoryStore'
```

Add to the `WorkspaceState` interface (after `insertTab`):

```ts
  closeTabInWorkspace: (tabId: string) => void
```

Add the implementation inside the `create` block (after `insertTab`):

```ts
      closeTabInWorkspace: (tabId) => {
        const tabStore = useTabStore.getState()
        const tab = tabStore.tabs[tabId]
        if (!tab || tab.locked) return

        const ws = get().findWorkspaceByTab(tabId)

        // 1. Pre-compute adjacent tab (before any mutation)
        let nextTabId: string | null = null
        if (ws) {
          const idx = ws.tabs.indexOf(tabId)
          const remaining = ws.tabs.filter((id) => id !== tabId)
          nextTabId = remaining[Math.min(idx, remaining.length - 1)] ?? null
        } else {
          const { tabOrder } = tabStore
          const idx = tabOrder.indexOf(tabId)
          const remaining = tabOrder.filter((id) => id !== tabId)
          nextTabId = remaining[Math.min(idx, remaining.length - 1)] ?? null
        }

        // 2. Record history (before mutation — tab object still exists)
        useHistoryStore.getState().recordClose(tab, ws?.id)

        // 3. Remove from workspace
        if (ws) get().removeTabFromWorkspace(ws.id, tabId)

        // 4. Remove from tab store
        const wasActive = tabStore.activeTabId === tabId
        useTabStore.getState().closeTab(tabId)

        // 5. Sync active tab
        if (wasActive) {
          useTabStore.getState().setActiveTab(nextTabId)
        }
        if (ws && nextTabId) {
          get().setWorkspaceActiveTab(ws.id, nextTabId)
        }
      },
```

- [ ] **Step 4: Run tests**

Run: `cd spa && npx vitest run src/features/workspace/store.test.ts`
Expected: ALL PASS

- [ ] **Step 5: Commit**

```bash
git add spa/src/features/workspace/store.ts spa/src/features/workspace/store.test.ts
git commit -m "feat: add closeTabInWorkspace composite action"
```

---

### Task 4: Simplify `useShortcuts.ts` close-tab handler

**Files:**
- Modify: `spa/src/hooks/useShortcuts.ts:1-6,50-77`
- Modify: `spa/src/hooks/useShortcuts.test.ts:217-231`

- [ ] **Step 1: Update test — remove conditional assertion**

In `spa/src/hooks/useShortcuts.test.ts`, replace the "selects next tab within workspace after closing" test (lines 217-231):

```ts
    it('selects next tab within workspace after closing', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(3)
      useTabStore.getState().setActiveTab(tabs[1].id)
      useWorkspaceStore.getState().setWorkspaceActiveTab(
        useWorkspaceStore.getState().activeWorkspaceId!,
        tabs[1].id,
      )
      renderHook(() => useShortcuts())

      fire('close-tab')
      const state = useTabStore.getState()
      const wsId = useWorkspaceStore.getState().activeWorkspaceId!
      const ws = useWorkspaceStore.getState().workspaces.find((w) => w.id === wsId)!
      // Must select adjacent tab within workspace — no conditional
      expect(state.activeTabId).toBe(tabs[2].id)
      expect(ws.tabs).toContain(state.activeTabId)
      expect(ws.activeTabId).toBe(tabs[2].id)
    })
```

- [ ] **Step 2: Add close-last-tab-in-workspace test**

After the test above, add:

```ts
    it('closes last tab in workspace → activeTabId null', () => {
      const { fire } = mockElectronAPI()
      const tabs = seedTabs(1)
      useTabStore.getState().setActiveTab(tabs[0].id)
      renderHook(() => useShortcuts())

      fire('close-tab')
      expect(useTabStore.getState().activeTabId).toBeNull()
      expect(useTabStore.getState().tabs[tabs[0].id]).toBeUndefined()
    })
```

- [ ] **Step 3: Run tests to verify failures**

Run: `cd spa && npx vitest run src/hooks/useShortcuts.test.ts -t "close-tab"`
Expected: FAIL — the workspace activeTabId assertion will fail because current code doesn't sync it

- [ ] **Step 4: Rewrite the close-tab handler in useShortcuts.ts**

In `spa/src/hooks/useShortcuts.ts`, add import at line 6:

```ts
import { destroyBrowserViewIfNeeded } from '../lib/browser-cleanup'
```

Replace the entire close-tab block (lines 50-77) with:

```ts
      if (action === 'close-tab') {
        const { activeTabId, tabs } = tabState
        if (!activeTabId || !visibleIds.includes(activeTabId)) return
        const tab = tabs[activeTabId]
        if (!tab || tab.locked) return
        destroyBrowserViewIfNeeded(tab)
        useWorkspaceStore.getState().closeTabInWorkspace(activeTabId)
        return
      }
```

- [ ] **Step 5: Run tests**

Run: `cd spa && npx vitest run src/hooks/useShortcuts.test.ts`
Expected: ALL PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/hooks/useShortcuts.ts spa/src/hooks/useShortcuts.test.ts
git commit -m "refactor: simplify close-tab shortcut to use closeTabInWorkspace"
```

---

### Task 5: Simplify `hooks.ts` handleCloseTab

**Files:**
- Modify: `spa/src/features/workspace/hooks.ts:1-7,47-62`

- [ ] **Step 1: Update hooks.ts**

In `spa/src/features/workspace/hooks.ts`, add import:

```ts
import { destroyBrowserViewIfNeeded } from '../../lib/browser-cleanup'
```

Remove the now-unused imports: `useHistoryStore`, `getPrimaryPane`.

Replace `handleCloseTab` (lines 47-62) with:

```ts
  const handleCloseTab = useCallback((tabId: string) => {
    const tab = tabs[tabId]
    if (!tab || tab.locked) return
    destroyBrowserViewIfNeeded(tab)
    useWorkspaceStore.getState().closeTabInWorkspace(tabId)
  }, [tabs])
```

Remove `findWorkspaceByTab` and `removeTabFromWorkspace` from the destructured workspace store selectors (lines 23-24) if they are no longer used elsewhere in the file. Check: `findWorkspaceByTab` is still used in `handleSelectTab` (line 38), so keep it. `removeTabFromWorkspace` — check all usages in the file. It's only used in the old `handleCloseTab`, so remove it from the selector.

- [ ] **Step 2: Run existing tests**

Run: `cd spa && npx vitest run src/features/workspace/hooks.test.ts`
Expected: ALL PASS

- [ ] **Step 3: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/features/workspace/hooks.ts
git commit -m "refactor: simplify handleCloseTab to use closeTabInWorkspace"
```

---

### Task 6: Migrate `TerminatedPane.tsx`

**Files:**
- Modify: `spa/src/components/TerminatedPane.tsx:2,21,42`
- Modify: `spa/src/components/TerminatedPane.test.tsx:88-99`

- [ ] **Step 1: Update the test**

In `spa/src/components/TerminatedPane.test.tsx`, the test "has a close tab button that calls closeTab" (line 88) needs a workspace setup so `closeTabInWorkspace` can find the tab. Add workspace store import and setup in the test file's `beforeEach` or in-test setup. Locate how `setupTab` works in the test file first, then add workspace context.

Read the top of the test file to understand the setup, then update the test to add the tab to a workspace:

The test at line 88-99 currently just calls `setupTab(content)` which adds the tab to `useTabStore`. After `setupTab(content)`, add:

```ts
    useWorkspaceStore.getState().reset()
    const ws = useWorkspaceStore.getState().addWorkspace('Test')
    useWorkspaceStore.getState().addTabToWorkspace(ws.id, TAB_ID)
```

And add import: `import { useWorkspaceStore } from '../stores/useWorkspaceStore'`

- [ ] **Step 2: Update TerminatedPane.tsx**

Replace the import and usage:

```ts
// Replace:
import { useTabStore } from '../stores/useTabStore'
// With:
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { destroyBrowserViewIfNeeded } from '../lib/browser-cleanup'
import { useTabStore } from '../stores/useTabStore'
```

Replace `const closeTab = useTabStore((s) => s.closeTab)` (line 21) with:

```ts
  const closeTabInWorkspace = useWorkspaceStore((s) => s.closeTabInWorkspace)
```

Replace the button onClick (line 42):

```ts
      <button className="text-sm text-zinc-400 hover:text-zinc-200 mb-8" onClick={() => {
        const tab = useTabStore.getState().tabs[tabId]
        if (tab) destroyBrowserViewIfNeeded(tab)
        closeTabInWorkspace(tabId)
      }}>
```

- [ ] **Step 3: Run tests**

Run: `cd spa && npx vitest run src/components/TerminatedPane.test.tsx`
Expected: ALL PASS

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/TerminatedPane.tsx spa/src/components/TerminatedPane.test.tsx
git commit -m "refactor: migrate TerminatedPane to closeTabInWorkspace"
```

---

### Task 7: Migrate `WorkspaceSettingsPage.tsx`

**Files:**
- Modify: `spa/src/features/workspace/components/WorkspaceSettingsPage.tsx:134-153`

- [ ] **Step 1: Update the delete workspace handler**

In `WorkspaceSettingsPage.tsx`, the `onConfirm` callback (lines 134-153) currently does:
1. Loop through tabs calling `recordClose` + `closeTab`
2. Call `removeWorkspace`
3. Handle activeTabId fallback

Replace lines 134-153 with:

```ts
              onConfirm={(closedTabIds) => {
                const wsStore = useWorkspaceStore.getState()
                closedTabIds.forEach((id) => {
                  wsStore.closeTabInWorkspace(id)
                })
                wsStore.removeWorkspace(workspaceId)
                // If some tabs were preserved (locked), go to home mode
                const hasPreservedTabs = closedTabIds.length < tabItems.length
                if (hasPreservedTabs) {
                  useWorkspaceStore.getState().setActiveWorkspace(null)
                } else {
                  const { activeWorkspaceId: newWsId, workspaces: remaining } = useWorkspaceStore.getState()
                  const newWs = remaining.find((w) => w.id === newWsId)
                  const nextTab = newWs?.activeTabId ?? newWs?.tabs[0]
                  if (nextTab) useTabStore.getState().setActiveTab(nextTab)
                }
                setShowDelete(false)
              }}
```

Note: `useHistoryStore` import can be removed from this file if it's no longer used elsewhere. Check the file — if `useHistoryStore` is not used anywhere else, remove the import.

- [ ] **Step 2: Run tests**

Run: `cd spa && npx vitest run src/features/workspace`
Expected: ALL PASS

- [ ] **Step 3: Commit**

```bash
git add spa/src/features/workspace/components/WorkspaceSettingsPage.tsx
git commit -m "refactor: migrate WorkspaceSettingsPage to closeTabInWorkspace"
```

---

### Task 8: Migrate `host-lifecycle.ts`

**Files:**
- Modify: `spa/src/lib/host-lifecycle.ts:77-90`

- [ ] **Step 1: Update the cascade delete**

In `spa/src/lib/host-lifecycle.ts`, the close-tabs loop (lines 77-90) currently calls `tabStore.closeTab(tabId)`. Replace with `closeTabInWorkspace`:

```ts
  if (closeTabs) {
    const wsStore = useWorkspaceStore.getState()
    // Close all tmux-session tabs for this host (scan ALL panes, not just primary)
    for (const [tabId, tab] of Object.entries(tabStore.tabs)) {
      let hasHostPane = false
      scanPaneTree(tab.layout, (pane) => {
        if (pane.content.kind === 'tmux-session' && pane.content.hostId === hostId) {
          hasHostPane = true
        }
      })
      if (hasHostPane) {
        snapshot.closedTabs.push(tab)
        wsStore.closeTabInWorkspace(tabId)
      }
    }
```

Add import at the top of the file:

```ts
import { useWorkspaceStore } from '../features/workspace/store'
```

- [ ] **Step 2: Run tests**

Run: `cd spa && npx vitest run src/lib/host-lifecycle.test.ts`
Expected: ALL PASS

- [ ] **Step 3: Commit**

```bash
git add spa/src/lib/host-lifecycle.ts
git commit -m "refactor: migrate host-lifecycle to closeTabInWorkspace"
```

---

### Task 9: Final verification

- [ ] **Step 1: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: ALL tests pass

- [ ] **Step 2: Run lint**

Run: `cd spa && pnpm run lint`
Expected: Clean

- [ ] **Step 3: Run build**

Run: `cd spa && pnpm run build`
Expected: Success

- [ ] **Step 4: Commit any lint fixes if needed**

Only if lint found auto-fixable issues.
