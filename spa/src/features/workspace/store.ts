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
  reorderWorkspaces: (orderedIds: string[]) => void
  findWorkspaceByTab: (tabId: string) => Workspace | null
  insertTab: (tabId: string, workspaceId?: string | null) => void
  closeTabInWorkspace: (tabId: string, opts?: { skipHistory?: boolean }) => void
  renameWorkspace: (wsId: string, name: string) => void
  setWorkspaceIcon: (wsId: string, icon: string) => void
  setWorkspaceIconWeight: (wsId: string, weight: IconWeight) => void
  importWorkspace: (ws: Workspace) => void
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

      reorderWorkspaces: (orderedIds) =>
        set((state) => {
          const byId = new Map(state.workspaces.map((ws) => [ws.id, ws]))
          const seen = new Set<string>()
          const reordered: Workspace[] = []
          for (const id of orderedIds) {
            const ws = byId.get(id)
            if (ws) { reordered.push(ws); seen.add(id) }
          }
          // Preserve workspaces not in orderedIds (defensive — prevents data loss)
          for (const ws of state.workspaces) {
            if (!seen.has(ws.id)) reordered.push(ws)
          }
          return { workspaces: reordered }
        }),

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

      closeTabInWorkspace: (tabId, opts) => {
        const tabStore = useTabStore.getState()
        const tab = tabStore.tabs[tabId]
        if (!tab || tab.locked) return

        const ws = get().findWorkspaceByTab(tabId)

        // 1. Pre-compute next tab (before any mutation)
        //    Priority: visitHistory (scoped) → adjacent in workspace/tabOrder
        const scopeIds = ws
          ? new Set(ws.tabs.filter((id) => id !== tabId))
          : new Set(tabStore.tabOrder.filter((id) => id !== tabId))

        let nextTabId: string | null = null
        // Try visitHistory first (most recent visited tab still in scope)
        const { visitHistory } = tabStore
        for (let i = visitHistory.length - 1; i >= 0; i--) {
          if (scopeIds.has(visitHistory[i])) {
            nextTabId = visitHistory[i]
            break
          }
        }
        // Fallback to adjacent
        if (nextTabId === null) {
          const ordered = ws ? ws.tabs : tabStore.tabOrder
          const idx = ordered.indexOf(tabId)
          const remaining = ordered.filter((id) => id !== tabId)
          nextTabId = remaining[Math.min(idx, remaining.length - 1)] ?? null
        }

        // Pre-compute wasActive flags before any mutation
        const wasActive = tabStore.activeTabId === tabId
        const wasWorkspaceActive = ws ? ws.activeTabId === tabId : false

        // 2. Record history (before mutation — tab object still exists)
        if (!opts?.skipHistory) {
          useHistoryStore.getState().recordClose(tab, ws?.id)
        }

        // 3. Remove from workspace
        if (ws) get().removeTabFromWorkspace(ws.id, tabId)

        // 4. Remove from tab store
        useTabStore.getState().closeTab(tabId)

        // 5. Sync active tab
        if (wasActive) {
          useTabStore.getState().setActiveTab(nextTabId)
        }
        if (ws && wasWorkspaceActive && nextTabId) {
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

      importWorkspace: (ws) =>
        set((state) => ({
          workspaces: state.workspaces.some((w) => w.id === ws.id)
            ? state.workspaces
            : [...state.workspaces, ws],
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
