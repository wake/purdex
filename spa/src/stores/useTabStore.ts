import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { Tab, PaneContent } from '../types/tab'
import { createTab } from '../types/tab'
import { getPrimaryPane, findPane, updatePaneInLayout } from '../lib/pane-tree'
import { contentMatches } from '../lib/pane-utils'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../lib/storage'

interface TabState {
  tabs: Record<string, Tab>
  tabOrder: string[]
  activeTabId: string | null

  addTab: (tab: Tab) => void
  openSingletonTab: (content: PaneContent) => string
  closeTab: (id: string) => void
  setActiveTab: (id: string | null) => void
  setViewMode: (tabId: string, paneId: string, mode: 'terminal' | 'stream') => void
  setPaneContent: (tabId: string, paneId: string, content: PaneContent) => void
  splitPane: (tabId: string, paneId: string, direction: 'h' | 'v', content: PaneContent) => void
  closePane: (tabId: string, paneId: string) => void
  reorderTabs: (order: string[]) => void
  togglePin: (id: string) => void
  toggleLock: (id: string) => void
}

export const useTabStore = create<TabState>()(
  persist(
    (set, get) => ({
      tabs: {},
      tabOrder: [],
      activeTabId: null,

      addTab: (tab) =>
        set((state) => {
          if (state.tabs[tab.id]) return state // dedup guard
          return {
            tabs: { ...state.tabs, [tab.id]: tab },
            tabOrder: [...state.tabOrder, tab.id],
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
          let newActiveId = state.activeTabId
          if (state.activeTabId === id) {
            const oldIndex = state.tabOrder.indexOf(id)
            newActiveId = newOrder[Math.min(oldIndex, newOrder.length - 1)] ?? null
          }
          return { tabs: remainingTabs, tabOrder: newOrder, activeTabId: newActiveId }
        }),

      setActiveTab: (id) =>
        set((state) => {
          if (id === null) return { activeTabId: null }
          if (!state.tabs[id]) return state
          return { activeTabId: id }
        }),

      setViewMode: (tabId, paneId, mode) =>
        set((state) => {
          const tab = state.tabs[tabId]
          if (!tab) return state
          const pane = findPane(tab.layout, paneId)
          if (!pane || pane.content.kind !== 'session') return state
          const newLayout = updatePaneInLayout(tab.layout, paneId, {
            kind: 'session',
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

      // Stub — no-op for now
      splitPane: () => {},

      // Stub — no-op for now
      closePane: () => {},

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
    }),
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
  ),
)

syncManager.register(STORAGE_KEYS.TABS, useTabStore)
