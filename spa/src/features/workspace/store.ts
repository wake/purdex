import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { createWorkspace, type Workspace, type IconWeight } from '../../types/tab'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../../lib/storage'
import { useTabStore } from '../../stores/useTabStore'
import { useHistoryStore } from '../../stores/useHistoryStore'

interface WorkspaceState {
  workspaces: Workspace[]
  activeWorkspaceId: string | null

  addWorkspace: (name: string, opts?: { icon?: string }) => Workspace
  removeWorkspace: (wsId: string) => void
  setActiveWorkspace: (wsId: string | null) => void
  addTabToWorkspace: (wsId: string, tabId: string) => void
  removeTabFromWorkspace: (wsId: string, tabId: string) => void
  setWorkspaceActiveTab: (wsId: string, tabId: string) => void
  reorderWorkspaceTabs: (wsId: string, tabIds: string[]) => void
  findWorkspaceByTab: (tabId: string) => Workspace | null
  insertTab: (tabId: string, workspaceId?: string | null) => void
  closeTabInWorkspace: (tabId: string) => void
  renameWorkspace: (wsId: string, name: string) => void
  setWorkspaceIcon: (wsId: string, icon: string) => void
  setWorkspaceIconWeight: (wsId: string, weight: IconWeight) => void
  reset: () => void
}

function createDefaultState(): Pick<WorkspaceState, 'workspaces' | 'activeWorkspaceId'> {
  return { workspaces: [], activeWorkspaceId: null }
}

export const useWorkspaceStore = create<WorkspaceState>()(
  persist(
    (set, get) => ({
      ...createDefaultState(),

      addWorkspace: (name, opts) => {
        const ws = createWorkspace(name, opts?.icon)
        set((state) => ({
          workspaces: [...state.workspaces, ws],
          // Auto-activate if this is the first workspace
          activeWorkspaceId: state.activeWorkspaceId ?? ws.id,
        }))
        return ws
      },

      removeWorkspace: (wsId) =>
        set((state) => {
          const remaining = state.workspaces.filter((ws) => ws.id !== wsId)
          if (remaining.length === state.workspaces.length) return state // wsId not found
          const activeId = state.activeWorkspaceId === wsId
            ? (remaining[0]?.id ?? null)
            : state.activeWorkspaceId
          return { workspaces: remaining, activeWorkspaceId: activeId }
        }),

      setActiveWorkspace: (wsId) =>
        set({ activeWorkspaceId: wsId }),

      addTabToWorkspace: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) => {
            if (ws.id !== wsId) return ws
            if (ws.tabs.includes(tabId)) return ws
            return { ...ws, tabs: [...ws.tabs, tabId] }
          }),
        })),

      removeTabFromWorkspace: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId
              ? {
                  ...ws,
                  tabs: ws.tabs.filter((id) => id !== tabId),
                  activeTabId: ws.activeTabId === tabId ? null : ws.activeTabId,
                }
              : ws,
          ),
        })),

      setWorkspaceActiveTab: (wsId, tabId) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, activeTabId: tabId } : ws,
          ),
        })),

      reorderWorkspaceTabs: (wsId, tabIds) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, tabs: tabIds } : ws,
          ),
        })),

      findWorkspaceByTab: (tabId) => {
        return get().workspaces.find((ws) => ws.tabs.includes(tabId)) ?? null
      },

      insertTab: (tabId, workspaceId) => {
        const targetWsId = workspaceId === null
          ? null
          : workspaceId !== undefined
            ? workspaceId
            : get().activeWorkspaceId

        if (!targetWsId) return

        set((state) => ({
          workspaces: state.workspaces.map((ws) => {
            if (ws.id === targetWsId) {
              if (ws.tabs.includes(tabId)) return { ...ws, activeTabId: tabId }
              return { ...ws, tabs: [...ws.tabs, tabId], activeTabId: tabId }
            }
            // Remove from other workspaces (singleton tab dedup)
            if (!ws.tabs.includes(tabId)) return ws
            return {
              ...ws,
              tabs: ws.tabs.filter((id) => id !== tabId),
              activeTabId: ws.activeTabId === tabId ? null : ws.activeTabId,
            }
          }),
        }))
      },

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

      renameWorkspace: (wsId, name) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, name } : ws,
          ),
        })),

      setWorkspaceIcon: (wsId, icon) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, icon: icon || undefined } : ws,
          ),
        })),

      setWorkspaceIconWeight: (wsId, weight) =>
        set((state) => ({
          workspaces: state.workspaces.map((ws) =>
            ws.id === wsId ? { ...ws, iconWeight: weight } : ws,
          ),
        })),

      reset: () => set(createDefaultState()),
    }),
    {
      name: STORAGE_KEYS.WORKSPACES,
      storage: purdexStorage,
      version: 1,
      partialize: (state) => ({
        workspaces: state.workspaces,
        activeWorkspaceId: state.activeWorkspaceId,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.WORKSPACES, useWorkspaceStore)
