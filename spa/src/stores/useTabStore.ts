import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { Tab, PaneContent, PaneLayout, TerminatedReason, LayoutPattern } from '../types/tab'
import { createTab } from '../types/tab'
import { getPrimaryPane, findPane, updatePaneInLayout, splitAtPane, removePane, applyLayoutPattern } from '../lib/pane-tree'
import { contentMatches } from '../lib/pane-utils'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

// --- Persist migration helpers ---
// These functions handle legacy persisted data whose shape no longer matches
// current TypeScript types, so `any` casts are unavoidable.

function migrateLayout(layout: PaneLayout): PaneLayout {
  if (layout.type === 'leaf') {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
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

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function migrateTabStore(state: any, version: number): any {
  if (version < 2) {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const tabs: Record<string, any> = {}
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    for (const [id, tab] of Object.entries(state.tabs as Record<string, any>)) {
      tabs[id] = { ...tab, layout: migrateLayout(tab.layout) }
    }
    return { ...state, tabs }
  }
  return state
}

// --- Terminated marking helpers ---

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

interface TabState {
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null
  visitHistory: string[]

  addTab: (tab: Tab, afterTabId?: string) => void
  openSingletonTab: (content: PaneContent) => string
  closeTab: (id: string) => void
  setActiveTab: (id: string | null) => void
  setViewMode: (tabId: string, paneId: string, mode: 'terminal' | 'stream') => void
  setPaneContent: (tabId: string, paneId: string, content: PaneContent) => void
  splitPane: (tabId: string, paneId: string, direction: 'h' | 'v', content: PaneContent) => void
  closePane: (tabId: string, paneId: string) => void
  resizePanes: (tabId: string, splitId: string, sizes: number[]) => void
  applyLayout: (tabId: string, pattern: LayoutPattern) => void
  detachPane: (tabId: string, paneId: string, afterTabId?: string) => string | null
  reorderTabs: (order: string[]) => void
  togglePin: (id: string) => void
  toggleLock: (id: string) => void
  updateSessionCache: (hostId: string, sessionCode: string, cachedName: string) => void
  markTerminated: (hostId: string, sessionCode: string, reason: TerminatedReason) => void
  markHostTerminated: (hostId: string, reason: TerminatedReason) => void
}

export const useTabStore = create<TabState>()(
  persist(
    (set, get) => ({
      tabs: {},
      tabOrder: [],
      activeTabId: null,
      visitHistory: [],

      addTab: (tab, afterTabId) =>
        set((state) => {
          if (state.tabs[tab.id]) return state // dedup guard
          let newOrder: string[]
          if (afterTabId) {
            const idx = state.tabOrder.indexOf(afterTabId)
            if (idx !== -1) {
              // If afterTabId is pinned and new tab is not, skip past pinned group
              let insertIdx = idx + 1
              if (!tab.pinned && state.tabs[afterTabId]?.pinned) {
                while (insertIdx < state.tabOrder.length && state.tabs[state.tabOrder[insertIdx]]?.pinned) {
                  insertIdx++
                }
              }
              newOrder = [...state.tabOrder]
              newOrder.splice(insertIdx, 0, tab.id)
            } else {
              newOrder = [...state.tabOrder, tab.id]
            }
          } else {
            newOrder = [...state.tabOrder, tab.id]
          }
          return {
            tabs: { ...state.tabs, [tab.id]: tab },
            tabOrder: newOrder,
            activeTabId: state.activeTabId ?? tab.id,
          }
        }),

      openSingletonTab: (content) => {
        const state = get()
        // Scan all tabs' primary pane for matching content
        for (const id of state.tabOrder) {
          const tab = state.tabs[id]
          if (!tab) continue
          const primary = getPrimaryPane(tab.layout)
          if (contentMatches(primary.content, content)) {
            get().setActiveTab(id)
            return id
          }
        }
        // Not found — create new tab
        const tab = createTab(content)
        get().addTab(tab)
        get().setActiveTab(tab.id)
        return tab.id
      },

      closeTab: (id) =>
        set((state) => {
          if (!state.tabs[id]) return state
          if (state.tabs[id].locked) return state
          // eslint-disable-next-line @typescript-eslint/no-unused-vars
          const { [id]: _removed, ...remainingTabs } = state.tabs
          const newOrder = state.tabOrder.filter((tid) => tid !== id)
          // Clean closed tab from visitHistory; active tab selection is caller's responsibility
          const newHistory = state.visitHistory.filter((tid) => tid !== id)
          return {
            tabs: remainingTabs,
            tabOrder: newOrder,
            activeTabId: state.activeTabId === id ? null : state.activeTabId,
            visitHistory: newHistory,
          }
        }),

      setActiveTab: (id) =>
        set((state) => {
          if (id === null) return { activeTabId: null }
          if (!state.tabs[id]) return state
          if (id === state.activeTabId) return state
          // Record current tab in visitHistory (dedup: remove newId from history first)
          const newHistory = state.activeTabId !== null
            ? [...state.visitHistory.filter((tid) => tid !== id), state.activeTabId]
            : state.visitHistory.filter((tid) => tid !== id)
          return { activeTabId: id, visitHistory: newHistory }
        }),

      setViewMode: (tabId, paneId, mode) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const pane = findPane(tab.layout, paneId)
          if (!pane || pane.content.kind !== 'tmux-session') return state
          const newLayout = updatePaneInLayout(tab.layout, paneId, {
            kind: 'tmux-session',
            hostId: pane.content.hostId,
            sessionCode: pane.content.sessionCode,
            mode,
            cachedName: pane.content.cachedName,
            tmuxInstance: pane.content.tmuxInstance,
          })
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),

      setPaneContent: (tabId, paneId, content) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const newLayout = updatePaneInLayout(tab.layout, paneId, content)
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),

      splitPane: (tabId, paneId, direction, content) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const newLayout = splitAtPane(tab.layout, paneId, direction, content)
          if (newLayout === tab.layout) return state
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),

      closePane: (tabId, paneId) => {
        const state = get()
        const tab = state.tabs[tabId]
        if (!tab) return
        const newLayout = removePane(tab.layout, paneId)
        if (newLayout === null) {
          get().closeTab(tabId)
          return
        }
        set({ tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } })
      },

      resizePanes: (tabId, splitId, sizes) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const update = (layout: PaneLayout): PaneLayout => {
            if (layout.type === 'leaf') return layout
            if (layout.id === splitId) return { ...layout, sizes }
            const newChildren = layout.children.map(update)
            return newChildren.some((c, i) => c !== layout.children[i])
              ? { ...layout, children: newChildren }
              : layout
          }
          const newLayout = update(tab.layout)
          if (newLayout === tab.layout) return state
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),

      applyLayout: (tabId, pattern) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const newLayout = applyLayoutPattern(tab.layout, pattern)
          return { tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout } } }
        }),

      detachPane: (tabId, paneId, afterTabId) => {
        const state = get()
        const tab = state.tabs[tabId]
        if (!tab) return null
        const pane = findPane(tab.layout, paneId)
        if (!pane) return null
        if (tab.layout.type === 'leaf') return null
        const newLayout = removePane(tab.layout, paneId)
        if (!newLayout) return null
        const newTab = createTab(pane.content)
        let newOrder: string[]
        if (afterTabId) {
          const idx = state.tabOrder.indexOf(afterTabId)
          if (idx !== -1) {
            newOrder = [...state.tabOrder]
            newOrder.splice(idx + 1, 0, newTab.id)
          } else {
            newOrder = [...state.tabOrder, newTab.id]
          }
        } else {
          newOrder = [...state.tabOrder, newTab.id]
        }
        set({
          tabs: { ...state.tabs, [tabId]: { ...tab, layout: newLayout }, [newTab.id]: newTab },
          tabOrder: newOrder,
        })
        return newTab.id
      },

      reorderTabs: (order) =>
        set({ tabOrder: order }),

      togglePin: (id) =>
        set((state) => {
          const tab = state.tabs[id]
          if (!tab) return state
          const newPinned = !tab.pinned
          const updated = { ...tab, pinned: newPinned }
          const newOrder = state.tabOrder.filter((tid) => tid !== id)
          const firstNormalIdx = newOrder.findIndex((tid) => !state.tabs[tid]?.pinned)
          const insertIdx = firstNormalIdx === -1 ? newOrder.length : firstNormalIdx
          newOrder.splice(insertIdx, 0, id)
          return { tabs: { ...state.tabs, [id]: updated }, tabOrder: newOrder }
        }),

      toggleLock: (id) =>
        set((state) => {
          const tab = state.tabs[id]
          if (!tab) return state
          return { tabs: { ...state.tabs, [id]: { ...tab, locked: !tab.locked } } }
        }),

      updateSessionCache: (hostId, sessionCode, cachedName) =>
        set((state) => {
          let changed = false
          const tabs = { ...state.tabs }
          for (const [id, tab] of Object.entries(tabs)) {
            const primary = getPrimaryPane(tab.layout)
            const c = primary.content
            if (c.kind === 'tmux-session' && c.hostId === hostId && c.sessionCode === sessionCode && c.cachedName !== cachedName) {
              tabs[id] = {
                ...tab,
                layout: updatePaneInLayout(tab.layout, primary.id, { ...c, cachedName }),
              }
              changed = true
            }
          }
          return changed ? { tabs } : state
        }),

      markTerminated: (hostId, sessionCode, reason) =>
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

      markHostTerminated: (hostId, reason) =>
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
    }),
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
    },
  ),
)

syncManager.register(STORAGE_KEYS.TABS, useTabStore)
