import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { createWorkspace, type Workspace } from '../../types/tab'
import { purdexStorage, STORAGE_KEYS, syncManager } from '../../lib/storage'

interface WorkspaceState {
  workspaces: Workspace[]
  activeWorkspaceId: string | null

  addWorkspace: (name: string, opts?: { color?: string; icon?: string }) => Workspace
  removeWorkspace: (wsId: string) => void
  setActiveWorkspace: (wsId: string | null) => void
  addTabToWorkspace: (wsId: string, tabId: string) => void
  removeTabFromWorkspace: (wsId: string, tabId: string) => void
  setWorkspaceActiveTab: (wsId: string, tabId: string) => void
  reorderWorkspaceTabs: (wsId: string, tabIds: string[]) => void
  findWorkspaceByTab: (tabId: string) => Workspace | null
  insertTab: (tabId: string, workspaceId?: string | null) => void
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
        const ws = createWorkspace(name, opts?.color, opts?.icon)
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
        const state = get()
        let targetWsId: string | null
        if (workspaceId === null) {
          targetWsId = null
        } else if (workspaceId !== undefined) {
          targetWsId = workspaceId
        } else {
          targetWsId = state.activeWorkspaceId
        }

        if (targetWsId) {
          state.addTabToWorkspace(targetWsId, tabId)
          state.setWorkspaceActiveTab(targetWsId, tabId)
        }
      },

      reset: () => set(createDefaultState()),
    }),
    {
      name: STORAGE_KEYS.WORKSPACES,
      storage: purdexStorage,
      version: 2,
      migrate: (persisted, version) => {
        if (version === 1) {
          const old = persisted as { workspaces?: Workspace[]; activeWorkspaceId?: string }
          return {
            workspaces: old.workspaces ?? [],
            activeWorkspaceId: old.activeWorkspaceId ?? null,
          }
        }
        return persisted as { workspaces: Workspace[]; activeWorkspaceId: string | null }
      },
      partialize: (state) => ({
        workspaces: state.workspaces,
        activeWorkspaceId: state.activeWorkspaceId,
      }),
    },
  ),
)

syncManager.register(STORAGE_KEYS.WORKSPACES, useWorkspaceStore)
